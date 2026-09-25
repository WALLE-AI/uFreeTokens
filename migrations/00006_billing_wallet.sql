-- 钱包、账本、冻结。金额统一为 BIGINT 微元（1 元 = 1,000,000）。
-- ledger_entries 只追加：应用层与 §7.9 的结算逻辑保证不做 UPDATE/DELETE；
-- 触发器兜底禁止修改/删除，防止误操作破坏账本完整性。见技术方案 §6.5、§6.6、§7.9。

-- +goose Up
-- +goose StatementBegin
CREATE TABLE wallets (
    account_id      BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    cash_balance    BIGINT NOT NULL DEFAULT 0,
    bonus_balance   BIGINT NOT NULL DEFAULT 0,
    frozen          BIGINT NOT NULL DEFAULT 0 CHECK (frozen >= 0),
    version         BIGINT NOT NULL DEFAULT 0,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE credit_grants (
    id            BIGSERIAL PRIMARY KEY,
    account_id    BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    source        TEXT NOT NULL CHECK (source IN ('signup','promotion','compensation','invite')),
    promotion_id  BIGINT REFERENCES promotions(id),
    amount        BIGINT NOT NULL CHECK (amount >= 0),
    remaining     BIGINT NOT NULL CHECK (remaining >= 0),
    model_scope   TEXT[],
    expires_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_credit_grants_active ON credit_grants (account_id, expires_at) WHERE remaining > 0;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE ledger_entries (
    id            BIGSERIAL PRIMARY KEY,
    account_id    BIGINT NOT NULL REFERENCES accounts(id),
    type          TEXT NOT NULL CHECK (type IN ('recharge','consume','refund','grant','grant_expire','adjust')),
    amount        BIGINT NOT NULL,
    balance_kind  TEXT NOT NULL CHECK (balance_kind IN ('cash','bonus')),
    grant_id      BIGINT REFERENCES credit_grants(id),
    cash_after    BIGINT NOT NULL,
    bonus_after   BIGINT NOT NULL,
    ref_type      TEXT NOT NULL CHECK (ref_type IN ('request','payment_order','promotion','admin')),
    ref_id        TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (ref_type, ref_id, type, balance_kind, grant_id)
);
CREATE INDEX idx_ledger_entries_account ON ledger_entries (account_id, created_at DESC);

CREATE OR REPLACE FUNCTION forbid_ledger_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'ledger_entries is append-only: % is not allowed', TG_OP;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_ledger_entries_no_update
    BEFORE UPDATE OR DELETE ON ledger_entries
    FOR EACH ROW EXECUTE FUNCTION forbid_ledger_mutation();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE reservations (
    request_id    TEXT PRIMARY KEY,
    account_id    BIGINT NOT NULL REFERENCES accounts(id),
    amount        BIGINT NOT NULL CHECK (amount >= 0),
    status        TEXT NOT NULL DEFAULT 'held' CHECK (status IN ('held','settled','released')),
    expires_at    TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_reservations_expiring ON reservations (expires_at) WHERE status = 'held';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS reservations;
DROP TRIGGER IF EXISTS trg_ledger_entries_no_update ON ledger_entries;
DROP FUNCTION IF EXISTS forbid_ledger_mutation();
DROP TABLE IF EXISTS ledger_entries;
DROP TABLE IF EXISTS credit_grants;
DROP TABLE IF EXISTS wallets;
-- +goose StatementEnd
