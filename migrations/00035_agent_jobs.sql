-- 运营智能体后台作业（设计 §15.2–15.4，实施方案 M3-B01）：
--   agent_jobs        作业定义、调度、游标、预算与熔断状态；
--   agent_operator    后台智能体专用角色：只有各领域只读权限 + agent:use，永远没有写权限；
--   agent-bot         服务主体（admin_users 行）：密码 '!' 不是合法哈希且状态为 disabled，无法登录；
--                     它只用于只读工具调度，写操作永远以审批人身份执行。
-- 种子作业全部 enabled=false，由运营在「智能作业」页逐个开启（实施方案 §9.1 灰度第 4 步）。

-- +goose Up
-- +goose StatementBegin
CREATE TABLE agent_jobs (
    id                 BIGSERIAL PRIMARY KEY,
    code               TEXT   NOT NULL UNIQUE,
    name               TEXT   NOT NULL DEFAULT '',
    playbook           TEXT   NOT NULL,
    enabled            BOOLEAN NOT NULL DEFAULT false,
    schedule           TEXT   NOT NULL DEFAULT '',           -- 与 price_sources.schedule 同语法；空 = 仅事件/手动
    trigger_query      TEXT,                                 -- 代码内注册的待处理查询名（不存 SQL）
    cursor             JSONB,
    model              TEXT,                                 -- 覆盖默认模型
    daily_token_budget BIGINT NOT NULL DEFAULT 2000000,
    max_items_per_run  INT    NOT NULL DEFAULT 20,
    next_run_at        TIMESTAMPTZ,
    run_requested      BOOLEAN NOT NULL DEFAULT false,       -- 手动“立即运行”
    failure_count      INT    NOT NULL DEFAULT 0,
    last_run_at        TIMESTAMPTZ,
    last_status        TEXT   NOT NULL DEFAULT '',
    last_error         TEXT   NOT NULL DEFAULT '',
    last_session_id    BIGINT REFERENCES agent_sessions(id) ON DELETE SET NULL,
    paused_reason      TEXT   NOT NULL DEFAULT '',           -- circuit_breaker 等：熔断后自动停用
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE agent_sessions ADD CONSTRAINT fk_agent_sessions_job FOREIGN KEY (job_id) REFERENCES agent_jobs(id) ON DELETE SET NULL;
ALTER TABLE agent_proposals ADD CONSTRAINT fk_agent_proposals_job FOREIGN KEY (job_id) REFERENCES agent_jobs(id) ON DELETE SET NULL;
CREATE INDEX idx_agent_sessions_job ON agent_sessions (job_id, created_at DESC) WHERE job_id IS NOT NULL;
CREATE INDEX idx_agent_proposals_job ON agent_proposals (job_id, id DESC) WHERE job_id IS NOT NULL;

INSERT INTO admin_roles (code, name, permissions) VALUES
    ('agent_operator', '后台智能体（只读）', ARRAY['catalog:read','pricing:read','observe:read','agent:use'])
ON CONFLICT (code) DO NOTHING;

INSERT INTO admin_users (email, name, password_hash, status)
VALUES ('agent-bot@admin.local', 'agent-bot', '!', 'disabled')
ON CONFLICT (email) DO NOTHING;
INSERT INTO admin_user_roles (admin_user_id, role_code)
SELECT id, 'agent_operator' FROM admin_users WHERE email = 'agent-bot@admin.local'
ON CONFLICT DO NOTHING;

INSERT INTO agent_jobs (code, name, playbook, schedule, trigger_query, max_items_per_run) VALUES
    ('price_change_triage',   '调价预审',     'price_triage',          '@every 1h', 'pending_price_changes', 20),
    ('listing_triage',        '待上架处理',   'listing_triage',        '@every 1h', 'pending_listings',      10),
    ('offer_triage',          '优惠分拣',     'offer_triage',          '@every 1h', 'new_offers',            10),
    ('alias_matching',        '榜单模型映射', 'alias_matching',        '@daily',    'pending_aliases',       30),
    ('held_run_diagnosis',    '扣留运行诊断', 'held_run_diagnosis',    '@every 1h', 'held_benchmark_runs',   3),
    ('metadata_enrich',       '元数据补全',   'metadata_enrich',       '@daily',    'missing_metadata',      5),
    ('public_app_governance', '应用榜治理',   'public_app_governance', '@daily',    NULL,                    20),
    ('source_diagnosis',      '数据源诊断',   'source_diagnosis',      '@every 1h', 'failing_sources',       3)
ON CONFLICT (code) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE agent_proposals DROP CONSTRAINT IF EXISTS fk_agent_proposals_job;
ALTER TABLE agent_sessions DROP CONSTRAINT IF EXISTS fk_agent_sessions_job;
DROP INDEX IF EXISTS idx_agent_proposals_job;
DROP INDEX IF EXISTS idx_agent_sessions_job;
DROP TABLE IF EXISTS agent_jobs;
DELETE FROM admin_user_roles WHERE role_code = 'agent_operator';
-- agent-bot 可能已被会话引用：保留用户行，只撤销角色（迁移只做加法，回退不丢会话数据外键）。
DELETE FROM admin_roles WHERE code = 'agent_operator';
-- +goose StatementEnd
