package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type riskMemoryRepo struct {
	RiskSessionRepository
	items       []RiskSessionIncident
	keys        map[int64][]string
	lookupCalls int
}

func (r *riskMemoryRepo) Create(_ context.Context, item *RiskSessionIncident, keys []string) error {
	item.ID = int64(len(r.items) + 1)
	r.items = append(r.items, *item)
	if r.keys == nil {
		r.keys = map[int64][]string{}
	}
	r.keys[item.ID] = keys
	return nil
}
func (r *riskMemoryRepo) Get(_ context.Context, id int64) (*RiskSessionIncident, error) {
	for _, item := range r.items {
		if item.ID == id {
			return &item, nil
		}
	}
	return nil, ErrRiskSessionNotFound
}
func (r *riskMemoryRepo) FindBlocked(_ context.Context, userID int64, keys []string) (*RiskSessionIncident, error) {
	r.lookupCalls++
	for _, item := range r.items {
		if item.UserID != userID || item.ReleasedAt != nil {
			continue
		}
		for _, stored := range r.keys[item.ID] {
			for _, key := range keys {
				if stored == key {
					return &item, nil
				}
			}
		}
	}
	return nil, nil
}
func (r *riskMemoryRepo) HasReleased(_ context.Context, userID int64, keys []string) (bool, error) {
	for _, item := range r.items {
		if item.UserID != userID || item.ReleasedAt == nil {
			continue
		}
		for _, stored := range r.keys[item.ID] {
			for _, key := range keys {
				if stored == key {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func (r *riskMemoryRepo) Release(_ context.Context, id, actorID int64, reason string) (*RiskSessionIncident, error) {
	for i := range r.items {
		if r.items[i].ID == id {
			now := time.Now()
			r.items[i].ReleasedAt = &now
			r.items[i].ReleasedByUserID = &actorID
			r.items[i].ReleaseReason = reason
			r.items[i].Blocked = false
			out := r.items[i]
			return &out, nil
		}
	}
	return nil, ErrRiskSessionNotFound
}
func (r *riskMemoryRepo) UpdateRetention(context.Context, int) error { return nil }

type riskTestCipher struct{ fail bool }

func (c riskTestCipher) Encrypt(s string) (string, error) {
	if c.fail {
		return "", errors.New("private failure")
	}
	return base64.StdEncoding.EncodeToString([]byte(s)), nil
}
func (c riskTestCipher) Decrypt(s string) (string, error) {
	v, e := base64.StdEncoding.DecodeString(s)
	return string(v), e
}
func riskTestService() (context.Context, *RiskSessionService, *riskMemoryRepo) {
	repo := &riskMemoryRepo{}
	settings := &contentModerationTestSettingRepo{values: map[string]string{SettingKeyRiskSessionConfig: `{"enabled":true,"snapshot_retention_days":180}`}}
	return context.Background(), NewRiskSessionService(settings, repo, riskTestCipher{}), repo
}

func TestRiskSessionExactSnapshotAndRelease(t *testing.T) {
	ctx, s, repo := riskTestService()
	body := []byte("{\"input\":\"完整历史\"}\n\x00")
	require.NoError(t, s.Capture(ctx, RiskSessionCaptureInput{UserID: 7, APIKeyID: 3, Source: "local_audit", Keys: []string{"session-x"}, RequestBody: body, ResponseBody: []byte("rejected"), ResponseScope: "local_rejection_summary"}))
	require.NotContains(t, repo.items[0].EncryptedSnapshot, "完整历史")
	detail, err := s.Get(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, string(body), detail.RequestBody)
	require.Equal(t, "rejected", detail.ResponseBody)
	require.Equal(t, "local_rejection_summary", detail.ResponseScope)
	require.EqualValues(t, len(body), detail.RequestBytes)
	blocked, err := s.FindBlocked(ctx, 7, []string{"session-x"})
	require.NoError(t, err)
	require.NotNil(t, blocked)
	other, err := s.FindBlocked(ctx, 8, []string{"session-x"})
	require.NoError(t, err)
	require.Nil(t, other)
	_, err = s.Release(ctx, 1, 99, "reviewed")
	require.NoError(t, err)
	blocked, err = s.FindBlocked(ctx, 7, []string{"session-x"})
	require.NoError(t, err)
	require.Nil(t, blocked)
	detail, err = s.Get(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, string(body), detail.RequestBody)
	require.Equal(t, "reviewed", detail.ReleaseReason)
	released, err := s.WasReleased(ctx, 7, []string{"session-x"})
	require.NoError(t, err)
	require.True(t, released)
	released, err = s.WasReleased(ctx, 8, []string{"session-x"})
	require.NoError(t, err)
	require.False(t, released)
}

func TestRiskSessionSnapshotFailuresNeverLoseBlock(t *testing.T) {
	for _, kind := range []string{"encryption", "oversize", "empty"} {
		t.Run(kind, func(t *testing.T) {
			ctx, s, repo := riskTestService()
			body := []byte("history")
			switch kind {
			case "encryption":
				s.encryptor = riskTestCipher{fail: true}
			case "oversize":
				body = make([]byte, RiskSessionSnapshotMaxBytes+1)
			case "empty":
				body = nil
			}
			require.NoError(t, s.Capture(ctx, RiskSessionCaptureInput{UserID: 7, Source: "upstream_cyber", Keys: []string{"session-x"}, RequestBody: body}))
			require.Equal(t, "unavailable_"+kind, repo.items[0].SnapshotStatus)
			require.Empty(t, repo.items[0].EncryptedSnapshot)
			blocked, err := s.FindBlocked(ctx, 7, []string{"session-x"})
			require.NoError(t, err)
			require.NotNil(t, blocked)
		})
	}
}
func TestRiskSessionLongHistoryLookupAndExpiredBody(t *testing.T) {
	ctx, s, repo := riskTestService()
	require.NoError(t, s.Capture(ctx, RiskSessionCaptureInput{UserID: 7, Source: "upstream_cyber", Keys: []string{"ancient"}, RequestBody: []byte("history")}))
	keys := make([]string, 300)
	for i := range keys {
		keys[i] = fmt.Sprint("unmatched-", i)
	}
	keys = append(keys, "ancient")
	hit, err := s.FindBlocked(ctx, 7, keys)
	require.NoError(t, err)
	require.NotNil(t, hit)
	require.Equal(t, 3, repo.lookupCalls)
	repo.items[0].SnapshotExpiresAt = time.Now().Add(-time.Hour)
	detail, err := s.Get(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, "expired", detail.SnapshotStatus)
	require.Empty(t, detail.RequestBody)
	require.True(t, detail.Blocked)
	hit, err = s.FindBlocked(ctx, 7, []string{"ancient"})
	require.NoError(t, err)
	require.NotNil(t, hit)
}
func TestRiskSessionConfigAndSnapshotIntegrity(t *testing.T) {
	ctx, s, repo := riskTestService()
	_, err := s.UpdateConfig(ctx, RiskSessionConfig{Enabled: true, SnapshotRetentionDays: 0})
	require.Error(t, err)
	require.NoError(t, s.Capture(ctx, RiskSessionCaptureInput{UserID: 7, Source: "local_audit", Keys: []string{"a"}, RequestBody: []byte("hello")}))
	repo.items[0].RequestSHA256 = strings.Repeat("0", 64)
	detail, err := s.Get(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, "unreadable", detail.SnapshotStatus)
	require.Empty(t, detail.RequestBody)
	_, err = s.UpdateConfig(ctx, RiskSessionConfig{Enabled: false, SnapshotRetentionDays: 180})
	require.NoError(t, err)
	hit, err := s.FindBlocked(ctx, 7, []string{"a"})
	require.NoError(t, err)
	require.Nil(t, hit)
	require.NoError(t, s.Capture(ctx, RiskSessionCaptureInput{UserID: 7, Source: "local_audit", Keys: []string{"b"}, RequestBody: []byte("x")}))
	require.Len(t, repo.items, 1)
}

type riskBlockingSettings struct {
	SettingRepository
	started chan struct{}
	release chan struct{}
}

func (r *riskBlockingSettings) GetValue(context.Context, string) (string, error) {
	close(r.started)
	<-r.release
	return `{"enabled":true,"snapshot_retention_days":180}`, nil
}
func TestRiskSessionConfigWaitHonorsCallerCancellation(t *testing.T) {
	settings := &riskBlockingSettings{started: make(chan struct{}), release: make(chan struct{})}
	svc := NewRiskSessionService(settings, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := svc.Config(ctx); done <- err }()
	<-settings.started
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("configuration waiter ignored caller cancellation")
	}
	close(settings.release)
	cfg, err := svc.Config(context.Background())
	require.NoError(t, err)
	require.True(t, cfg.Enabled)
}
