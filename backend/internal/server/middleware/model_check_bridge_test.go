//go:build unit

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/modelcheckbridge"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestModelCheckBridgeKeepsJWTAndSessionBindingEnforcement(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, path, method, remote, signedIP string
		unsigned, invalidJWT, disabled       bool
		want                                 int
	}{
		{name: "bound session auth", path: "/api/v1/auth/me", want: 200},
		{name: "bound session key list", path: "/api/v1/keys", want: 200},
		{name: "bound session key lookup", path: "/api/v1/keys/13", want: 200},
		{name: "signed bridge is not JWT authority", invalidJWT: true, want: 401},
		{name: "different signed IP still invalidates bound session", signedIP: "203.0.113.9", want: 401},
		{name: "unsigned loopback cannot restore identity", unsigned: true, want: 401},
		{name: "remote caller cannot use even valid bridge signature", remote: "198.51.100.7:1234", want: 401},
		{name: "disabled bridge cannot restore identity", disabled: true, want: 401},
		{name: "unlisted endpoint cannot restore identity", path: "/api/v1/user", want: 401},
		{name: "mutation cannot restore identity", path: "/api/v1/keys", method: "POST", want: 401},
		{name: "normal public authentication is unchanged", unsigned: true, remote: "203.0.113.8:1234", want: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{ModelCheck: config.ModelCheckConfig{Enabled: !tc.disabled, BridgeSecret: strings.Repeat("s", 32)}}
			cfg.JWT.Secret = "test-jwt-secret-32bytes-long!!!"
			cfg.JWT.AccessTokenExpireMinutes = 60
			cfg.SetTrustForwardedIPForAPIKeyACL(false)
			user := &service.User{ID: 2, Email: "fixture@example.com", Role: "user", Status: service.StatusActive, TokenVersion: 1, TokenVersionResolved: true}
			repo := &stubJWTUserRepo{users: map[int64]*service.User{2: user}}
			auth := service.NewAuthService(nil, repo, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
			settings := service.NewSettingService(fakeSettingRepo{values: map[string]string{service.SettingKeySessionBindingEnabled: "true"}}, cfg)
			ua := strings.Repeat("A", 512)
			binding := &service.SessionBinding{IP: "203.0.113.8", UserAgent: ua}
			token, err := auth.GenerateToken(service.WithSessionBinding(context.Background(), binding), user)
			require.NoError(t, err)
			if tc.invalidJWT {
				token = "invalid-token"
			}
			path, method := tc.path, tc.method
			if path == "" {
				path = "/api/v1/auth/me"
			}
			if method == "" {
				method = http.MethodGet
			}
			router := gin.New()
			require.NoError(t, router.SetTrustedProxies(nil))
			router.Use(SessionBindingContext(cfg), jwtAuth(auth, repo, nil, settings, nil))
			router.Handle(method, path, func(c *gin.Context) {
				require.Equal(t, binding.Hash(), requestSessionBinding(c).Hash())
				require.Empty(t, c.GetHeader(modelcheckbridge.Header))
				c.Status(http.StatusOK)
			})
			req := httptest.NewRequest(method, path, nil)
			req.RemoteAddr = "127.0.0.1:8096"
			if tc.remote != "" {
				req.RemoteAddr = tc.remote
			}
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("User-Agent", ua)
			req.Header.Set("X-Forwarded-For", "192.0.2.99")
			if !tc.unsigned {
				clientIP := binding.IP
				if tc.signedIP != "" {
					clientIP = tc.signedIP
				}
				signature, err := modelcheckbridge.Sign(cfg.ModelCheck.BridgeSecret, req, modelcheckbridge.Identity{IP: clientIP, UserAgent: ua}, time.Now())
				require.NoError(t, err)
				req.Header.Set(modelcheckbridge.Header, signature)
			} else {
				req.Header.Set(modelcheckbridge.Header, "forged-public-header")
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, tc.want, w.Code, w.Body.String())
		})
	}
}
