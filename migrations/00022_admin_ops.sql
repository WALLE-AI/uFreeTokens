-- 运营后台剩余的数据模型补齐，见
-- docs/运营后台接口与数据库设计问题分析及执行方案.md §4.1、§4.2、§4.5 与 B5/B8。
--
-- 1. channel_health_events：网关熔断器状态变化、上游 Key 进入冷却的事件。网关只把
--    事件推进 Redis 列表（不在请求路径上写库），worker 批量落到这里，供
--    GET /channels/health 展示最近事件与事后排查。
-- 2. job_runs：worker 各周期任务的执行记录（开始/结束/成败/摘要），供运维查看任务
--    是否在跑、上次什么时候成功。
-- 3. provider_accounts.base_url_changed_at：base_url 变更后 24 小时内，带着解密密钥
--    请求上游的 upstream-models 只允许超级管理员调用（防止先改地址再立即外带密钥）。
-- 4. price_books.created_by / price_change_requests.decided_by 外键指向 admin_users：
--    此前是客户端自报的数字；历史上找不到对应管理员的值归到 system(0)。
-- 5. ledger_entries.journal_id：同一次结算/调账/赠送写下的多条流水共享一个 ID，
--    便于按"一笔业务"对账。历史流水为 NULL。
-- 6. request_logs.virtual_model_id：请求日志同时记录模型 ID（此前只有名称）。
-- 7. admin_users 的 TOTP 两步验证（可选，按管理员启用）。密钥用 KEK 信封加密存储。

-- +goose Up
-- +goose StatementBegin
CREATE TABLE channel_health_events (
    id              BIGSERIAL PRIMARY KEY,
    channel_id      BIGINT,
    provider_key_id BIGINT,
    event           TEXT        NOT NULL CHECK (event IN ('breaker_open','breaker_half_open','breaker_closed','key_cooldown')),
    detail          JSONB,
    gateway         TEXT,
    occurred_at     TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_channel_health_events_channel ON channel_health_events (channel_id, occurred_at DESC) WHERE channel_id IS NOT NULL;
CREATE INDEX idx_channel_health_events_key ON channel_health_events (provider_key_id, occurred_at DESC) WHERE provider_key_id IS NOT NULL;
CREATE INDEX idx_channel_health_events_time ON channel_health_events (occurred_at DESC);

CREATE TABLE job_runs (
    id          BIGSERIAL PRIMARY KEY,
    job         TEXT        NOT NULL,
    started_at  TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ,
    status      TEXT        NOT NULL CHECK (status IN ('running','success','failed')),
    detail      JSONB
);
CREATE INDEX idx_job_runs_job ON job_runs (job, started_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE provider_accounts ADD COLUMN base_url_changed_at TIMESTAMPTZ;

UPDATE price_books SET created_by = 0 WHERE created_by IS NOT NULL AND created_by NOT IN (SELECT id FROM admin_users);
UPDATE price_change_requests SET decided_by = 0 WHERE decided_by IS NOT NULL AND decided_by NOT IN (SELECT id FROM admin_users);
ALTER TABLE price_books ADD CONSTRAINT fk_price_books_created_by FOREIGN KEY (created_by) REFERENCES admin_users(id);
ALTER TABLE price_change_requests ADD CONSTRAINT fk_price_change_requests_decided_by FOREIGN KEY (decided_by) REFERENCES admin_users(id);

ALTER TABLE ledger_entries ADD COLUMN journal_id UUID;
CREATE INDEX idx_ledger_entries_journal ON ledger_entries (journal_id) WHERE journal_id IS NOT NULL;

ALTER TABLE request_logs ADD COLUMN virtual_model_id BIGINT;

ALTER TABLE admin_users
    ADD COLUMN totp_secret_enc  BYTEA,
    ADD COLUMN totp_dek_wrapped BYTEA,
    ADD COLUMN totp_enabled     BOOLEAN NOT NULL DEFAULT false,
    -- 最近一次验证通过的 30 秒时间片：同一个验证码不能被重放
    ADD COLUMN totp_last_step   BIGINT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE admin_users DROP COLUMN IF EXISTS totp_last_step, DROP COLUMN IF EXISTS totp_enabled, DROP COLUMN IF EXISTS totp_dek_wrapped, DROP COLUMN IF EXISTS totp_secret_enc;
ALTER TABLE request_logs DROP COLUMN IF EXISTS virtual_model_id;
DROP INDEX IF EXISTS idx_ledger_entries_journal;
ALTER TABLE ledger_entries DROP COLUMN IF EXISTS journal_id;
ALTER TABLE price_change_requests DROP CONSTRAINT IF EXISTS fk_price_change_requests_decided_by;
ALTER TABLE price_books DROP CONSTRAINT IF EXISTS fk_price_books_created_by;
ALTER TABLE provider_accounts DROP COLUMN IF EXISTS base_url_changed_at;
DROP TABLE IF EXISTS job_runs;
DROP TABLE IF EXISTS channel_health_events;
-- +goose StatementEnd
