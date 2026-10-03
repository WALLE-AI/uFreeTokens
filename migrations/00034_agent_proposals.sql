-- 智能体提案收件箱（设计 §15.4，实施方案 M2-B01）：agent_tool_calls 中 risk=write 的调用
-- 都会落一条；交互对话与后台作业共用，是收件箱、页面行内建议与侧栏徽标的数据源。
-- agent_jobs 留到 00035。

-- +goose Up
-- +goose StatementBegin
CREATE TABLE agent_proposals (
    id            BIGSERIAL PRIMARY KEY,
    tool_call_id  TEXT   NOT NULL UNIQUE REFERENCES agent_tool_calls(id) ON DELETE CASCADE,
    session_id    BIGINT NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
    job_id        BIGINT,                                -- 00035 加外键
    playbook      TEXT   NOT NULL DEFAULT '',
    tool          TEXT   NOT NULL,
    target_type   TEXT   NOT NULL,                        -- price_change_request / pending_listing / ...
    target_id     TEXT   NOT NULL,
    summary       TEXT   NOT NULL,
    rationale     TEXT   NOT NULL DEFAULT '',             -- 模型给出的理由
    evidence      JSONB,                                  -- [{url, quote}]，服务端已校验
    confidence    NUMERIC(3,2),
    required_perm TEXT   NOT NULL,
    status        TEXT   NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending','approved','rejected','executed','failed','stale','superseded')),
    decided_by    BIGINT REFERENCES admin_users(id),
    decided_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- 同一对象同一种操作只保留一条待处理提案（后台作业去重）。
CREATE UNIQUE INDEX uq_agent_proposals_pending ON agent_proposals (target_type, target_id, tool) WHERE status = 'pending';
CREATE INDEX idx_agent_proposals_status ON agent_proposals (status, required_perm, created_at DESC);
CREATE INDEX idx_agent_proposals_target ON agent_proposals (target_type, target_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_proposals;
-- +goose StatementEnd
