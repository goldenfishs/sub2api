// Package modelcheckbridge authenticates the loopback-only model-check companion.
// It conveys a request's network identity, never authority: normal JWT/API-key
// middleware must still authenticate every callback.
package modelcheckbridge

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const Header = "X-Lumivia-Model-Check-Bridge"

type Identity struct {
	IP        string
	UserAgent string
}

type claims struct {
	Version     int    `json:"v"`
	IssuedAt    int64  `json:"ts"`
	IP          string `json:"ip"`
	UserAgent   string `json:"ua"` // base64url preserves the exact HTTP header bytes
	Credentials string `json:"auth"`
	Method      string `json:"method"`
	Path        string `json:"path"`
}

func ValidSecret(secret string) bool {
	if len(secret) < 32 || len(secret) > 256 {
		return false
	}
	for _, b := range []byte(secret) {
		if b < 33 || b > 126 {
			return false
		}
	}
	return true
}

// ParseLoopbackOrigin rejects DNS names, credentials, paths and redirects. Both
// processes share a network namespace; no system HTTP proxy may handle this hop.
func ParseLoopbackOrigin(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Scheme != "http" || u.User != nil || u.Host == "" ||
		(u.Path != "" && u.Path != "/") || u.RawPath != "" || strings.ContainsAny(raw, "?#") {
		return nil, fmt.Errorf("must be an HTTP loopback IP origin without credentials, path, query or fragment")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("must use a literal loopback IP address")
	}
	u.Path = ""
	return u, nil
}

func IsLoopbackPeer(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

var keyPath = regexp.MustCompile(`^/api/v1/keys/[1-9][0-9]*$`)

// Only the companion's existing read-only account checks can restore identity.
// In particular, the bridge cannot issue tokens, mutate keys or call a gateway.
func IsAuthCallback(r *http.Request) bool {
	if r.Method != http.MethodGet || r.URL.EscapedPath() != r.URL.Path {
		return false
	}
	switch r.URL.Path {
	case "/api/v1/auth/me", "/api/v1/keys", "/api/v1/admin/settings/admin-api-key":
		return true
	default:
		return keyPath.MatchString(r.URL.Path)
	}
}

func credentialsHash(r *http.Request) string {
	material := r.Header.Get("Authorization") + "\x00" + r.Header.Get("X-Api-Key")
	// Even an empty admin-key header selects key authentication in the
	// companion. Bind its presence so it cannot turn into a JWT fallback.
	if _, present := r.Header["X-Api-Key"]; present {
		material += "\x00present"
	}
	sum := sha256.Sum256([]byte(material))
	return hex.EncodeToString(sum[:])
}

func Sign(secret string, r *http.Request, identity Identity, now time.Time) (string, error) {
	if !ValidSecret(secret) || net.ParseIP(identity.IP) == nil || len(identity.UserAgent) > 512 || len(r.URL.RequestURI()) > 4096 {
		return "", fmt.Errorf("invalid model-check bridge context")
	}
	data, err := json.Marshal(claims{
		Version: 1, IssuedAt: now.Unix(), IP: identity.IP,
		UserAgent:   base64.RawURLEncoding.EncodeToString([]byte(identity.UserAgent)),
		Credentials: credentialsHash(r), Method: r.Method, Path: r.URL.RequestURI(),
	})
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(data)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// Verify requires a direct loopback peer and a fresh signature bound to the
// method, exact path/query, UA and credentials of this particular request.
func Verify(secret string, r *http.Request, now time.Time) (Identity, bool) {
	if !ValidSecret(secret) || !IsLoopbackPeer(r.RemoteAddr) || len(r.Header.Values(Header)) != 1 {
		return Identity{}, false
	}
	token := r.Header.Get(Header)
	if len(token) > 8192 {
		return Identity{}, false
	}
	payload, signature, ok := strings.Cut(token, ".")
	if !ok {
		return Identity{}, false
	}
	sig, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return Identity{}, false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return Identity{}, false
	}
	data, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return Identity{}, false
	}
	var c claims
	if json.Unmarshal(data, &c) != nil || c.Version != 1 || c.IssuedAt < now.Unix()-30 || c.IssuedAt > now.Unix()+5 ||
		net.ParseIP(c.IP) == nil || c.Method != r.Method || c.Path != r.URL.RequestURI() ||
		!hmac.Equal([]byte(c.Credentials), []byte(credentialsHash(r))) {
		return Identity{}, false
	}
	ua, err := base64.RawURLEncoding.DecodeString(c.UserAgent)
	if err != nil || len(ua) > 512 || string(ua) != r.UserAgent() {
		return Identity{}, false
	}
	return Identity{IP: c.IP, UserAgent: string(ua)}, true
}
