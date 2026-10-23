package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type riskHandlerRepo struct {
	items     []service.RiskSessionIncident
	keys      map[int64][]string
	lookupErr error
}

func (r *riskHandlerRepo) Create(_ context.Context, item *service.RiskSessionIncident, keys []string) error {
	item.ID = int64(len(r.items) + 1)
	r.items = append(r.items, *item)
	if r.keys == nil {
		r.keys = map[int64][]string{}
	}
	r.keys[item.ID] = append([]string(nil), keys...)
	return nil
}
func (r *riskHandlerRepo) FindBlocked(_ context.Context, uid int64, keys []string) (*service.RiskSessionIncident, error) {
	if r.lookupErr != nil {
		return nil, r.lookupErr
	}
	for _, item := range r.items {
		if item.UserID != uid || item.ReleasedAt != nil {
			continue
		}
		for _, existing := range r.keys[item.ID] {
			for _, key := range keys {
				if existing == key {
					copyItem := item
					return &copyItem, nil
				}
			}
		}
	}
	return nil, nil
}

func (r *riskHandlerRepo) HasReleased(_ context.Context, uid int64, keys []string) (bool, error) {
	for _, item := range r.items {
		if item.UserID != uid || item.ReleasedAt == nil {
			continue
		}
		for _, existing := range r.keys[item.ID] {
			for _, key := range keys {
				if existing == key {
					return true, nil
				}
			}
		}
	}
	return false, nil
}
func (r *riskHandlerRepo) List(context.Context, int, int) ([]service.RiskSessionIncident, int64, error) {
	return r.items, int64(len(r.items)), nil
}
func (r *riskHandlerRepo) Get(_ context.Context, id int64) (*service.RiskSessionIncident, error) {
	item := r.items[id-1]
	return &item, nil
}
func (r *riskHandlerRepo) Release(_ context.Context, id, actor int64, reason string) (*service.RiskSessionIncident, error) {
	now := time.Now()
	r.items[id-1].ReleasedAt = &now
	r.items[id-1].Blocked = false
	return &r.items[id-1], nil
}
func (*riskHandlerRepo) CleanupExpired(context.Context, time.Time) (int64, error) { return 0, nil }
func (*riskHandlerRepo) UpdateRetention(context.Context, int) error               { return nil }

type riskHandlerCipher struct{}

func (riskHandlerCipher) Encrypt(v string) (string, error) { return "test:" + v, nil }
func (riskHandlerCipher) Decrypt(v string) (string, error) {
	return strings.TrimPrefix(v, "test:"), nil
}

func newRiskHandlerService(t *testing.T, allowlist string) (*service.ContentModerationService, *service.RiskSessionService, *riskHandlerRepo) {
	t.Helper()
	settings := &contentModerationHandlerSettingRepo{values: map[string]string{
		service.SettingKeyRiskControlEnabled: "true", service.SettingKeyCyberPolicyUserAllowlist: allowlist,
		service.SettingKeyRiskSessionConfig:       `{"enabled":true,"snapshot_retention_days":180}`,
		service.SettingKeyContentModerationConfig: `{"enabled":true,"mode":"pre_block","all_groups":true,"sample_rate":100,"keyword_blocking_mode":"keyword_only","blocked_keywords":["risk-marker"],"block_as_cyber_policy":true,"block_status":403}`,
	}}
	repo := &riskHandlerRepo{}
	risk := service.NewRiskSessionService(settings, repo, riskHandlerCipher{})
	legacy := service.NewContentModerationService(settings, &contentModerationHandlerTestRepo{}, nil, nil, nil, nil, nil, nil)
	legacy.SetRiskSessions(risk)
	return legacy, risk, repo
}

func riskHandlerContext(body string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	return c
}

func TestPermanentRiskSessionLocalRollbackAndRelease(t *testing.T) {
	legacy, risk, repo := newRiskHandlerService(t, "")
	key := &service.APIKey{ID: 3, UserID: 7, Name: "original", User: &service.User{ID: 7, Role: "user"}}
	subject := middleware.AuthSubject{UserID: 7}
	body := `{"model":"test","input":[{"role":"user","content":"setup"},{"role":"assistant","content":"ready"},{"role":"user","content":"risk-marker"}]}`
	c := riskHandlerContext(body)
	d := runSecurityAudit(c, nil, nil, legacy, key, subject, service.ContentModerationProtocolOpenAIResponses, "test", []byte(body), "http")
	require.Equal(t, securityaudit.DecisionBlock, d.Kind)
	require.Len(t, repo.items, 1)
	stored, err := risk.Get(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, body, stored.RequestBody)
	require.Equal(t, "local_audit", stored.Source)
	// Rewinding and replacing the rejected turn, changing model and own key
	// must still stop before the empty coordinator or any upstream dispatch.
	retry := strings.ReplaceAll(strings.ReplaceAll(body, "risk-marker", "safe rewrite"), `"test"`, `"other-model"`)
	key.ID = 99
	c = riskHandlerContext(retry)
	d = runSecurityAudit(c, nil, nil, legacy, key, subject, service.ContentModerationProtocolOpenAIResponses, "other-model", []byte(retry), "http")
	require.Equal(t, riskSessionBlockedCode, d.ErrorCode)
	require.True(t, d.ClientResponse.CyberPolicy)
	require.Len(t, repo.items, 1, "repeat admission must not archive duplicate bodies")
	_, err = risk.Release(context.Background(), 1, 1, "reviewed")
	require.NoError(t, err)
	c = riskHandlerContext(retry)
	d = runSecurityAudit(c, nil, nil, legacy, key, subject, service.ContentModerationProtocolOpenAIResponses, "other-model", []byte(retry), "http")
	require.True(t, d.AllowNextStage)
}

func TestPermanentRiskSessionUnavailableDoesNotBecomeCyber(t *testing.T) {
	legacy, _, repo := newRiskHandlerService(t, "")
	repo.lookupErr = errors.New("storage unavailable")
	body := `{"input":"safe"}`
	d := runSecurityAudit(riskHandlerContext(body), nil, nil, legacy, &service.APIKey{ID: 3, UserID: 7}, middleware.AuthSubject{UserID: 7}, service.ContentModerationProtocolOpenAIResponses, "test", []byte(body), "http")
	require.Equal(t, securityaudit.DecisionUnavailable, d.Kind)
	require.Equal(t, 503, d.HTTPStatus)
	require.Nil(t, d.ClientResponse)
	require.Empty(t, repo.items)
}

func TestPermanentRiskSessionExemptions(t *testing.T) {
	for _, admin := range []bool{false, true} {
		allowlist := "7"
		role := "user"
		if admin {
			allowlist = ""
			role = "admin"
		}
		legacy, _, repo := newRiskHandlerService(t, allowlist)
		repo.lookupErr = errors.New("must not look up exempt user")
		key := &service.APIKey{ID: 3, UserID: 7, User: &service.User{ID: 7, Role: role}}
		d := checkPermanentRiskSession(riskHandlerContext(`{"input":"safe"}`), nil, legacy, key, middleware.AuthSubject{UserID: 7}, service.ContentModerationProtocolOpenAIResponses, "test", []byte(`{"input":"safe"}`), "http")
		require.Nil(t, d)
		require.Empty(t, repo.items)
	}
}

func TestPermanentRiskSessionUpstreamUsesAdmittedWSTurn(t *testing.T) {
	legacy, risk, repo := newRiskHandlerService(t, "")
	key := &service.APIKey{ID: 3, UserID: 7, User: &service.User{ID: 7, Role: "user"}}
	body := `{"type":"response.create","input":"safe input"}`
	c := riskHandlerContext(body)
	c.Set(securityAuditWSTurnContextKey, 4)
	require.Nil(t, checkPermanentRiskSession(c, nil, legacy, key, middleware.AuthSubject{UserID: 7}, service.ContentModerationProtocolOpenAIResponses, "test", []byte(body), "subsequent_turn"))
	h := &OpenAIGatewayHandler{contentModerationService: legacy}
	require.True(t, h.captureUpstreamRiskSession(c, key, "test", []byte(`{"input":"changed upstream payload"}`), &service.CyberPolicyMark{Message: "refused", Body: `{"error":{"code":"cyber_policy"}}`}))
	require.Len(t, repo.items, 1)
	stored, err := risk.Get(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, body, stored.RequestBody)
	require.Equal(t, "websocket_turn_only", stored.SnapshotScope)
	require.Contains(t, stored.RequestID, ":ws:4")
	require.Equal(t, "upstream_cyber", stored.Source)
}
