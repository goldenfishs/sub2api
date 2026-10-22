package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func newAuditCyberResponseService(t *testing.T, enabled bool) (*service.ContentModerationService, *contentModerationHandlerTestRepo) {
	t.Helper()
	cfg := service.ContentModerationConfig{
		Enabled: true, Mode: service.ContentModerationModePreBlock, AllGroups: true, SampleRate: 100,
		BlockStatus: 403, BlockMessage: "Local audit blocked this request", BlockAsCyberPolicy: enabled,
		KeywordBlockingMode: service.ContentModerationKeywordModeKeywordOnly, BlockedKeywords: []string{"audit-test-marker"},
		AutoBanEnabled: true, AutoBanOnlyCyberPolicy: true,
	}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	settings := &contentModerationHandlerSettingRepo{values: map[string]string{
		service.SettingKeyRiskControlEnabled: "true", service.SettingKeyContentModerationConfig: string(raw),
	}}
	repo := &contentModerationHandlerTestRepo{}
	svc := service.NewContentModerationService(settings, repo, nil, nil, nil, nil, nil, nil)
	return svc, repo
}

func TestAuditCyberResponseHTTPGate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, enabled := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			name := "json"
			if stream {
				name = "stream"
			}
			if enabled {
				name += "_cyber"
			}
			t.Run(name, func(t *testing.T) {
				svc, repo := newAuditCyberResponseService(t, enabled)
				h := &OpenAIGatewayHandler{
					gatewayService: &service.OpenAIGatewayService{}, billingCacheService: &service.BillingCacheService{},
					apiKeyService: &service.APIKeyService{}, contentModerationService: svc,
					securityAuditCoordinator: securityaudit.NewCoordinator(securityaudit.NewLegacyModerationAdapter(svc), nil),
					concurrencyHelper:        NewConcurrencyHelper(service.NewConcurrencyService(&concurrencyCacheMock{}), SSEPingFormatNone, time.Second),
				}
				router := gin.New()
				router.Use(securityAuditMediaTestMiddleware)
				var captured *gin.Context
				router.POST("/v1/responses", func(c *gin.Context) { captured = c; h.Responses(c) })
				body, err := json.Marshal(map[string]any{"model": "gpt-test", "input": "audit-test-marker", "stream": stream})
				require.NoError(t, err)
				recorder := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
				request.Header.Set("Content-Type", "application/json")
				router.ServeHTTP(recorder, request)
				// The empty scheduling/billing dependencies cannot forward; reaching the
				// intended block proves the real gate returned before those side effects.
				if !enabled {
					require.Equal(t, 403, recorder.Code)
					require.Contains(t, recorder.Body.String(), "content_policy_violation")
				} else if stream {
					require.Equal(t, 200, recorder.Code)
					require.Contains(t, recorder.Header().Get("Content-Type"), "text/event-stream")
					var events []string
					for _, line := range strings.Split(recorder.Body.String(), "\n") {
						if strings.HasPrefix(line, "data:") {
							events = append(events, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
						}
					}
					require.Len(t, events, 2)
					require.Equal(t, "response.created", gjson.Get(events[0], "type").String())
					require.Equal(t, "response.failed", gjson.Get(events[1], "type").String())
					require.Equal(t, "failed", gjson.Get(events[1], "response.status").String())
					require.Equal(t, "cyber_policy", gjson.Get(events[1], "response.error.code").String())
					require.Equal(t, gjson.Get(events[0], "response.id").String(), gjson.Get(events[1], "response.id").String())
					mark, ok := service.GetOpsStreamError(captured)
					require.True(t, ok)
					require.Equal(t, "content_policy_violation", mark.Code)
					require.False(t, mark.CountTowardsSLA)
				} else {
					require.Equal(t, 400, recorder.Code)
					require.Equal(t, "cyber_policy", gjson.GetBytes(recorder.Body.Bytes(), "error.code").String())
				}
				require.Nil(t, service.GetOpsCyberPolicy(captured), "local formatting must not become upstream ban evidence")
				require.Eventually(t, func() bool { return len(repo.logSnapshot()) == 1 }, time.Second, 10*time.Millisecond)
				require.Equal(t, service.ContentModerationActionKeywordBlock, repo.logSnapshot()[0].Action)
			})
		}
	}
}

func TestAuditCyberResponseOnlyMapsBlocksAndOpenAIProtocols(t *testing.T) {
	svc, _ := newAuditCyberResponseService(t, true)
	for _, kind := range []securityaudit.DecisionKind{securityaudit.DecisionBlock, securityaudit.DecisionAllow, securityaudit.DecisionFlag, securityaudit.DecisionInvalid, securityaudit.DecisionUnavailable} {
		for _, protocol := range []string{service.ContentModerationProtocolOpenAIResponses, service.ContentModerationProtocolOpenAIChat, service.ContentModerationProtocolOpenAIImages, service.ContentModerationProtocolAnthropicMessages, service.ContentModerationProtocolGemini} {
			t.Run(string(kind)+"/"+protocol, func(t *testing.T) {
				c, recorder := securityAuditErrorTestContext(t)
				engine := &handlerPromptEngine{mode: securityaudit.ModeBlocking, decision: &securityaudit.PromptDecision{Kind: kind}}
				decision := runSecurityAudit(c, nil, securityaudit.NewCoordinator(nil, engine), svc, nil, middleware.AuthSubject{}, protocol, "gpt-test", []byte(`{"stream":true}`), "http")
				want := kind == securityaudit.DecisionBlock && strings.HasPrefix(protocol, "openai_")
				require.Equal(t, want, auditCyberPolicyEnabled(decision))
				if want && protocol != service.ContentModerationProtocolOpenAIResponses {
					(&GatewayHandler{}).openAISecurityAuditError(c, decision)
					require.Equal(t, 400, recorder.Code, "Chat and Images must keep a JSON error envelope")
					require.Equal(t, "cyber_policy", gjson.GetBytes(recorder.Body.Bytes(), "error.code").String())
				}
			})
		}
	}
}

func TestAuditCyberResponseWebSocket(t *testing.T) {
	svc, _ := newAuditCyberResponseService(t, true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		c, _ := gin.CreateTestContext(w)
		c.Request = r
		decision := runSecurityAudit(c, nil, nil, svc, nil, middleware.AuthSubject{UserID: 7}, service.ContentModerationProtocolOpenAIResponses, "gpt-test", []byte(`{"type":"response.create","input":"audit-test-marker"}`), "first_turn")
		writeSecurityAuditWSError(r.Context(), conn, decision)
		_ = conn.Close(securityAuditWSCloseStatus(decision), securityAuditWSCloseReason(decision))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer func() { _ = conn.CloseNow() }()
	_, created, err := conn.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, "response.created", gjson.GetBytes(created, "type").String())
	_, failed, err := conn.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, "response.failed", gjson.GetBytes(failed, "type").String())
	require.Equal(t, "cyber_policy", gjson.GetBytes(failed, "response.error.code").String())
	_, _, err = conn.Read(ctx)
	require.Equal(t, coderws.StatusPolicyViolation, coderws.CloseStatus(err))
}
