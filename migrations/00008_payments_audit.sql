-- 充值订单与管理员操作审计。见技术方案 §7.11、§7.15。

-- +goose Up
-- +goose StatementBegin
CREATE TABLE payment_orders (
    order_no      TEXT PRIMARY KEY,
    account_id    BIGINT NOT NULL REFERENCES accounts(id),
    amount        BIGINT NOT NULL CHECK (amount > 0),
    channel       TEXT NOT NULL CHECK (channel IN ('wechat','alipay','stripe','manual')),
    status        TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','paid','failed','refunded')),
    external_id   TEXT,
    paid_at       TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_payment_orders_account ON payment_orders (account_id, created_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE admin_audit_logs (
    id          BIGSERIAL PRIMARY KEY,
    actor_id    BIGINT NOT NULL,
    action      TEXT NOT NULL,
    target_type TEXT NOT NULL,
    target_id   TEXT NOT NULL,
    before      JSONB,
    after       JSONB,
    ip          INET,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_admin_audit_logs_target ON admin_audit_logs (target_type, target_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS admin_audit_logs;
DROP TABLE IF EXISTS payment_orders;
-- +goose StatementEnd
