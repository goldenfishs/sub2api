package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const openAITierProbePrefix = "openai_tier_probe_"
const openAITierProbeTTL = 6 * time.Hour

type OpenAIServiceTierCapability struct {
	Model        string    `json:"model"`
	Tier         string    `json:"tier"`
	Status       string    `json:"status"`
	ObservedTier string    `json:"observed_tier,omitempty"`
	CheckedAt    time.Time `json:"checked_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	HTTPStatus   int       `json:"http_status,omitempty"`
	Detail       string    `json:"detail,omitempty"`
	Fingerprint  string    `json:"fingerprint"`
}

func acceleratedOpenAITier(tier string) string {
	switch normalizedOpenAIServiceTierValue(tier) {
	case OpenAIFastTierPriority:
		return OpenAIFastTierPriority
	case OpenAIFastTierUltrafast:
		return OpenAIFastTierUltrafast
	default:
		return ""
	}
}

func openAITierProbeModel(account *Account, model string) string {
	model = strings.TrimSpace(model)
	if !account.IsOpenAIPassthroughEnabled() {
		model = account.GetMappedModel(model)
	}
	if account.IsOpenAIOAuthLike() {
		model = normalizeOpenAIModelForUpstream(account, model)
	}
	return model
}

func openAITierProbeKey(model, tier string) string {
	h := sha256.Sum256([]byte(model + "\x00" + tier))
	return openAITierProbePrefix + hex.EncodeToString(h[:12])
}

// A result belongs to the actual endpoint and credential identity, not just its
// row ID. Credential / URL / header edits invalidate old observations immediately.
func openAITierProbeFingerprint(a *Account) string {
	b, _ := json.Marshal([]any{a.Type, a.GetOpenAIBaseURL(), a.GetOpenAIProtocolAPIKey(), a.GetCredential("chatgpt_account_id"), a.GetCredential("refresh_token"), a.Credentials[credKeyHeaderOverrides], a.Credentials[credKeyHeaderOverrideEnabled], a.ProxyID, a.ParentAccountID})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:16])
}

func openAITierCapability(a *Account, model, tier string) *OpenAIServiceTierCapability {
	if a == nil || a.Extra == nil {
		return nil
	}
	raw, ok := a.Extra[openAITierProbeKey(model, tier)]
	if !ok {
		return nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var c OpenAIServiceTierCapability
	if json.Unmarshal(b, &c) != nil || c.Model != model || c.Tier != tier || c.Fingerprint != openAITierProbeFingerprint(a) {
		return nil
	}
	return &c
}

func accountSupportsOpenAIServiceTier(a *Account, model, tier string, now time.Time) bool {
	if a == nil || a.Platform != PlatformOpenAI {
		return false
	}
	c := openAITierCapability(a, openAITierProbeModel(a, model), tier)
	return c != nil && c.Status == "supported" && now.Before(c.ExpiresAt)
}

// A 200 response is not evidence of accelerated execution. Only a completed
// response that declares the requested tier establishes confirmed support.
func classifyOpenAIServiceTierProbe(model, tier string, status int, body []byte, probeErr error, now time.Time) OpenAIServiceTierCapability {
	c := OpenAIServiceTierCapability{Model: model, Tier: tier, Status: "unknown", CheckedAt: now, ExpiresAt: now.Add(openAITierProbeTTL), HTTPStatus: status}
	if probeErr != nil {
		c.Status = "error"
		c.Detail = "probe_transport_error"
		c.ExpiresAt = now.Add(15 * time.Minute)
		return c
	}
	if status < 200 || status >= 300 {
		c.Status = "error"
		c.Detail = "probe_http_error"
		c.ExpiresAt = now.Add(15 * time.Minute)
		msg := strings.ToLower(extractUpstreamErrorMessage(body))
		if (status == 400 || status == 403 || status == 422) && (strings.Contains(msg, "service_tier") || strings.Contains(msg, tier)) && (strings.Contains(msg, "not support") || strings.Contains(msg, "unsupported") || strings.Contains(msg, "not available") || strings.Contains(msg, "not allowed") || strings.Contains(msg, "not eligible")) {
			c.Status = "unsupported"
			c.Detail = "tier_rejected"
			c.ExpiresAt = now.Add(openAITierProbeTTL)
		}
		return c
	}
	final, ok := extractCodexFinalResponse(string(body))
	if !ok && gjson.GetBytes(body, "status").String() == "completed" {
		final = body
		ok = true
	}
	if !ok || gjson.GetBytes(final, "status").String() != "completed" {
		c.Status = "error"
		c.Detail = "probe_not_completed"
		c.ExpiresAt = now.Add(15 * time.Minute)
		return c
	}
	c.ObservedTier = normalizedOpenAIServiceTierValue(gjson.GetBytes(final, "service_tier").String())
	if c.ObservedTier == tier {
		c.Status = "supported"
		c.Detail = "response_confirmed"
	} else {
		c.Detail = "tier_not_confirmed"
	}
	return c
}

type openAITierRequiredKey struct{}
type openAITierCandidatePolicy func(*Account, string) string

func openAITierCandidateEligible(ctx context.Context, account *Account, model string) bool {
	requirement := ctx.Value(openAITierRequiredKey{})
	if policy, ok := requirement.(openAITierCandidatePolicy); ok {
		tier := policy(account, model)
		return tier != "" && accountSupportsOpenAIServiceTier(account, model, tier, time.Now())
	}
	tier, _ := requirement.(string)
	return tier == "" || accountSupportsOpenAIServiceTier(account, model, tier, time.Now())
}

type openAITierRoutingKey struct{}
type openAITierRoutingState struct {
	mu        sync.Mutex
	requested string
	effective string
	accountID int64
}

// Each request / WS connection owns its state. Nothing is shared between users.
func WithOpenAIServiceTierRouting(ctx context.Context, body []byte) context.Context {
	return context.WithValue(ctx, openAITierRoutingKey{}, &openAITierRoutingState{requested: acceleratedOpenAITier(openAIRequestPayloadView(body).Get("service_tier").String())})
}
func openAITierRouting(ctx context.Context) *openAITierRoutingState {
	if ctx == nil {
		return nil
	}
	v, _ := ctx.Value(openAITierRoutingKey{}).(*openAITierRoutingState)
	return v
}
func (r *openAITierRoutingState) requestedTier() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.requested
}
func (r *openAITierRoutingState) selectTier(tier string, id int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.effective = tier
	r.accountID = id
}
func applyOpenAITierRoutingFallback(ctx context.Context, account *Account, body []byte) ([]byte, error) {
	r := openAITierRouting(ctx)
	if r == nil || account == nil {
		return body, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.accountID == account.ID && r.effective == "default" && acceleratedOpenAITier(gjson.GetBytes(body, "service_tier").String()) != "" {
		return sjson.SetBytes(body, "service_tier", "default")
	}
	return body, nil
}

// Two complete scheduler passes preserve all existing group, pricing, health,
// quota, exclusion, sticky and concurrency checks. Ordinary accounts never compete
// with confirmed accelerated accounts in the first pass.
func (s *OpenAIGatewayService) selectAccountWithServiceTier(ctx context.Context, groupID *int64, model string, excluded map[int64]struct{}, selectFn func(context.Context, map[int64]struct{}) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error)) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	r := openAITierRouting(ctx)
	if r == nil || r.requestedTier() == "" {
		return selectFn(ctx, excluded)
	}
	tier := r.requestedTier()
	// Account-scoped policy can force priority or filter the client's tier.
	// Evaluate the same policy as forwarding before checking each capability.
	policy := openAITierCandidatePolicy(func(a *Account, model string) string {
		body, _ := json.Marshal(map[string]string{"service_tier": tier})
		updated, err := s.applyOpenAIFastPolicyToBodyBeforeRouting(ctx, a, openAITierProbeModel(a, model), body)
		if err != nil {
			return ""
		}
		return acceleratedOpenAITier(gjson.GetBytes(updated, "service_tier").String())
	})
	accounts, err := s.listSchedulableAccounts(ctx, groupID, PlatformOpenAI)
	if err != nil {
		return nil, OpenAIAccountScheduleDecision{}, err
	}
	preferredExcluded := cloneExcludedAccountIDs(excluded)
	if preferredExcluded == nil {
		preferredExcluded = make(map[int64]struct{})
	}
	for i := range accounts {
		a := &accounts[i]
		effectiveTier := policy(a, model)
		if effectiveTier == "" || !accountSupportsOpenAIServiceTier(a, model, effectiveTier, time.Now()) {
			preferredExcluded[a.ID] = struct{}{}
		}
		if s.tierProber != nil {
			probeModel := openAITierProbeModel(a, model)
			c := openAITierCapability(a, probeModel, tier)
			if c == nil || !time.Now().Before(c.ExpiresAt) {
				s.tierProber.enqueue(a.ID, model, false)
			}
		}
	}
	selection, decision, err := selectFn(context.WithValue(ctx, openAITierRequiredKey{}, policy), preferredExcluded)
	if err == nil && selection != nil && selection.Account != nil {
		// A wait plan still represents eligible capacity and must not silently send
		// this request to a standard account merely to bypass the normal queue.
		r.selectTier(policy(selection.Account, model), selection.Account.ID)
		return selection, decision, nil
	}
	if err != nil && !errors.Is(err, ErrNoAvailableAccounts) && !errors.Is(err, ErrNoAvailableCompactAccounts) {
		return selection, decision, err
	}
	selection, decision, err = selectFn(ctx, excluded)
	if err == nil && selection != nil && selection.Account != nil {
		r.selectTier("default", selection.Account.ID)
	}
	return selection, decision, err
}

// WS cannot move an established response chain between accounts. Reconnect
// before a changed model/tier is sent, allowing normal selection to run again.
func ValidateOpenAIServiceTierWSTurn(ctx context.Context, account *Account, body []byte, model string) bool {
	r := openAITierRouting(ctx)
	if r == nil {
		return true
	}
	requested := acceleratedOpenAITier(openAIRequestPayloadView(body).Get("service_tier").String())
	r.mu.Lock()
	defer r.mu.Unlock()
	if requested != r.requested {
		return false
	}
	if requested != "" && r.effective != "default" && !accountSupportsOpenAIServiceTier(account, model, r.effective, time.Now()) {
		return false
	}
	return true
}

// Account edits invalidate badges as well as scheduling, without exposing the
// credential fingerprint in the admin API response.
func OpenAIServiceTierDisplayExtra(account *Account, extra map[string]any) map[string]any {
	if account == nil || len(extra) == 0 {
		return extra
	}
	out := make(map[string]any, len(extra))
	for key, value := range extra {
		out[key] = value
		if !strings.HasPrefix(key, openAITierProbePrefix) {
			continue
		}
		b, err := json.Marshal(value)
		if err != nil {
			continue
		}
		var c OpenAIServiceTierCapability
		if json.Unmarshal(b, &c) != nil {
			continue
		}
		if c.Fingerprint != openAITierProbeFingerprint(account) {
			c.Status = "unknown"
			c.Detail = "account_changed"
		}
		c.Fingerprint = ""
		out[key] = c
	}
	return out
}
