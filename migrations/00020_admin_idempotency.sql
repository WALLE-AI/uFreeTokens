-- 运营后台 POST 接口的 Idempotency-Key 支持（B7），见
-- docs/运营后台接口与数据库设计问题分析及执行方案.md §3 B7。
--
-- 同一管理员用同一个 Idempotency-Key 重复提交同一请求时，直接重放第一次的响应，
-- 不会重复执行（网络超时后前端自动重试、用户连点提交都不会产生重复数据）。
-- 记录保留 24 小时，过期由 cmd/admin 的清理任务删除。admin 进程不持有 Redis，
-- 所以放在 Postgres 里。

-- +goose Up
-- +goose StatementBegin
CREATE TABLE admin_idempotency_keys (
    admin_user_id  BIGINT      NOT NULL REFERENCES admin_users(id),
    key            TEXT        NOT NULL,
    request_hash   BYTEA       NOT NULL,
    status_code    INT,                 -- NULL = 请求仍在处理中
    response_body  BYTEA,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at   TIMESTAMPTZ,
    PRIMARY KEY (admin_user_id, key)
);
CREATE INDEX idx_admin_idempotency_keys_created ON admin_idempotency_keys (created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS admin_idempotency_keys;
-- +goose StatementEnd
