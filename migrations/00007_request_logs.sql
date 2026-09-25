-- 合并 V1 的 Usage + Request Session 为单一事实来源，按天分区。见技术方案 §6.8。
-- 本迁移创建父表 + 启动所需的若干天分区；后续分区由 worker 按计划任务自动创建
-- （internal/reqlog，尚未实现——启动前需确保未来分区存在，否则插入会失败）。

-- +goose Up
-- +goose StatementBegin
CREATE TABLE request_logs (
    request_id          TEXT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL,
    account_id          BIGINT NOT NULL,
    api_key_id          BIGINT NOT NULL,
    virtual_model       TEXT NOT NULL,
    channel_id          BIGINT,
    provider_key_id     BIGINT,
    endpoint            TEXT NOT NULL,
    is_stream           BOOLEAN NOT NULL,
    status              TEXT NOT NULL,
    http_status         INT,
    error_code          TEXT,
    attempts            SMALLINT NOT NULL DEFAULT 1,
    attempt_trace       JSONB,
    ttft_ms             INT,
    latency_ms          INT,
    input_tokens        INT,
    cache_read_tokens   INT,
    cache_write_tokens  INT,
    output_tokens       INT,
    reasoning_tokens    INT,
    usage_source        TEXT NOT NULL CHECK (usage_source IN ('upstream','estimated','mixed')),
    sell_price_book_id  BIGINT,
    cost_price_book_id  BIGINT,
    promotion_ids       BIGINT[],
    list_amount         BIGINT,
    charged_amount      BIGINT,
    cost_amount         BIGINT,
    upstream_cost       NUMERIC(20,10),
    fx_rate             NUMERIC(12,6),
    client_ip           INET,
    user_agent          TEXT,
    PRIMARY KEY (request_id, created_at)
) PARTITION BY RANGE (created_at);

CREATE INDEX idx_request_logs_account ON request_logs (account_id, created_at DESC);
-- +goose StatementEnd

-- 启动分区：今天起 14 天，够本地开发与首次部署使用。
-- +goose StatementBegin
DO $$
DECLARE
    d date := current_date;
BEGIN
    FOR i IN 0..13 LOOP
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS request_logs_%s PARTITION OF request_logs FOR VALUES FROM (%L) TO (%L)',
            to_char(d + i, 'YYYYMMDD'),
            d + i,
            d + i + 1
        );
    END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS request_logs CASCADE;
-- +goose StatementEnd
