package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLumiviaCyberOnlyBanKeywordThenCyber(t *testing.T) {
	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.Mode = ContentModerationModePreBlock
	cfg.KeywordBlockingMode = ContentModerationKeywordModeKeywordOnly
	cfg.BlockedKeywords = []string{"test-block-marker"}
	cfg.BanThreshold = 1
	cfg.EmailOnHit = false
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	settings := &contentModerationTestSettingRepo{values: map[string]string{
		SettingKeyRiskControlEnabled: "true", SettingKeyContentModerationConfig: string(raw),
	}}
	repo := &contentModerationTestRepo{}
	users := &contentModerationTestUserRepo{user: &User{ID: 1001, Role: RoleUser, Status: StatusActive}}
	invalidator := &contentModerationTestAuthCacheInvalidator{}
	svc := NewContentModerationService(settings, repo, nil, nil, users, nil, invalidator, nil)
	enabled := true
	view, err := svc.UpdateConfig(context.Background(), UpdateContentModerationConfigInput{AutoBanOnlyCyberPolicy: &enabled})
	require.NoError(t, err)
	require.True(t, view.AutoBanOnlyCyberPolicy)
	view, err = svc.GetConfig(context.Background())
	require.NoError(t, err)
	require.True(t, view.AutoBanOnlyCyberPolicy)
	for i := 0; i < 2; i++ {
		decision, err := svc.Check(context.Background(), ContentModerationCheckInput{UserID: 1001,
			Protocol: ContentModerationProtocolOpenAIChat,
			Body:     []byte(`{"messages":[{"role":"user","content":"test-block-marker"}]}`)})
		require.NoError(t, err)
		require.True(t, decision.Blocked)
		logs := requireContentModerationLogCount(t, repo, i+1)
		require.Equal(t, ContentModerationActionKeywordBlock, logs[i].Action)
		require.False(t, logs[i].AutoBanned)
		require.Zero(t, logs[i].ViolationCount)
	}
	require.Equal(t, StatusActive, users.user.Status)
	require.Empty(t, users.updated)
	require.Empty(t, invalidator.userIDs)
	decision, err := svc.Check(context.Background(), ContentModerationCheckInput{UserID: 1001,
		Protocol: ContentModerationProtocolOpenAIChat,
		Body:     []byte(`{"messages":[{"role":"user","content":"hello"}]}`)})
	require.NoError(t, err)
	require.True(t, decision.Allowed)
	svc.RecordCyberPolicyEvent(context.Background(), CyberPolicyRecordInput{
		UserID: 1001, Model: "gpt-6.1-sol", Endpoint: "/v1/responses", UpstreamStatus: 400,
		UpstreamBody: `{"error":{"code":"cyber_policy"}}`,
	})
	logs := requireContentModerationLogCount(t, repo, 3)
	require.True(t, logs[2].AutoBanned)
	require.Equal(t, 1, logs[2].ViolationCount)
	require.Equal(t, StatusDisabled, users.user.Status)
	require.Equal(t, []int64{1001}, invalidator.userIDs)
}

func TestLumiviaCyberOnlyBanPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, action, role string
		enabled, wantBan   bool
	}{
		{"first cyber ignores threshold", ContentModerationActionCyberPolicy, RoleUser, true, true},
		{"keyword never bans", ContentModerationActionKeywordBlock, RoleUser, true, false},
		{"other moderation never bans", ContentModerationActionBlock, RoleUser, true, false},
		{"master switch respected", ContentModerationActionCyberPolicy, RoleUser, false, false},
		{"admin exemption preserved", ContentModerationActionCyberPolicy, RoleAdmin, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultContentModerationConfig()
			cfg.AutoBanOnlyCyberPolicy = true
			cfg.AutoBanEnabled = tc.enabled
			cfg.BanThreshold = 99
			users := &contentModerationTestUserRepo{user: &User{ID: 1001, Role: tc.role, Status: StatusActive}}
			svc := NewContentModerationService(nil, &contentModerationTestRepo{}, nil, nil, users, nil, nil, nil)
			log := newContentModerationFlaggedLog(1001)
			log.Action = tc.action
			require.Equal(t, tc.wantBan, svc.applyFlaggedAccountSideEffects(context.Background(), cfg, log))
			require.Equal(t, tc.wantBan, log.AutoBanned)
			require.Equal(t, tc.wantBan, users.user.Status == StatusDisabled)
		})
	}
}
