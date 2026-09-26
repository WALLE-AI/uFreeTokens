-- 专属渠道（技术方案 Phase 4）：一个渠道可以限定只对指定的几个账户可见，
-- 用来给企业客户配独享的路由/配额池，不跟公共流量混在一起。语义和现有的
-- allowed_tiers 完全对称：NULL = 不限制，非 NULL = 白名单。

-- +goose Up
-- +goose StatementBegin
ALTER TABLE channels ADD COLUMN allowed_account_ids BIGINT[];
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE channels DROP COLUMN IF EXISTS allowed_account_ids;
-- +goose StatementEnd
