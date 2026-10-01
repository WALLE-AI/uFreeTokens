package app

import (
	"encoding/json"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/benchmarks"
	"github.com/WALLE-AI/uFreeTokens/internal/rankings"
)

// 公开排行榜与基准测试接口（public.go）的 OpenAPI 描述，见 docs/基准测试与排行榜数据服务技术方案.md §3.4。
// 拆出单独文件只是为了不让 gateway_openapi.go 过长；登记方式与其余接口完全相同。

var rankingCommonDocs = map[string]l10n{
	"period":      {"Echo of the requested period: `day`, `week` or `month`.", "所请求的统计期：`day`、`week` 或 `month`。"},
	"from":        {"First day of the period (inclusive), `YYYY-MM-DD` in `tz`.", "统计期第一天（含），`tz` 时区的 `YYYY-MM-DD`。"},
	"to":          {"Day after the last day of the period (exclusive). Periods are complete days ending yesterday.", "统计期最后一天的后一天（不含）。统计期由截至昨天的完整自然日组成。"},
	"tz":          {"Time zone used to cut days, always `Asia/Shanghai`.", "切日时区，固定为 `Asia/Shanghai`。"},
	"updated_at":  {"When the underlying daily data was last materialized (about every 30 minutes); `null` if there is no data.", "底层日数据最近一次物化的时间（约每 30 分钟一次）；无数据时为 `null`。"},
	"methodology": {"The rules used to compute this ranking.", "本榜单的统计口径。"},
}

var rankingTokenDocs = map[string]l10n{
	"total_tokens": {"Total counted tokens of the period, including `others`. Omitted unless the server is configured to publish absolute numbers.", "统计期内计入的 token 总量（含 `others`）。服务端未开启公开绝对值时省略。"},
	"others":       {"Everything not listed individually: entries below the privacy threshold, non-public models, entries beyond `limit`.", "未单独上榜的部分：未达隐私阈值的、非公开模型、超出 `limit` 的。"},
}

func withCommon(prefix string, docs map[string]l10n, extra ...map[string]l10n) map[string]l10n {
	out := map[string]l10n{}
	for _, m := range append([]map[string]l10n{rankingCommonDocs}, extra...) {
		for k, v := range m {
			out[prefix+"."+k] = v
		}
	}
	for k, v := range docs {
		out[prefix+"."+k] = v
	}
	return out
}

var modelEntryDocs = map[string]l10n{
	"rank":             {"1-based rank.", "名次，从 1 开始。"},
	"model":            {"Model ID (same as `GET /v1/catalog` `name`).", "模型 ID（与 `GET /v1/catalog` 的 `name` 相同）。"},
	"display_name":     {"Display name of the model.", "模型展示名。"},
	"author":           {"Model author: the part of the model ID before `/`.", "模型作者：模型 ID 中 `/` 之前的部分。"},
	"provider_display": {"Display name of the model vendor. Omitted if not set.", "模型厂商展示名；未录入时省略。"},
	"deprecated":       {"`true` if the model has been retired (listed for history; cannot be called).", "模型已下架时为 `true`（作为历史数据展示，不能调用）。"},
}

var benchmarkDocs = map[string]l10n{
	"slug":             {"URL-safe identifier, e.g. `gpqa-diamond`.", "URL 标识，如 `gpqa-diamond`。"},
	"name":             {"Benchmark name.", "基准名称。"},
	"category":         {"`general`, `coding`, `agents`, `reasoning`, `chinese`, `search`, `media`, `artifacts` or `embedding`.", "`general`、`coding`、`agents`、`reasoning`、`chinese`、`search`、`media`、`artifacts` 或 `embedding`。"},
	"description":      {"What the benchmark measures.", "基准测什么。"},
	"metric_name":      {"Name of the score metric, e.g. `accuracy`, `elo`.", "分数指标名，如 `accuracy`、`elo`。"},
	"metric_unit":      {"`percent` (score is 0–100) or a free-form unit such as `elo`.", "`percent`（分数为 0–100）或自由格式的单位，如 `elo`。"},
	"higher_is_better": {"Whether a higher score is better.", "分数是否越高越好。"},
	"source_name":      {"External data source; `null` for the platform's own evaluations.", "外部数据来源；平台自建评测为 `null`。"},
	"source_url":       {"Link to the external source, or `null`.", "外部来源链接，或 `null`。"},
	"license":          {"License of imported external data (e.g. `CC-BY-4.0`); display `source_name` as attribution. `null` for manually entered or self-evaluated benchmarks.", "导入的外部数据的许可（如 `CC-BY-4.0`），展示时请以 `source_name` 署名；手工录入或平台自建评测为 `null`。"},
	"run":              {"The latest published run, or `null` if none has been published yet.", "最新一次已发布的评测批次；尚未发布时为 `null`。"},
	"models_count":     {"Number of models in the published run.", "已发布批次的参评模型数。"},
	"champions":        {"Quality / value / speed champions of the published run.", "已发布批次的质量 / 性价比 / 速度冠军。"},
}

var benchmarkResultDocs = map[string]l10n{
	"model_label":         {"Model name as reported by the run (models not listed on the platform can take part).", "评测批次中的模型名（平台未上架的模型也可参评）。"},
	"model":               {"Model ID on this platform if the result is linked to a public model, otherwise `null`.", "关联到平台公开模型时为其模型 ID，否则为 `null`。"},
	"score":               {"Score in `metric_unit`.", "分数，单位见 `metric_unit`。"},
	"cost_per_task_micro": {"Average cost per task in micro-units of `run.cost_currency` (1,000,000 = 1 USD/CNY), or `null`.", "单题平均成本，`run.cost_currency` 的微单位（1,000,000 = 1 美元/元），或 `null`。"},
	"avg_duration_ms":     {"Average duration per task in milliseconds, or `null`.", "单题平均耗时（毫秒），或 `null`。"},
}

func publicFieldDocs() map[string]l10n {
	out := map[string]l10n{}
	merge := func(m map[string]l10n) { maps.Copy(out, m) }

	merge(withCommon("ModelRanking", map[string]l10n{
		"models": {"Ranked models.", "上榜模型。"},
	}, rankingTokenDocs))
	merge(withCommon("AuthorRanking", map[string]l10n{
		"authors": {"Ranked authors (top 9).", "上榜作者（前 9 名）。"},
	}, rankingTokenDocs))
	merge(withCommon("AppRanking", map[string]l10n{
		"apps": {"Ranked apps.", "上榜应用。"},
	}, rankingTokenDocs))
	merge(withCommon("SpeedRanking", map[string]l10n{
		"models": {"Models sorted by output speed, fastest first.", "按输出速度从快到慢排序的模型。"},
	}))
	for k, v := range modelEntryDocs {
		out["ModelRankingEntry."+k] = v
		out["SpeedRankingEntry."+k] = v
	}
	merge(map[string]l10n{
		"ModelRankingEntry.tokens": {"Counted tokens (after the per-account cap). Omitted unless absolute numbers are published.", "计入排名的 token 数（已应用单账户上限）。未公开绝对值时省略。"},
		"ModelRankingEntry.share":  {"Share of `total_tokens` (0–1).", "占 `total_tokens` 的比例（0–1）。"},
		"ModelRankingEntry.change": {"Change versus the previous period of equal length (`0.12` = +12%); `null` if the previous period had no usage.", "与上一个等长统计期相比的变化（`0.12` = +12%）；上一期无用量时为 `null`。"},
		"ModelRankingEntry.series": {"Daily series, only with `series=day`.", "每日序列，仅 `series=day` 时返回。"},

		"SpeedRankingEntry.tokens_per_second":      {"Median (P50) output speed of successful streaming requests, in tokens per second (each request: output tokens ÷ generation time; estimated from a bucketed histogram). Used for ranking.", "成功流式请求输出速度的中位数（P50），单位 token/秒（每个请求为输出 token ÷ 生成耗时；由分桶直方图估算）。排名依据。"},
		"SpeedRankingEntry.mean_tokens_per_second": {"Token-weighted mean output speed: total output tokens ÷ total generation time.", "按 token 加权的平均输出速度：输出 token 之和 ÷ 生成耗时之和。"},

		"RankingSeriesPoint.day":    {"Day, `YYYY-MM-DD`.", "日期，`YYYY-MM-DD`。"},
		"RankingSeriesPoint.tokens": {"Counted tokens that day. Omitted unless absolute numbers are published.", "当天计入的 token 数。未公开绝对值时省略。"},
		"RankingSeriesPoint.share":  {"Share of that day's platform-wide public tokens (0–1).", "占当天全平台公开 token 的比例（0–1）。"},

		"RankingOthers.tokens": {"Tokens of `others`. Omitted unless absolute numbers are published.", "\"其他\"的 token 数。未公开绝对值时省略。"},
		"RankingOthers.share":  {"Share of `others` (0–1).", "\"其他\"的占比（0–1）。"},

		"RankingMethodology.min_distinct_accounts": {"Minimum number of distinct accounts in the period for an entry to be listed individually.", "单独上榜所需的统计期内最少独立账户数。"},
		"RankingMethodology.max_account_share":     {"One account counts for at most this share of an entry's raw period total (anti-gaming cap).", "单个账户最多计入该条目当期原始总量的比例（防刷上限）。"},
		"RankingMethodology.excludes_internal":     {"Internal, load-test and evaluation accounts are excluded.", "已排除内部、压测与评测账户。"},
		"RankingMethodology.success_only":          {"Only successful requests are counted (input + output tokens; output already includes reasoning tokens).", "只统计成功请求（输入 + 输出 token；输出已包含推理 token）。"},
		"RankingMethodology.shows_absolute":        {"Whether absolute token numbers are published.", "是否公开绝对 token 数。"},

		"AuthorRankingEntry.rank":             {"1-based rank.", "名次，从 1 开始。"},
		"AuthorRankingEntry.author":           {"Model author: the part of the model ID before `/`.", "模型作者：模型 ID 中 `/` 之前的部分。"},
		"AuthorRankingEntry.provider_display": {"Display name of the author.", "作者展示名。"},
		"AuthorRankingEntry.tokens":           {"Counted tokens. Omitted unless absolute numbers are published.", "计入的 token 数。未公开绝对值时省略。"},
		"AuthorRankingEntry.share":            {"Share of `total_tokens` (0–1).", "占 `total_tokens` 的比例（0–1）。"},
		"AuthorRankingEntry.change":           {"Change versus the previous period; `null` if it had no usage.", "与上一期相比的变化；上一期无用量时为 `null`。"},
		"AuthorRankingEntry.models":           {"Number of the author's public models used in the period.", "统计期内被使用的该作者公开模型数。"},

		"AppRankingEntry.rank":     {"1-based rank.", "名次，从 1 开始。"},
		"AppRankingEntry.app_name": {"App name declared with the `X-Title` request header.", "通过请求头 `X-Title` 声明的应用名。"},
		"AppRankingEntry.app_url":  {"App origin (`scheme://host`) declared with `HTTP-Referer`; empty if not declared.", "通过 `HTTP-Referer` 声明的应用地址（`scheme://host`）；未声明时为空。"},
		"AppRankingEntry.tokens":   {"Counted tokens. Omitted unless absolute numbers are published.", "计入的 token 数。未公开绝对值时省略。"},
		"AppRankingEntry.share":    {"Share of `total_tokens` (0–1).", "占 `total_tokens` 的比例（0–1）。"},
		"AppRankingEntry.change":   {"Change versus the previous period; `null` if it had no usage.", "与上一期相比的变化；上一期无用量时为 `null`。"},

		"BenchmarkListResponse.object": {"Always `list`.", "固定为 `list`。"},
		"BenchmarkListResponse.data":   {"Published benchmarks.", "已发布的基准。"},

		"BenchmarkRun.id":            {"Run ID.", "评测批次 ID。"},
		"BenchmarkRun.origin":        {"`self_eval` (measured on this platform), `manual` or `import` (external data).", "`self_eval`（平台实测），`manual` 或 `import`（外部数据）。"},
		"BenchmarkRun.run_at":        {"When the evaluation was run.", "评测时间。"},
		"BenchmarkRun.cost_currency": {"Currency of `cost_per_task_micro`: `USD` or `CNY`.", "`cost_per_task_micro` 的币种：`USD` 或 `CNY`。"},
		"BenchmarkRun.notes":         {"Notes about the run.", "批次说明。"},

		"BenchmarkChampions.quality": {"Best score.", "分数最好。"},
		"BenchmarkChampions.value":   {"Lowest cost per task among models scoring at least the median.", "分数不低于中位数的模型中单题成本最低。"},
		"BenchmarkChampions.speed":   {"Lowest average duration among models scoring at least the median.", "分数不低于中位数的模型中平均耗时最短。"},

		"BenchmarkResult.rank":         {"1-based rank; ties share a rank.", "名次，从 1 开始；同分并列。"},
		"BenchmarkResult.error_rate":   {"Share of failed tasks (0–1), or `null`.", "失败题目占比（0–1），或 `null`。"},
		"BenchmarkResult.sample_count": {"Number of tasks evaluated, or `null`.", "评测题目数，或 `null`。"},
		"BenchmarkResult.extra":        {"Extra data such as sample media URLs, or `null`.", "附加数据（如样例媒体链接），或 `null`。"},

		"BenchmarkDetail.results": {"Full leaderboard of the published run, best first.", "已发布批次的完整排行榜，从好到差。"},

		"ModelBenchmarksResponse.object": {"Always `list`.", "固定为 `list`。"},
		"ModelBenchmarksResponse.model":  {"The requested model ID.", "请求的模型 ID。"},
		"ModelBenchmarksResponse.data":   {"The model's results, grouped by category.", "该模型的成绩，按分类排列。"},

		"ModelBenchmark.slug":             benchmarkDocs["slug"],
		"ModelBenchmark.name":             benchmarkDocs["name"],
		"ModelBenchmark.category":         benchmarkDocs["category"],
		"ModelBenchmark.metric_name":      benchmarkDocs["metric_name"],
		"ModelBenchmark.metric_unit":      benchmarkDocs["metric_unit"],
		"ModelBenchmark.higher_is_better": benchmarkDocs["higher_is_better"],
		"ModelBenchmark.source_name":      benchmarkDocs["source_name"],
		"ModelBenchmark.source_url":       benchmarkDocs["source_url"],
		"ModelBenchmark.license":          benchmarkDocs["license"],
		"ModelBenchmark.run_at":           {"When the published run was evaluated.", "已发布批次的评测时间。"},
		"ModelBenchmark.model_label":      {"Name on the leaderboard; when several variants (e.g. reasoning effort) map to the model, the best one is returned.", "榜单上的模型名；多个档位（如推理强度）都对应该模型时返回成绩最好的一个。"},
		"ModelBenchmark.score":            {"Score in `metric_unit`.", "分数，单位见 `metric_unit`。"},
		"ModelBenchmark.rank":             {"1-based rank in the published run; ties share a rank.", "在已发布批次中的名次，从 1 开始；同分并列。"},
		"ModelBenchmark.models_count":     {"Number of entries in the published run.", "已发布批次的参评条目数。"},
		"ModelBenchmark.extra":            {"Extra data from the source (e.g. confidence interval, vote count), or `null`.", "来源附带的数据（如置信区间、票数），或 `null`。"},
	})
	for k, v := range benchmarkDocs {
		out["Benchmark."+k] = v
		out["BenchmarkDetail."+k] = v
	}
	for k, v := range benchmarkResultDocs {
		out["BenchmarkResult."+k] = v
		out["BenchmarkChampion."+k] = v
	}
	return out
}

var publicSchemaDocs = map[string]l10n{
	"ModelRanking":            {"Model token-usage ranking.", "模型 token 用量榜。"},
	"ModelRankingEntry":       {"A ranked model.", "一个上榜模型。"},
	"RankingSeriesPoint":      {"One day of a model's trend.", "模型趋势中的一天。"},
	"RankingOthers":           {"Aggregate of entries not listed individually.", "未单独上榜部分的汇总。"},
	"RankingMethodology":      {"Ranking rules.", "榜单统计口径。"},
	"AuthorRanking":           {"Model author (vendor) token share.", "模型作者（厂商）token 份额。"},
	"AuthorRankingEntry":      {"A ranked author.", "一个上榜作者。"},
	"SpeedRanking":            {"Fastest models by output speed.", "按输出速度排名的模型。"},
	"SpeedRankingEntry":       {"A ranked model. Upstream providers are not disclosed.", "一个上榜模型。不披露上游供应商。"},
	"AppRanking":              {"Apps and clients ranked by tokens.", "按 token 排名的应用与客户端。"},
	"AppRankingEntry":         {"A ranked app.", "一个上榜应用。"},
	"BenchmarkListResponse":   {"Published benchmarks.", "已发布的基准列表。"},
	"Benchmark":               {"A published benchmark with its latest published run summary.", "一个已发布基准及其最新已发布批次的摘要。"},
	"BenchmarkDetail":         {"A published benchmark with the full leaderboard of its latest published run.", "一个已发布基准及其最新已发布批次的完整排行榜。"},
	"BenchmarkRun":            {"An evaluation run.", "一次评测批次。"},
	"BenchmarkChampions":      {"Champions of a run; each may be `null` when there is not enough data.", "批次冠军；数据不足时对应项为 `null`。"},
	"BenchmarkChampion":       {"A champion result.", "一项冠军结果。"},
	"BenchmarkResult":         {"One model's result in a run.", "某模型在批次中的结果。"},
	"ModelBenchmarksResponse": {"A model's results across published benchmarks.", "某模型在各已发布基准上的成绩。"},
	"ModelBenchmark":          {"A model's result on one benchmark.", "某模型在一个基准上的成绩。"},
}

func init() {
	maps.Copy(gatewayFieldDocs, publicFieldDocs())
	maps.Copy(gatewaySchemaDocs, publicSchemaDocs)
}

func queryParam(name string, schema map[string]any, example any, d l10n) map[string]any {
	p := map[string]any{"name": name, "in": "query", "required": false, "schema": schema}
	if example != nil {
		p["example"] = example
	}
	return described(p, d)
}

var (
	periodParam = queryParam("period", map[string]any{"type": "string", "enum": []string{"day", "week", "month"}, "default": "week"}, "week",
		l10n{"Statistics period: the last 1 / 7 / 30 complete days (Asia/Shanghai) ending yesterday.", "统计期：截至昨天的最近 1 / 7 / 30 个完整自然日（Asia/Shanghai）。"})
	limitParam = queryParam("limit", map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": 20}, nil,
		l10n{"Maximum number of entries.", "最多返回的条数。"})
	publicCacheHeader = map[string]any{"Cache-Control": described(map[string]any{"schema": map[string]any{"type": "string"}, "example": "public, max-age=300"},
		l10n{"`public, max-age=300`.", "`public, max-age=300`。"})}
	publicRateLimit  = l10n{"60 requests per minute per client IP (shared by all public ranking and benchmark endpoints).", "每个客户端 IP 每分钟 60 次（所有公开榜单与基准测试接口共享）。"}
	rankingsErrors   = []string{"invalid_request", "rate_limit_exceeded", "internal_error", "service_unavailable", "not_implemented"}
	rankingsErrorMsg = map[string]string{"invalid_request": "'period' must be one of day, week, month.", "internal_error": "Failed to load rankings.",
		"service_unavailable": "Public rankings are temporarily unavailable.", "not_implemented": "Public rankings are not available on this deployment."}
)

const rankingRulesEN = "\n\n**Rules.** Only successful requests count (input + output tokens; output already includes reasoning tokens). Internal, load-test and evaluation accounts are excluded. An entry is listed only if at least `methodology.min_distinct_accounts` distinct accounts used it in the period; one account counts for at most `methodology.max_account_share` of an entry's period total. Only models visible in the public catalog are listed; everything else is folded into `others`. Absolute token numbers are omitted unless the operator publishes them. When rankings are switched off the endpoint returns `503 service_unavailable`.\n\nNo API key required; responses carry `Cache-Control: public, max-age=300`."

const rankingRulesZH = "\n\n**口径。** 只统计成功请求（输入 + 输出 token；输出已包含推理 token）。排除内部、压测与评测账户。统计期内至少有 `methodology.min_distinct_accounts` 个独立账户使用过才单独上榜；单个账户最多计入该条目当期总量的 `methodology.max_account_share`。只列出公开目录中可见的模型，其余计入 `others`。运营未开启时不返回绝对 token 数。榜单下线时返回 `503 service_unavailable`。\n\n无需 API Key；响应带 `Cache-Control: public, max-age=300`。"

func publicOperations() ([]gatewayOperation, error) {
	tok := func(v int64) *int64 { return &v }
	chg := func(v float64) *float64 { return &v }
	updated := time.Date(2026, 10, 1, 8, 30, 0, 0, rankings.Location)
	method := rankings.RankingMethodology{MinDistinctAccounts: 3, MaxAccountShare: 0.2, ExcludesInternal: true, SuccessOnly: true}
	p, from, to, tz := rankings.PeriodWeek, "2026-09-24", "2026-10-01", "Asia/Shanghai"

	modelsEx := rankings.ModelRanking{Period: p, From: from, To: to, TZ: tz, UpdatedAt: &updated, Methodology: method,
		Models: []rankings.ModelRankingEntry{
			{Rank: 1, Model: "deepseek-ai/DeepSeek-V4-Flash", DisplayName: "DeepSeek V4 Flash", Author: "deepseek-ai", ProviderDisplay: "DeepSeek",
				Share: 0.4213, Change: chg(0.12), Series: []rankings.RankingSeriesPoint{{Day: "2026-09-24", Share: 0.39}, {Day: "2026-09-25", Share: 0.41}}},
			{Rank: 2, Model: "BAAI/bge-m3", DisplayName: "BGE M3", Author: "BAAI", ProviderDisplay: "BAAI", Share: 0.1802},
		},
		Others: rankings.RankingOthers{Share: 0.3985}}
	authorsEx := rankings.AuthorRanking{Period: p, From: from, To: to, TZ: tz, UpdatedAt: &updated, Methodology: method,
		Authors: []rankings.AuthorRankingEntry{{Rank: 1, Author: "deepseek-ai", ProviderDisplay: "DeepSeek", Share: 0.52, Change: chg(-0.03), Models: 3}},
		Others:  rankings.RankingOthers{Share: 0.48}}
	speedEx := rankings.SpeedRanking{Period: p, From: from, To: to, TZ: tz, UpdatedAt: &updated, Methodology: method,
		Models: []rankings.SpeedRankingEntry{{Rank: 1, Model: "deepseek-ai/DeepSeek-V4-Flash", DisplayName: "DeepSeek V4 Flash", Author: "deepseek-ai", ProviderDisplay: "DeepSeek", TokensPerSecond: 85.3, MeanTokensPerSecond: 91.7}}}
	appsMethod := method
	appsMethod.ShowsAbsolute = true
	appsEx := rankings.AppRanking{Period: p, From: from, To: to, TZ: tz, UpdatedAt: &updated, Methodology: appsMethod, TotalTokens: tok(52_000_000),
		Apps:   []rankings.AppRankingEntry{{Rank: 1, AppName: "Cline", AppURL: "https://cline.bot", Tokens: tok(15_600_000), Share: 0.3, Change: chg(0.1)}},
		Others: rankings.RankingOthers{Tokens: tok(36_400_000), Share: 0.7}}

	src, srcURL := "Example Leaderboard", "https://example.com/gpqa"
	model := "deepseek-ai/DeepSeek-V4-Flash"
	cost, dur := int64(12_300), 5300
	champ := &benchmarks.BenchmarkChampion{ModelLabel: "DeepSeek V4 Flash", Model: &model, Score: 71.2, CostPerTaskMicro: &cost, AvgDurationMs: &dur}
	bench := benchmarks.Benchmark{Slug: "gpqa-diamond", Name: "GPQA Diamond", Category: "reasoning",
		Description: "Graduate-level science questions.", MetricName: "accuracy", MetricUnit: "percent", HigherIsBetter: true,
		SourceName: &src, SourceURL: &srcURL, ModelsCount: 1,
		Run:       &benchmarks.BenchmarkRun{ID: 12, Origin: "import", RunAt: time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC), CostCurrency: "USD"},
		Champions: benchmarks.BenchmarkChampions{Quality: champ, Value: champ, Speed: champ}}
	license := "CC-BY-4.0"
	bench.License = &license
	modelBenchEx := modelBenchmarksResponse{Object: "list", Model: model, Data: []benchmarks.ModelBenchmark{{Slug: "gpqa-diamond", Name: "GPQA Diamond",
		Category: "reasoning", MetricName: "Accuracy", MetricUnit: "percent", HigherIsBetter: true, SourceName: &src, SourceURL: &srcURL,
		License: &license, RunAt: time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC), ModelLabel: "deepseek-v4-flash_max", Score: 71.2, Rank: 8, ModelsCount: 120,
		Extra: json.RawMessage(`{"stderr":1.8}`)}}}
	errRate, samples := 0.01, 198
	detailEx := benchmarks.BenchmarkDetail{Benchmark: bench, Results: []benchmarks.BenchmarkResult{{Rank: 1, ModelLabel: "DeepSeek V4 Flash", Model: &model,
		Score: 71.2, CostPerTaskMicro: &cost, AvgDurationMs: &dur, ErrorRate: &errRate, SampleCount: &samples, Extra: json.RawMessage("null")}}}

	rankingOp := func(path, opID string, summary, desc l10n, params []any, schema, example any) gatewayOperation {
		return gatewayOperation{method: http.MethodGet, path: path, opID: opID, tag: "Rankings", auth: "none",
			summary: summary, desc: l10n{desc.en + rankingRulesEN, desc.zh + rankingRulesZH}, rateLimit: &publicRateLimit,
			params: params, respSchema: schema, respExample: example, respHeaders: publicCacheHeader,
			errors: rankingsErrors, errMessages: rankingsErrorMsg}
	}
	seriesParam := queryParam("series", map[string]any{"type": "string", "enum": []string{"none", "day"}, "default": "none"}, "day",
		l10n{"`day` adds a daily series to each model (for trend charts).", "为 `day` 时每个模型附带每日序列（用于趋势图）。"})
	modelsLimit := queryParam("limit", map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": 20}, 20,
		l10n{"Maximum number of models.", "最多返回的模型数。"})

	benchErrMsg := map[string]string{"invalid_request": "'category' must be one of " + strings.Join(benchmarks.Categories, ", ") + ".",
		"internal_error": "Failed to load benchmarks.", "not_found": "Benchmark not found."}
	benchDescEN := " No API key required (the same content is returned with or without one); responses carry `Cache-Control: public, max-age=300`. Results from external sources (`run.origin` = `manual`/`import`) use the source's own cost figures; `self_eval` runs are measured on this platform at its actual prices."
	benchDescZH := "无需 API Key（带不带 Key 返回相同内容）；响应带 `Cache-Control: public, max-age=300`。外部来源的结果（`run.origin` 为 `manual`/`import`）成本沿用来源口径；`self_eval` 为平台按实际售价实测。"

	return []gatewayOperation{
		rankingOp("/v1/rankings/models", "getModelRankings", l10n{"Get model usage rankings", "获取模型用量榜"},
			l10n{"Models ranked by tokens processed on the platform in the period, with share, change versus the previous period and (with `series=day`) a daily trend.",
				"按统计期内平台处理的 token 量对模型排名，附带份额、与上一期相比的变化，以及（`series=day` 时）每日趋势。"},
			[]any{periodParam, modelsLimit, seriesParam}, rankings.ModelRanking{}, modelsEx),
		rankingOp("/v1/rankings/authors", "getAuthorRankings", l10n{"Get model author market share", "获取模型厂商份额"},
			l10n{"Token share by model author (the part of the model ID before `/`): top 9 plus `others`. An author needs at least `min_distinct_accounts` distinct accounts across its models.",
				"按模型作者（模型 ID 中 `/` 之前的部分）统计 token 份额：前 9 名加 `others`。作者在其全部模型上需至少有 `min_distinct_accounts` 个独立账户。"},
			[]any{periodParam}, rankings.AuthorRanking{}, authorsEx),
		rankingOp("/v1/rankings/speed", "getSpeedRankings", l10n{"Get fastest models", "获取最快推理速度榜"},
			l10n{"Models ranked by median (P50) output speed of successful streaming requests; each request's speed is output tokens ÷ generation time (last chunk − first chunk). Non-streaming requests are not counted because queueing and generation cannot be separated. A model needs at least 10 such requests in the period. Upstream providers are not disclosed.",
				"按成功流式请求输出速度的中位数（P50）对模型排名；单个请求的速度 = 输出 token ÷ 生成耗时（最后一个 chunk − 第一个 chunk）。非流式请求无法区分排队与生成，不参与统计。模型在统计期内需至少有 10 个此类请求。不披露上游供应商。"},
			[]any{periodParam, limitParam}, rankings.SpeedRanking{}, speedEx),
		rankingOp("/v1/rankings/tools", "getToolCallRankings", l10n{"Get tool-calling rankings", "获取工具调用榜"},
			l10n{"Models ranked by tokens of successful requests whose response contained tool calls. Same response shape as `GET /v1/rankings/models` (without series).",
				"按响应中含工具调用的成功请求的 token 量对模型排名。响应形状同 `GET /v1/rankings/models`（无序列）。"},
			[]any{periodParam, limitParam}, rankings.ModelRanking{}, modelsEx),
		rankingOp("/v1/rankings/multimodal", "getMultimodalRankings", l10n{"Get image-input rankings", "获取多模态图像处理榜"},
			l10n{"Models ranked by tokens of successful requests that contained image inputs (`image_url` content parts). Same response shape as `GET /v1/rankings/models` (without series).",
				"按含图片输入（`image_url` 内容块）的成功请求的 token 量对模型排名。响应形状同 `GET /v1/rankings/models`（无序列）。"},
			[]any{periodParam, limitParam}, rankings.ModelRanking{}, modelsEx),
		rankingOp("/v1/rankings/apps", "getAppRankings", l10n{"Get app rankings", "获取热门应用榜"},
			l10n{"Apps and clients ranked by tokens. Only requests that declare an app with the `X-Title` header (≤ 64 characters; optional `HTTP-Referer`, of which only `scheme://host` is kept) are counted; requests with the same `HTTP-Referer` origin are grouped as one app. Operators may block or merge impersonating app names.",
				"按 token 量对应用与客户端排名。只统计用请求头 `X-Title`（≤ 64 个字符；可选 `HTTP-Referer`，只保留 `scheme://host`）声明了应用的请求；`HTTP-Referer` 来源相同的请求归为同一应用。运营可屏蔽或合并冒用的应用名。"},
			[]any{periodParam, limitParam}, rankings.AppRanking{}, appsEx),
		{
			method: http.MethodGet, path: "/v1/benchmarks", opID: "listBenchmarks", tag: "Benchmarks", auth: "none",
			summary: l10n{"List benchmarks", "列出基准测试"},
			desc: l10n{"Published benchmarks with the summary of their latest published run: model count and quality / value / speed champions (value and speed are chosen among models scoring at least the median)." + benchDescEN,
				"已发布的基准及其最新已发布批次的摘要：参评模型数，以及质量 / 性价比 / 速度三项冠军（性价比与速度在分数不低于中位数的模型中评选）。" + benchDescZH},
			rateLimit: &publicRateLimit,
			params: []any{queryParam("category", map[string]any{"type": "string", "enum": benchmarks.Categories}, "reasoning",
				l10n{"Only benchmarks of this category.", "只返回该分类的基准。"})},
			respSchema: benchmarkListResponse{}, respExample: benchmarkListResponse{Object: "list", Data: []benchmarks.Benchmark{bench}},
			respHeaders: publicCacheHeader,
			errors:      []string{"invalid_request", "rate_limit_exceeded", "internal_error"}, errMessages: benchErrMsg,
		},
		{
			method: http.MethodGet, path: "/v1/benchmarks/{slug}", opID: "getBenchmark", tag: "Benchmarks", auth: "none",
			summary: l10n{"Get a benchmark leaderboard", "获取基准排行榜"},
			desc: l10n{"A published benchmark with the full leaderboard of its latest published run (best first; ties share a rank). Unknown or unpublished slugs return `404 not_found`." + benchDescEN,
				"一个已发布基准及其最新已发布批次的完整排行榜（从好到差，同分并列）。不存在或未发布的 slug 返回 `404 not_found`。" + benchDescZH},
			rateLimit: &publicRateLimit,
			params: []any{described(map[string]any{"name": "slug", "in": "path", "required": true, "schema": map[string]any{"type": "string"}, "example": "gpqa-diamond"},
				l10n{"Benchmark slug.", "基准的 slug。"})},
			respSchema: benchmarks.BenchmarkDetail{}, respExample: detailEx, respHeaders: publicCacheHeader,
			errors: []string{"not_found", "rate_limit_exceeded", "internal_error"}, errMessages: benchErrMsg,
		},
		{
			method: http.MethodGet, path: "/v1/model-benchmarks", opID: "getModelBenchmarks", tag: "Benchmarks", auth: "none",
			summary: l10n{"Get a model's benchmark results", "获取模型的评测成绩"},
			desc: l10n{"A public model's results on every published benchmark (one entry per benchmark, its best variant), for model detail pages. Unknown or non-public models return an empty list." + benchDescEN,
				"某个公开模型在全部已发布基准上的成绩（每个基准一条，取最好的档位），用于模型详情页。模型不存在或不公开时返回空列表。" + benchDescZH},
			rateLimit: &publicRateLimit,
			params: []any{described(map[string]any{"name": "model", "in": "query", "required": true, "schema": map[string]any{"type": "string"}, "example": model},
				l10n{"Model ID as listed in `GET /v1/catalog`.", "模型 ID，同 `GET /v1/catalog`。"})},
			respSchema: modelBenchmarksResponse{}, respExample: modelBenchEx, respHeaders: publicCacheHeader,
			errors:      []string{"invalid_request", "rate_limit_exceeded", "internal_error"},
			errMessages: map[string]string{"invalid_request": "'model' is required.", "internal_error": "Failed to load benchmarks."},
		},
	}, nil
}

// publicTagDocs 是公开数据接口的两个标签。
var publicTagDocs = []struct {
	name string
	d    l10n
}{
	{"Rankings", l10n{"Public usage rankings (no API key).", "公开用量排行榜（无需 API Key）。"}},
	{"Benchmarks", l10n{"Public benchmark leaderboards (no API key).", "公开基准测试排行榜（无需 API Key）。"}},
}
