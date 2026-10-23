package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type riskHandlerRepo struct {
	service.RiskSessionRepository
	actor  int64
	reason string
}

func (r *riskHandlerRepo) Get(context.Context, int64) (*service.RiskSessionIncident, error) {
	return &service.RiskSessionIncident{ID: 4, SnapshotStatus: "expired", UserID: 7}, nil
}
func (r *riskHandlerRepo) Release(_ context.Context, _ int64, actor int64, reason string) (*service.RiskSessionIncident, error) {
	r.actor = actor
	r.reason = reason
	return &service.RiskSessionIncident{ID: 4, Blocked: false}, nil
}
func (r *riskHandlerRepo) UpdateRetention(context.Context, int) error { return nil }

func TestRiskSessionAdminDetailNoStoreAndReleaseActor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &riskHandlerRepo{}
	svc := service.NewRiskSessionService(nil, repo, nil)
	h := NewRiskSessionHandler(svc)
	router := gin.New()
	router.GET("/sessions/:id", h.Get)
	router.POST("/sessions/:id/release", h.Release)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sessions/4", nil))
	require.Equal(t, 200, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/sessions/4/release", strings.NewReader(`{"reason":"ok"}`)))
	require.Equal(t, 401, w.Code)
	require.Zero(t, repo.actor)
	authenticated := gin.New()
	authenticated.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 91})
		c.Next()
	})
	authenticated.POST("/sessions/:id/release", h.Release)
	w = httptest.NewRecorder()
	authenticated.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/sessions/4/release", strings.NewReader(`{"reason":"reviewed"}`)))
	require.Equal(t, 200, w.Code)
	require.EqualValues(t, 91, repo.actor)
	require.Equal(t, "reviewed", repo.reason)
}
