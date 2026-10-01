-- 外部数据采集模块（docs/外部数据采集模块（价格情报与评测榜单）技术方案.md）：
--   price_sources       泛化为"数据源"：domain 区分价格 / 优惠 / 评测榜单，并补上调度状态
--                       （next_run_at、失败计数、条件请求缓存）与许可证/对外展示开关。
--                       表名保持不变，避免牵动现有价格同步代码与审计记录。
--   data_source_runs    每次抓取的执行记录（运营后台"运行历史"）。
--   upstream_offers     上游市场上的免费 / 限时优惠情报，人工确认后可"采用"为 promotions。
--   model_aliases       评测榜单里的模型名 -> 虚拟模型 的映射（自动匹配 + 人工确认）。
--   benchmarks          增加来源、外部键、别名命名空间、scores 投影键；分类扩充。
--   benchmark_runs      增加 content_hash，导入内容没变就不新建 run。

-- +goose Up
-- +goose StatementBegin
ALTER TABLE price_sources
    ADD COLUMN domain               TEXT NOT NULL DEFAULT 'price' CHECK (domain IN ('price','offer','benchmark')),
    ADD COLUMN name                 TEXT NOT NULL DEFAULT '',
    ADD COLUMN license              TEXT,
    ADD COLUMN attribution          TEXT,
    -- 许可证是否允许对外展示：false 的来源只在运营后台可见，不进 /v1/benchmarks、不投影进 scores。
    ADD COLUMN public_display       BOOLEAN NOT NULL DEFAULT false,
    -- 评测榜单：导入通过异常检查后是否自动发布（否则留草稿等人工发布）。
    ADD COLUMN auto_publish         BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN next_run_at          TIMESTAMPTZ,
    ADD COLUMN last_run_at          TIMESTAMPTZ,
    ADD COLUMN last_error           TEXT,
    ADD COLUMN consecutive_failures INT NOT NULL DEFAULT 0,
    ADD COLUMN http_etag            TEXT,
    ADD COLUMN http_last_modified   TEXT;
CREATE INDEX idx_price_sources_due ON price_sources (next_run_at) WHERE enabled;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE data_source_runs (
    id            BIGSERIAL PRIMARY KEY,
    source_id     BIGINT NOT NULL REFERENCES price_sources(id) ON DELETE CASCADE,
    started_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at   TIMESTAMPTZ,
    status        TEXT NOT NULL CHECK (status IN ('running','ok','unchanged','failed','rejected')),
    items_fetched INT,
    items_changed INT,
    content_hash  BYTEA,
    error         TEXT,
    detail        JSONB
);
CREATE INDEX idx_data_source_runs_source ON data_source_runs (source_id, started_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE upstream_offers (
    id                   BIGSERIAL PRIMARY KEY,
    source_id            BIGINT REFERENCES price_sources(id) ON DELETE SET NULL,
    provider_code        TEXT NOT NULL,
    upstream_model       TEXT,                 -- NULL = 账号级 / 全场优惠
    offer_type           TEXT NOT NULL CHECK (offer_type IN
                            ('free_model','discount','off_peak','free_quota','new_user_credit','price_cut')),
    -- 价格乘数：0.5 = 五折，0 = 免费；无法量化的优惠为 NULL。
    discount_ratio       NUMERIC(6,5) CHECK (discount_ratio >= 0 AND discount_ratio <= 1),
    quota                JSONB,
    limits               JSONB,
    starts_at            TIMESTAMPTZ,
    ends_at              TIMESTAMPTZ,
    conditions           TEXT,
    evidence_url         TEXT,
    evidence_excerpt     TEXT,
    detection            TEXT NOT NULL CHECK (detection IN ('structured','price_diff','llm_extract','manual')),
    fingerprint          BYTEA NOT NULL UNIQUE,
    status               TEXT NOT NULL DEFAULT 'new' CHECK (status IN ('new','confirmed','ignored','expired','adopted')),
    adopted_promotion_id BIGINT REFERENCES promotions(id),
    decided_by_name      TEXT,
    decided_at           TIMESTAMPTZ,
    first_seen_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_upstream_offers_status ON upstream_offers (status, last_seen_at DESC);
CREATE INDEX idx_upstream_offers_source ON upstream_offers (source_id, offer_type, status);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE model_aliases (
    namespace                  TEXT NOT NULL,   -- 一个榜单家族一个命名空间（lmarena / epoch / ...）
    external_label             TEXT NOT NULL,   -- 榜单上的原始模型名
    virtual_model_id           BIGINT REFERENCES virtual_models(id) ON DELETE SET NULL,
    -- auto：规则匹配，直接生效；suggested：模糊匹配，等人工确认，不生效；
    -- confirmed：人工确认；ignored：人工确认"平台没有这个模型"；unmatched：还没有任何候选。
    status                     TEXT NOT NULL CHECK (status IN ('auto','suggested','confirmed','ignored','unmatched')),
    method                     TEXT NOT NULL CHECK (method IN ('exact','normalized','fuzzy','manual','none')),
    confidence                 NUMERIC(4,3),
    variant                    TEXT,            -- 推理档位等后缀（high / thinking / max ...）
    seen_count                 INT NOT NULL DEFAULT 1,
    first_seen_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_by_name            TEXT,
    decided_at                 TIMESTAMPTZ,
    PRIMARY KEY (namespace, external_label)
);
CREATE INDEX idx_model_aliases_status ON model_aliases (status, last_seen_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE benchmarks DROP CONSTRAINT benchmarks_category_check;
ALTER TABLE benchmarks ADD CONSTRAINT benchmarks_category_check
    CHECK (category IN ('general','coding','agents','reasoning','chinese','search','media','artifacts','embedding'));
ALTER TABLE benchmarks
    ADD COLUMN source_id       BIGINT REFERENCES price_sources(id) ON DELETE SET NULL,
    ADD COLUMN external_key    TEXT UNIQUE,
    ADD COLUMN alias_namespace TEXT,
    -- 发布时把每个关联模型的（最好）分数写进 virtual_model_metadata.scores 的这个键。
    ADD COLUMN score_key       TEXT;
ALTER TABLE benchmark_runs ADD COLUMN content_hash BYTEA;
-- +goose StatementEnd

-- 内置数据源。公开数据集类来源默认启用；需要密钥或授权的来源默认停用、不对外展示。
-- +goose StatementBegin
-- 按 (fetcher, url) 去重：回滚后重新迁移、或已经手工建过同一来源时不重复插入。
INSERT INTO price_sources (domain, name, level, kind, fetcher, url, schedule, enabled, license, attribution, public_display, auto_publish, config)
SELECT v.domain, v.name, v.level, v.kind, v.fetcher, v.url, v.schedule, v.enabled, v.license, v.attribution, v.public_display, v.auto_publish, v.config::jsonb
FROM (VALUES
('price', 'OpenRouter 模型价格', 'L4', 'api', 'openrouter_models', 'https://openrouter.ai/api/v1/models', '@every 6h', true,
  NULL, 'OpenRouter', false, false,
  '{"detect_offers": true, "offer_provider": "openrouter"}'),
('price', 'models.dev 多厂商价格', 'L4', 'dataset', 'modelsdev', 'https://models.dev/api.json', '@every 6h', true,
  'MIT', 'models.dev', false, false,
  '{"providers": ["openai","anthropic","google","xai","mistral","deepseek","moonshotai","moonshotai-cn","zhipuai","zai","alibaba","alibaba-cn","siliconflow","siliconflow-cn","minimax","minimax-cn","stepfun","volcengine","groq"],
    "detect_offers": true,
    "offer_providers": ["google","mistral","deepseek","moonshotai","moonshotai-cn","zhipuai","zai","alibaba-cn","siliconflow","siliconflow-cn","groq"]}'),
('price', 'LiteLLM 价格数据集', 'L4', 'dataset', 'litellm_dataset',
  'https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json', '@daily', false,
  'MIT', 'LiteLLM', false, false, '{}'),
('benchmark', 'LMArena 排行榜', 'L4', 'dataset', 'tabular', NULL, '@every 12h', true,
  'CC-BY-4.0', 'LMArena (arena.ai) leaderboard dataset, CC BY 4.0', true, true,
  '{"alias_namespace": "lmarena", "format": "parquet",
    "boards": [
      {"key": "lmarena:text_style_control:overall", "score_key": "arena_text",
       "url": "https://hf-mirror.com/datasets/lmarena-ai/leaderboard-dataset/resolve/main/text_style_control/latest-00000-of-00001.parquet",
       "filter": {"category": "overall"}, "label": "model_name", "score": "rating", "run_at": "leaderboard_publish_date",
       "extra": {"rank": "rank", "ci_low": "rating_lower", "ci_high": "rating_upper", "votes": "vote_count", "organization": "organization", "license": "license"},
       "benchmark": {"slug": "lmarena-text", "name": "LMArena Text", "category": "general", "metric_name": "Arena Elo", "metric_unit": "elo",
         "source_url": "https://lmarena.ai/leaderboard/text", "description": "人类盲测投票的文本对话 Elo 排名（风格控制后）。"}},
      {"key": "lmarena:text_style_control:chinese", "score_key": "arena_chinese",
       "url": "https://hf-mirror.com/datasets/lmarena-ai/leaderboard-dataset/resolve/main/text_style_control/latest-00000-of-00001.parquet",
       "filter": {"category": "chinese"}, "label": "model_name", "score": "rating", "run_at": "leaderboard_publish_date",
       "extra": {"rank": "rank", "ci_low": "rating_lower", "ci_high": "rating_upper", "votes": "vote_count", "organization": "organization"},
       "benchmark": {"slug": "lmarena-chinese", "name": "LMArena 中文", "category": "chinese", "metric_name": "Arena Elo", "metric_unit": "elo",
         "source_url": "https://lmarena.ai/leaderboard/text/chinese", "description": "LMArena 中文提示子集的 Elo 排名。"}},
      {"key": "lmarena:text_style_control:coding", "score_key": "arena_coding",
       "url": "https://hf-mirror.com/datasets/lmarena-ai/leaderboard-dataset/resolve/main/text_style_control/latest-00000-of-00001.parquet",
       "filter": {"category": "coding"}, "label": "model_name", "score": "rating", "run_at": "leaderboard_publish_date",
       "extra": {"rank": "rank", "ci_low": "rating_lower", "ci_high": "rating_upper", "votes": "vote_count", "organization": "organization"},
       "benchmark": {"slug": "lmarena-coding", "name": "LMArena Coding", "category": "coding", "metric_name": "Arena Elo", "metric_unit": "elo",
         "source_url": "https://lmarena.ai/leaderboard/text/coding", "description": "LMArena 编程类提示子集的 Elo 排名。"}},
      {"key": "lmarena:webdev:overall", "score_key": "arena_webdev",
       "url": "https://hf-mirror.com/datasets/lmarena-ai/leaderboard-dataset/resolve/main/webdev/latest-00000-of-00001.parquet",
       "filter": {"category": "overall"}, "label": "model_name", "score": "rating", "run_at": "leaderboard_publish_date",
       "extra": {"rank": "rank", "ci_low": "rating_lower", "ci_high": "rating_upper", "votes": "vote_count", "organization": "organization"},
       "benchmark": {"slug": "lmarena-webdev", "name": "LMArena WebDev", "category": "artifacts", "metric_name": "Arena Elo", "metric_unit": "elo",
         "source_url": "https://lmarena.ai/leaderboard/webdev", "description": "生成网页应用的人类盲测 Elo 排名。"}},
      {"key": "lmarena:vision:overall", "score_key": "arena_vision",
       "url": "https://hf-mirror.com/datasets/lmarena-ai/leaderboard-dataset/resolve/main/vision/latest-00000-of-00001.parquet",
       "filter": {"category": "overall"}, "label": "model_name", "score": "rating", "run_at": "leaderboard_publish_date",
       "extra": {"rank": "rank", "ci_low": "rating_lower", "ci_high": "rating_upper", "votes": "vote_count", "organization": "organization"},
       "benchmark": {"slug": "lmarena-vision", "name": "LMArena Vision", "category": "media", "metric_name": "Arena Elo", "metric_unit": "elo",
         "source_url": "https://lmarena.ai/leaderboard/vision", "description": "图文理解的人类盲测 Elo 排名。"}}
    ]}'),
('benchmark', 'Epoch AI Benchmarking Hub', 'L4', 'dataset', 'tabular', 'https://epoch.ai/data/benchmark_data.zip', '@daily', true,
  'CC-BY-4.0', 'Epoch AI, ''AI Benchmarking Hub'', CC BY 4.0（外部来源数据沿用原许可）', true, true,
  '{"alias_namespace": "epoch", "format": "zip_csv",
    "boards": [
      {"key": "epoch:gpqa_diamond", "file": "gpqa_diamond.csv", "label": "Model version", "score": "mean_score", "scale": 100, "score_key": "gpqa_diamond",
       "extra": {"stderr": "stderr", "organization": "Organization", "release_date": "Release date"},
       "benchmark": {"slug": "gpqa-diamond", "name": "GPQA Diamond", "category": "reasoning", "metric_name": "Accuracy", "metric_unit": "percent",
         "source_url": "https://epoch.ai/benchmarks/gpqa-diamond", "description": "研究生水平的科学问答（Epoch AI 自跑）。"}},
      {"key": "epoch:swe_bench_verified", "file": "swe_bench_verified.csv", "label": "Model version", "score": "mean_score", "scale": 100, "score_key": "swe_bench_verified",
       "extra": {"stderr": "stderr", "organization": "Organization"},
       "benchmark": {"slug": "swe-bench-verified", "name": "SWE-bench Verified", "category": "coding", "metric_name": "Resolved", "metric_unit": "percent",
         "source_url": "https://epoch.ai/benchmarks/swe-bench-verified", "description": "真实 GitHub issue 修复（Epoch AI 自跑）。"}},
      {"key": "epoch:hle", "file": "hle_external.csv", "label": "Model version", "score": "Accuracy", "scale": 100, "score_key": "hle",
       "extra": {"organization": "Organization", "calibration_error": "Calibration Error"},
       "benchmark": {"slug": "humanitys-last-exam", "name": "Humanity''s Last Exam", "category": "reasoning", "metric_name": "Accuracy", "metric_unit": "percent",
         "source_url": "https://lastexam.ai", "description": "前沿学科难题（外部来源，经 Epoch AI 汇总）。"}},
      {"key": "epoch:terminal_bench", "file": "terminalbench_external.csv", "label": "Model version", "score": "Accuracy mean", "scale": 100, "score_key": "terminal_bench",
       "extra": {"agent": "Agent", "organization": "Organization"},
       "benchmark": {"slug": "terminal-bench", "name": "Terminal-Bench", "category": "agents", "metric_name": "Accuracy", "metric_unit": "percent",
         "source_url": "https://www.tbench.ai/leaderboard", "description": "终端环境下的智能体任务（外部来源，Apache-2.0，每个模型取最佳 agent）。"}},
      {"key": "epoch:aider_polyglot", "file": "aider_polyglot_external.csv", "label": "Model version", "score": "Percent correct", "score_key": "aider_polyglot",
       "duration_seconds": "Time per case (seconds)",
       "extra": {"edit_format": "Edit format", "organization": "Organization"},
       "benchmark": {"slug": "aider-polyglot", "name": "Aider Polyglot", "category": "coding", "metric_name": "Percent correct", "metric_unit": "percent",
         "source_url": "https://aider.chat/docs/leaderboards/", "description": "多语言代码编辑（外部来源，Apache-2.0）。"}},
      {"key": "epoch:arc_agi_2", "file": "arc_agi_2_external.csv", "label": "Model version", "score": "Score", "scale": 100, "score_key": "arc_agi_2",
       "cost_usd": "Cost per task",
       "extra": {"organization": "Organization"},
       "benchmark": {"slug": "arc-agi-2", "name": "ARC-AGI-2", "category": "reasoning", "metric_name": "Score", "metric_unit": "percent",
         "source_url": "https://arcprize.org/leaderboard", "description": "抽象推理谜题（外部来源，经 Epoch AI 汇总）。"}},
      {"key": "epoch:livebench", "file": "live_bench_external.csv", "label": "Model version", "score": "Global average",
       "extra": {"reasoning": "Reasoning average", "coding": "Coding average", "math": "Mathematics average", "version": "LiveBench Version"},
       "benchmark": {"slug": "livebench", "name": "LiveBench", "category": "reasoning", "metric_name": "Global average", "metric_unit": "score",
         "source_url": "https://livebench.ai", "description": "定期换题、防数据污染的综合基准（外部来源）。"}},
      {"key": "epoch:eci", "file": "epoch_capabilities_index/eci_scores.csv", "label": "Model", "score": "eci", "score_key": "epoch_eci",
       "extra": {"ci_low": "eci_ci_low", "ci_high": "eci_ci_high", "organization": "Organization", "accessibility": "Accessibility group"},
       "benchmark": {"slug": "epoch-capabilities-index", "name": "Epoch Capabilities Index", "category": "general", "metric_name": "ECI", "metric_unit": "index",
         "source_url": "https://epoch.ai/benchmarks/eci", "description": "Epoch AI 跨基准综合能力指数。"}}
    ]}'),
('benchmark', 'Artificial Analysis（免费 API，仅内部参考）', 'L4', 'api', 'tabular', 'https://artificialanalysis.ai/api/v2/language/models/free', '@daily', false,
  'proprietary-internal', 'Artificial Analysis（免费档禁止再分发，对外展示需商业授权）', false, false,
  '{"alias_namespace": "artificial_analysis", "format": "json", "rows_path": "data",
    "auth_header": "x-api-key", "auth_header_env": "UFT_DATASYNC_AA_API_KEY",
    "boards": [
      {"key": "aa:intelligence_index", "label": "slug", "score": "evaluations.artificial_analysis_intelligence_index", "score_key": "intelligence_index",
       "extra": {"name": "name", "creator": "model_creator.name", "output_tps": "median_output_tokens_per_second"},
       "benchmark": {"slug": "aa-intelligence-index", "name": "AA Intelligence Index", "category": "general", "metric_name": "Index", "metric_unit": "index",
         "source_url": "https://artificialanalysis.ai"}},
      {"key": "aa:coding_index", "label": "slug", "score": "evaluations.artificial_analysis_coding_index", "score_key": "coding_index",
       "extra": {"name": "name", "creator": "model_creator.name"},
       "benchmark": {"slug": "aa-coding-index", "name": "AA Coding Index", "category": "coding", "metric_name": "Index", "metric_unit": "index",
         "source_url": "https://artificialanalysis.ai"}}
    ]}')
) AS v(domain, name, level, kind, fetcher, url, schedule, enabled, license, attribution, public_display, auto_publish, config)
WHERE NOT EXISTS (SELECT 1 FROM price_sources ps WHERE ps.fetcher = v.fetcher AND ps.url IS NOT DISTINCT FROM v.url
                  AND (v.url IS NOT NULL OR ps.name = v.name));
-- +goose StatementEnd

-- +goose StatementBegin
-- 已有来源没有 next_run_at：设为 now，让调度器下一轮就接手（schedule 为空的来源永远不会被调度）。
UPDATE price_sources SET next_run_at = now() WHERE next_run_at IS NULL AND schedule <> '';
UPDATE price_sources SET name = fetcher || ' #' || id WHERE name = '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE benchmark_runs DROP COLUMN IF EXISTS content_hash;
ALTER TABLE benchmarks DROP COLUMN IF EXISTS score_key, DROP COLUMN IF EXISTS alias_namespace,
    DROP COLUMN IF EXISTS external_key, DROP COLUMN IF EXISTS source_id;
DELETE FROM benchmarks WHERE category NOT IN ('agents','media','artifacts','reasoning','search');
ALTER TABLE benchmarks DROP CONSTRAINT benchmarks_category_check;
ALTER TABLE benchmarks ADD CONSTRAINT benchmarks_category_check
    CHECK (category IN ('agents','media','artifacts','reasoning','search'));
DROP TABLE IF EXISTS model_aliases;
DROP TABLE IF EXISTS upstream_offers;
DROP TABLE IF EXISTS data_source_runs;
-- 删掉本迁移新增的来源；已经产生过价格观测（只追加、不可删）或待上架记录的保留下来。
DELETE FROM price_sources ps
WHERE (ps.domain <> 'price' OR ps.fetcher IN ('modelsdev', 'openrouter_models', 'litellm_dataset') AND ps.name <> ps.fetcher || ' #' || ps.id)
  AND NOT EXISTS (SELECT 1 FROM price_observations o WHERE o.source_id = ps.id)
  AND NOT EXISTS (SELECT 1 FROM pending_model_listings l WHERE l.source_id = ps.id);
DROP INDEX IF EXISTS idx_price_sources_due;
ALTER TABLE price_sources
    DROP COLUMN IF EXISTS http_last_modified, DROP COLUMN IF EXISTS http_etag, DROP COLUMN IF EXISTS consecutive_failures,
    DROP COLUMN IF EXISTS last_error, DROP COLUMN IF EXISTS last_run_at, DROP COLUMN IF EXISTS next_run_at,
    DROP COLUMN IF EXISTS auto_publish, DROP COLUMN IF EXISTS public_display, DROP COLUMN IF EXISTS attribution,
    DROP COLUMN IF EXISTS license, DROP COLUMN IF EXISTS name, DROP COLUMN IF EXISTS domain;
-- +goose StatementEnd
