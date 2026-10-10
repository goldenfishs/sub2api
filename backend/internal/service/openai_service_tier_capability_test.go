package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func tierTestAccount(id int64, tier string) Account {
	a := Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 5, Credentials: map[string]any{"api_key": "test", "base_url": "https://example.com"}, Extra: map[string]any{}}
	if tier != "" {
		model := "gpt-6-astra"
		a.Extra[openAITierProbeKey(model, tier)] = OpenAIServiceTierCapability{Model: model, Tier: tier, Status: "supported", CheckedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour), Fingerprint: openAITierProbeFingerprint(&a)}
	}
	return a
}

func TestServiceTierProbeRequiresFinalConfirmation(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		status   int
		body     string
		want     string
		observed string
	}{
		{"confirmed", 200, `data: {"type":"response.completed","response":{"status":"completed","service_tier":"priority"}}` + "\n\n", "supported", "priority"},
		{"only created", 200, `data: {"type":"response.created","response":{"status":"in_progress","service_tier":"priority"}}` + "\n\n", "error", ""},
		{"default is ambiguous", 200, `{"status":"completed","service_tier":"default"}`, "unknown", "default"},
		{"missing", 200, `{"status":"completed"}`, "unknown", ""},
		{"html", 200, `<html>OK</html>`, "error", ""},
		{"incomplete", 200, `{"status":"incomplete","service_tier":"priority"}`, "error", ""},
		{"rejected", 400, `{"error":{"message":"service_tier priority is not supported"}}`, "unsupported", ""},
		{"auth failure", 401, `{"error":{"message":"invalid key"}}`, "error", ""},
		{"rate limited", 429, `{"error":{"message":"rate limited"}}`, "error", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := classifyOpenAIServiceTierProbe("gpt-6-astra", "priority", tt.status, []byte(tt.body), nil, now)
			require.Equal(t, tt.want, c.Status)
			require.Equal(t, tt.observed, c.ObservedTier)
		})
	}
	c := classifyOpenAIServiceTierProbe("gpt-6-astra", "priority", 200, nil, context.DeadlineExceeded, now)
	require.Equal(t, "error", c.Status)
	require.NotContains(t, c.Detail, "token")
}

func TestServiceTierCapabilityScopedAndExpires(t *testing.T) {
	a := tierTestAccount(1, "priority")
	require.True(t, accountSupportsOpenAIServiceTier(&a, "gpt-6-astra", "priority", time.Now()))
	require.False(t, accountSupportsOpenAIServiceTier(&a, "gpt-6.1-sol", "priority", time.Now()))
	require.False(t, accountSupportsOpenAIServiceTier(&a, "gpt-6-astra", "ultrafast", time.Now()))
	require.False(t, accountSupportsOpenAIServiceTier(&a, "gpt-6-astra", "priority", time.Now().Add(2*time.Hour)))
	a.Credentials["api_key"] = "changed"
	require.False(t, accountSupportsOpenAIServiceTier(&a, "gpt-6-astra", "priority", time.Now()))
}

func TestServiceTierSelectionTwoPasses(t *testing.T) {
	for _, test := range []struct {
		name        string
		unavailable bool
		excluded    bool
		requestTier string
	}{
		{name: "fast preferred", requestTier: "fast"}, {name: "ultrafast separate", requestTier: "ultrafast"}, {name: "fallback when unavailable", unavailable: true, requestTier: "priority"}, {name: "failover excludes failed fast", excluded: true, requestTier: "priority"},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, b := tierTestAccount(1, "priority"), tierTestAccount(2, "")
			svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{a, b}}, cfg: &config.Config{}}
			ctx := WithOpenAIServiceTierRouting(context.Background(), []byte(fmt.Sprintf(`{"service_tier":%q}`, test.requestTier)))
			excluded := map[int64]struct{}{}
			if test.excluded {
				excluded[1] = struct{}{}
			}
			calls := 0
			selection, _, err := svc.selectAccountWithServiceTier(ctx, nil, "gpt-6-astra", excluded, func(ctx context.Context, ids map[int64]struct{}) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
				calls++
				// Ordinary account is listed first to catch accidental score-only preference.
				for _, acc := range []*Account{&b, &a} {
					if _, skip := ids[acc.ID]; skip {
						continue
					}
					if acc.ID == 1 && test.unavailable {
						continue
					}
					if !openAITierCandidateEligible(ctx, acc, "gpt-6-astra") {
						continue
					}
					return &AccountSelectionResult{Account: acc}, OpenAIAccountScheduleDecision{}, nil
				}
				return nil, OpenAIAccountScheduleDecision{}, ErrNoAvailableAccounts
			})
			require.NoError(t, err)
			body, err := svc.applyOpenAIFastPolicyToBody(ctx, selection.Account, "gpt-6-astra", []byte(fmt.Sprintf(`{"service_tier":%q}`, test.requestTier)))
			require.NoError(t, err)
			if test.unavailable || test.excluded || test.requestTier == "ultrafast" {
				require.Equal(t, int64(2), selection.Account.ID)
				require.Equal(t, 2, calls)
				require.Equal(t, "default", gjson.GetBytes(body, "service_tier").String())
			} else {
				require.Equal(t, int64(1), selection.Account.ID)
				require.Equal(t, 1, calls)
				require.Equal(t, "priority", gjson.GetBytes(body, "service_tier").String())
			}
			if test.excluded {
				require.Contains(t, excluded, int64(1))
			} else {
				require.Empty(t, excluded)
			}
		})
	}
}

func TestServiceTierSelectionDoesNotFallbackOnInternalError(t *testing.T) {
	svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{}, cfg: &config.Config{}}
	ctx := WithOpenAIServiceTierRouting(context.Background(), []byte(`{"service_tier":"priority"}`))
	calls := 0
	_, _, err := svc.selectAccountWithServiceTier(ctx, nil, "gpt-6-astra", nil, func(context.Context, map[int64]struct{}) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
		calls++
		return nil, OpenAIAccountScheduleDecision{}, errors.New("database unavailable")
	})
	require.Error(t, err)
	require.Equal(t, 1, calls)
}

func TestServiceTierFallbackWSAndBilling(t *testing.T) {
	a := tierTestAccount(1, "")
	ctx := WithOpenAIServiceTierRouting(context.Background(), []byte(`{"service_tier":"ultrafast"}`))
	openAITierRouting(ctx).selectTier("default", a.ID)
	svc := &OpenAIGatewayService{}
	body, blocked, err := svc.applyOpenAIFastPolicyToWSResponseCreate(ctx, &a, "gpt-6-astra", []byte(`{"type":"response.create","model":"gpt-6-astra","service_tier":"ultrafast"}`))
	require.NoError(t, err)
	require.Nil(t, blocked)
	require.Equal(t, "default", gjson.GetBytes(body, "service_tier").String())
	resolution := ResolveOpenAIServiceTierBilling(&a, *extractOpenAIServiceTierFromBody(body), "default")
	require.Equal(t, "default", resolution.Billing)
	require.False(t, ValidateOpenAIServiceTierWSTurn(ctx, &a, []byte(`{"service_tier":"priority"}`), "gpt-6-astra"))
	// No mutation leaks into an independent request or a non-create WS frame.
	unchanged, _, err := svc.applyOpenAIFastPolicyToWSResponseCreate(ctx, &a, "gpt-6-astra", []byte(`{"type":"response.cancel"}`))
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"response.cancel"}`, string(unchanged))
}

func TestGPT61SolUltrafastPricingSixTimes(t *testing.T) {
	svc := NewBillingService(&config.Config{}, &PricingService{})
	for _, model := range []string{"gpt-6.1-sol", "openai/gpt-6.1-sol", "gpt-6.1-sol-max"} {
		for _, n := range []int{1000, 300000} {
			tokens := UsageTokens{InputTokens: n, OutputTokens: 500, CacheReadTokens: 100, CacheCreationTokens: 100}
			normal, err := svc.CalculateCostWithServiceTier(model, tokens, 1, "default")
			require.NoError(t, err)
			ultra, err := svc.CalculateCostWithServiceTier(model, tokens, 1, "ultrafast")
			require.NoError(t, err)
			require.InDelta(t, normal.TotalCost*6, ultra.TotalCost, 1e-9)
		}
	}
}

func TestServiceTierRealSchedulersPreferConfirmedOverSticky(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		t.Run(fmt.Sprint(advanced), func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
			fast, normal := tierTestAccount(1, "priority"), tierTestAccount(2, "")
			fast.Priority = 100
			normal.Priority = 0
			groupID := int64(9)
			fast.GroupIDs = []int64{groupID}
			normal.GroupIDs = []int64{groupID}
			cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"session": 2}}
			svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{normal, fast}}, cache: cache, cfg: newSchedulerTestOpenAIWSV2Config()}
			if advanced {
				svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("true")
			}
			ctx := WithOpenAIServiceTierRouting(context.Background(), []byte(`{"service_tier":"priority"}`))
			result, _, err := svc.SelectAccountWithSchedulerForCapability(ctx, &groupID, "", "session", "gpt-6-astra", nil, OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityResponses, false, false, true)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, int64(1), result.Account.ID)
			if result.ReleaseFunc != nil {
				result.ReleaseFunc()
			}
			ctx = WithOpenAIServiceTierRouting(context.Background(), []byte(`{"service_tier":"priority"}`))
			result, _, err = svc.SelectAccountWithSchedulerForCapability(ctx, &groupID, "", "session", "gpt-6-astra", map[int64]struct{}{1: {}}, OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityResponses, false, false, true)
			require.NoError(t, err)
			require.Equal(t, int64(2), result.Account.ID)
			if result.ReleaseFunc != nil {
				result.ReleaseFunc()
			}
			body, err := svc.applyOpenAIFastPolicyToBody(ctx, result.Account, "gpt-6-astra", []byte(`{"service_tier":"priority"}`))
			require.NoError(t, err)
			require.Equal(t, "default", gjson.GetBytes(body, "service_tier").String())
		})
	}
}

func TestServiceTierRoutingUsesFinalPolicyTier(t *testing.T) {
	fast, ultra := tierTestAccount(1, "priority"), tierTestAccount(2, "ultrafast")
	svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{ultra, fast}}, cfg: &config.Config{}}
	ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: 7, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, ForceOpenAIFast: true})
	ctx = WithOpenAIServiceTierRouting(ctx, []byte(`{"service_tier":"ultrafast"}`))
	selection, _, err := svc.selectAccountWithServiceTier(ctx, nil, "gpt-6-astra", nil, func(ctx context.Context, excluded map[int64]struct{}) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
		for _, a := range []*Account{&ultra, &fast} {
			if _, skip := excluded[a.ID]; !skip && openAITierCandidateEligible(ctx, a, "gpt-6-astra") {
				return &AccountSelectionResult{Account: a}, OpenAIAccountScheduleDecision{}, nil
			}
		}
		return nil, OpenAIAccountScheduleDecision{}, ErrNoAvailableAccounts
	})
	require.NoError(t, err)
	require.Equal(t, fast.ID, selection.Account.ID)
	body, err := svc.applyOpenAIFastPolicyToBody(ctx, selection.Account, "gpt-6-astra", []byte(`{"service_tier":"ultrafast"}`))
	require.NoError(t, err)
	require.Equal(t, "priority", gjson.GetBytes(body, "service_tier").String())
	require.True(t, ValidateOpenAIServiceTierWSTurn(ctx, &fast, []byte(`{"service_tier":"ultrafast"}`), "gpt-6-astra"))
}

func TestServiceTierProbeTransportAndPersistence(t *testing.T) {
	a := tierTestAccount(5, "")
	a.Credentials["base_url"] = "https://example.com/v1"
	a.Credentials["header_override_enabled"] = true
	a.Credentials["header_overrides"] = map[string]any{"x-custom-route": "probe-route"}
	a.Credentials["model_mapping"] = map[string]any{"gpt-6-astra": "mapped-astra"}
	updates := make(chan map[string]any, 2)
	repo := &snapshotUpdateAccountRepo{stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{a}}, updateExtraCalls: updates}
	responses := []*http.Response{}
	for _, tier := range []string{"priority", "ultrafast"} {
		responses = append(responses, &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(fmt.Sprintf("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"service_tier\":%q}}\n\n", tier)))})
	}
	upstream := &httpUpstreamRecorder{responses: responses}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: &config.Config{}}
	p := newOpenAIServiceTierProber(svc)
	p.ctx = context.Background()
	p.probe(openAITierProbeJob{id: a.ID, model: "gpt-6-astra"})
	require.Len(t, upstream.requests, 2)
	for i, tier := range []string{"priority", "ultrafast"} {
		req := upstream.requests[i]
		require.Equal(t, "https://example.com/v1/responses", req.URL.String())
		require.Equal(t, "probe-route", req.Header[resolveWireCasing("x-custom-route")][0])
		require.Equal(t, "Bearer test", req.Header.Get("Authorization"))
		require.Equal(t, "mapped-astra", gjson.GetBytes(upstream.bodies[i], "model").String())
		require.Equal(t, tier, gjson.GetBytes(upstream.bodies[i], "service_tier").String())
		require.True(t, gjson.GetBytes(upstream.bodies[i], "stream").Bool())
		select {
		case update := <-updates:
			c, ok := update[openAITierProbeKey("mapped-astra", tier)].(OpenAIServiceTierCapability)
			require.True(t, ok)
			require.Equal(t, "supported", c.Status)
			require.Equal(t, openAITierProbeFingerprint(&a), c.Fingerprint)
		default:
			t.Fatal("missing persisted probe")
		}
	}
}

func TestServiceTierProbeStopsAtTerminalWithoutEOF(t *testing.T) {
	reader := io.MultiReader(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"service_tier\":\"priority\"}}\n\n"), passthroughErrReadCloser{err: context.DeadlineExceeded})
	body, err := readOpenAITierProbeBody(reader)
	require.NoError(t, err)
	require.Equal(t, "supported", classifyOpenAIServiceTierProbe("gpt-6-astra", "priority", 200, body, nil, time.Now()).Status)
	_, err = readOpenAITierProbeBody(strings.NewReader(strings.Repeat("x", (1<<20)+10)))
	require.Error(t, err)
}

func TestServiceTierProbeQueueBoundedAndStop(t *testing.T) {
	p := newOpenAIServiceTierProber(nil)
	require.False(t, p.enqueue(1, "gpt-6-astra", false))
	p.ctx = context.Background()
	require.False(t, p.enqueue(1, "attacker-arbitrary-model", false))
	require.True(t, p.enqueue(1, "gpt-6-astra", false))
	require.True(t, p.enqueue(1, "gpt-6-astra", true))
	require.Len(t, p.queue, 1)
	for i := 2; i <= 256; i++ {
		require.True(t, p.enqueue(int64(i), "gpt-6-astra", false))
	}
	require.False(t, p.enqueue(257, "gpt-6-astra", false))
	p = newOpenAIServiceTierProber(nil)
	p.start()
	p.stop()
	require.False(t, p.enqueue(1, "gpt-6-astra", true))
}

func TestServiceTierHeaderChangeInvalidatesCapability(t *testing.T) {
	a := tierTestAccount(1, "")
	a.Credentials["header_override_enabled"] = true
	a.Credentials["header_overrides"] = map[string]any{"x-route": "one"}
	before := openAITierProbeFingerprint(&a)
	a.Credentials["header_overrides"] = map[string]any{"x-route": "two"}
	require.NotEqual(t, before, openAITierProbeFingerprint(&a))
}

func TestServiceTierNativeResponsesFallbackReachesWire(t *testing.T) {
	for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
		t.Run(accountType, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			a := tierTestAccount(9, "")
			a.Type = accountType
			a.Credentials["access_token"] = "test"
			body := []byte(`{"model":"gpt-6-astra","instructions":"test","input":"hi","stream":true,"service_tier":"ultrafast"}`)
			ctx := WithOpenAIServiceTierRouting(context.Background(), body)
			openAITierRouting(ctx).selectTier("default", a.ID)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body)).WithContext(ctx)
			upstream := &httpUpstreamRecorder{err: errors.New("stop after wire capture")}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			_, err := svc.Forward(ctx, c, &a, body)
			require.Error(t, err)
			require.NotNil(t, upstream.lastReq)
			require.Equal(t, "default", gjson.GetBytes(upstream.lastBody, "service_tier").String())
		})
	}
}

func TestServiceTierChannelForwardModelIsUsed(t *testing.T) {
	a := tierTestAccount(1, "priority")
	ctx := WithOpenAIForwardModel(context.Background(), "gpt-6-astra", false)
	ctx = context.WithValue(ctx, openAITierRequiredKey{}, "priority")
	require.True(t, openAITierCandidateEligible(ctx, &a, "public-alias"))
	require.False(t, openAITierCandidateEligible(context.WithValue(context.Background(), openAITierRequiredKey{}, "priority"), &a, "public-alias"))
}

func TestServiceTierConvertedProtocolsFallbackReachesWire(t *testing.T) {
	for _, protocol := range []string{"chat", "messages"} {
		t.Run(protocol, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			a := tierTestAccount(10, "")
			body := []byte(`{"model":"gpt-6-astra","messages":[{"role":"user","content":"hi"}],"max_tokens":32,"stream":true,"service_tier":"priority"}`)
			ctx := WithOpenAIServiceTierRouting(context.Background(), body)
			openAITierRouting(ctx).selectTier("default", a.ID)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+protocol, bytes.NewReader(body)).WithContext(ctx)
			c.Request.Header.Set("anthropic-beta", "fast-mode-2026-02-01")
			upstream := &httpUpstreamRecorder{err: errors.New("stop after wire capture")}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			var err error
			if protocol == "chat" {
				_, err = svc.ForwardAsChatCompletions(ctx, c, &a, body, "", "")
			} else {
				_, err = svc.ForwardAsAnthropic(ctx, c, &a, body, "", "")
			}
			require.Error(t, err)
			require.NotNil(t, upstream.lastReq)
			require.Equal(t, "default", gjson.GetBytes(upstream.lastBody, "service_tier").String())
		})
	}
}
