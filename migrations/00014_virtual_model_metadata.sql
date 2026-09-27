-- 运营录入的虚拟模型展示层元数据（技术方案迭代5：GET /v1/catalog 公开目录）。
-- 和 virtual_models 本身的硬性配置（能不能路由、上下文窗口多大）分开存——
-- 运营改文案/评分不应该 touch 被 catalog 快照加载器和路由逻辑依赖的那张表。
-- 一对一：一个虚拟模型最多一行元数据，没有就是运营还没录入过，不是错误。
--
-- request_logs 已经有 (account_id, created_at DESC) 索引（见 00007），
-- GET /console/usage 和 GET /console/logs 复用它，这次迁移不需要新加索引。

-- +goose Up
-- +goose StatementBegin
CREATE TABLE virtual_model_metadata (
    virtual_model_id BIGINT PRIMARY KEY REFERENCES virtual_models(id) ON DELETE CASCADE,
    display_name     TEXT,
    description      TEXT,
    provider_display TEXT,
    tags             TEXT[] NOT NULL DEFAULT '{}',
    scores           JSONB,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS virtual_model_metadata;
-- +goose StatementEnd
