-- 请求链路采集增强与公开榜单的速度 / 工具调用 / 多模态 / 应用四个板块
-- （docs/基准测试与排行榜数据服务技术方案.md §2.1、§3.2、§4、§8.2，阶段 3）。
--
-- request_logs 新增：
--   gen_ms        生成阶段耗时：流式 = 最后一个 chunk 时间 − 第一个 chunk 时间；非流式无法区分
--                 排队与生成，为 NULL，不参与速度榜；
--   tool_calls    响应里的工具调用条数（非流式数 message.tool_calls；流式按 (choice, index) 去重）；
--   image_inputs  请求消息里 image_url 内容块的个数；
--   app_name / app_url  调用方主动声明的应用（请求头 X-Title / HTTP-Referer），app_name ≤ 64 字符，
--                 app_url 只保留 scheme://host。
-- 新增列都允许 NULL：添加时不重写已有分区，历史行为 NULL。
--
-- usage_hourly / public_model_usage_daily / public_model_account_daily 追加对应的求和列；
-- public_app_usage_daily / public_app_account_daily 是应用榜的日级物化（来源 request_logs，
-- 只含声明了 X-Title 的请求）；public_app_rules 是运营维护的应用屏蔽 / 合并规则（防冒用）。

-- +goose Up
-- +goose StatementBegin
ALTER TABLE request_logs
    ADD COLUMN gen_ms       INT,
    ADD COLUMN tool_calls   INT,
    ADD COLUMN image_inputs INT,
    ADD COLUMN app_name     TEXT,
    ADD COLUMN app_url      TEXT;
-- 应用物化任务每 30 分钟扫最近两天里声明了应用的请求。
CREATE INDEX IF NOT EXISTS idx_request_logs_app_time ON request_logs (created_at) WHERE app_name IS NOT NULL;

ALTER TABLE usage_hourly
    ADD COLUMN speed_requests      BIGINT NOT NULL DEFAULT 0, -- 成功、流式、gen_ms > 0 的请求数
    ADD COLUMN speed_output_tokens BIGINT NOT NULL DEFAULT 0, -- 上述请求的输出 token
    ADD COLUMN speed_gen_ms        BIGINT NOT NULL DEFAULT 0, -- 上述请求的生成耗时之和
    ADD COLUMN tool_requests       BIGINT NOT NULL DEFAULT 0, -- 成功且有工具调用的请求数
    ADD COLUMN tool_tokens         BIGINT NOT NULL DEFAULT 0, -- 上述请求的 input + output token
    ADD COLUMN image_requests      BIGINT NOT NULL DEFAULT 0, -- 成功且含图片输入的请求数
    ADD COLUMN image_tokens        BIGINT NOT NULL DEFAULT 0;

ALTER TABLE public_model_usage_daily
    ADD COLUMN speed_requests      BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN speed_output_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN speed_gen_ms        BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN tool_requests       BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN tool_tokens         BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN image_requests      BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN image_tokens        BIGINT NOT NULL DEFAULT 0;

ALTER TABLE public_model_account_daily
    ADD COLUMN tool_tokens  BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN image_tokens BIGINT NOT NULL DEFAULT 0;

-- app_key：有 app_url 时按域名归并（同一域名下的不同 X-Title 视为同一应用），否则按小写的应用名。
CREATE TABLE public_app_usage_daily (
    day               DATE   NOT NULL,
    app_key           TEXT   NOT NULL,
    app_name          TEXT   NOT NULL,              -- 当天该 key 下请求最多的 X-Title
    app_url           TEXT   NOT NULL DEFAULT '',
    requests          BIGINT NOT NULL,
    tokens            BIGINT NOT NULL,
    distinct_accounts INT    NOT NULL,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (day, app_key)
);

CREATE TABLE public_app_account_daily (
    day        DATE   NOT NULL,
    app_key    TEXT   NOT NULL,
    account_id BIGINT NOT NULL,
    tokens     BIGINT NOT NULL,
    PRIMARY KEY (day, app_key, account_id)
);

-- 运营维护的应用规则：block = 不上榜（计入"其他"）；merge = 并入 merge_into 指向的 app_key。
-- display_name 非空时覆盖榜单上的展示名。
CREATE TABLE public_app_rules (
    id           BIGSERIAL PRIMARY KEY,
    app_key      TEXT NOT NULL UNIQUE,
    action       TEXT NOT NULL CHECK (action IN ('block','merge','rename')),
    merge_into   TEXT,
    display_name TEXT,
    note         TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((action = 'merge') = (merge_into IS NOT NULL)),
    CHECK (action <> 'rename' OR display_name IS NOT NULL)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public_app_rules;
DROP TABLE IF EXISTS public_app_account_daily;
DROP TABLE IF EXISTS public_app_usage_daily;
ALTER TABLE public_model_account_daily DROP COLUMN IF EXISTS tool_tokens, DROP COLUMN IF EXISTS image_tokens;
ALTER TABLE public_model_usage_daily
    DROP COLUMN IF EXISTS speed_requests, DROP COLUMN IF EXISTS speed_output_tokens, DROP COLUMN IF EXISTS speed_gen_ms,
    DROP COLUMN IF EXISTS tool_requests, DROP COLUMN IF EXISTS tool_tokens,
    DROP COLUMN IF EXISTS image_requests, DROP COLUMN IF EXISTS image_tokens;
ALTER TABLE usage_hourly
    DROP COLUMN IF EXISTS speed_requests, DROP COLUMN IF EXISTS speed_output_tokens, DROP COLUMN IF EXISTS speed_gen_ms,
    DROP COLUMN IF EXISTS tool_requests, DROP COLUMN IF EXISTS tool_tokens,
    DROP COLUMN IF EXISTS image_requests, DROP COLUMN IF EXISTS image_tokens;
DROP INDEX IF EXISTS idx_request_logs_app_time;
ALTER TABLE request_logs
    DROP COLUMN IF EXISTS gen_ms, DROP COLUMN IF EXISTS tool_calls, DROP COLUMN IF EXISTS image_inputs,
    DROP COLUMN IF EXISTS app_name, DROP COLUMN IF EXISTS app_url;
-- +goose StatementEnd
