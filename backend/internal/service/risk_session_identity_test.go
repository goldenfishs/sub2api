package service

import (
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func riskIdentityContext(headers map[string]string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	for k, v := range headers {
		c.Request.Header.Set(k, v)
	}
	return c
}

func riskIdentityIntersects(a, b []string) bool {
	set := make(map[string]bool, len(a))
	for _, k := range a {
		set[k] = true
	}
	for _, k := range b {
		if set[k] {
			return true
		}
	}
	return false
}

func TestRiskSessionIdentityRetryRewriteAndContinuation(t *testing.T) {
	hit := []byte(`{"model":"old-model","messages":[{"role":"user","content":"start"},{"role":"assistant","content":"unique answer"},{"role":"user","content":"trigger"}]}`)
	blocked := RiskSessionBlockKeys(7, nil, hit)
	require.Len(t, blocked, 2)
	for name, body := range map[string]string{
		"same request":         string(hit),
		"rollback last user":   `{"messages":[{"role":"user","content":"start"},{"role":"assistant","content":"unique answer"}]}`,
		"rewrite last user":    `{"model":"new-model","messages":[{"role":"user","content":"start"},{"role":"assistant","content":"unique answer"},{"role":"user","content":"rewritten"}]}`,
		"continue":             `{"messages":[{"role":"user","content":"start"},{"role":"assistant","content":"unique answer"},{"role":"user","content":"trigger"},{"role":"assistant","content":"reply"},{"role":"user","content":"next"}]}`,
		"rewrite and continue": `{"messages":[{"role":"user","content":"start"},{"role":"assistant","content":"unique answer"},{"role":"user","content":"rewritten"},{"role":"assistant","content":"reply"},{"role":"user","content":"next"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			require.True(t, riskIdentityIntersects(blocked, RiskSessionLookupKeys(7, nil, []byte(body))))
		})
	}
	// An earlier rewind without a stable session signal cannot be attributed.
	early := []byte(`{"messages":[{"role":"user","content":"start"},{"role":"user","content":"new branch"}]}`)
	require.False(t, riskIdentityIntersects(blocked, RiskSessionLookupKeys(7, nil, early)))
}

func TestRiskSessionIdentityStableSessionAndUserScope(t *testing.T) {
	c1 := riskIdentityContext(map[string]string{"session_id": "private-session", "Authorization": "Bearer key-1", "X-Forwarded-For": "192.0.2.1"})
	c2 := riskIdentityContext(map[string]string{"X-Conversation-ID": "private-session", "Authorization": "Bearer key-2", "X-Forwarded-For": "192.0.2.2", "User-Agent": "different-client"})
	hit := RiskSessionBlockKeys(77, c1, []byte(`{"model":"first","input":"trigger"}`))
	retry := RiskSessionLookupKeys(77, c2, []byte(`{"model":"second","input":"rewound"}`))
	require.True(t, riskIdentityIntersects(hit, retry))
	require.False(t, riskIdentityIntersects(hit, RiskSessionLookupKeys(78, c2, []byte(`{"model":"first","input":"trigger"}`))))
	require.False(t, riskIdentityIntersects(hit, RiskSessionLookupKeys(77, nil, []byte(`{"input":"unrelated"}`))))
	require.Equal(t, RiskSessionBlockKeys(77, nil, []byte(`{"prompt_cache_key":"pck"}`)), RiskSessionBlockKeys(77, nil, []byte(`{"type":"response.create","response":{"prompt_cache_key":"pck"}}`)))
	require.Len(t, RiskSessionBlockKeys(77, nil, []byte(`{"prompt_cache_key":"pck"}`)), 1)
	require.Empty(t, RiskSessionBlockKeys(0, c1, []byte(`{"input":"trigger"}`)))
	require.Empty(t, RiskSessionLookupKeys(-1, c1, []byte(`{"input":"trigger"}`)))
	// Rotation of API credentials, client network or model has no influence.
	require.Equal(t, RiskSessionBlockKeys(77, c1, []byte(`{"input":"trigger"}`)), RiskSessionBlockKeys(77, c2, []byte(`{"model":"other","input":"trigger","temperature":0.7}`)))
}

func TestRiskSessionIdentityFirstTurnTemplatesDoNotBlockUnrelatedQueries(t *testing.T) {
	blocked := RiskSessionBlockKeys(7, nil, []byte(`{"instructions":"shared system","input":[{"role":"user","content":"fixed environment"},{"role":"user","content":"trigger"}]}`))
	unrelated := RiskSessionLookupKeys(7, nil, []byte(`{"instructions":"shared system","input":[{"role":"user","content":"fixed environment"},{"role":"user","content":"benign"}]}`))
	require.Len(t, blocked, 1, "shared environment must not become a rewrite-tolerant block")
	require.False(t, riskIdentityIntersects(blocked, unrelated))
	for _, body := range []string{
		`{"model":"same","instructions":"shared"}`,
		`{"messages":[{"role":"system","content":"shared"}]}`,
		`{"messages":[{"role":"user","content":""}]}`,
		`{"messages":[{"role":"assistant"}]}`,
		`{"messages":[{"type":"unknown","role":"user"}]}`,
		`{"model":"image-model","size":"1024x1024"}`,
		`{"previous_response_id":"resp_opaque"}`,
		`{"input":false}`,
		`null`, `[]`, `{`, ``,
	} {
		require.Empty(t, RiskSessionBlockKeys(7, nil, []byte(body)), body)
		require.Empty(t, RiskSessionLookupKeys(7, nil, []byte(body)), body)
	}
}

func TestRiskSessionIdentityProtocolAndJSONCanonicalization(t *testing.T) {
	bodies := []string{
		`{"instructions":"system text","input":[{"role":"user","content":"start"},{"role":"assistant","content":"answer"},{"role":"user","content":"trigger"}]}`,
		`{"messages":[{"content":"system text","role":"system"},{"content":"start","role":"user"},{"content":"answer","role":"assistant"},{"content":"trigger","role":"user"}]}`,
		`{"system":[{"type":"text","text":"system text","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":[{"type":"text","text":"start"}]},{"role":"assistant","content":[{"type":"text","text":"answer"}]},{"role":"user","content":[{"type":"text","text":"trigger"}]}]}`,
		`{"systemInstruction":{"parts":[{"text":"system text"}]},"contents":[{"role":"user","parts":[{"text":"start"}]},{"role":"model","parts":[{"text":"answer"}]},{"role":"user","parts":[{"text":"trigger"}]}]}`,
		`{"type":"response.create","response":{"instructions":"system text","input":[{"type":"message","role":"human","content":[{"type":"input_text","text":"start"}]},{"type":"message","id":"msg_generated","status":"completed","role":"model","content":[{"type":"output_text","text":"answer","annotations":[]}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"trigger"}]}]}}`,
	}
	expected := RiskSessionBlockKeys(12, nil, []byte(bodies[0]))
	for _, body := range bodies {
		require.Equal(t, expected, RiskSessionBlockKeys(12, nil, []byte(body)), body)
		require.True(t, riskIdentityIntersects(expected, RiskSessionLookupKeys(12, nil, []byte(body))))
	}
	require.Equal(t, RiskSessionBlockKeys(12, nil, []byte(`{"input":"hello"}`)), RiskSessionBlockKeys(12, nil, []byte(`{"input":[{"type":"input_text","text":"hello"}]}`)))
}

func TestRiskSessionIdentityToolResultsDoNotStartUserTurn(t *testing.T) {
	first := []byte(`{"messages":[{"role":"user","content":"start"},{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"calc","input":{"b":2,"a":1}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","content":"three"}]}]}`)
	reordered := []byte(`{"messages":[{"content":"start","role":"user"},{"content":[{"name":"calc","id":"c1","type":"tool_use","input":{"a":1,"b":2}}],"role":"assistant"},{"content":[{"content":"three","type":"tool_result","tool_use_id":"c1"}],"role":"user"}]}`)
	require.Len(t, RiskSessionBlockKeys(1, nil, first), 1, "tool result is not a human rewrite boundary")
	require.Equal(t, RiskSessionBlockKeys(1, nil, first), RiskSessionBlockKeys(1, nil, reordered))
	gemini := []byte(`{"contents":[{"role":"user","parts":[{"text":"start"}]},{"role":"model","parts":[{"functionCall":{"name":"calc","args":{"a":1}}}]},{"role":"user","parts":[{"functionResponse":{"name":"calc","response":{"value":1}}}]}]}`)
	require.Len(t, RiskSessionBlockKeys(1, nil, gemini), 1)
	responses := []byte(`{"input":[{"role":"user","content":"start"},{"type":"function_call","name":"calc","call_id":"c1","arguments":"{}"},{"type":"function_call_output","call_id":"c1","output":"one"},{"role":"user","content":"trigger"}]}`)
	require.Len(t, RiskSessionBlockKeys(1, nil, responses), 2)
}

func TestRiskSessionIdentityLongHistoryRetainsBlockedPrefix(t *testing.T) {
	items := []map[string]string{{"role": "user", "content": "first"}, {"role": "assistant", "content": "answer"}, {"role": "user", "content": "trigger"}}
	initial, err := json.Marshal(map[string]any{"messages": items})
	require.NoError(t, err)
	blocked := RiskSessionBlockKeys(3, nil, initial)
	for i := 0; i < 2048; i++ {
		items = append(items, map[string]string{"role": "user", "content": "padding-" + strconv.Itoa(i)})
	}
	long, err := json.Marshal(map[string]any{"messages": items})
	require.NoError(t, err)
	lookup := RiskSessionLookupKeys(3, nil, long)
	require.Len(t, lookup, len(items))
	require.Contains(t, lookup, blocked[0])
	require.Contains(t, lookup, blocked[1])
}

func TestRiskSessionIdentityMediaExactContentAndNoLeaking(t *testing.T) {
	base := []byte(`{"model":"image-model","prompt":"private-prompt","image":{"url":"https://private.example/a","detail":"high"},"size":"1024x1024"}`)
	reordered := []byte(`{"size":"2048x2048","model":"new-image-model","image":{"detail":"high","url":"https://private.example/a"},"prompt":"private-prompt"}`)
	otherImage := []byte(`{"model":"image-model","prompt":"private-prompt","image":{"url":"https://private.example/b","detail":"high"}}`)
	keys := RiskSessionBlockKeys(555, nil, base)
	require.Len(t, keys, 1)
	require.Equal(t, keys, RiskSessionBlockKeys(555, nil, reordered))
	require.NotEqual(t, keys, RiskSessionBlockKeys(555, nil, otherImage))
	keys = append(keys, RiskSessionBlockKeys(555, riskIdentityContext(map[string]string{"session_id": "secret-session"}), []byte(`{"input":"private-input"}`))...)
	for _, key := range keys {
		require.Len(t, key, 64)
		_, err := hex.DecodeString(key)
		require.NoError(t, err)
		for _, secret := range []string{"private", "secret-session"} {
			require.NotContains(t, key, secret)
		}
	}
	// Different framed messages cannot smuggle an item boundary into text.
	a := []byte(`{"messages":[{"role":"user","content":"one|item=two"}]}`)
	b := []byte(`{"messages":[{"role":"user","content":"one"},{"role":"user","content":"two"}]}`)
	require.NotEqual(t, RiskSessionBlockKeys(555, nil, a), RiskSessionBlockKeys(555, nil, b))
	require.NotPanics(t, func() { RiskSessionLookupKeys(1, &gin.Context{}, []byte(strings.Repeat("x", 1024))) })
}
