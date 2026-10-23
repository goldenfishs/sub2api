package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"golang.org/x/sync/singleflight"
)

const (
	SettingKeyRiskSessionConfig = "risk_session_config"
	RiskSessionSnapshotMaxBytes = 32 * 1024 * 1024
	riskSessionMaxKeys          = 128
)

var ErrRiskSessionNotFound = infraerrors.NotFound("RISK_SESSION_NOT_FOUND", "会话封锁记录不存在")

type RiskSessionConfig struct {
	Enabled               bool `json:"enabled"`
	SnapshotRetentionDays int  `json:"snapshot_retention_days"`
}

type RiskSessionCaptureInput struct {
	UserID, APIKeyID                                                  int64
	GroupID                                                           *int64
	RequestID, Source, Reason, Model, Endpoint, UserEmail, APIKeyName string
	Keys                                                              []string
	RequestBody, ResponseBody                                         []byte
	SnapshotScope                                                     string
	ResponseScope                                                     string
}

type RiskSessionIncident struct {
	ID                int64      `json:"id"`
	CreatedAt         time.Time  `json:"created_at"`
	UserID            int64      `json:"user_id"`
	APIKeyID          int64      `json:"api_key_id"`
	GroupID           *int64     `json:"group_id,omitempty"`
	UserEmail         string     `json:"user_email"`
	APIKeyName        string     `json:"api_key_name"`
	RequestID         string     `json:"request_id"`
	Source            string     `json:"source"`
	Reason            string     `json:"reason"`
	Model             string     `json:"model"`
	Endpoint          string     `json:"endpoint"`
	Blocked           bool       `json:"blocked"`
	ReleasedAt        *time.Time `json:"released_at,omitempty"`
	ReleasedByUserID  *int64     `json:"released_by_user_id,omitempty"`
	ReleaseReason     string     `json:"release_reason"`
	SnapshotStatus    string     `json:"snapshot_status"`
	SnapshotScope     string     `json:"snapshot_scope"`
	ResponseScope     string     `json:"response_scope"`
	SnapshotError     string     `json:"snapshot_error,omitempty"`
	SnapshotExpiresAt time.Time  `json:"snapshot_expires_at"`
	RequestBytes      int64      `json:"request_bytes"`
	ResponseBytes     int64      `json:"response_bytes"`
	RequestSHA256     string     `json:"request_sha256"`
	ResponseSHA256    string     `json:"response_sha256"`
	RequestBody       string     `json:"request_body,omitempty"`
	ResponseBody      string     `json:"response_body,omitempty"`
	EncryptedSnapshot string     `json:"-"`
}

type RiskSessionRepository interface {
	Create(context.Context, *RiskSessionIncident, []string) error
	FindBlocked(context.Context, int64, []string) (*RiskSessionIncident, error)
	HasReleased(context.Context, int64, []string) (bool, error)
	List(context.Context, int, int) ([]RiskSessionIncident, int64, error)
	Get(context.Context, int64) (*RiskSessionIncident, error)
	Release(context.Context, int64, int64, string) (*RiskSessionIncident, error)
	CleanupExpired(context.Context, time.Time) (int64, error)
}

type RiskSessionService struct {
	settings      SettingRepository
	repo          RiskSessionRepository
	encryptor     SecretEncryptor
	configMu      sync.Mutex
	cachedConfig  *RiskSessionConfig
	configExpires time.Time
	configVersion uint64
	configReads   singleflight.Group
}

func NewRiskSessionService(settings SettingRepository, repo RiskSessionRepository, encryptor SecretEncryptor) *RiskSessionService {
	return &RiskSessionService{settings: settings, repo: repo, encryptor: encryptor}
}

func (s *RiskSessionService) Config(ctx context.Context) (RiskSessionConfig, error) {
	fallback := RiskSessionConfig{SnapshotRetentionDays: 180}
	if s == nil || s.settings == nil {
		return fallback, nil
	}
	s.configMu.Lock()
	if s.cachedConfig != nil && time.Now().Before(s.configExpires) {
		cfg := *s.cachedConfig
		s.configMu.Unlock()
		return cfg, nil
	}
	s.configMu.Unlock()
	pending := s.configReads.DoChan("config", func() (any, error) {
		s.configMu.Lock()
		version := s.configVersion
		s.configMu.Unlock()
		// A caller abandoning its request must not cancel the shared reload for others.
		loadCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		cfg := RiskSessionConfig{SnapshotRetentionDays: 180}
		raw, err := s.settings.GetValue(loadCtx, SettingKeyRiskSessionConfig)
		if err != nil && !errors.Is(err, ErrSettingNotFound) {
			return cfg, err
		}
		if raw != "" {
			if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
				return cfg, fmt.Errorf("decode risk session config: %w", err)
			}
		}
		if cfg.SnapshotRetentionDays < 1 || cfg.SnapshotRetentionDays > 3650 {
			return cfg, infraerrors.BadRequest("INVALID_RISK_SESSION_RETENTION", "正文保留天数须为 1–3650")
		}
		s.configMu.Lock()
		if version == s.configVersion {
			s.cachedConfig = &cfg
			s.configExpires = time.Now().Add(time.Second)
		}
		s.configMu.Unlock()
		return cfg, nil
	})
	select {
	case <-ctx.Done():
		return fallback, ctx.Err()
	case result := <-pending:
		if result.Err != nil {
			return fallback, result.Err
		}
		cfg, ok := result.Val.(RiskSessionConfig)
		if !ok {
			return fallback, errors.New("invalid risk session config result")
		}
		return cfg, nil
	}
}

func (s *RiskSessionService) UpdateConfig(ctx context.Context, cfg RiskSessionConfig) (RiskSessionConfig, error) {
	if cfg.SnapshotRetentionDays < 1 || cfg.SnapshotRetentionDays > 3650 {
		return cfg, infraerrors.BadRequest("INVALID_RISK_SESSION_RETENTION", "正文保留天数须为 1–3650")
	}
	// New captures use this retention; existing evidence keeps its recorded expiry.
	raw, err := json.Marshal(cfg)
	if err != nil {
		return cfg, err
	}
	if err := s.settings.Set(ctx, SettingKeyRiskSessionConfig, string(raw)); err != nil {
		return cfg, err
	}
	// Invalidate rather than cache this input: concurrent saves may commit in any order.
	s.configMu.Lock()
	s.configVersion++
	s.cachedConfig = nil
	s.configExpires = time.Time{}
	s.configMu.Unlock()
	s.configReads.Forget("config")
	return cfg, nil
}

func riskSessionFingerprints(keys []string) ([]string, error) {
	out := make([]string, 0, len(keys))
	seen := map[string]struct{}{}
	for _, key := range keys {
		if strings.TrimSpace(key) == "" {
			continue
		}
		sum := sha256.Sum256([]byte(key))
		hash := hex.EncodeToString(sum[:])
		if _, ok := seen[hash]; ok {
			continue
		}
		seen[hash] = struct{}{}
		out = append(out, hash)
	}
	return out, nil
}

func riskSessionHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// Capture stores a durable block even if the optional payload cannot be encrypted.
// Only bodies are accepted: authorization headers and cookies are never captured.
func (s *RiskSessionService) Capture(ctx context.Context, in RiskSessionCaptureInput) error {
	cfg, err := s.Config(ctx)
	if err != nil {
		return err
	}
	if !cfg.Enabled {
		return nil
	}
	if in.UserID <= 0 || (in.Source != "local_audit" && in.Source != "upstream_cyber") {
		return errors.New("invalid risk session capture identity or source")
	}
	if len(in.Keys) > riskSessionMaxKeys {
		return errors.New("too many risk session capture fingerprints")
	}
	keys, err := riskSessionFingerprints(in.Keys)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	item := &RiskSessionIncident{CreatedAt: now, UserID: in.UserID, APIKeyID: in.APIKeyID, GroupID: in.GroupID, UserEmail: in.UserEmail, APIKeyName: in.APIKeyName, RequestID: in.RequestID, Source: in.Source, Reason: in.Reason, Model: in.Model, Endpoint: in.Endpoint, Blocked: len(keys) > 0, SnapshotScope: in.SnapshotScope, ResponseScope: in.ResponseScope, SnapshotStatus: "stored", SnapshotExpiresAt: now.AddDate(0, 0, cfg.SnapshotRetentionDays), RequestBytes: int64(len(in.RequestBody)), ResponseBytes: int64(len(in.ResponseBody)), RequestSHA256: riskSessionHash(in.RequestBody), ResponseSHA256: riskSessionHash(in.ResponseBody)}
	if item.SnapshotScope == "" {
		item.SnapshotScope = "received_request_only"
	}
	switch {
	case len(in.RequestBody) == 0 && len(in.ResponseBody) == 0:
		item.SnapshotStatus = "unavailable_empty"
	case int64(len(in.RequestBody))+int64(len(in.ResponseBody)) > RiskSessionSnapshotMaxBytes:
		item.SnapshotStatus = "unavailable_oversize"
		item.SnapshotError = "Snapshot exceeds 32 MiB storage limit; no body was saved."
	case s.encryptor == nil:
		item.SnapshotStatus = "unavailable_encryption"
	default:
		var compressed bytes.Buffer
		zw := gzip.NewWriter(&compressed)
		var header [8]byte
		binary.BigEndian.PutUint64(header[:], uint64(len(in.RequestBody)))
		_, err = zw.Write(header[:])
		if err == nil {
			_, err = zw.Write(in.RequestBody)
		}
		if err == nil {
			_, err = zw.Write(in.ResponseBody)
		}
		closeErr := zw.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			item.EncryptedSnapshot, err = s.encryptor.Encrypt(compressed.String())
		}
		if err != nil {
			item.EncryptedSnapshot = ""
			item.SnapshotStatus = "unavailable_encryption"
			item.SnapshotError = "Snapshot encryption failed; the permanent block remains active."
		}
	}
	return s.repo.Create(ctx, item, keys)
}

func (s *RiskSessionService) FindBlocked(ctx context.Context, userID int64, keys []string) (*RiskSessionIncident, error) {
	cfg, err := s.Config(ctx)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled || userID <= 0 {
		return nil, nil
	}
	fingerprints, err := riskSessionFingerprints(keys)
	if err != nil {
		return nil, err
	}
	if len(fingerprints) == 0 {
		return nil, nil
	}
	// No positive cache: a committed manual release takes effect on the next lookup.
	for offset := 0; offset < len(fingerprints); offset += riskSessionMaxKeys {
		end := min(offset+riskSessionMaxKeys, len(fingerprints))
		item, err := s.repo.FindBlocked(ctx, userID, fingerprints[offset:end])
		if err != nil || item != nil {
			return item, err
		}
	}
	return nil, nil
}

// WasReleased lets an explicit durable release override an older expiring Redis block.
// The caller must check FindBlocked first: a new risk incident always wins.
func (s *RiskSessionService) WasReleased(ctx context.Context, userID int64, keys []string) (bool, error) {
	cfg, err := s.Config(ctx)
	if err != nil {
		return false, err
	}
	if !cfg.Enabled || userID <= 0 {
		return false, nil
	}
	fingerprints, err := riskSessionFingerprints(keys)
	if err != nil {
		return false, err
	}
	for offset := 0; offset < len(fingerprints); offset += riskSessionMaxKeys {
		end := min(offset+riskSessionMaxKeys, len(fingerprints))
		released, err := s.repo.HasReleased(ctx, userID, fingerprints[offset:end])
		if err != nil || released {
			return released, err
		}
	}
	return false, nil
}

func (s *RiskSessionService) List(ctx context.Context, page, pageSize int) ([]RiskSessionIncident, int64, error) {
	return s.repo.List(ctx, page, pageSize)
}

func (s *RiskSessionService) Get(ctx context.Context, id int64) (*RiskSessionIncident, error) {
	item, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if item.SnapshotStatus != "stored" {
		return item, nil
	}
	if !time.Now().Before(item.SnapshotExpiresAt) {
		item.EncryptedSnapshot = ""
		item.SnapshotStatus = "expired"
		return item, nil
	}
	if s.encryptor == nil {
		item.SnapshotStatus = "unreadable"
		item.SnapshotError = "Snapshot decryption unavailable."
		return item, nil
	}
	plain, err := s.encryptor.Decrypt(item.EncryptedSnapshot)
	item.EncryptedSnapshot = ""
	if err == nil {
		var zr *gzip.Reader
		zr, err = gzip.NewReader(strings.NewReader(plain))
		if err == nil {
			var decoded []byte
			decoded, err = io.ReadAll(io.LimitReader(zr, RiskSessionSnapshotMaxBytes+9))
			_ = zr.Close()
			if err == nil && len(decoded) >= 8 && len(decoded) <= RiskSessionSnapshotMaxBytes+8 {
				n := binary.BigEndian.Uint64(decoded[:8])
				body := decoded[8:]
				if n <= uint64(len(body)) && int64(n) == item.RequestBytes && int64(len(body))-int64(n) == item.ResponseBytes && riskSessionHash(body[:n]) == item.RequestSHA256 && riskSessionHash(body[n:]) == item.ResponseSHA256 {
					item.RequestBody = string(body[:n])
					item.ResponseBody = string(body[n:])
					return item, nil
				}
			}
		}
	}
	item.SnapshotStatus = "unreadable"
	item.SnapshotError = "Snapshot could not be decrypted or its integrity check failed."
	return item, nil
}

func (s *RiskSessionService) Release(ctx context.Context, id, actorID int64, reason string) (*RiskSessionIncident, error) {
	if id <= 0 || actorID <= 0 {
		return nil, infraerrors.BadRequest("INVALID_RISK_SESSION_RELEASE", "无效的解封操作")
	}
	if len(reason) > 2000 {
		return nil, infraerrors.BadRequest("INVALID_RISK_SESSION_RELEASE", "解封原因过长")
	}
	return s.repo.Release(ctx, id, actorID, strings.TrimSpace(reason))
}

func (s *RiskSessionService) CleanupExpired(ctx context.Context) error {
	if s == nil || s.repo == nil {
		return nil
	}
	_, err := s.repo.CleanupExpired(ctx, time.Now())
	return err
}

func (s *ContentModerationService) SetRiskSessionService(risk *RiskSessionService) {
	s.riskSessions.Store(risk)
}
func (s *ContentModerationService) SetRiskSessions(risk *RiskSessionService) {
	s.SetRiskSessionService(risk)
}
func (s *ContentModerationService) RiskSessions() *RiskSessionService {
	if s == nil {
		return nil
	}
	return s.riskSessions.Load()
}

// Existing permanent blocks are user-scoped; model/group audit filters do not bypass them.
func (s *ContentModerationService) RiskSessionApplies(ctx context.Context, userID int64) (bool, error) {
	if s == nil || userID <= 0 {
		return false, nil
	}
	snap, err := s.loadRuntimeSnapshot(ctx)
	if err != nil {
		return false, err
	}
	if snap == nil || !snap.riskControlEnabled {
		return false, nil
	}
	_, exempt := snap.allowlistedUsers[userID]
	return !exempt, nil
}

func ProvideContentModerationService(settings SettingRepository, repo ContentModerationRepository, hashes ContentModerationHashCache, groups GroupRepository, users UserRepository, proxies ProxyRepository, invalidator APIKeyAuthCacheInvalidator, email *EmailService, risk *RiskSessionService) *ContentModerationService {
	svc := NewContentModerationService(settings, repo, hashes, groups, users, proxies, invalidator, email)
	svc.SetRiskSessionService(risk)
	return svc
}
