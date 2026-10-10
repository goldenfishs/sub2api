package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/tidwall/gjson"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type openAITierProbeJob struct {
	id    int64
	model string
	force bool
}
type openAIServiceTierProber struct {
	service *AccountTestService
	mu      sync.Mutex
	pending map[string]bool
	queue   chan openAITierProbeJob
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
}

func newOpenAIServiceTierProber(s *AccountTestService) *openAIServiceTierProber {
	return &openAIServiceTierProber{service: s, pending: make(map[string]bool), queue: make(chan openAITierProbeJob, 256)}
}
func (p *openAIServiceTierProber) start() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ctx != nil {
		return
	}
	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.done = make(chan struct{})
	go p.run()
}
func (p *openAIServiceTierProber) stop() {
	p.mu.Lock()
	cancel, done := p.cancel, p.done
	p.mu.Unlock()
	if cancel != nil {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}
}

// Bound automatic and manual probing to the advertised baseline. Arbitrary
// downstream model names must never create unbounded paid jobs or metadata.
func openAITierProbeAllowedModel(model string) bool {
	return model == "gpt-6-astra" || model == "gpt-6.1-sol"
}
func (p *openAIServiceTierProber) enqueue(id int64, model string, force bool) bool {
	if p == nil || id <= 0 || !openAITierProbeAllowedModel(model) {
		return false
	}
	key := fmt.Sprintf("%d:%s", id, model)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ctx == nil || p.ctx.Err() != nil {
		return false
	}
	if p.pending[key] {
		return true
	}
	p.pending[key] = true
	select {
	case p.queue <- openAITierProbeJob{id, model, force}:
		return true
	default:
		delete(p.pending, key)
		return false
	}
}
func (p *openAIServiceTierProber) run() {
	defer close(p.done)
	// Startup grace avoids competing with token refresh and readiness checks.
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-timer.C:
			p.scan()
		case <-ticker.C:
			p.scan()
		case job := <-p.queue:
			p.probe(job)
			p.mu.Lock()
			delete(p.pending, fmt.Sprintf("%d:%s", job.id, job.model))
			p.mu.Unlock()
		}
	}
}
func (p *openAIServiceTierProber) scan() {
	ctx, cancel := context.WithTimeout(p.ctx, 15*time.Second)
	defer cancel()
	accounts, err := p.service.accountRepo.ListSchedulableByPlatform(ctx, PlatformOpenAI)
	if err != nil {
		return
	}
	for i := range accounts {
		a := &accounts[i]
		// Automatic baseline checks cover the two official Ultrafast models.
		// Model mappings still probe the real upstream model for each baseline.
		for _, model := range []string{"gpt-6-astra", "gpt-6.1-sol"} {
			if a.IsModelSupported(model) {
				p.enqueue(a.ID, model, false)
			}
		}
	}
}
func (p *openAIServiceTierProber) probe(job openAITierProbeJob) {
	for _, tier := range []string{OpenAIFastTierPriority, OpenAIFastTierUltrafast} {
		ctx, cancel := context.WithTimeout(p.ctx, 60*time.Second)
		account, err := p.service.accountRepo.GetByID(ctx, job.id)
		if err != nil || account == nil || account.Platform != PlatformOpenAI || !account.IsModelSupported(job.model) || !account.IsSchedulableForModelWithContext(ctx, job.model) {
			cancel()
			return
		}
		model := openAITierProbeModel(account, job.model)
		old := openAITierCapability(account, model, tier)
		if !job.force && old != nil && time.Now().Before(old.ExpiresAt) {
			cancel()
			continue
		}
		result := p.service.probeOpenAIServiceTier(ctx, account, model, tier)
		// Cancelled shutdown probes and edits made during flight must not overwrite
		// the previous credential's result or create false capability information.
		if p.ctx.Err() == nil {
			persistCtx, persistCancel := context.WithTimeout(p.ctx, 5*time.Second)
			current, e := p.service.accountRepo.GetByID(persistCtx, job.id)
			if e == nil && current != nil && result.Fingerprint == openAITierProbeFingerprint(current) {
				_ = p.service.accountRepo.UpdateExtra(persistCtx, account.ID, map[string]any{openAITierProbeKey(model, tier): result})
			}
			persistCancel()
		}
		cancel()
	}
}

func (s *AccountTestService) QueueOpenAIServiceTierProbe(ctx context.Context, id int64, model string) (bool, error) {
	a, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return false, err
	}
	if a.Platform != PlatformOpenAI {
		return false, fmt.Errorf("service tier probes require an OpenAI account")
	}
	if model == "" {
		model = "gpt-6-astra"
	}
	if !openAITierProbeAllowedModel(model) || !a.IsModelSupported(model) {
		return false, fmt.Errorf("model is not enabled on this account")
	}
	if s.tierProber == nil {
		return false, fmt.Errorf("service tier probe worker is unavailable")
	}
	return s.tierProber.enqueue(id, model, true), nil
}

func (s *AccountTestService) probeOpenAIServiceTier(ctx context.Context, account *Account, model, tier string) OpenAIServiceTierCapability {
	fingerprint := openAITierProbeFingerprint(account)
	finish := func(status int, body []byte, err error) OpenAIServiceTierCapability {
		r := classifyOpenAIServiceTierProbe(model, tier, status, body, err, time.Now())
		r.Fingerprint = fingerprint
		return r
	}
	credential := account
	if account.IsShadow() {
		var err error
		credential, err = resolveCredentialAccount(ctx, s.accountRepo, account)
		if err != nil {
			return finish(0, nil, err)
		}
	}
	isOAuth := credential.IsOAuth()
	payload := map[string]any{"model": model, "input": []any{map[string]any{"role": "user", "content": "Reply only OK."}}, "instructions": "Reply briefly.", "stream": true, "service_tier": tier, "reasoning": map[string]any{"effort": "low"}}
	endpoint := chatgptCodexAPIURL
	token := ""
	if isOAuth {
		payload["store"] = false
		token = credential.GetOpenAIAccessToken()
	} else {
		base := account.GetOpenAIBaseURL()
		if base == "" {
			base = "https://api.openai.com"
		}
		normalized, err := s.validateUpstreamBaseURL(base)
		if err != nil {
			return finish(0, nil, err)
		}
		endpoint = buildOpenAIResponsesURLForPlatform(account.Platform, normalized)
		token = account.GetOpenAIProtocolAPIKey()
		payload["max_output_tokens"] = 128
	}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return finish(0, nil, err)
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if credential.IsOpenAIAgentIdentity() {
		headers, e := buildAgentIdentityAuthenticationHeaders(ctx, s.accountRepo, s.agentIdentityWS, &s.agentIdentityTaskMu, credential)
		if e != nil {
			return finish(0, nil, e)
		}
		for k, values := range headers {
			for _, v := range values {
				req.Header.Add(k, v)
			}
		}
	} else {
		if token == "" {
			return finish(0, nil, fmt.Errorf("missing credential"))
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	applyOpenAICodexProbeHeaders(req.Header)
	if isOAuth {
		req.Host = "chatgpt.com"
		enforceCodexIdentityHeadersWithUA(req.Header, credential.GetOpenAIUserAgent())
		setOpenAIChatGPTAccountHeaders(req.Header, credential)
		if fp := resolveCodexFingerprintIDsFromRequest(account, req.Header); fp != nil {
			applyCodexFingerprintHeaders(req.Header, fp)
		}
	}
	account.ApplyHeaderOverrides(req.Header)
	setOpenAICodexRoutingHintFromBody(req.Header, credential, b)
	proxy := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxy = account.Proxy.URL()
	}
	resp, err := s.doOpenAIAccountTestUpstream(req, proxy, account, true)
	if err != nil {
		return finish(0, nil, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := readOpenAITierProbeBody(resp.Body)
	return finish(resp.StatusCode, body, err)
}

// Stop as soon as a terminal SSE event arrives, even if the server keeps the
// connection open. JSON responses remain supported, with a shared size bound.
func readOpenAITierProbeBody(reader io.Reader) ([]byte, error) {
	scanner := bufio.NewScanner(io.LimitReader(reader, (1<<20)+1))
	scanner.Buffer(make([]byte, 4096), (1<<20)+1)
	var body bytes.Buffer
	for scanner.Scan() {
		line := scanner.Bytes()
		_, _ = body.Write(line)
		_ = body.WriteByte('\n')
		if body.Len() > 1<<20 {
			return nil, fmt.Errorf("probe response exceeds limit")
		}
		data := strings.TrimSpace(strings.TrimPrefix(string(line), "data:"))
		switch gjson.Get(data, "type").String() {
		case "response.completed", "response.failed", "response.incomplete", "error":
			return body.Bytes(), nil
		}
	}
	return body.Bytes(), scanner.Err()
}
