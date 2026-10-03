-- 运营后台智能体（Harness）：会话、消息、工具调用与审计关联，见
-- 《运营后台 Agent 模块（Harness 智能体）技术架构设计方案》§4、实施方案 M0-B03。
--
-- 只做加法：新表 + admin_audit_logs 两个可空新列 + 给 operator/pricing 角色追加 agent:use。
-- 关闭 agent.enabled 即可回滚功能，无需回退本迁移。

-- +goose Up
-- +goose StatementBegin
CREATE TABLE agent_sessions (
    id            BIGSERIAL PRIMARY KEY,
    admin_user_id BIGINT NOT NULL REFERENCES admin_users(id),
    title         TEXT   NOT NULL DEFAULT '',
    playbook      TEXT,                                  -- 可空：自由对话
    context_ref   JSONB,                                 -- 页面入口带入的对象 [{type, id, label}]
    mode          TEXT   NOT NULL DEFAULT 'interactive' CHECK (mode IN ('interactive','batch')),
    status        TEXT   NOT NULL DEFAULT 'idle'
                  CHECK (status IN ('idle','running','awaiting_approval','completed','stopped','failed')),
    status_reason TEXT   NOT NULL DEFAULT '',
    model         TEXT   NOT NULL DEFAULT '',
    job_id        BIGINT,                                -- 后台作业产生的会话（00035 加外键）
    -- 当前运行：run_id 非空且 status='running' 表示有运行占位；run_deadline 过后视为失联可被接管。
    run_id           TEXT,
    run_deadline     TIMESTAMPTZ,
    cancel_requested BOOLEAN NOT NULL DEFAULT false,
    tokens_in     BIGINT NOT NULL DEFAULT 0,
    tokens_out    BIGINT NOT NULL DEFAULT 0,
    turns         INT    NOT NULL DEFAULT 0,
    archived      BOOLEAN NOT NULL DEFAULT false,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_agent_sessions_user ON agent_sessions (admin_user_id, updated_at DESC);

CREATE TABLE agent_messages (
    id           BIGSERIAL PRIMARY KEY,
    session_id   BIGINT NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
    seq          INT    NOT NULL,
    role         TEXT   NOT NULL CHECK (role IN ('user','assistant','tool','summary','report')),
    content      TEXT   NOT NULL DEFAULT '',
    tool_calls   JSONB,                                  -- assistant 发起的调用 [{id, name, arguments}]
    tool_call_id TEXT,                                   -- role=tool 时对应的调用
    compacted    BOOLEAN NOT NULL DEFAULT false,         -- 已被摘要替代（原文保留）
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (session_id, seq)
);

CREATE TABLE agent_tool_calls (
    id            TEXT PRIMARY KEY,                      -- "s{session_id}_{模型返回的 call id}"，全局唯一
    session_id    BIGINT NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
    run_id        TEXT   NOT NULL DEFAULT '',
    tool          TEXT   NOT NULL,
    risk          TEXT   NOT NULL CHECK (risk IN ('read','write')),
    args          JSONB  NOT NULL,
    args_hash     TEXT   NOT NULL DEFAULT '',
    status        TEXT   NOT NULL
                  CHECK (status IN ('running','done','error','denied','pending_approval','approved','rejected','stale','executed','failed','superseded')),
    summary       TEXT   NOT NULL DEFAULT '',
    required_perm TEXT   NOT NULL DEFAULT '',
    etag          TEXT,
    before        JSONB,
    after         JSONB,
    http_status   INT,
    result        JSONB,                                 -- 裁剪/脱敏后的结果
    decided_by    BIGINT REFERENCES admin_users(id),
    decided_at    TIMESTAMPTZ,
    decision_note TEXT,
    duration_ms   INT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_agent_tool_calls_session ON agent_tool_calls (session_id, created_at);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE admin_audit_logs
    ADD COLUMN agent_session_id   BIGINT,
    ADD COLUMN agent_tool_call_id TEXT;
CREATE INDEX idx_admin_audit_logs_agent ON admin_audit_logs (agent_session_id) WHERE agent_session_id IS NOT NULL;

UPDATE admin_roles SET permissions = permissions || ARRAY['agent:use']
 WHERE code IN ('operator','pricing') AND NOT ('agent:use' = ANY(permissions));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE admin_roles SET permissions = array_remove(array_remove(permissions, 'agent:use'), 'agent:admin');
DROP INDEX IF EXISTS idx_admin_audit_logs_agent;
ALTER TABLE admin_audit_logs DROP COLUMN IF EXISTS agent_tool_call_id;
ALTER TABLE admin_audit_logs DROP COLUMN IF EXISTS agent_session_id;
DROP TABLE IF EXISTS agent_tool_calls;
DROP TABLE IF EXISTS agent_messages;
DROP TABLE IF EXISTS agent_sessions;
-- +goose StatementEnd
