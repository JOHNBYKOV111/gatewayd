-- 0001_init.up.sql

CREATE TABLE IF NOT EXISTS keys (
    id            BIGSERIAL PRIMARY KEY,
    key_hash      TEXT NOT NULL UNIQUE,
    key_prefix    TEXT NOT NULL,
    key_last4     TEXT NOT NULL,
    owner         TEXT NOT NULL DEFAULT '',
    allowed_models TEXT[] NOT NULL DEFAULT '{}',
    rpm_limit     INTEGER NOT NULL DEFAULT 60,
    tpm_limit     INTEGER NOT NULL DEFAULT 100000,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at    TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_keys_hash ON keys(key_hash);

CREATE TABLE IF NOT EXISTS budgets (
    id            BIGSERIAL PRIMARY KEY,
    key_id        BIGINT NOT NULL REFERENCES keys(id) ON DELETE CASCADE,
    period        TEXT NOT NULL,              -- 'daily' | 'monthly'
    limit_rubles  NUMERIC(12, 4) NOT NULL,
    spent_rubles  NUMERIC(12, 4) NOT NULL DEFAULT 0,
    reset_at      TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (key_id, period)
);

CREATE INDEX IF NOT EXISTS idx_budgets_key_period ON budgets(key_id, period);

CREATE TABLE IF NOT EXISTS call_logs (
    id            BIGSERIAL PRIMARY KEY,
    timestamp     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    key_id        BIGINT REFERENCES keys(id) ON DELETE SET NULL,
    model         TEXT NOT NULL,
    provider      TEXT NOT NULL,
    prompt_hash   TEXT NOT NULL DEFAULT '',
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens  INTEGER NOT NULL DEFAULT 0,
    cost_rubles   NUMERIC(12, 6) NOT NULL DEFAULT 0,
    latency_ms    INTEGER NOT NULL DEFAULT 0,
    ttft_ms       INTEGER NOT NULL DEFAULT 0,
    error         TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_call_logs_timestamp ON call_logs(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_call_logs_key_id ON call_logs(key_id);
CREATE INDEX IF NOT EXISTS idx_call_logs_model ON call_logs(model);