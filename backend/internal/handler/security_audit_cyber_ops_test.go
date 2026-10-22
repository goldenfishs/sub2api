package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAuditCyberResponseOpsMiddlewareKeepsLocalAttribution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name   string
		stream bool
		prompt bool
	}{
		{name: "keyword JSON"},
		{name: "keyword SSE", stream: true},
		{name: "prompt JSON", prompt: true},
		{name: "prompt SSE", stream: true, prompt: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupOpsErrorLogTestQueue(t, 2)
			ops := service.NewOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			svc, _ := newAuditCyberResponseService(t, true)
			var coordinator *securityaudit.Coordinator
			localCode := "content_policy_violation"
			if tc.prompt {
				coordinator = securityaudit.NewCoordinator(nil, &handlerPromptEngine{
					mode:     securityaudit.ModeBlocking,
					decision: &securityaudit.PromptDecision{Kind: securityaudit.DecisionBlock},
				})
				localCode = securityaudit.ErrorCodeBlocked
			}
			router := gin.New()
			router.Use(OpsErrorLoggerMiddleware(ops))
			router.POST("/v1/responses", func(c *gin.Context) {
				setOpsRequestContext(c, "gpt-test", tc.stream)
				body := []byte(`{"input":"audit-test-marker","stream":false}`)
				if tc.stream {
					body = []byte(`{"input":"audit-test-marker","stream":true}`)
				}
				decision := runSecurityAudit(c, nil, coordinator, svc, nil, middleware.AuthSubject{UserID: 7},
					service.ContentModerationProtocolOpenAIResponses, "gpt-test", body, "http")
				require.True(t, writeAuditCyberPolicyHTTP(c, decision))
				require.Nil(t, service.GetOpsCyberPolicy(c), "local blocks are not upstream policy evidence")
			})
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
			if tc.stream {
				require.Equal(t, http.StatusOK, recorder.Code)
				require.Contains(t, recorder.Body.String(), "response.failed")
			} else {
				require.Equal(t, http.StatusBadRequest, recorder.Code)
				require.Equal(t, "cyber_policy", gjson.GetBytes(recorder.Body.Bytes(), "error.code").String())
			}
			require.Equal(t, "local_audit", recorder.Header().Get("X-Sub2API-Policy-Source"))
			require.Equal(t, int64(1), OpsErrorLogQueueLength(), "each local block must produce exactly one ops entry")
			entry := (<-opsErrorLogQueue).entry
			require.Equal(t, http.StatusBadRequest, entry.StatusCode)
			require.Equal(t, tc.stream, entry.Stream)
			require.Equal(t, "permission_error", entry.ErrorType)
			require.Equal(t, "request", entry.ErrorPhase)
			require.Equal(t, "client", entry.ErrorOwner)
			require.Equal(t, "client_request", entry.ErrorSource)
			require.Nil(t, entry.AccountID)
			require.Empty(t, entry.UpstreamModel)
			require.Nil(t, entry.UpstreamStatusCode)
			require.Nil(t, entry.UpstreamErrorMessage)
			require.Nil(t, entry.UpstreamErrorDetail)
			if tc.stream {
				require.True(t, entry.IsBusinessLimited, "a local audit refusal must not count as an upstream outage")
				require.Equal(t, localCode, gjson.Get(entry.ErrorBody, "error.code").String())
			}
		})
	}
}
