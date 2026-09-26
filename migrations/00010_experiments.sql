-- A/B Routing（技术方案 Phase 3）：渠道打上实验分组标签，request_logs 记录一次
-- 请求实际走的是哪个分组，供事后按 experiment_key/variant_label 聚合对比
-- 成本/延迟/成功率。流量怎么分配不需要新机制——同一 experiment_key 下的几个
-- 渠道本来就可以用现有的 priority/weight 做加权分流（技术方案 §7.5），这里补的
-- 是"事后能不能看出哪个请求走了哪个分组"这一环。

-- +goose Up
-- +goose StatementBegin
ALTER TABLE channels ADD COLUMN experiment_key TEXT;
ALTER TABLE channels ADD COLUMN variant_label TEXT;
ALTER TABLE channels ADD CONSTRAINT channels_experiment_pair_chk
    CHECK ((experiment_key IS NULL) = (variant_label IS NULL));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE request_logs ADD COLUMN experiment_key TEXT;
ALTER TABLE request_logs ADD COLUMN variant_label TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE request_logs DROP COLUMN IF EXISTS variant_label;
ALTER TABLE request_logs DROP COLUMN IF EXISTS experiment_key;
ALTER TABLE channels DROP CONSTRAINT IF EXISTS channels_experiment_pair_chk;
ALTER TABLE channels DROP COLUMN IF EXISTS variant_label;
ALTER TABLE channels DROP COLUMN IF EXISTS experiment_key;
-- +goose StatementEnd
