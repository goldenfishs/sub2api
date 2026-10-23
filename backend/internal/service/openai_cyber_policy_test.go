package service

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCyberPolicyRawEvidencePreservesFullEventAndTurnBoundary(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	payload, err := json.Marshal(map[string]any{
		"type":     "response.failed",
		"response": map[string]any{"error": map[string]any{"code": "cyber_policy", "message": "refused"}, "evidence": strings.Repeat("x", 12*1024)},
	})
	require.NoError(t, err)
	expected := bytes.Clone(payload)
	require.True(t, markOpenAICyberPolicyEvent(c, payload, 200, nil))
	mark := GetOpsCyberPolicy(c)
	require.Equal(t, expected, mark.RawBody)
	require.EqualValues(t, len(expected), mark.RawBodyBytes)
	require.Equal(t, "upstream_error_event", mark.RawBodyScope)
	require.Len(t, mark.Body, 4096, "legacy logging remains bounded")
	// The SSE/WS decoder can recycle its input storage without corrupting evidence.
	copy(payload, []byte("corrupted"))
	require.Equal(t, expected, mark.RawBody)
	MarkOpsCyberPolicy(c, CyberPolicyMark{RawBody: []byte("other event"), RawBodyScope: "upstream_error_event"})
	require.Equal(t, expected, GetOpsCyberPolicy(c).RawBody, "first hit owns the turn")
	serialized, err := json.Marshal(mark)
	require.NoError(t, err)
	require.NotContains(t, string(serialized), "RawBody")
	require.NotContains(t, string(serialized), strings.Repeat("x", 5000))
	ClearOpsCyberPolicy(c)
	require.Nil(t, GetOpsCyberPolicy(c))
	next := []byte(`{"error":{"code":"cyber_policy","message":"next turn"}}`)
	require.True(t, markOpenAICyberPolicyEvent(c, next, 400, nil))
	require.Equal(t, next, GetOpsCyberPolicy(c).RawBody)
	require.Equal(t, "upstream_error_body_bounded", GetOpsCyberPolicy(c).RawBodyScope)
}

func TestCyberPolicyRawEvidenceOversizeIsUnavailableNotTruncated(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := bytes.Repeat([]byte("x"), RiskSessionSnapshotMaxBytes+1)
	MarkOpsCyberPolicy(c, CyberPolicyMark{Body: "legacy summary", RawBody: body, RawBodyScope: "upstream_error_event"})
	mark := GetOpsCyberPolicy(c)
	require.Nil(t, mark.RawBody)
	require.EqualValues(t, len(body), mark.RawBodyBytes)
	require.Equal(t, "upstream_error_event_oversize_unavailable", mark.RawBodyScope)
	require.Equal(t, "legacy summary", mark.Body)
}

func TestMarkAndGetOpsCyberPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	require.Nil(t, GetOpsCyberPolicy(c), "no mark initially")

	MarkOpsCyberPolicy(c, CyberPolicyMark{
		Code:           "cyber_policy",
		Message:        "This request was flagged for cyber policy.",
		Body:           `{"error":{"code":"cyber_policy"}}`,
		UpstreamStatus: 400,
	})

	got := GetOpsCyberPolicy(c)
	require.NotNil(t, got)
	require.Equal(t, "cyber_policy", got.Code)
	require.Equal(t, 400, got.UpstreamStatus)
}

func TestMarkOpsCyberPolicyFirstWins(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	MarkOpsCyberPolicy(c, CyberPolicyMark{Code: "cyber_policy", Message: "first"})
	MarkOpsCyberPolicy(c, CyberPolicyMark{Code: "cyber_policy", Message: "second"})
	require.Equal(t, "first", GetOpsCyberPolicy(c).Message, "first mark wins, later marks ignored")
}

func TestMarkOpsCyberPolicyNilContext(t *testing.T) {
	MarkOpsCyberPolicy(nil, CyberPolicyMark{Code: "cyber_policy"})
	require.Nil(t, GetOpsCyberPolicy(nil))
}

// TestClearOpsCyberPolicy_AllowsRemark verifies F1: after Clear, Get returns nil
// and a subsequent Mark takes effect (per-turn lifecycle in WS connections).
func TestClearOpsCyberPolicy_AllowsRemark(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	MarkOpsCyberPolicy(c, CyberPolicyMark{Message: "first", UpstreamStatus: 200})
	require.NotNil(t, GetOpsCyberPolicy(c))

	ClearOpsCyberPolicy(c)
	require.Nil(t, GetOpsCyberPolicy(c), "mark must be invisible after Clear")

	MarkOpsCyberPolicy(c, CyberPolicyMark{Message: "second", UpstreamStatus: 400})
	got := GetOpsCyberPolicy(c)
	require.NotNil(t, got, "re-mark after Clear must take effect")
	require.Equal(t, "second", got.Message)
}

func TestDetectOpenAICyberPolicy(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		hit     bool
		msg     string
	}{
		{"top-level error", `{"error":{"code":"cyber_policy","message":"flagged"}}`, true, "flagged"},
		{"response-wrapped", `{"response":{"error":{"code":"cyber_policy","message":"  bad  "}}}`, true, "bad"},
		{"case-insensitive", `{"error":{"code":"Cyber_Policy"}}`, true, ""},
		{"content_policy not cyber", `{"error":{"code":"content_policy","message":"x"}}`, false, ""},
		{"safety message not cyber", `{"error":{"type":"safety_error","message":"high-risk cyber activity"}}`, false, ""},
		{"empty", ``, false, ""},
		{"upstream_error", `{"error":{"code":"upstream_error"}}`, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hit, code, msg := detectOpenAICyberPolicy([]byte(tc.payload))
			require.Equal(t, tc.hit, hit)
			if tc.hit {
				require.Equal(t, "cyber_policy", code)
				require.Equal(t, tc.msg, msg)
			}
		})
	}
}
