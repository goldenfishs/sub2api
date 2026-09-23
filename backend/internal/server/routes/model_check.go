package routes

import (
	"net"
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/modelcheckbridge"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

// RegisterModelCheckRoutes exposes only the companion API. The companion keeps
// its per-route user/admin authorization; the bridge authenticates this hop.
func RegisterModelCheckRoutes(v1 *gin.RouterGroup, cfg *config.Config) {
	if cfg == nil || !cfg.ModelCheck.Enabled {
		return
	}
	v1.Any("/model-check/*path", modelCheckProxy(cfg.ModelCheck))
}

func modelCheckProxy(cfg config.ModelCheckConfig) gin.HandlerFunc {
	if cfg.Validate() != nil {
		return func(c *gin.Context) { modelCheckUnavailable(c.Writer) }
	}
	target, _ := modelcheckbridge.ParseLoopbackOrigin(cfg.Upstream)
	proxy := &httputil.ReverseProxy{
		Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(target)
			// Rewrite runs after hop-by-hop stripping. Do not propagate public
			// forwarding headers, cookies, or caller-supplied internal headers.
			p.Out.Header = modelCheckHeaders(p.In.Header)
			p.Out.Header.Set(modelcheckbridge.Header, p.In.Header.Get(modelcheckbridge.Header))
		},
		Transport: &http.Transport{
			Proxy:        nil,
			DialContext:  (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			MaxIdleConns: 20, MaxIdleConnsPerHost: 20, IdleConnTimeout: time.Minute,
			// The integration API can wait up to 120 seconds for a result.
			ResponseHeaderTimeout: 130 * time.Second,
		},
		ModifyResponse: func(r *http.Response) error {
			r.Header.Del(modelcheckbridge.Header)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			// Never disclose internal addresses, headers or transport errors.
			modelCheckUnavailable(w)
		},
	}
	return func(c *gin.Context) {
		r := c.Request.Clone(c.Request.Context())
		r.Header = modelCheckHeaders(c.Request.Header)
		token, err := modelcheckbridge.Sign(cfg.BridgeSecret, r, modelcheckbridge.Identity{
			IP: middleware.SecurityClientIP(c), UserAgent: c.Request.UserAgent(),
		}, time.Now())
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid_model_check_request"})
			return
		}
		r.Header.Set(modelcheckbridge.Header, token)
		proxy.ServeHTTP(c.Writer, r)
	}
}

func modelCheckHeaders(source http.Header) http.Header {
	result := make(http.Header)
	// An explicit empty value suppresses Go's default outgoing User-Agent.
	result.Set("User-Agent", source.Get("User-Agent"))
	for _, name := range []string{"Authorization", "X-Api-Key", "User-Agent", "Content-Type", "Accept", "Idempotency-Key"} {
		if _, present := source[name]; present {
			result.Set(name, source.Get(name))
		}
	}
	return result
}

func modelCheckUnavailable(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`{"code":503,"message":"model_check_unavailable","reason":"model_check_unavailable"}`))
}
