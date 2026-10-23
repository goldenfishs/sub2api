package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type riskSessionRepository struct{ db *sql.DB }

func NewRiskSessionRepository(db *sql.DB) service.RiskSessionRepository {
	return &riskSessionRepository{db: db}
}

const riskSessionColumns = `i.id,i.created_at,i.user_id,i.api_key_id,i.group_id,i.user_email,i.api_key_name,i.request_id,i.source,i.reason,i.model,i.endpoint,i.released_at,i.released_by_user_id,i.release_reason,
 CASE WHEN i.snapshot_status='stored' AND i.snapshot_expires_at<=NOW() THEN 'expired' ELSE i.snapshot_status END,
 i.snapshot_scope,i.response_scope,i.snapshot_error,i.snapshot_expires_at,i.request_bytes,i.response_bytes,i.request_sha256,i.response_sha256,
 (i.released_at IS NULL AND EXISTS(SELECT 1 FROM risk_session_keys k WHERE k.incident_id=i.id))`

type riskSessionScanner interface{ Scan(...any) error }

func scanRiskSession(row riskSessionScanner, body bool) (*service.RiskSessionIncident, error) {
	item := &service.RiskSessionIncident{}
	args := []any{&item.ID, &item.CreatedAt, &item.UserID, &item.APIKeyID, &item.GroupID, &item.UserEmail, &item.APIKeyName, &item.RequestID, &item.Source, &item.Reason, &item.Model, &item.Endpoint, &item.ReleasedAt, &item.ReleasedByUserID, &item.ReleaseReason, &item.SnapshotStatus, &item.SnapshotScope, &item.ResponseScope, &item.SnapshotError, &item.SnapshotExpiresAt, &item.RequestBytes, &item.ResponseBytes, &item.RequestSHA256, &item.ResponseSHA256, &item.Blocked}
	if body {
		args = append(args, &item.EncryptedSnapshot)
	}
	if err := row.Scan(args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrRiskSessionNotFound
		}
		return nil, err
	}
	return item, nil
}

func (r *riskSessionRepository) Create(ctx context.Context, item *service.RiskSessionIncident, keys []string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('risk_session'),hashtext($1::text))`, item.UserID); err != nil {
		return err
	}
	storedStatus, storedError := item.SnapshotStatus, item.SnapshotError
	if item.EncryptedSnapshot != "" {
		storedStatus = "unavailable_storage"
		storedError = "Snapshot storage did not complete; the permanent block remains active."
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO risk_session_incidents(created_at,user_id,api_key_id,group_id,user_email,api_key_name,request_id,source,reason,model,endpoint,snapshot_status,snapshot_scope,response_scope,snapshot_error,snapshot_expires_at,request_bytes,response_bytes,request_sha256,response_sha256,encrypted_snapshot)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21) RETURNING id`, item.CreatedAt, item.UserID, item.APIKeyID, item.GroupID, item.UserEmail, item.APIKeyName, item.RequestID, item.Source, item.Reason, item.Model, item.Endpoint, storedStatus, item.SnapshotScope, item.ResponseScope, storedError, item.SnapshotExpiresAt, item.RequestBytes, item.ResponseBytes, item.RequestSHA256, item.ResponseSHA256, nil).Scan(&item.ID)
	if err != nil {
		return fmt.Errorf("create risk session incident: %w", err)
	}
	if len(keys) > 0 {
		_, err = tx.ExecContext(ctx, `INSERT INTO risk_session_keys(user_id,fingerprint,incident_id) SELECT $1,UNNEST($2::text[]),$3 ON CONFLICT DO NOTHING`, item.UserID, pq.Array(keys), item.ID)
		if err != nil {
			return fmt.Errorf("create risk session fingerprints: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// Commit the permanent block first. A large payload write failure must not erase it.
	if item.EncryptedSnapshot != "" {
		_, err = r.db.ExecContext(ctx, `UPDATE risk_session_incidents SET encrypted_snapshot=$2,snapshot_status=$3,snapshot_error=$4 WHERE id=$1`, item.ID, item.EncryptedSnapshot, item.SnapshotStatus, item.SnapshotError)
		if err != nil {
			slog.Warn("risk_session.snapshot_storage_failed", "incident_id", item.ID)
			return nil
		}
	}
	return nil
}

func (r *riskSessionRepository) FindBlocked(ctx context.Context, userID int64, keys []string) (*service.RiskSessionIncident, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	item, err := scanRiskSession(r.db.QueryRowContext(ctx, `SELECT `+riskSessionColumns+` FROM risk_session_incidents i WHERE i.released_at IS NULL AND i.user_id=$1 AND EXISTS(SELECT 1 FROM risk_session_keys k WHERE k.incident_id=i.id AND k.user_id=$1 AND k.fingerprint=ANY($2::text[])) ORDER BY i.created_at DESC,i.id DESC LIMIT 1`, userID, pq.Array(keys)), false)
	if errors.Is(err, service.ErrRiskSessionNotFound) {
		return nil, nil
	}
	return item, err
}

func (r *riskSessionRepository) HasReleased(ctx context.Context, userID int64, keys []string) (bool, error) {
	if len(keys) == 0 {
		return false, nil
	}
	var found bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM risk_session_keys k JOIN risk_session_incidents i ON i.id=k.incident_id WHERE k.user_id=$1 AND i.user_id=$1 AND k.fingerprint=ANY($2::text[]) AND i.released_at IS NOT NULL)`, userID, pq.Array(keys)).Scan(&found)
	return found, err
}

func (r *riskSessionRepository) List(ctx context.Context, page, pageSize int) ([]service.RiskSessionIncident, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM risk_session_incidents`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+riskSessionColumns+` FROM risk_session_incidents i ORDER BY i.created_at DESC,i.id DESC LIMIT $1 OFFSET $2`, pageSize, int64(page-1)*int64(pageSize))
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]service.RiskSessionIncident, 0)
	for rows.Next() {
		item, err := scanRiskSession(rows, false)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *item)
	}
	return out, total, rows.Err()
}

func (r *riskSessionRepository) Get(ctx context.Context, id int64) (*service.RiskSessionIncident, error) {
	return scanRiskSession(r.db.QueryRowContext(ctx, `SELECT `+riskSessionColumns+`,CASE WHEN i.snapshot_expires_at>NOW() THEN COALESCE(i.encrypted_snapshot,'') ELSE '' END FROM risk_session_incidents i WHERE i.id=$1`, id), true)
}

func (r *riskSessionRepository) Release(ctx context.Context, id, actorID int64, reason string) (*service.RiskSessionIncident, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var userID int64
	if err = tx.QueryRowContext(ctx, `SELECT user_id FROM risk_session_incidents WHERE id=$1`, id).Scan(&userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrRiskSessionNotFound
		}
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('risk_session'),hashtext($1::text))`, userID); err != nil {
		return nil, err
	}
	// Release the connected same-user session, including concurrent/retried incidents.
	// The advisory lock serializes captures, avoiding a half-released fingerprint cluster.
	_, err = tx.ExecContext(ctx, `WITH RECURSIVE related(id) AS (
 SELECT id FROM risk_session_incidents WHERE id=$1 AND released_at IS NULL
 UNION
 SELECT k2.incident_id FROM related r
 JOIN risk_session_keys k1 ON k1.incident_id=r.id AND k1.user_id=$4
 JOIN risk_session_keys k2 ON k2.user_id=k1.user_id AND k2.fingerprint=k1.fingerprint
 JOIN risk_session_incidents i2 ON i2.id=k2.incident_id AND i2.released_at IS NULL
 ) UPDATE risk_session_incidents SET released_at=NOW(),released_by_user_id=$2,release_reason=$3 WHERE id IN(SELECT id FROM related) AND released_at IS NULL`, id, actorID, strings.TrimSpace(reason), userID)
	if err != nil {
		return nil, err
	}
	item, err := scanRiskSession(tx.QueryRowContext(ctx, `SELECT `+riskSessionColumns+` FROM risk_session_incidents i WHERE i.id=$1`, id), false)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return item, nil
}

func (r *riskSessionRepository) CleanupExpired(ctx context.Context, now time.Time) (int64, error) {
	result, err := r.db.ExecContext(ctx, `UPDATE risk_session_incidents SET encrypted_snapshot=NULL,snapshot_status='expired',snapshot_error='' WHERE encrypted_snapshot IS NOT NULL AND snapshot_expires_at<=$1`, now)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
