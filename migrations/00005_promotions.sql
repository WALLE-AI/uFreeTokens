-- 促销引擎：成本面（side=cost）与售价面（side=sell）分离。见技术方案 §7.10。

-- +goose Up
-- +goose StatementBegin
CREATE TABLE promotions (
    id            BIGSERIAL PRIMARY KEY,
    name          TEXT NOT NULL,
    side          TEXT NOT NULL CHECK (side IN ('cost','sell')),
    type          TEXT NOT NULL CHECK (type IN ('cost_free','cost_discount','price_discount','free_quota','credit_grant')),
    priority      INT NOT NULL DEFAULT 0,
    stackable     BOOLEAN NOT NULL DEFAULT false,
    scope         JSONB NOT NULL DEFAULT '{}',
    params        JSONB NOT NULL DEFAULT '{}',
    budget_total  BIGINT,
    budget_used   BIGINT NOT NULL DEFAULT 0,
    starts_at     TIMESTAMPTZ NOT NULL,
    ends_at       TIMESTAMPTZ,
    status        TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','paused','ended')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_promotions_active ON promotions (side, status, starts_at, ends_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE promotion_counters (
    promotion_id  BIGINT NOT NULL REFERENCES promotions(id) ON DELETE CASCADE,
    account_id    BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    period_key    TEXT NOT NULL,
    used_tokens   BIGINT NOT NULL DEFAULT 0,
    used_amount   BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (promotion_id, account_id, period_key)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS promotion_counters;
DROP TABLE IF EXISTS promotions;
-- +goose StatementEnd
