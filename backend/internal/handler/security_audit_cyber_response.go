package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const auditCyberPolicyResponseKey = "sub2api.local_audit.cyber_response"

func auditCyberPolicyEnabled(decision *securityaudit.Decision) bool {
	return decision != nil && decision.Kind == securityaudit.DecisionBlock && !decision.AllowNextStage &&
		decision.ClientResponse != nil && decision.ClientResponse.CyberPolicy
}

func writeAuditCyberPolicyHTTP(c *gin.Context, decision *securityaudit.Decision) bool {
	if !auditCyberPolicyEnabled(decision) {
		return false
	}
	// Keep the local audit code in operational logs. In particular, never call
	// MarkOpsCyberPolicy: that is reserved for actual upstream evidence and bans.
	service.SetOpsLocalErrorType(c, "permission_error", securityAuditErrorCode(decision))
	c.Set(auditCyberPolicyResponseKey, true)
	c.Header("X-Sub2API-Policy-Source", "local_audit")
	client := decision.ClientResponse
	if client.Protocol == service.ContentModerationProtocolOpenAIResponses && client.Stream {
		service.MarkOpsStreamErrorValue(c, service.OpsStreamError{
			ErrType: "permission_error", Code: securityAuditErrorCode(decision),
			Message: securityAuditMessage(decision), IntendedStatus: http.StatusBadRequest,
			RequestScoped: true,
		})
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("X-Accel-Buffering", "no")
		c.Status(http.StatusOK)
		for _, event := range auditCyberPolicyEvents(decision) {
			c.SSEvent(event["type"].(string), event)
		}
		c.Writer.Flush()
		return true
	}
	c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
		"type": "invalid_request_error", "code": "cyber_policy", "param": nil,
		"message": securityAuditMessage(decision),
	}})
	return true
}

func auditCyberPolicyEvents(decision *securityaudit.Decision) []gin.H {
	id := "resp_local_audit_" + uuid.NewString()
	createdAt := time.Now().Unix()
	response := func(status string, policyError any) gin.H {
		return gin.H{
			"id": id, "object": "response", "created_at": createdAt,
			"status": status, "model": decision.ClientResponse.Model,
			"output": []any{}, "error": policyError, "usage": nil,
		}
	}
	return []gin.H{
		{"type": "response.created", "sequence_number": 0, "response": response("in_progress", nil)},
		{"type": "response.failed", "sequence_number": 1, "response": response("failed", gin.H{
			"code": "cyber_policy", "message": securityAuditMessage(decision),
		})},
	}
}

func writeAuditCyberPolicyWS(ctx context.Context, conn *coderws.Conn, decision *securityaudit.Decision) {
	if ctx == nil {
		ctx = context.Background()
	}
	writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for _, event := range auditCyberPolicyEvents(decision) {
		payload, err := json.Marshal(event)
		if err != nil || conn.Write(writeCtx, coderws.MessageText, payload) != nil {
			return
		}
	}
}
