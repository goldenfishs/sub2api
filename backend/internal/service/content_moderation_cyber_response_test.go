package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContentModerationBlockAsCyberPolicyConfigRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := &contentModerationRuntimeSettingRepo{values: map[string]string{
		SettingKeyContentModerationConfig: `{"block_status":403,"auto_ban_only_cyber_policy":true}`,
	}}
	svc := NewContentModerationService(repo, nil, nil, nil, nil, nil, nil, nil)

	// Legacy configurations keep their existing response, and reads share one cache load.
	require.False(t, svc.BlockAsCyberPolicy(ctx))
	require.False(t, svc.BlockAsCyberPolicy(ctx))
	_, multipleCalls := repo.calls()
	require.Equal(t, 1, multipleCalls)

	enabled := true
	view, err := svc.UpdateConfig(ctx, UpdateContentModerationConfigInput{BlockAsCyberPolicy: &enabled})
	require.NoError(t, err)
	require.True(t, view.BlockAsCyberPolicy)
	require.True(t, svc.BlockAsCyberPolicy(ctx), "saving must update the runtime cache immediately")
	require.True(t, view.AutoBanOnlyCyberPolicy)
	require.Equal(t, 403, view.BlockStatus)

	stored, err := repo.GetValue(ctx, SettingKeyContentModerationConfig)
	require.NoError(t, err)
	var saved ContentModerationConfig
	require.NoError(t, json.Unmarshal([]byte(stored), &saved))
	require.True(t, saved.BlockAsCyberPolicy)
	view, err = svc.GetConfig(ctx)
	require.NoError(t, err)
	require.True(t, view.BlockAsCyberPolicy)

	message := "custom audit block message"
	view, err = svc.UpdateConfig(ctx, UpdateContentModerationConfigInput{BlockMessage: &message})
	require.NoError(t, err)
	require.True(t, view.BlockAsCyberPolicy, "omitting the optional toggle must preserve it")

	enabled = false
	view, err = svc.UpdateConfig(ctx, UpdateContentModerationConfigInput{BlockAsCyberPolicy: &enabled})
	require.NoError(t, err)
	require.False(t, view.BlockAsCyberPolicy)
	require.False(t, svc.BlockAsCyberPolicy(ctx))
	require.Equal(t, message, view.BlockMessage)
}

func TestContentModerationBlockAsCyberPolicyUnavailableConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		svc  *ContentModerationService
	}{
		{name: "nil service"},
		{name: "missing repository", svc: &ContentModerationService{}},
		{name: "repository error", svc: &ContentModerationService{settingRepo: &contentModerationRuntimeSettingRepo{
			getMultipleErr: errors.New("settings unavailable"),
		}}},
		{name: "invalid config", svc: &ContentModerationService{settingRepo: &contentModerationRuntimeSettingRepo{
			values: map[string]string{SettingKeyContentModerationConfig: `{invalid`},
		}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.False(t, tc.svc.BlockAsCyberPolicy(context.Background()))
		})
	}
}

func TestContentModerationBlockAsCyberPolicyPreservesLocalAuditAndBanPolicy(t *testing.T) {
	ctx := context.Background()
	cfg := defaultContentModerationConfig()
	cfg.Enabled = true
	cfg.BlockAsCyberPolicy = true
	cfg.AutoBanOnlyCyberPolicy = true
	cfg.Mode = ContentModerationModePreBlock
	cfg.KeywordBlockingMode = ContentModerationKeywordModeKeywordOnly
	cfg.BlockedKeywords = []string{"audit-response-test-marker"}
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
	require.True(t, svc.BlockAsCyberPolicy(ctx))

	decision, err := svc.Check(ctx, ContentModerationCheckInput{
		UserID: 1001, Protocol: ContentModerationProtocolOpenAIChat,
		Body: []byte(`{"messages":[{"role":"user","content":"audit-response-test-marker"}]}`),
	})
	require.NoError(t, err)
	require.True(t, decision.Blocked)
	require.Equal(t, cfg.BlockStatus, decision.StatusCode, "transport formatting must not change the audit decision")
	require.Equal(t, cfg.BlockMessage, decision.Message)
	logs := requireContentModerationLogCount(t, repo, 1)
	require.Equal(t, ContentModerationActionKeywordBlock, logs[0].Action)
	require.False(t, logs[0].AutoBanned)
	require.Zero(t, logs[0].ViolationCount)
	require.Equal(t, StatusActive, users.user.Status)
	require.Empty(t, invalidator.userIDs)

	// Actual upstream cyber_policy events still use the original immediate-ban policy.
	svc.RecordCyberPolicyEvent(ctx, CyberPolicyRecordInput{
		UserID: 1001, Model: "gpt-6.1-sol", Endpoint: "/v1/responses", UpstreamStatus: 400,
		UpstreamBody: `{"error":{"code":"cyber_policy"}}`,
	})
	logs = requireContentModerationLogCount(t, repo, 2)
	require.Equal(t, ContentModerationActionCyberPolicy, logs[1].Action)
	require.True(t, logs[1].AutoBanned)
	require.Equal(t, 1, logs[1].ViolationCount)
	require.Equal(t, StatusDisabled, users.user.Status)
	require.Equal(t, []int64{1001}, invalidator.userIDs)
}
