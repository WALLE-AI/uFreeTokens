-- 账户体系：account 是计费主体（个人或组织），从第一天起就存在，
-- 避免 V1 方案里"多租户放到 Phase4"导致的后期大迁移。见技术方案 §6.1、§6.2。

-- +goose Up
-- +goose StatementBegin
CREATE TABLE accounts (
    id            BIGSERIAL PRIMARY KEY,
    type          TEXT NOT NULL CHECK (type IN ('personal','organization')),
    name          TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended','closed')),
    tier          TEXT NOT NULL DEFAULT 'free',
    credit_limit  BIGINT NOT NULL DEFAULT 0 CHECK (credit_limit >= 0),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE users (
    id             BIGSERIAL PRIMARY KEY,
    email          CITEXT UNIQUE,
    phone          TEXT UNIQUE,
    password_hash  TEXT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended','closed')),
    email_verified BOOLEAN NOT NULL DEFAULT false,
    phone_verified BOOLEAN NOT NULL DEFAULT false,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (email IS NOT NULL OR phone IS NOT NULL)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE account_members (
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT   NOT NULL CHECK (role IN ('owner','admin','developer','billing','viewer')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, user_id)
);
CREATE INDEX idx_account_members_user ON account_members (user_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE api_keys (
    id                BIGSERIAL PRIMARY KEY,
    account_id        BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    created_by        BIGINT REFERENCES users(id),
    name              TEXT NOT NULL,
    display_prefix    TEXT NOT NULL,
    key_hmac          BYTEA NOT NULL UNIQUE,
    status            TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled','revoked')),
    allowed_models    TEXT[],
    allowed_ips       CIDR[],
    rpm_limit         INT,
    tpm_limit         INT,
    concurrency_limit INT,
    budget_limit      BIGINT,
    budget_period     TEXT CHECK (budget_period IN ('none','daily','monthly')),
    expires_at        TIMESTAMPTZ,
    last_used_at      TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_api_keys_account ON api_keys (account_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS account_members;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS accounts;
-- +goose StatementEnd
