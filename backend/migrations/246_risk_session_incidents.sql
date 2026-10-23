-- Permanent session blocks and encrypted triggering-request snapshots are independent.
-- No foreign keys to users/keys: historical evidence survives account deletion.
CREATE TABLE IF NOT EXISTS risk_session_incidents (
 id BIGSERIAL PRIMARY KEY,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 user_id BIGINT NOT NULL,
 api_key_id BIGINT NOT NULL DEFAULT 0,
 group_id BIGINT,
 user_email TEXT NOT NULL DEFAULT '',
 api_key_name TEXT NOT NULL DEFAULT '',
 request_id TEXT NOT NULL DEFAULT '',
 source TEXT NOT NULL CHECK(source IN ('local_audit','upstream_cyber')),
 reason TEXT NOT NULL DEFAULT '',
 model TEXT NOT NULL DEFAULT '',
 endpoint TEXT NOT NULL DEFAULT '',
 released_at TIMESTAMPTZ,
 released_by_user_id BIGINT,
 release_reason TEXT NOT NULL DEFAULT '',
 snapshot_status TEXT NOT NULL,
 snapshot_scope TEXT NOT NULL,
 response_scope TEXT NOT NULL DEFAULT '',
 snapshot_error TEXT NOT NULL DEFAULT '',
 snapshot_expires_at TIMESTAMPTZ NOT NULL,
 request_bytes BIGINT NOT NULL,
 response_bytes BIGINT NOT NULL,
 request_sha256 TEXT NOT NULL,
 response_sha256 TEXT NOT NULL,
 encrypted_snapshot TEXT
);
CREATE TABLE IF NOT EXISTS risk_session_keys (
 user_id BIGINT NOT NULL,
 fingerprint TEXT NOT NULL CHECK(length(fingerprint)=64),
 incident_id BIGINT NOT NULL REFERENCES risk_session_incidents(id) ON DELETE CASCADE,
 PRIMARY KEY(user_id,fingerprint,incident_id)
);
CREATE INDEX IF NOT EXISTS risk_session_keys_incident_idx ON risk_session_keys(incident_id);
CREATE INDEX IF NOT EXISTS risk_session_incidents_created_idx ON risk_session_incidents(created_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS risk_session_incidents_expiry_idx ON risk_session_incidents(snapshot_expires_at) WHERE encrypted_snapshot IS NOT NULL;
