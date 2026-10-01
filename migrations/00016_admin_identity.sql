-- 运营后台管理员身份、角色与会话（B5 安全基线），见
-- docs/运营后台接口与数据库设计问题分析及执行方案.md §3 B5、§4.1。
--
-- 在此之前 cmd/admin 只有一个共享令牌，审计日志的操作人来自客户端自填的
-- X-Actor-* 请求头，不可信。本迁移之后：
--   - admin_users / admin_sessions 提供真实的登录身份；
--   - admin_roles.permissions 定义角色能做什么（'*' 表示全部）；
--   - admin_audit_logs.actor_id 外键指向 admin_users，id=0 保留给 system
--     （历史审计记录、应急令牌、系统自动操作）。

-- +goose Up
-- +goose StatementBegin
CREATE TABLE admin_users (
    id              BIGSERIAL PRIMARY KEY,
    email           CITEXT NOT NULL UNIQUE,
    name            TEXT   NOT NULL,
    password_hash   TEXT   NOT NULL,
    status          TEXT   NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
    last_login_at   TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- password_hash='!' 不是合法的 argon2id 哈希，system 永远无法登录。
INSERT INTO admin_users (id, email, name, password_hash, status)
VALUES (0, 'system@admin.local', 'system', '!', 'disabled');

CREATE TABLE admin_roles (
    code        TEXT PRIMARY KEY,
    name        TEXT   NOT NULL,
    permissions TEXT[] NOT NULL DEFAULT '{}'
);
INSERT INTO admin_roles (code, name, permissions) VALUES
    ('super_admin', '超级管理员', ARRAY['*']),
    ('operator',    '运营',       ARRAY['account:read','account:write','catalog:read','catalog:write','provider_key:write','pricing:read','observe:read','audit:read']),
    ('pricing',     '定价',       ARRAY['account:read','catalog:read','pricing:read','pricing:write','price_change:approve','observe:read','audit:read']),
    ('finance',     '财务',       ARRAY['account:read','account:write','wallet:adjust','catalog:read','pricing:read','observe:read','audit:read']),
    ('support',     '客服',       ARRAY['account:read','catalog:read','pricing:read','observe:read','audit:read']);

CREATE TABLE admin_user_roles (
    admin_user_id BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
    role_code     TEXT   NOT NULL REFERENCES admin_roles(code),
    PRIMARY KEY (admin_user_id, role_code)
);

-- 会话令牌只存 SHA-256，泄露数据库不等于泄露可用令牌。
CREATE TABLE admin_sessions (
    id                 BIGSERIAL PRIMARY KEY,
    admin_user_id      BIGINT NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
    token_hash         BYTEA  NOT NULL UNIQUE,
    ip                 INET,
    user_agent         TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at         TIMESTAMPTZ NOT NULL,
    absolute_expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_admin_sessions_user ON admin_sessions (admin_user_id);
CREATE INDEX idx_admin_sessions_expires ON admin_sessions (absolute_expires_at);

-- 登录尝试记录，同时用于失败次数限流（admin 进程不持有 Redis）。
CREATE TABLE admin_login_events (
    id            BIGSERIAL PRIMARY KEY,
    email         CITEXT NOT NULL,
    admin_user_id BIGINT REFERENCES admin_users(id),
    success       BOOLEAN NOT NULL,
    ip            INET,
    user_agent    TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_admin_login_events_email ON admin_login_events (email, created_at DESC) WHERE NOT success;
CREATE INDEX idx_admin_login_events_ip ON admin_login_events (ip, created_at DESC) WHERE NOT success;
-- +goose StatementEnd

-- 审计补充上下文；actor_id 改为外键。历史记录的 actor_id 来自客户端自填的
-- X-Actor-ID，没有对应的管理员，统一归到 system(0)，原值保留在 actor_name 里可查。
-- +goose StatementBegin
ALTER TABLE admin_audit_logs
    ADD COLUMN request_id TEXT,
    ADD COLUMN session_id BIGINT,
    ADD COLUMN user_agent TEXT;
UPDATE admin_audit_logs SET actor_id = 0 WHERE actor_id NOT IN (SELECT id FROM admin_users);
ALTER TABLE admin_audit_logs ADD CONSTRAINT fk_admin_audit_logs_actor
    FOREIGN KEY (actor_id) REFERENCES admin_users(id);
CREATE INDEX idx_admin_audit_logs_actor ON admin_audit_logs (actor_id, created_at DESC);
CREATE INDEX idx_admin_audit_logs_action ON admin_audit_logs (action text_pattern_ops, created_at DESC);
-- +goose StatementEnd

-- 上游 base_url 的域名白名单（防 SSRF / 密钥外带）：非空时 base_url 的主机
-- 必须等于其中一项或是其子域名；为空表示只做内网地址拦截。
-- +goose StatementBegin
ALTER TABLE providers ADD COLUMN allowed_hosts TEXT[] NOT NULL DEFAULT '{}';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE providers DROP COLUMN IF EXISTS allowed_hosts;
DROP INDEX IF EXISTS idx_admin_audit_logs_action;
DROP INDEX IF EXISTS idx_admin_audit_logs_actor;
ALTER TABLE admin_audit_logs DROP CONSTRAINT IF EXISTS fk_admin_audit_logs_actor;
ALTER TABLE admin_audit_logs DROP COLUMN IF EXISTS user_agent;
ALTER TABLE admin_audit_logs DROP COLUMN IF EXISTS session_id;
ALTER TABLE admin_audit_logs DROP COLUMN IF EXISTS request_id;
DROP TABLE IF EXISTS admin_login_events;
DROP TABLE IF EXISTS admin_sessions;
DROP TABLE IF EXISTS admin_user_roles;
DROP TABLE IF EXISTS admin_roles;
DROP TABLE IF EXISTS admin_users;
-- +goose StatementEnd
