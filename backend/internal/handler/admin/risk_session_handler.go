package admin

import (
	"errors"
	"io"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type RiskSessionHandler struct{ service *service.RiskSessionService }

func NewRiskSessionHandler(svc *service.RiskSessionService) *RiskSessionHandler {
	return &RiskSessionHandler{service: svc}
}
func (h *RiskSessionHandler) GetConfig(c *gin.Context) {
	cfg, err := h.service.Config(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cfg)
}
func (h *RiskSessionHandler) UpdateConfig(c *gin.Context) {
	middleware.SetAuditAction(c, "admin.risk_sessions.config.update")
	var cfg service.RiskSessionConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	updated, err := h.service.UpdateConfig(c.Request.Context(), cfg)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"enabled": updated.Enabled})
	response.Success(c, updated)
}
func (h *RiskSessionHandler) List(c *gin.Context) {
	page, size := response.ParsePagination(c)
	if size > 100 {
		size = 100
	}
	items, total, err := h.service.List(c.Request.Context(), page, size)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, items, total, page, size)
}
func (h *RiskSessionHandler) Get(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid incident id")
		return
	}
	item, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, item)
}
func (h *RiskSessionHandler) Release(c *gin.Context) {
	middleware.SetAuditAction(c, "admin.risk_sessions.release")
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid incident id")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		response.BadRequest(c, "Invalid request")
		return
	}
	item, err := h.service.Release(c.Request.Context(), id, subject.UserID, req.Reason)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, item)
}
