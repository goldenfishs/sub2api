ALTER TABLE user_subscriptions
    ADD COLUMN IF NOT EXISTS auto_advance_week boolean NOT NULL DEFAULT false;

CREATE INDEX IF NOT EXISTS idx_user_subscriptions_auto_advance_week
    ON user_subscriptions (id)
    WHERE auto_advance_week = true AND status = 'active' AND deleted_at IS NULL;
