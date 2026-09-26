-- ClickHouse 用量分析 ETL 的增量同步游标（技术方案 §7.13："Phase2 起 worker 把
-- 分区数据同步到 ClickHouse"）。见 internal/chsync 包文档——这里只有游标状态
-- 表，没有 ClickHouse 本身，也没有真正写 ClickHouse 的 Sink 实现。

-- +goose Up
-- +goose StatementBegin
-- last_created_at 默认值用一个足够早的具体时间戳，不用 -infinity——Go 的
-- time.Time 没法表示 -infinity，pgx 扫描时会直接报错，见 internal/chsync。
CREATE TABLE analytics_sync_state (
    name            TEXT PRIMARY KEY,
    last_created_at TIMESTAMPTZ NOT NULL DEFAULT '0001-01-01T00:00:00Z',
    last_request_id TEXT NOT NULL DEFAULT '',
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS analytics_sync_state;
-- +goose StatementEnd
