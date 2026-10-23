//go:build integration

package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestRiskSessionPostgresDurabilityIsolationRetentionAndRelease(t *testing.T) {
	ctx := context.Background()
	settings := NewSettingRepository(testEntClient(t))
	key := service.SettingKeyRiskSessionConfig
	old, oldErr := settings.GetValue(ctx, key)
	t.Cleanup(func() {
		if oldErr == nil {
			_ = settings.Set(ctx, key, old)
		} else {
			_ = settings.Delete(ctx, key)
		}
	})
	require.NoError(t, settings.Set(ctx, key, `{"enabled":true,"snapshot_retention_days":180}`))
	repo := NewRiskSessionRepository(integrationDB)
	cfg := &config.Config{}
	cfg.Totp.EncryptionKey = strings.Repeat("42", 32)
	cipher, err := NewAESEncryptor(cfg)
	require.NoError(t, err)
	svc := service.NewRiskSessionService(settings, repo, cipher)
	userID := int64(88002410)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM risk_session_incidents WHERE user_id=$1`, userID)
	})
	request := []byte(`{"input":[{"role":"user","content":"history"}],"model":"test"}`)
	require.NoError(t, svc.Capture(ctx, service.RiskSessionCaptureInput{UserID: userID, APIKeyID: 52, Source: "upstream_cyber", RequestBody: request, ResponseBody: []byte(`{"code":"cyber_policy"}`), ResponseScope: "upstream_error_event", Keys: []string{"thread-first", "history-prefix"}}))
	// Construct a fresh service/repository to prove no process cache is required.
	svc = service.NewRiskSessionService(settings, NewRiskSessionRepository(integrationDB), cipher)
	hit, err := svc.FindBlocked(ctx, userID, []string{"history-prefix"})
	require.NoError(t, err)
	require.NotNil(t, hit)
	other, err := svc.FindBlocked(ctx, userID+1, []string{"history-prefix"})
	require.NoError(t, err)
	require.Nil(t, other)
	detail, err := svc.Get(ctx, hit.ID)
	require.NoError(t, err)
	require.Equal(t, string(request), detail.RequestBody)
	require.Equal(t, "upstream_error_event", detail.ResponseScope)
	var raw string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT encrypted_snapshot FROM risk_session_incidents WHERE id=$1`, hit.ID).Scan(&raw))
	require.NotContains(t, raw, "history")
	// Cleanup deletes only the encrypted payload; its incident and fingerprints remain.
	_, err = integrationDB.ExecContext(ctx, `UPDATE risk_session_incidents SET snapshot_expires_at=NOW()-INTERVAL '1 day' WHERE id=$1`, hit.ID)
	require.NoError(t, err)
	n, err := repo.CleanupExpired(ctx, time.Now())
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, int64(1))
	detail, err = svc.Get(ctx, hit.ID)
	require.NoError(t, err)
	require.Equal(t, "expired", detail.SnapshotStatus)
	require.Empty(t, detail.RequestBody)
	blocked, err := svc.FindBlocked(ctx, userID, []string{"thread-first"})
	require.NoError(t, err)
	require.NotNil(t, blocked)
	// Two further captures form a connected session through overlapping keys.
	require.NoError(t, svc.Capture(ctx, service.RiskSessionCaptureInput{UserID: userID, Source: "local_audit", RequestBody: request, Keys: []string{"history-prefix", "new-key"}}))
	require.NoError(t, svc.Capture(ctx, service.RiskSessionCaptureInput{UserID: userID, Source: "local_audit", RequestBody: request, Keys: []string{"new-key", "latest-key"}}))
	released, err := svc.Release(ctx, hit.ID, 71, "reviewed")
	require.NoError(t, err)
	require.False(t, released.Blocked)
	repeated, err := svc.Release(ctx, hit.ID, 72, "second")
	require.NoError(t, err)
	require.Equal(t, int64(71), *repeated.ReleasedByUserID)
	require.Equal(t, "reviewed", repeated.ReleaseReason)
	blocked, err = svc.FindBlocked(ctx, userID, []string{"thread-first"})
	require.NoError(t, err)
	require.Nil(t, blocked)
	blocked, err = svc.FindBlocked(ctx, userID, []string{"latest-key"})
	require.NoError(t, err)
	require.Nil(t, blocked)
	wasReleased, err := svc.WasReleased(ctx, userID, []string{"latest-key"})
	require.NoError(t, err)
	require.True(t, wasReleased)
	wasReleased, err = svc.WasReleased(ctx, userID+1, []string{"latest-key"})
	require.NoError(t, err)
	require.False(t, wasReleased)
	_, err = svc.Release(ctx, -99, 71, "")
	require.Error(t, err)
}
