-- Provider / 上游 Key / 虚拟模型 / 渠道。运行时统计（延迟、成功率、冷却）不落库，
-- 见技术方案 §7.6；这里只存配置。

-- +goose Up
-- +goose StatementBegin
CREATE TABLE providers (
    id          BIGSERIAL PRIMARY KEY,
    code        TEXT UNIQUE NOT NULL,
    name        TEXT NOT NULL,
    protocol    TEXT NOT NULL CHECK (protocol IN ('openai','anthropic','gemini')),
    currency    TEXT NOT NULL DEFAULT 'CNY',
    status      TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE provider_accounts (
    id              BIGSERIAL PRIMARY KEY,
    provider_id     BIGINT NOT NULL REFERENCES providers(id),
    name            TEXT NOT NULL,
    base_url        TEXT NOT NULL,
    region          TEXT,
    cost_multiplier NUMERIC(6,4) NOT NULL DEFAULT 1,
    extra           JSONB NOT NULL DEFAULT '{}',
    status          TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled'))
);
CREATE INDEX idx_provider_accounts_provider ON provider_accounts (provider_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE provider_keys (
    id                  BIGSERIAL PRIMARY KEY,
    provider_account_id BIGINT NOT NULL REFERENCES provider_accounts(id) ON DELETE CASCADE,
    secret_ciphertext   BYTEA NOT NULL,
    secret_dek_wrapped  BYTEA NOT NULL,
    secret_last4        TEXT NOT NULL,
    rpm_limit           INT,
    tpm_limit           INT,
    concurrency_limit   INT,
    weight              INT NOT NULL DEFAULT 100,
    status              TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled','exhausted','revoked')),
    disabled_reason     TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_provider_keys_account ON provider_keys (provider_account_id) WHERE status = 'active';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE virtual_models (
    id             BIGSERIAL PRIMARY KEY,
    name           TEXT UNIQUE NOT NULL,
    family         TEXT NOT NULL,
    type           TEXT NOT NULL CHECK (type IN ('chat','embedding','image','audio','rerank')),
    context_window INT NOT NULL,
    max_output     INT NOT NULL,
    capabilities   TEXT[] NOT NULL DEFAULT '{}',
    visible_tiers  TEXT[] NOT NULL DEFAULT '{free,pro,enterprise}',
    status         TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','deprecated','hidden')),
    aliases        TEXT[] NOT NULL DEFAULT '{}'
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE channels (
    id                  BIGSERIAL PRIMARY KEY,
    virtual_model_id    BIGINT NOT NULL REFERENCES virtual_models(id),
    provider_account_id BIGINT NOT NULL REFERENCES provider_accounts(id),
    upstream_model      TEXT NOT NULL,
    priority            INT NOT NULL DEFAULT 0,
    weight              INT NOT NULL DEFAULT 100,
    capabilities        TEXT[],
    context_window      INT,
    param_overrides     JSONB NOT NULL DEFAULT '{}',
    allowed_tiers       TEXT[],
    status              TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
    UNIQUE (virtual_model_id, provider_account_id, upstream_model)
);
CREATE INDEX idx_channels_vm ON channels (virtual_model_id) WHERE status = 'active';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS channels;
DROP TABLE IF EXISTS virtual_models;
DROP TABLE IF EXISTS provider_keys;
DROP TABLE IF EXISTS provider_accounts;
DROP TABLE IF EXISTS providers;
-- +goose StatementEnd
