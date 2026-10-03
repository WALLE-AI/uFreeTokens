-- 全局助手的报表（《运营后台全局助手执行方案》P3）：create_report 把文字结论、图表、表格组装成报表，
-- 图表与表格只引用本会话的数据集（agent_datasets，写入后不再修改，等同快照）。
-- visibility：private 仅本人（有 audit:read 的管理员也可查看）；shared 所有 agent:use 管理员可见。
-- 另外播种两个定时报表作业（默认停用，在「智能作业」页开启）：运营日报、经营周报；
-- 并允许工具调用的 risk 取 artifact（create_report：只写报表、不改业务数据，直接执行）。

-- +goose Up
-- +goose StatementBegin
CREATE TABLE agent_reports (
    id             BIGSERIAL PRIMARY KEY,
    session_id     BIGINT NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
    tool_call_id   TEXT   NOT NULL DEFAULT '',
    owner_admin_id BIGINT NOT NULL REFERENCES admin_users(id),
    job_id         BIGINT REFERENCES agent_jobs(id) ON DELETE SET NULL,
    title          TEXT   NOT NULL,
    summary        TEXT   NOT NULL DEFAULT '',
    sections       JSONB  NOT NULL,
    dataset_ids    BIGINT[] NOT NULL DEFAULT '{}',
    visibility     TEXT   NOT NULL DEFAULT 'private' CHECK (visibility IN ('private', 'shared')),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_agent_reports_owner ON agent_reports (owner_admin_id, id DESC);
CREATE INDEX idx_agent_reports_shared ON agent_reports (id DESC) WHERE visibility = 'shared';

ALTER TABLE agent_tool_calls DROP CONSTRAINT agent_tool_calls_risk_check;
ALTER TABLE agent_tool_calls ADD CONSTRAINT agent_tool_calls_risk_check CHECK (risk IN ('read', 'write', 'artifact'));

INSERT INTO agent_jobs (code, name, playbook, schedule, trigger_query, max_items_per_run) VALUES
    ('daily_ops_report',      '运营日报', 'daily_ops_report',      '0 9 * * *', NULL, 1),
    ('weekly_revenue_report', '经营周报', 'weekly_revenue_report', '0 9 * * 1', NULL, 1)
ON CONFLICT (code) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM agent_jobs WHERE code IN ('daily_ops_report', 'weekly_revenue_report');
UPDATE agent_tool_calls SET risk = 'read' WHERE risk = 'artifact';
ALTER TABLE agent_tool_calls DROP CONSTRAINT agent_tool_calls_risk_check;
ALTER TABLE agent_tool_calls ADD CONSTRAINT agent_tool_calls_risk_check CHECK (risk IN ('read', 'write'));
DROP TABLE IF EXISTS agent_reports;
-- +goose StatementEnd
