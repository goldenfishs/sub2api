package routes

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/modelcheckbridge"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestModelCheckProxySignsResolvedIdentityAndPreservesAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secret := strings.Repeat("s", 32)
	for _, ua := range []string{"", strings.Repeat("A", 512)} {
		t.Run("user-agent-length-"+strings.Repeat("x", len(ua)/512), func(t *testing.T) {
			adminKey := "fixture-admin-key"
			if ua == "" {
				adminKey = ""
			}
			type observed struct {
				identity   modelcheckbridge.Identity
				verified   bool
				header     http.Header
				path, body string
			}
			seen := make(chan observed, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				identity, verified := modelcheckbridge.Verify(secret, r, time.Now())
				body, _ := io.ReadAll(r.Body)
				seen <- observed{identity: identity, verified: verified, header: r.Header.Clone(), path: r.URL.RequestURI(), body: string(body)}
				w.Header().Set("Content-Type", "image/webp")
				w.Header().Set("Location", "/api/v1/model-check/admin/runs/fixture")
				w.Header().Set(modelcheckbridge.Header, r.Header.Get(modelcheckbridge.Header))
				w.WriteHeader(202)
				_, _ = w.Write([]byte("image-fixture"))
			}))
			defer upstream.Close()
			cfg := &config.Config{ModelCheck: config.ModelCheckConfig{Enabled: true, Upstream: upstream.URL, BridgeSecret: secret}}
			cfg.SetTrustForwardedIPForAPIKeyACL(false)
			router := gin.New()
			require.NoError(t, router.SetTrustedProxies([]string{"198.51.100.1"}))
			router.Use(middleware.SessionBindingContext(cfg))
			RegisterModelCheckRoutes(router.Group("/api/v1"), cfg)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/model-check/admin/tests?wait_seconds=120", strings.NewReader(`{"channel_id":"fixture"}`))
			req.RemoteAddr = "198.51.100.1:1234"
			req.Header.Set("User-Agent", ua)
			req.Header.Set("X-Forwarded-For", "203.0.113.8")
			req.Header.Set("CF-Connecting-IP", "192.0.2.99")
			req.Header.Set("Authorization", "Bearer fixture-jwt")
			req.Header.Set("X-Api-Key", adminKey)
			req.Header.Set("Idempotency-Key", "fixture-idempotency")
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Cookie", "private=session")
			req.Header.Set(modelcheckbridge.Header, "forged")
			req.Header.Set("Connection", modelcheckbridge.Header+", X-Forwarded-For")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, 202, w.Code)
			require.Equal(t, "image-fixture", w.Body.String())
			require.Equal(t, "image/webp", w.Header().Get("Content-Type"))
			require.Equal(t, "/api/v1/model-check/admin/runs/fixture", w.Header().Get("Location"))
			require.Empty(t, w.Header().Get(modelcheckbridge.Header))
			actual := <-seen
			require.True(t, actual.verified)
			require.Equal(t, "203.0.113.8", actual.identity.IP)
			require.Equal(t, ua, actual.identity.UserAgent)
			require.Equal(t, req.URL.RequestURI(), actual.path)
			require.Equal(t, `{"channel_id":"fixture"}`, actual.body)
			require.Equal(t, "fixture-idempotency", actual.header.Get("Idempotency-Key"))
			require.Equal(t, adminKey, actual.header.Get("X-Api-Key"))
			require.Contains(t, actual.header, "X-Api-Key", "an empty admin key must not become JWT fallback")
			require.Equal(t, "Bearer fixture-jwt", actual.header.Get("Authorization"))
			for _, name := range []string{"X-Forwarded-For", "CF-Connecting-IP", "Cookie", "Connection"} {
				require.Empty(t, actual.header.Get(name), name)
			}
		})
	}
}

func TestModelCheckProxyDisabledOrUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterModelCheckRoutes(router.Group("/api/v1"), &config.Config{})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/model-check/overview", nil))
	require.Equal(t, 404, w.Code)

	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	upstream.Close()
	cfg := &config.Config{ModelCheck: config.ModelCheckConfig{Enabled: true, Upstream: upstream.URL, BridgeSecret: strings.Repeat("s", 32)}}
	router = gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.Use(middleware.SessionBindingContext(cfg))
	RegisterModelCheckRoutes(router.Group("/api/v1"), cfg)
	w = httptest.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	router.ServeHTTP(w, httptest.NewRequestWithContext(ctx, "GET", "/api/v1/model-check/overview", nil))
	require.Equal(t, 503, w.Code)
	require.Contains(t, w.Body.String(), "model_check_unavailable")
	require.NotContains(t, w.Body.String(), upstream.URL)
	require.NotContains(t, w.Body.String(), cfg.ModelCheck.BridgeSecret)
}
