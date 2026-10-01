-- 用量小时汇总、request_logs 兜底分区与运营后台检索索引（B8 性能与数据生命周期），见
-- docs/运营后台接口与数据库设计问题分析及执行方案.md §4.5。
--
-- 1. usage_hourly：worker 每 5 分钟把最近 2 小时的 request_logs 汇总进来
--    （internal/reqlog.RollupUsage）。统计接口时间窗超过 48 小时时读汇总表，
--    不再扫描原始日志；延迟分位数用固定分桶的直方图近似（lat_b0..lat_b13）。
--    account_id 是维度之一，所以 count(DISTINCT account_id) 在汇总表上仍是精确的。
-- 2. request_logs_default：兜底分区。此前 worker 停摆、或插入的时间超出已建分区时
--    写入直接失败（计费日志丢失）；现在落进兜底分区并由监控告警。兜底分区里有
--    数据时，覆盖这些数据的新分区无法创建——应尽快把数据挪走。
-- 3. 运营后台列表/检索用到但缺失的索引。生产库数据量大时，这些 CREATE INDEX
--    应改为在维护窗口里按分区 CONCURRENTLY 执行（写在上线手册里）。

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS request_logs_default PARTITION OF request_logs DEFAULT;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE usage_hourly (
    bucket            TIMESTAMPTZ NOT NULL,
    account_id        BIGINT      NOT NULL,
    api_key_id        BIGINT      NOT NULL,
    virtual_model     TEXT        NOT NULL,
    virtual_model_id  BIGINT,
    channel_id        BIGINT,
    provider_id       BIGINT,
    requests          BIGINT NOT NULL,
    success           BIGINT NOT NULL,
    estimated         BIGINT NOT NULL,
    input_tokens      BIGINT NOT NULL,
    output_tokens     BIGINT NOT NULL,
    cache_read_tokens BIGINT NOT NULL,
    reasoning_tokens  BIGINT NOT NULL,
    charged_micro     BIGINT NOT NULL,
    list_micro        BIGINT NOT NULL,
    cost_micro        BIGINT NOT NULL,
    -- 成功请求的总延迟与流式首 token 延迟直方图，桶上界（毫秒）：
    -- 50,100,200,300,500,750,1000,1500,2000,3000,5000,10000,30000,+inf
    lat_b0 BIGINT NOT NULL DEFAULT 0, lat_b1 BIGINT NOT NULL DEFAULT 0, lat_b2 BIGINT NOT NULL DEFAULT 0,
    lat_b3 BIGINT NOT NULL DEFAULT 0, lat_b4 BIGINT NOT NULL DEFAULT 0, lat_b5 BIGINT NOT NULL DEFAULT 0,
    lat_b6 BIGINT NOT NULL DEFAULT 0, lat_b7 BIGINT NOT NULL DEFAULT 0, lat_b8 BIGINT NOT NULL DEFAULT 0,
    lat_b9 BIGINT NOT NULL DEFAULT 0, lat_b10 BIGINT NOT NULL DEFAULT 0, lat_b11 BIGINT NOT NULL DEFAULT 0,
    lat_b12 BIGINT NOT NULL DEFAULT 0, lat_b13 BIGINT NOT NULL DEFAULT 0,
    ttft_b0 BIGINT NOT NULL DEFAULT 0, ttft_b1 BIGINT NOT NULL DEFAULT 0, ttft_b2 BIGINT NOT NULL DEFAULT 0,
    ttft_b3 BIGINT NOT NULL DEFAULT 0, ttft_b4 BIGINT NOT NULL DEFAULT 0, ttft_b5 BIGINT NOT NULL DEFAULT 0,
    ttft_b6 BIGINT NOT NULL DEFAULT 0, ttft_b7 BIGINT NOT NULL DEFAULT 0, ttft_b8 BIGINT NOT NULL DEFAULT 0,
    ttft_b9 BIGINT NOT NULL DEFAULT 0, ttft_b10 BIGINT NOT NULL DEFAULT 0, ttft_b11 BIGINT NOT NULL DEFAULT 0,
    ttft_b12 BIGINT NOT NULL DEFAULT 0, ttft_b13 BIGINT NOT NULL DEFAULT 0,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_usage_hourly_key ON usage_hourly
    (bucket, account_id, api_key_id, virtual_model, channel_id) NULLS NOT DISTINCT;
CREATE INDEX idx_usage_hourly_bucket   ON usage_hourly (bucket);
CREATE INDEX idx_usage_hourly_model    ON usage_hourly (virtual_model, bucket);
CREATE INDEX idx_usage_hourly_channel  ON usage_hourly (channel_id, bucket);
CREATE INDEX idx_usage_hourly_provider ON usage_hourly (provider_id, bucket);
CREATE INDEX idx_usage_hourly_account  ON usage_hourly (account_id, bucket);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_channels_provider_account ON channels (provider_account_id);
CREATE INDEX IF NOT EXISTS idx_channels_upstream_model_trgm ON channels USING gin (upstream_model gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_api_keys_name_trgm ON api_keys USING gin (name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_api_keys_created_by ON api_keys (created_by) WHERE created_by IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_users_email_trgm ON users USING gin ((email::text) gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_provider_accounts_name_trgm ON provider_accounts USING gin (name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_request_logs_api_key_time ON request_logs (api_key_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_request_logs_provider_key_time ON request_logs (provider_key_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_request_logs_error_code_time ON request_logs (error_code, created_at DESC) WHERE error_code IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_request_logs_error_code_time;
DROP INDEX IF EXISTS idx_request_logs_provider_key_time;
DROP INDEX IF EXISTS idx_request_logs_api_key_time;
DROP INDEX IF EXISTS idx_provider_accounts_name_trgm;
DROP INDEX IF EXISTS idx_users_email_trgm;
DROP INDEX IF EXISTS idx_api_keys_created_by;
DROP INDEX IF EXISTS idx_api_keys_name_trgm;
DROP INDEX IF EXISTS idx_channels_upstream_model_trgm;
DROP INDEX IF EXISTS idx_channels_provider_account;
DROP TABLE IF EXISTS usage_hourly;
-- 兜底分区为空时直接删除；有数据时 DETACH 后保留为普通表，由运维决定如何处理。
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM request_logs_default) THEN
        ALTER TABLE request_logs DETACH PARTITION request_logs_default;
    ELSE
        DROP TABLE request_logs_default;
    END IF;
END $$;
-- +goose StatementEnd
