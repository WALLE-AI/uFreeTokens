-- 上游价格同步流水线（技术方案 §7.16）：价格来源、抓取到的原始观测、待审批/
-- 已处理的变更提案。fx_rates 已在 00004_pricing.sql 建过，这里不重复建。

-- +goose Up
-- +goose StatementBegin
CREATE TABLE price_sources (
    id                BIGSERIAL PRIMARY KEY,
    provider_id       BIGINT REFERENCES providers(id),
    level             TEXT NOT NULL CHECK (level IN ('L1','L2','L3','L4','L5')),
    kind              TEXT NOT NULL CHECK (kind IN ('api','html','dataset','billing','manual')),
    fetcher           TEXT NOT NULL,
    url               TEXT,
    schedule          TEXT NOT NULL DEFAULT '',
    config            JSONB NOT NULL DEFAULT '{}',
    enabled           BOOLEAN NOT NULL DEFAULT true,
    last_success_at   TIMESTAMPTZ,
    last_content_hash BYTEA,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE price_observations (          -- 只追加：每次抓取到的"某模型某时刻的价格"
    id             BIGSERIAL PRIMARY KEY,
    source_id      BIGINT NOT NULL REFERENCES price_sources(id),
    upstream_model TEXT NOT NULL,
    spec           JSONB NOT NULL,         -- 归一化后的 PriceSpec
    spec_hash      BYTEA NOT NULL,
    raw_object     TEXT,                   -- 原始内容/证据（留作审计，本阶段不接对象存储，直接存文本）
    observed_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_price_observations_source_model ON price_observations (source_id, upstream_model, observed_at DESC);
CREATE INDEX idx_price_observations_model_time ON price_observations (upstream_model, observed_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE price_change_requests (
    id               BIGSERIAL PRIMARY KEY,
    channel_id       BIGINT NOT NULL REFERENCES channels(id),
    current_book_id  BIGINT REFERENCES price_books(id),
    proposed_spec    JSONB NOT NULL,
    diff             JSONB NOT NULL,          -- 每个计量项的旧价/新价/变化率 + 校验告警
    max_change_ratio NUMERIC(10,4) NOT NULL DEFAULT 0,
    direction        TEXT NOT NULL CHECK (direction IN ('up','down','mixed','new','removed')),
    evidence         BIGINT[] NOT NULL DEFAULT '{}', -- price_observations.id 列表
    impact_7d        BIGINT,                   -- 按近 7 天用量估算的成本变化（微元）；本阶段未实现，恒为 NULL
    effective_from   TIMESTAMPTZ NOT NULL,
    status           TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','auto_approved','approved','rejected','applied','superseded','blocked')),
    decided_by       BIGINT,
    decided_at       TIMESTAMPTZ,
    applied_book_id  BIGINT REFERENCES price_books(id),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_price_change_requests_status ON price_change_requests (status, created_at);
CREATE INDEX idx_price_change_requests_channel ON price_change_requests (channel_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS price_change_requests;
DROP TABLE IF EXISTS price_observations;
DROP TABLE IF EXISTS price_sources;
-- +goose StatementEnd
