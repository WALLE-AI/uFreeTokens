-- 新模型自动发现队列（技术方案 §7.16.3 Mapper 阶段："匹配不到 -> 新模型发现队列，
-- 不自动上架"；Phase 3 的"新模型自动发现与一键上架"）。一条来源观测到的
-- upstream_model 如果在某个 provider 下找不到任何现有渠道，就记一条候选，
-- 等运营人工审核、补齐虚拟模型元数据和路由账号后一键生成草稿并发布。

-- +goose Up
-- +goose StatementBegin
CREATE TABLE pending_model_listings (
    id                         BIGSERIAL PRIMARY KEY,
    provider_id                BIGINT NOT NULL REFERENCES providers(id),
    upstream_model             TEXT NOT NULL,
    source_id                  BIGINT NOT NULL REFERENCES price_sources(id),
    observed_spec              JSONB NOT NULL,
    status                     TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','dismissed','published')),
    published_virtual_model_id BIGINT REFERENCES virtual_models(id),
    published_channel_id       BIGINT REFERENCES channels(id),
    first_observed_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_observed_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at                 TIMESTAMPTZ,
    UNIQUE (provider_id, upstream_model)
);
CREATE INDEX idx_pending_model_listings_status ON pending_model_listings (status, first_observed_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS pending_model_listings;
-- +goose StatementEnd
