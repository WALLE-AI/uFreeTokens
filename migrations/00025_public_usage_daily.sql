-- 公开排行榜的日级物化（docs/基准测试与排行榜数据服务技术方案.md §3.2、§3.3、§8，阶段 1）。
--
-- public_model_usage_daily：跨账户汇总、按 Asia/Shanghai 切日，GET /v1/rankings/* 只读它
--   和下面的账户分布表，不碰 usage_hourly / request_logs。按 virtual_model_id 归并（模型改名
--   后趋势线不断开），virtual_model_id 为空的历史行才回落到名称：model_key 分别是
--   'id:<id>' 与 'name:<name>'。只统计成功请求；output_tokens 已包含 reasoning token。
-- public_model_account_daily：同一口径下每个账户的 token 量。不对外暴露，只用来在
--   查询期内计算"独立账户数 ≥ 阈值"与"单账户计入量不超过当期总量的 20%"两条规则——
--   这两条都是期间口径，不能由每日的去重数相加得到。
-- 两张表都由 worker 的 public_usage_daily 任务从 usage_hourly 重算覆盖（幂等），
-- exclude_from_public_stats 的账户（内部测试、压测、评测账户）不计入。

-- +goose Up
-- +goose StatementBegin
CREATE TABLE public_model_usage_daily (
    day               DATE   NOT NULL,
    model_key         TEXT   NOT NULL,
    virtual_model     TEXT   NOT NULL,              -- 当天最后出现的名称快照
    virtual_model_id  BIGINT,
    author            TEXT   NOT NULL,              -- virtual_model 的 '/' 前缀（catalog.AuthorOf）
    requests          BIGINT NOT NULL,
    success           BIGINT NOT NULL,
    input_tokens      BIGINT NOT NULL,
    output_tokens     BIGINT NOT NULL,
    reasoning_tokens  BIGINT NOT NULL,
    distinct_accounts INT    NOT NULL,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (day, model_key)
);

CREATE TABLE public_model_account_daily (
    day        DATE   NOT NULL,
    model_key  TEXT   NOT NULL,
    account_id BIGINT NOT NULL,
    tokens     BIGINT NOT NULL,
    PRIMARY KEY (day, model_key, account_id)
);

ALTER TABLE accounts ADD COLUMN exclude_from_public_stats BOOLEAN NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE accounts DROP COLUMN IF EXISTS exclude_from_public_stats;
DROP TABLE IF EXISTS public_model_account_daily;
DROP TABLE IF EXISTS public_model_usage_daily;
-- +goose StatementEnd
