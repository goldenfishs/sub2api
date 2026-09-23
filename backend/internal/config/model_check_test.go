package config

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModelCheckConfigEnvironmentAndDefault(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("MODEL_CHECK_ENABLED", "false")
	t.Setenv("MODEL_CHECK_BRIDGE_SECRET", "")
	defaultCfg, err := Load()
	require.NoError(t, err)
	require.False(t, defaultCfg.ModelCheck.Enabled)
	require.Equal(t, "http://127.0.0.1:8096", defaultCfg.ModelCheck.Upstream)

	t.Setenv("MODEL_CHECK_ENABLED", "true")
	t.Setenv("MODEL_CHECK_UPSTREAM", "http://127.0.0.1:18096")
	t.Setenv("MODEL_CHECK_BRIDGE_SECRET", strings.Repeat("a", 64))
	cfg, err := Load()
	require.NoError(t, err)
	require.True(t, cfg.ModelCheck.Enabled)
	require.Equal(t, "http://127.0.0.1:18096", cfg.ModelCheck.Upstream)
	require.Equal(t, strings.Repeat("a", 64), cfg.ModelCheck.BridgeSecret)
	data, err := json.Marshal(cfg.ModelCheck)
	require.NoError(t, err)
	require.NotContains(t, string(data), cfg.ModelCheck.BridgeSecret)
}

func TestModelCheckConfigFailsClosed(t *testing.T) {
	for _, target := range []string{"https://127.0.0.1:8096", "http://localhost:8096", "http://10.0.0.1:8096", "http://example.com", "http://127.0.0.1:8096/path", "http://user:pass@127.0.0.1", "http://127.0.0.1?x=1", "http://127.0.0.1#x", "http://127.0.0.1?", "http://127.0.0.1#"} {
		cfg := ModelCheckConfig{Enabled: true, Upstream: target, BridgeSecret: strings.Repeat("s", 32)}
		require.Error(t, cfg.Validate(), target)
	}
	for _, secret := range []string{"", "too-short", strings.Repeat("s", 31) + " ", strings.Repeat("s", 257)} {
		cfg := ModelCheckConfig{Enabled: true, Upstream: "http://127.0.0.1:8096", BridgeSecret: secret}
		require.Error(t, cfg.Validate())
	}
	require.NoError(t, (ModelCheckConfig{Enabled: true, Upstream: "http://[::1]:8096", BridgeSecret: strings.Repeat("s", 32)}).Validate())
	require.NoError(t, (ModelCheckConfig{}).Validate())
}
