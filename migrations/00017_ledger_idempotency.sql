-- 账务幂等与只追加表的防篡改（B6 数据一致性），见
-- docs/运营后台接口与数据库设计问题分析及执行方案.md §4.2。
--
-- 1. ledger_entries 的唯一约束含可空的 grant_id，而 Postgres 默认把 NULL 视为
--    互不相等——现金流水（grant_id 为 NULL）永远不会冲突，约束形同虚设。改成
--    NULLS NOT DISTINCT（PG15+）。上线前先确认没有历史重复：
--      SELECT ref_type, ref_id, type, balance_kind, count(*) FROM ledger_entries
--      WHERE grant_id IS NULL GROUP BY 1,2,3,4 HAVING count(*) > 1;
--    有重复时本迁移会失败，需要先人工核对处理（流水只追加，不能直接删）。
-- 2. 赠金发放记录 ref_id 并按 (account_id, source, ref_id) 去重：重试不会重复发放。
-- 3. 支付回调按 (channel, external_id) 去重。
-- 4. admin_audit_logs、price_observations 与 ledger_entries 一样只追加：禁止
--    UPDATE/DELETE，三张表都禁止 TRUNCATE（行级触发器拦不住 TRUNCATE）。

-- +goose Up
-- +goose StatementBegin
ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_ref_type_ref_id_type_balance_kind_grant_id_key;
ALTER TABLE ledger_entries ADD CONSTRAINT uq_ledger_entries_ref
    UNIQUE NULLS NOT DISTINCT (ref_type, ref_id, type, balance_kind, grant_id);
CREATE INDEX IF NOT EXISTS idx_ledger_entries_grant ON ledger_entries (grant_id) WHERE grant_id IS NOT NULL;

ALTER TABLE wallets ADD CONSTRAINT chk_wallets_bonus_nonneg CHECK (bonus_balance >= 0) NOT VALID;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE credit_grants ADD COLUMN ref_id TEXT;
ALTER TABLE credit_grants ADD CONSTRAINT chk_credit_grants_remaining_le_amount CHECK (remaining <= amount) NOT VALID;
CREATE UNIQUE INDEX uq_credit_grants_ref ON credit_grants (account_id, source, ref_id) WHERE ref_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_credit_grants_promotion ON credit_grants (promotion_id) WHERE promotion_id IS NOT NULL;
-- 回填历史记录的 ref_id（来自对应的 grant 流水），便于按单号排查。
UPDATE credit_grants g SET ref_id = l.ref_id
FROM ledger_entries l
WHERE l.grant_id = g.id AND l.type = 'grant' AND g.ref_id IS NULL
  AND NOT EXISTS (SELECT 1 FROM credit_grants g2 WHERE g2.account_id = g.account_id AND g2.source = g.source AND g2.ref_id = l.ref_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX uq_payment_orders_external ON payment_orders (channel, external_id) WHERE external_id IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION forbid_truncate() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION '% is append-only: TRUNCATE is forbidden', TG_TABLE_NAME;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_admin_audit_logs_no_update BEFORE UPDATE OR DELETE ON admin_audit_logs
    FOR EACH ROW EXECUTE FUNCTION forbid_ledger_mutation();
CREATE TRIGGER trg_price_observations_no_update BEFORE UPDATE OR DELETE ON price_observations
    FOR EACH ROW EXECUTE FUNCTION forbid_ledger_mutation();
CREATE TRIGGER trg_ledger_entries_no_truncate BEFORE TRUNCATE ON ledger_entries
    FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();
CREATE TRIGGER trg_admin_audit_logs_no_truncate BEFORE TRUNCATE ON admin_audit_logs
    FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();
CREATE TRIGGER trg_price_observations_no_truncate BEFORE TRUNCATE ON price_observations
    FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_price_observations_no_truncate ON price_observations;
DROP TRIGGER IF EXISTS trg_admin_audit_logs_no_truncate ON admin_audit_logs;
DROP TRIGGER IF EXISTS trg_ledger_entries_no_truncate ON ledger_entries;
DROP TRIGGER IF EXISTS trg_price_observations_no_update ON price_observations;
DROP TRIGGER IF EXISTS trg_admin_audit_logs_no_update ON admin_audit_logs;
DROP FUNCTION IF EXISTS forbid_truncate();
DROP INDEX IF EXISTS uq_payment_orders_external;
DROP INDEX IF EXISTS idx_credit_grants_promotion;
DROP INDEX IF EXISTS uq_credit_grants_ref;
ALTER TABLE credit_grants DROP CONSTRAINT IF EXISTS chk_credit_grants_remaining_le_amount;
ALTER TABLE credit_grants DROP COLUMN IF EXISTS ref_id;
ALTER TABLE wallets DROP CONSTRAINT IF EXISTS chk_wallets_bonus_nonneg;
DROP INDEX IF EXISTS idx_ledger_entries_grant;
ALTER TABLE ledger_entries DROP CONSTRAINT IF EXISTS uq_ledger_entries_ref;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_ref_type_ref_id_type_balance_kind_grant_id_key
    UNIQUE (ref_type, ref_id, type, balance_kind, grant_id);
-- +goose StatementEnd
