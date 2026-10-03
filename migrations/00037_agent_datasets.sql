-- 全局助手的数据集（《运营后台全局助手执行方案》P2）：query_analytics 的完整结果存在这里，
-- 模型只看到 dataset_id + 预览行；图表、报表只能引用数据集里的数字（防止模型编造）。
-- 另给钱包流水补一个按时间的部分索引：分析查询按时间汇总充值/赠金/调账，不扫逐请求的 consume 流水。

-- +goose Up
-- +goose StatementBegin
CREATE TABLE agent_datasets (
    id           BIGSERIAL PRIMARY KEY,
    session_id   BIGINT NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
    tool_call_id TEXT   NOT NULL DEFAULT '',
    title        TEXT   NOT NULL DEFAULT '',
    query        JSONB  NOT NULL,                        -- 规范化后的查询参数（可复现）
    columns      JSONB  NOT NULL,
    rows         JSONB  NOT NULL,
    totals       JSONB,
    previous     JSONB,
    notes        JSONB,
    row_count    INT    NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_agent_datasets_session ON agent_datasets (session_id, id);

CREATE INDEX idx_ledger_entries_created_non_consume ON ledger_entries (created_at) WHERE type <> 'consume';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_ledger_entries_created_non_consume;
DROP TABLE IF EXISTS agent_datasets;
-- +goose StatementEnd
