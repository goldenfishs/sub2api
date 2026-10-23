package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	riskSessionBlockedCode = "session_permanently_blocked"
	riskSessionRequestKey  = "sub2api.risk_session.request"
	riskSessionReleasedKey = "sub2api.risk_session.released"
)

// This reference lives only as long as the request/WS turn. It is not a rolling
// conversation recorder. The service copies/compresses it only on a real hit.
type riskSessionRequest struct {
	body            []byte
	identityBody    []byte
	protocol, stage string
	scope           string
}

func riskSessionApplies(ctx context.Context, legacy *service.ContentModerationService, apiKey *service.APIKey, userID int64) (bool, error) {
	if legacy == nil || legacy.RiskSessions() == nil || userID <= 0 || (apiKey != nil && apiKey.User != nil && apiKey.User.IsAdmin()) {
		return false, nil
	}
	cfg, err := legacy.RiskSessions().Config(ctx)
	if err != nil || !cfg.Enabled {
		return false, err
	}
	return legacy.RiskSessionApplies(ctx, userID)
}

func riskSessionUnavailable() *securityaudit.Decision {
	return &securityaudit.Decision{Kind: securityaudit.DecisionUnavailable, HTTPStatus: http.StatusServiceUnavailable,
		ErrorCode: "risk_session_store_unavailable", ClientMessage: "会话风控暂时不可用，请稍后重试", AllowNextStage: false}
}

func checkPermanentRiskSession(c *gin.Context, reqLog *zap.Logger, legacy *service.ContentModerationService, apiKey *service.APIKey, subject middleware2.AuthSubject, protocol, model string, body []byte, stage string) *securityaudit.Decision {
	c.Set(riskSessionReleasedKey, false)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	applies, err := riskSessionApplies(ctx, legacy, apiKey, subject.UserID)
	if err != nil {
		if reqLog != nil {
			reqLog.Warn("risk_session.admission_unavailable", zap.Error(err))
		}
		return riskSessionUnavailable()
	}
	if !applies {
		return nil
	}
	// Replace on every WebSocket frame so an upstream hit never archives a
	// previous turn. HTTP internal protocol adapters retain the first audit body.
	snapshot := riskSessionSnapshot(c, body, protocol, stage)
	c.Set(riskSessionRequestKey, snapshot)
	keys := service.RiskSessionLookupKeys(subject.UserID, c, body)
	incident, err := legacy.RiskSessions().FindBlocked(ctx, subject.UserID, keys)
	if err != nil {
		if reqLog != nil {
			reqLog.Warn("risk_session.lookup_failed", zap.Error(err))
		}
		return riskSessionUnavailable()
	}
	if incident == nil {
		released, err := legacy.RiskSessions().WasReleased(ctx, subject.UserID, keys)
		if err != nil {
			return riskSessionUnavailable()
		}
		c.Set(riskSessionReleasedKey, released)
		return nil
	}
	c.Header("X-Sub2API-Risk-Session", strconv.FormatInt(incident.ID, 10))
	service.SetOpsLocalErrorType(c, "permission_error", riskSessionBlockedCode)
	return &securityaudit.Decision{Kind: securityaudit.DecisionBlock, HTTPStatus: http.StatusForbidden,
		ErrorCode: riskSessionBlockedCode, ClientMessage: "该会话已永久屏蔽，请联系管理员处理 / This conversation is permanently blocked", AllowNextStage: false}
}

func riskSessionSnapshot(c *gin.Context, body []byte, protocol, stage string) riskSessionRequest {
	snapshot := riskSessionRequest{body: body, identityBody: body, protocol: protocol, stage: stage, scope: "audit_payload_only"}
	if isSecurityAuditWebSocketStage(stage) {
		snapshot.scope = "websocket_turn_only"
		return snapshot
	}
	if original := pkghttputil.OriginalRequestBody(c.Request); original != nil {
		snapshot.body = original
		snapshot.scope = "received_request_only"
	}
	return snapshot
}

func captureLocalRiskSession(c *gin.Context, reqLog *zap.Logger, legacy *service.ContentModerationService, apiKey *service.APIKey, subject middleware2.AuthSubject, protocol, model string, body []byte, stage string, decision *securityaudit.Decision) {
	// Persist before releasing the blocked response, so an immediate client retry
	// observes the block. A canceled client must not cancel the evidence write.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 5*time.Second)
	defer cancel()
	applies, err := riskSessionApplies(ctx, legacy, apiKey, subject.UserID)
	if err != nil || !applies {
		return
	}
	input := buildContentModerationInput(c, apiKey, subject, protocol, model, body)
	snapshot := riskSessionSnapshot(c, body, protocol, stage)
	responseBody, err := json.Marshal(gin.H{"error": gin.H{"code": securityAuditErrorCode(decision), "message": securityAuditMessage(decision)}, "source": "local_audit"})
	if err != nil {
		return
	}
	err = legacy.RiskSessions().Capture(ctx, service.RiskSessionCaptureInput{
		UserID: input.UserID, APIKeyID: input.APIKeyID, GroupID: input.GroupID,
		UserEmail: input.UserEmail, APIKeyName: input.APIKeyName, RequestID: riskSessionRequestID(c, input.RequestID, stage),
		Source: "local_audit", Reason: securityAuditErrorCode(decision), Model: input.Model, Endpoint: input.Endpoint,
		Keys: service.RiskSessionBlockKeys(input.UserID, c, body), RequestBody: snapshot.body, ResponseBody: responseBody,
		SnapshotScope: snapshot.scope, ResponseScope: "local_rejection_summary",
	})
	if err != nil {
		c.Header("X-Sub2API-Snapshot-Status", "unavailable")
		if reqLog != nil {
			reqLog.Error("risk_session.capture_failed", zap.Error(err))
		}
	}
}

func riskSessionRequestID(c *gin.Context, requestID, stage string) string {
	if isSecurityAuditWebSocketStage(stage) {
		turn, _ := securityAuditWSTurn(c)
		return requestID + ":ws:" + strconv.Itoa(turn)
	}
	return requestID
}

// Returns true only after durable capture succeeds. On write failure the
// existing Redis block remains a fallback; the upstream decision is unchanged.
func (h *OpenAIGatewayHandler) captureUpstreamRiskSession(c *gin.Context, apiKey *service.APIKey, model string, fallbackBody []byte, mark *service.CyberPolicyMark) bool {
	if apiKey == nil || c == nil || c.Request == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 5*time.Second)
	defer cancel()
	applies, err := riskSessionApplies(ctx, h.contentModerationService, apiKey, apiKey.UserID)
	if err != nil || !applies {
		return false
	}
	snapshot := riskSessionSnapshot(c, fallbackBody, service.ContentModerationProtocolOpenAIResponses, "http")
	if raw, ok := c.Get(riskSessionRequestKey); ok {
		if current, ok := raw.(riskSessionRequest); ok {
			snapshot = current
		}
	}
	userEmail := ""
	if apiKey.User != nil {
		userEmail = apiKey.User.Email
	}
	responseBody := mark.RawBody
	responseScope := mark.RawBodyScope
	if len(responseBody) == 0 {
		responseScope = "upstream_error_summary"
		responseBody = []byte(mark.Body)
		if strings.TrimSpace(mark.Body) == "" {
			responseBody, _ = json.Marshal(gin.H{"error": gin.H{"code": "cyber_policy", "message": mark.Message}})
		}
		if mark.RawBodyBytes > 0 {
			responseScope = "upstream_error_summary_oversize"
		}
	}
	err = h.contentModerationService.RiskSessions().Capture(ctx, service.RiskSessionCaptureInput{
		UserID: apiKey.UserID, APIKeyID: apiKey.ID, GroupID: apiKey.GroupID,
		UserEmail: userEmail, APIKeyName: apiKey.Name,
		RequestID: riskSessionRequestID(c, contentModerationRequestID(c.Request.Context()), snapshot.stage),
		Source:    "upstream_cyber", Reason: "cyber_policy", Model: model, Endpoint: GetInboundEndpoint(c),
		Keys: service.RiskSessionBlockKeys(apiKey.UserID, c, snapshot.identityBody), RequestBody: snapshot.body, ResponseBody: responseBody,
		SnapshotScope: snapshot.scope, ResponseScope: responseScope,
	})
	if err != nil {
		slog.Error("risk_session.upstream_capture_failed", "user_id", apiKey.UserID, "error", err)
	}
	return err == nil
}
