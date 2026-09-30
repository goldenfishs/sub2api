package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGPT61SolIdentityAndManifest(t *testing.T) {
	require.Contains(t, openai.DefaultModelIDs(), "gpt-6.1-sol")
	for _, model := range []string{"gpt-6.1-sol", "openai/gpt-6.1-sol", "gpt-6.1-sol-max"} {
		require.True(t, openai.IsGPT61SolModelSpelling(model))
		require.Equal(t, "gpt-6.1-sol", normalizeKnownOpenAICodexModel(model))
		require.Equal(t, "gpt-6.1-sol", normalizeCodexModel(model))
		require.True(t, isOpenAIGPT6Model(model))
		require.True(t, shouldAutoInjectPromptCacheKeyForCompat(model))
	}
	for _, model := range []string{"gpt-6.1-sol-none", "gpt-6.1-sol-minimal", "gpt-6.1-sol-preview", "gpt-6.1-solitude"} {
		require.False(t, openai.IsGPT61SolModelSpelling(model))
	}
	descriptor := newConfiguredCodexModelDescriptor("gpt-6.1-sol")
	require.Equal(t, "GPT-6.1 Sol", descriptor.DisplayName)
	require.EqualValues(t, 1050000, descriptor.ContextWindow)
	require.EqualValues(t, 1050000, descriptor.MaxContextWindow)
	require.Equal(t, "medium", *descriptor.DefaultReasoningLevel)
	efforts := []string{}
	for _, level := range descriptor.SupportedReasoningLevels {
		efforts = append(efforts, level.Effort)
	}
	require.Equal(t, []string{"low", "medium", "high", "xhigh", "max"}, efforts)
	require.True(t, descriptor.SupportsParallelToolCalls)
	for _, effort := range efforts {
		require.Equal(t, effort, normalizeOpenAIReasoningEffortForModel(effort, "gpt-6.1-sol"))
	}
}

func TestGPT61SolRejectsUnsupportedReasoningBeforeUpstream(t *testing.T) {
	for _, route := range []string{"responses", "chat/completions"} {
		for _, effort := range []string{"none", "minimal"} {
			body := []byte(`{"model":"public","reasoning":{"effort":"` + effort + `"},"reasoning_effort":"` + effort + `","input":"hi","messages":[{"role":"user","content":"hi"}]}`)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+route, bytes.NewReader(body))
			svc := &OpenAIGatewayService{cfg: &config.Config{}}
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://relay.example/v1", "model_mapping": map[string]any{"public": "gpt-6.1-sol"}}}
			var err error
			if route == "responses" {
				_, err = svc.Forward(context.Background(), c, account, body)
			} else {
				_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
			}
			require.ErrorContains(t, err, "gpt-6.1-sol supports reasoning")
			require.Equal(t, http.StatusBadRequest, rec.Code)
		}
	}
}

func TestGPT61SolRawChatKeepsIDAndRejectsTools(t *testing.T) {
	for _, tools := range []bool{false, true} {
		body := []byte(`{"model":"gpt-6.1-sol","reasoning_effort":"high","messages":[{"role":"user","content":"hello"}]}`)
		if tools {
			body = []byte(`{"model":"gpt-6.1-sol","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup"}}]}`)
		}
		upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"chat_1","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))}}
		svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
		account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{"api_key": "fixture", "base_url": "https://api.openai.com"}}
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
		_, err := svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
		if tools {
			require.ErrorContains(t, err, "Responses-capable")
			require.Equal(t, 400, rec.Code)
			require.Nil(t, upstream.lastReq)
		} else {
			require.NoError(t, err)
			require.Equal(t, "gpt-6.1-sol", gjson.GetBytes(upstream.lastBody, "model").String())
			require.Equal(t, "high", gjson.GetBytes(upstream.lastBody, "reasoning_effort").String())
		}
	}
}

func TestGPT61SolToolHistoryDetection(t *testing.T) {
	for _, messages := range []string{`[{"role":"tool","content":"result"}]`, `[{"role":"function","content":"result"}]`, `[{"role":"assistant","tool_calls":[{"id":"a"}]}]`, `[{"role":"assistant","function_call":{"name":"lookup"}}]`} {
		require.True(t, hasGPT61SolChatTools([]byte(`{"messages":`+messages+`}`)))
	}
	require.False(t, hasGPT61SolChatTools([]byte(`{"messages":[{"role":"assistant","content":"hello"}]}`)))
}

func TestGPT61SolMessagesDisabledThinkingAndWebSocketValidation(t *testing.T) {
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "fixture", "base_url": "https://api.openai.com", "model_mapping": map[string]any{"public": "gpt-6.1-sol"}}}
	body := []byte(`{"model":"public","max_tokens":1000,"thinking":{"type":"disabled"},"messages":[{"role":"user","content":"hi"}]}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	_, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")
	require.ErrorContains(t, err, "gpt-6.1-sol supports reasoning")
	require.Equal(t, 400, rec.Code)
	_, _, err = normalizeOpenAIResponsesWebSocketCompatibilityBody([]byte(`{"type":"response.create","model":"public","reasoning":{"effort":"none"}}`), account, false)
	require.ErrorContains(t, err, "gpt-6.1-sol supports reasoning")
}

func TestGPT61SolWSHTTPBridgeRejectsNoneBeforeFiltering(t *testing.T) {
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://relay.example/v1", "model_mapping": map[string]any{"public": "gpt-6.1-sol"}}}
	_, err := prepareOpenAIWSHTTPBridgeBody(account, []byte(`{"type":"response.create","model":"public","reasoning":{"effort":"none"},"input":"hello"}`))
	require.ErrorContains(t, err, "gpt-6.1-sol supports reasoning")
}
