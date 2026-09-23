package modelcheckbridge

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const fixtureSecret = "0123456789abcdef0123456789abcdef"

// The identical vector is asserted by the Node companion, guarding the wire
// protocol across languages without requiring either runtime in the other job.
const fixtureToken = "eyJ2IjoxLCJ0cyI6MTgwMDAwMDAwMCwiaXAiOiIyMDMuMC4xMTMuOCIsInVhIjoiWm1sNGRIVnlaUzFoWjJWdWRBIiwiYXV0aCI6ImZlYzQxMTYzZmY5Y2IxYTNkNTU0NjdkYzQ3NDEyOGQxY2VmYmU3MTUwZmJiYjk2OWI2ZmY2MTViM2M4YjU0ZmUiLCJtZXRob2QiOiJHRVQiLCJwYXRoIjoiL2FwaS92MS9rZXlzP3N0YXR1cz1hY3RpdmUifQ.QGSEPphj_WUd58TcIjNDYEoHzQoYC6NJ5v93XeQMG2s"

func bridgeRequest() *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/keys?status=active", nil)
	r.RemoteAddr = "127.0.0.1:8096"
	r.Header.Set("Authorization", "Bearer fixture-user")
	r.Header.Set("User-Agent", "fixture-agent")
	return r
}

func TestModelCheckBridgeProtocolVector(t *testing.T) {
	r := bridgeRequest()
	now := time.Unix(1800000000, 0)
	identity := Identity{IP: "203.0.113.8", UserAgent: "fixture-agent"}
	token, err := Sign(fixtureSecret, r, identity, now)
	require.NoError(t, err)
	require.Equal(t, fixtureToken, token)
	r.Header.Set(Header, fixtureToken)
	actual, ok := Verify(fixtureSecret, r, now)
	require.True(t, ok)
	require.Equal(t, identity, actual)

	r.URL.Path = "/api/v1/admin/settings/admin-api-key"
	r.URL.RawQuery = ""
	r.Header.Del("Authorization")
	r.Header.Set("X-Api-Key", "fixture-admin-key")
	adminToken, err := Sign(fixtureSecret, r, identity, now)
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(adminToken, ".8U55OvCq-XaPT9TM2nwvUFGA2NPwT-gKCSgcily8fBQ"), "admin-key protocol vector must also match Node")
}

func TestModelCheckBridgeRejectsTamperingReplayAndRemotePeers(t *testing.T) {
	now := time.Unix(1800000000, 0)
	for _, tc := range []struct {
		name   string
		mutate func(*http.Request)
		clock  time.Time
	}{
		{name: "forged signature", mutate: func(r *http.Request) { r.Header.Set(Header, fixtureToken[:len(fixtureToken)-8]+"AAAAAAAA") }},
		{name: "remote peer", mutate: func(r *http.Request) { r.RemoteAddr = "203.0.113.10:1234" }},
		{name: "changed bearer", mutate: func(r *http.Request) { r.Header.Set("Authorization", "Bearer stolen-token") }},
		{name: "added admin key", mutate: func(r *http.Request) { r.Header.Set("X-Api-Key", "admin-key") }},
		{name: "added empty admin key", mutate: func(r *http.Request) { r.Header.Set("X-Api-Key", "") }},
		{name: "changed user agent", mutate: func(r *http.Request) { r.Header.Set("User-Agent", "different") }},
		{name: "changed method", mutate: func(r *http.Request) { r.Method = http.MethodPost }},
		{name: "changed route", mutate: func(r *http.Request) { r.URL.Path = "/api/v1/auth/me" }},
		{name: "changed query", mutate: func(r *http.Request) { r.URL.RawQuery = "status=disabled" }},
		{name: "duplicate bridge header", mutate: func(r *http.Request) { r.Header.Add(Header, fixtureToken) }},
		{name: "expired", clock: now.Add(31 * time.Second)},
		{name: "future", clock: now.Add(-6 * time.Second)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := bridgeRequest()
			r.Header.Set(Header, fixtureToken)
			if tc.mutate != nil {
				tc.mutate(r)
			}
			clock := now
			if !tc.clock.IsZero() {
				clock = tc.clock
			}
			_, ok := Verify(fixtureSecret, r, clock)
			require.False(t, ok)
		})
	}
}

func TestModelCheckBridgeCallbackScope(t *testing.T) {
	for _, path := range []string{"/api/v1/auth/me", "/api/v1/keys", "/api/v1/keys/13", "/api/v1/admin/settings/admin-api-key"} {
		require.True(t, IsAuthCallback(httptest.NewRequest(http.MethodGet, path, nil)), path)
		require.False(t, IsAuthCallback(httptest.NewRequest(http.MethodPost, path, nil)), path)
	}
	for _, path := range []string{"/api/v1/auth/refresh", "/api/v1/user", "/api/v1/keys/13/reveal", "/api/v1/keys/%31", "/api/v1/admin/users", "/v1/responses", "/api/v1/model-check/overview"} {
		require.False(t, IsAuthCallback(httptest.NewRequest(http.MethodGet, path, nil)), path)
	}
}

func TestModelCheckBridgePreservesLongAndUTF8UserAgent(t *testing.T) {
	r := bridgeRequest()
	ua := strings.Repeat("A", 509) + "中"
	r.Header.Set("User-Agent", ua)
	token, err := Sign(fixtureSecret, r, Identity{IP: "2001:db8::8", UserAgent: ua}, time.Now())
	require.NoError(t, err)
	r.Header.Set(Header, token)
	identity, ok := Verify(fixtureSecret, r, time.Now())
	require.True(t, ok)
	require.Equal(t, ua, identity.UserAgent)
}
