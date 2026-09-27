-- 运营后台（frontend/admin）所需的索引与字段，见
-- docs/cmd-admin 运营后台接口补全技术方案.md §11。
--
-- request_logs 的三个索引建在分区父表上，会自动下发到现有和未来的分区。
-- 本地/测试库数据量小，直接建即可；生产环境已有大量数据时建索引会锁表，
-- 需要按分区 CREATE INDEX CONCURRENTLY 后再 ATTACH 到父索引（写在上线手册里，
-- 不放进本迁移）。

-- +goose Up
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_request_logs_model_time   ON request_logs (virtual_model, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_request_logs_channel_time ON request_logs (channel_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_request_logs_errors_time  ON request_logs (created_at DESC) WHERE status <> 'success';
-- +goose StatementEnd

-- 操作人名称：RBAC 落地前的过渡，由 X-Actor-Name 请求头填写（技术方案 §0.6）。
-- +goose StatementBegin
ALTER TABLE admin_audit_logs ADD COLUMN IF NOT EXISTS actor_name TEXT;
CREATE INDEX IF NOT EXISTS idx_admin_audit_logs_time ON admin_audit_logs (created_at DESC, id DESC);
-- +goose StatementEnd

-- 调价审批理由与审批人名称（技术方案 §5.3）。
-- +goose StatementBegin
ALTER TABLE price_change_requests ADD COLUMN IF NOT EXISTS decision_reason TEXT;
ALTER TABLE price_change_requests ADD COLUMN IF NOT EXISTS decided_by_name TEXT;
-- +goose StatementEnd

-- 账户名称模糊检索（技术方案 §4.1）；00001 只启用了 citext、pgcrypto。
-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_accounts_name_trgm ON accounts USING gin (name gin_trgm_ops);
-- +goose StatementEnd

-- 人工调账幂等检查（wallet.Adjust）用的索引。不能建成唯一索引：ledger_entries
-- 只追加、禁止删除，历史上若已有重复的 (account_id, ref_id) 就再也建不起来。
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idx_ledger_entries_admin_ref ON ledger_entries (account_id, ref_id) WHERE ref_type = 'admin';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_ledger_entries_admin_ref;
DROP INDEX IF EXISTS idx_accounts_name_trgm;
ALTER TABLE price_change_requests DROP COLUMN IF EXISTS decided_by_name;
ALTER TABLE price_change_requests DROP COLUMN IF EXISTS decision_reason;
DROP INDEX IF EXISTS idx_admin_audit_logs_time;
ALTER TABLE admin_audit_logs DROP COLUMN IF EXISTS actor_name;
DROP INDEX IF EXISTS idx_request_logs_errors_time;
DROP INDEX IF EXISTS idx_request_logs_channel_time;
DROP INDEX IF EXISTS idx_request_logs_model_time;
-- +goose StatementEnd
