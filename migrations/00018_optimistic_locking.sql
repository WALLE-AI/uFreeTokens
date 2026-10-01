-- 运营后台可编辑实体的乐观锁与时间戳（B6 数据一致性），见
-- docs/运营后台接口与数据库设计问题分析及执行方案.md §4.3。
--
-- 详情接口返回 ETag: W/"<version>"，PATCH 带 If-Match 时版本不一致返回 412，
-- 防止两个运营照着各自打开的旧页面互相覆盖。created_at/updated_at 此前在
-- 这些表上缺失，一并补上（历史行的 created_at 只能记为迁移时间）。
--
-- provider_accounts → provider_keys 由 ON DELETE CASCADE 改为 RESTRICT：删除上游
-- 账号不应该静默删掉其下的加密密钥。

-- +goose Up
-- +goose StatementBegin
ALTER TABLE providers         ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                              ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                              ADD COLUMN IF NOT EXISTS version    INT         NOT NULL DEFAULT 1;
ALTER TABLE provider_accounts ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                              ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                              ADD COLUMN IF NOT EXISTS version    INT         NOT NULL DEFAULT 1;
ALTER TABLE provider_keys     ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                              ADD COLUMN IF NOT EXISTS version    INT         NOT NULL DEFAULT 1;
ALTER TABLE virtual_models    ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                              ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                              ADD COLUMN IF NOT EXISTS version    INT         NOT NULL DEFAULT 1;
ALTER TABLE channels          ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                              ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                              ADD COLUMN IF NOT EXISTS version    INT         NOT NULL DEFAULT 1;
ALTER TABLE accounts          ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                              ADD COLUMN IF NOT EXISTS version    INT         NOT NULL DEFAULT 1;
ALTER TABLE price_sources     ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                              ADD COLUMN IF NOT EXISTS version    INT         NOT NULL DEFAULT 1;
ALTER TABLE api_keys          ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                              ADD COLUMN IF NOT EXISTS version    INT         NOT NULL DEFAULT 1;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE provider_keys DROP CONSTRAINT provider_keys_provider_account_id_fkey;
ALTER TABLE provider_keys ADD CONSTRAINT provider_keys_provider_account_id_fkey
    FOREIGN KEY (provider_account_id) REFERENCES provider_accounts(id) ON DELETE RESTRICT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE provider_keys DROP CONSTRAINT provider_keys_provider_account_id_fkey;
ALTER TABLE provider_keys ADD CONSTRAINT provider_keys_provider_account_id_fkey
    FOREIGN KEY (provider_account_id) REFERENCES provider_accounts(id) ON DELETE CASCADE;
ALTER TABLE api_keys          DROP COLUMN IF EXISTS version, DROP COLUMN IF EXISTS updated_at;
ALTER TABLE price_sources     DROP COLUMN IF EXISTS version, DROP COLUMN IF EXISTS updated_at;
ALTER TABLE accounts          DROP COLUMN IF EXISTS version, DROP COLUMN IF EXISTS updated_at;
ALTER TABLE channels          DROP COLUMN IF EXISTS version, DROP COLUMN IF EXISTS updated_at, DROP COLUMN IF EXISTS created_at;
ALTER TABLE virtual_models    DROP COLUMN IF EXISTS version, DROP COLUMN IF EXISTS updated_at, DROP COLUMN IF EXISTS created_at;
ALTER TABLE provider_keys     DROP COLUMN IF EXISTS version, DROP COLUMN IF EXISTS updated_at;
ALTER TABLE provider_accounts DROP COLUMN IF EXISTS version, DROP COLUMN IF EXISTS updated_at, DROP COLUMN IF EXISTS created_at;
ALTER TABLE providers         DROP COLUMN IF EXISTS version, DROP COLUMN IF EXISTS updated_at, DROP COLUMN IF EXISTS created_at;
-- +goose StatementEnd
