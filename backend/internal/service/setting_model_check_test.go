//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestModelCheckPublicSettingsUsesDeploymentCapability(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		svc := NewSettingService(&settingPublicRepoStub{values: map[string]string{"model_check_enabled": "true"}}, &config.Config{
			ModelCheck: config.ModelCheckConfig{Enabled: enabled, BridgeSecret: "internal-secret-must-not-be-public"},
		})
		settings, err := svc.GetPublicSettings(context.Background())
		require.NoError(t, err)
		require.Equal(t, enabled, settings.ModelCheckEnabled)
		injected, err := svc.GetPublicSettingsForInjection(context.Background())
		require.NoError(t, err)
		require.Equal(t, enabled, injected.(*PublicSettingsInjectionPayload).ModelCheckEnabled)
		data, err := json.Marshal(injected)
		require.NoError(t, err)
		require.NotContains(t, string(data), "internal-secret-must-not-be-public")
	}
}
