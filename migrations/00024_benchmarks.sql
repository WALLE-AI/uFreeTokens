-- 基准测试数据实体（docs/基准测试与排行榜数据服务技术方案.md §3.2，阶段 2）。
--   benchmarks         基准定义（名称、分类、指标口径、外部来源），运营维护；
--   benchmark_runs     一次评测批次：运营录入 / 批量导入 / 自建评测（阶段 4）；
--                      同一基准只对外展示最新一次已发布的 run，旧 run 留作历史；
--   benchmark_results  某次 run 里每个模型的结果。model_label 是展示名，平台未上架的
--                      模型也能参评；已上架时关联 virtual_model_id，前端可跳模型详情。
-- 质量 / 性价比 / 速度三项冠军在接口层由 benchmark_results 计算，不单独存。

-- +goose Up
-- +goose StatementBegin
CREATE TABLE benchmarks (
    id               BIGSERIAL PRIMARY KEY,
    slug             TEXT NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    name             TEXT NOT NULL,
    category         TEXT NOT NULL CHECK (category IN ('agents','media','artifacts','reasoning','search')),
    description      TEXT NOT NULL DEFAULT '',
    metric_name      TEXT NOT NULL,
    metric_unit      TEXT NOT NULL DEFAULT 'percent',
    higher_is_better BOOLEAN NOT NULL DEFAULT true,
    source_name      TEXT,
    source_url       TEXT,
    status           TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','published','archived')),
    sort_order       INT NOT NULL DEFAULT 0,
    version          INT NOT NULL DEFAULT 1,            -- 乐观锁，同 00018（PATCH 走 If-Match）
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE benchmark_runs (
    id           BIGSERIAL PRIMARY KEY,
    benchmark_id BIGINT NOT NULL REFERENCES benchmarks(id) ON DELETE CASCADE,
    origin       TEXT NOT NULL CHECK (origin IN ('manual','import','self_eval')),
    run_at       TIMESTAMPTZ NOT NULL,
    notes        TEXT NOT NULL DEFAULT '',
    -- 结果里 cost_per_task_micro 的币种：外部来源多为 USD，自建评测取平台实扣（CNY）。
    cost_currency TEXT NOT NULL DEFAULT 'USD' CHECK (cost_currency IN ('USD','CNY')),
    published    BOOLEAN NOT NULL DEFAULT false,
    published_at TIMESTAMPTZ,
    created_by   BIGINT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_benchmark_runs_latest ON benchmark_runs (benchmark_id, published, run_at DESC);
-- 每个基准最多一个对外展示的 run：发布新 run 时旧 run 自动取消发布（退居历史）。
CREATE UNIQUE INDEX uq_benchmark_runs_published ON benchmark_runs (benchmark_id) WHERE published;

CREATE TABLE benchmark_results (
    run_id              BIGINT NOT NULL REFERENCES benchmark_runs(id) ON DELETE CASCADE,
    model_label         TEXT NOT NULL,
    virtual_model_id    BIGINT REFERENCES virtual_models(id) ON DELETE SET NULL,
    score               NUMERIC(10,4) NOT NULL,
    cost_per_task_micro BIGINT CHECK (cost_per_task_micro >= 0),
    avg_duration_ms     INT CHECK (avg_duration_ms >= 0),
    error_rate          NUMERIC(6,5) CHECK (error_rate >= 0 AND error_rate <= 1),
    sample_count        INT CHECK (sample_count >= 0),
    extra               JSONB,
    PRIMARY KEY (run_id, model_label)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS benchmark_results;
DROP TABLE IF EXISTS benchmark_runs;
DROP TABLE IF EXISTS benchmarks;
-- +goose StatementEnd
