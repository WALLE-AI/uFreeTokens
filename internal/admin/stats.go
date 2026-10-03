package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/reqlog"
	"github.com/shopspring/decimal"
)

// 用量统计（运营后台接口方案 §3）。时间窗不超过 48 小时时直接查 request_logs
// （按天分区裁剪 + 迁移 00015 的索引）；超过 48 小时读小时汇总表 usage_hourly
// （迁移 00021，worker 每 5 分钟重算最近 2 小时）。汇总表口径：
//   - 时间边界按整点小时对齐（from 向下取整、to 之前开始的小时都计入）；
//   - 最近约 5 分钟的数据可能尚未汇总；
//   - 延迟分位数由直方图线性插值近似；其余计数、金额、活跃账户数与原始表一致。
// 响应里的 source 字段标明本次用的是哪一种（raw / rollup）。
//
// 统计口径（§3.1）与 /console/usage 的差异：console 只统计 status='success'，
// 这里 requests 含失败（运营需要看错误率）；tokens、延迟只统计成功请求；
// 收入/成本对所有请求求和（失败请求的 charged_amount 本来就是 0 或 NULL）。

var ErrInvalidStatsRange = errors.New("admin: invalid stats time range")

const (
	maxHourlyRange = 7 * 24 * time.Hour
	maxDailyRange  = 90 * 24 * time.Hour
)

// Metrics 是一组聚合指标。比率与百分位在没有样本时为 nil。
type Metrics struct {
	Requests         int64            `json:"requests"`
	Success          int64            `json:"success"`
	ErrorRate        *decimal.Decimal `json:"error_rate"`
	InputTokens      int64            `json:"input_tokens"`
	OutputTokens     int64            `json:"output_tokens"`
	CacheReadTokens  int64            `json:"cache_read_tokens"`
	ReasoningTokens  int64            `json:"reasoning_tokens"`
	RevenueMicro     int64            `json:"revenue_micro"`
	ListAmountMicro  int64            `json:"list_amount_micro"`
	CostMicro        int64            `json:"cost_micro"`
	GrossProfitMicro int64            `json:"gross_profit_micro"`
	GrossMargin      *decimal.Decimal `json:"gross_margin"`
	P50LatencyMs     *int64           `json:"p50_latency_ms"`
	P95LatencyMs     *int64           `json:"p95_latency_ms"`
	P95TTFTMs        *int64           `json:"p95_ttft_ms"`
	EstimatedRatio   *decimal.Decimal `json:"estimated_ratio"`
	ActiveAccounts   int64            `json:"active_accounts"`
}

const metricsSelect = `count(*),
	count(*) FILTER (WHERE rl.status = 'success'),
	COALESCE(sum(rl.input_tokens) FILTER (WHERE rl.status = 'success'), 0),
	COALESCE(sum(rl.output_tokens) FILTER (WHERE rl.status = 'success'), 0),
	COALESCE(sum(rl.cache_read_tokens) FILTER (WHERE rl.status = 'success'), 0),
	COALESCE(sum(rl.reasoning_tokens) FILTER (WHERE rl.status = 'success'), 0),
	COALESCE(sum(rl.charged_amount), 0),
	COALESCE(sum(rl.list_amount), 0),
	COALESCE(sum(rl.cost_amount), 0),
	percentile_cont(0.5) WITHIN GROUP (ORDER BY rl.latency_ms) FILTER (WHERE rl.status = 'success'),
	percentile_cont(0.95) WITHIN GROUP (ORDER BY rl.latency_ms) FILTER (WHERE rl.status = 'success'),
	percentile_cont(0.95) WITHIN GROUP (ORDER BY rl.ttft_ms) FILTER (WHERE rl.status = 'success' AND rl.is_stream),
	count(*) FILTER (WHERE rl.usage_source = 'estimated'),
	count(DISTINCT rl.account_id)`

// metricsDest 返回 Scan 目标与一个在 Scan 之后计算派生指标的函数。
func metricsDest(m *Metrics) ([]any, func()) {
	var p50, p95, ttft *float64
	var estimated int64
	dest := []any{&m.Requests, &m.Success, &m.InputTokens, &m.OutputTokens, &m.CacheReadTokens, &m.ReasoningTokens,
		&m.RevenueMicro, &m.ListAmountMicro, &m.CostMicro, &p50, &p95, &ttft, &estimated, &m.ActiveAccounts}
	return dest, func() {
		finishMetrics(m, estimated)
		toMs := func(f *float64) *int64 {
			if f == nil {
				return nil
			}
			v := int64(*f + 0.5)
			return &v
		}
		m.P50LatencyMs, m.P95LatencyMs, m.P95TTFTMs = toMs(p50), toMs(p95), toMs(ttft)
	}
}

// rollupThreshold 以上的时间窗读汇总表。
const rollupThreshold = 48 * time.Hour

// statsSource 描述一个统计数据源（原始日志或小时汇总）的列名与聚合表达式。
type statsSource struct {
	name         string
	from         string
	timeCol      string
	providerCol  string
	metrics      string
	dest         func(*Metrics) ([]any, func())
	groupExprs   map[string]string
	orderMetrics map[string]string
}

var rawSource = statsSource{
	name: "raw", from: statsFrom, timeCol: "rl.created_at", providerCol: "pa.provider_id",
	metrics: metricsSelect, dest: metricsDest, groupExprs: groupExprs, orderMetrics: orderMetrics,
}

var rollupSource = statsSource{
	name: "rollup", from: "FROM usage_hourly rl", timeCol: "rl.bucket", providerCol: "rl.provider_id",
	metrics: rollupMetricsSelect, dest: rollupMetricsDest,
	groupExprs: map[string]string{
		"none": "''", "virtual_model": "rl.virtual_model", "channel": "COALESCE(rl.channel_id::text, '')",
		"provider": "COALESCE(rl.provider_id::text, '')", "account": "rl.account_id::text", "api_key": "rl.api_key_id::text",
	},
	orderMetrics: map[string]string{
		"requests": "sum(rl.requests)", "revenue_micro": "sum(rl.charged_micro)", "cost_micro": "sum(rl.cost_micro)",
		"gross_profit": "sum(rl.charged_micro) - sum(rl.cost_micro)", "input_tokens": "sum(rl.input_tokens)",
		"output_tokens": "sum(rl.output_tokens)", "errors": "sum(rl.requests) - sum(rl.success)",
	},
}

// sourceFor 按时间窗长度选择数据源。
func sourceFor(from, to time.Time) statsSource {
	if to.Sub(from) > rollupThreshold {
		return rollupSource
	}
	return rawSource
}

var rollupMetricsSelect = func() string {
	parts := []string{
		"COALESCE(sum(rl.requests), 0)", "COALESCE(sum(rl.success), 0)",
		"COALESCE(sum(rl.input_tokens), 0)", "COALESCE(sum(rl.output_tokens), 0)",
		"COALESCE(sum(rl.cache_read_tokens), 0)", "COALESCE(sum(rl.reasoning_tokens), 0)",
		"COALESCE(sum(rl.charged_micro), 0)", "COALESCE(sum(rl.list_micro), 0)", "COALESCE(sum(rl.cost_micro), 0)",
		"COALESCE(sum(rl.estimated), 0)", "count(DISTINCT rl.account_id)",
	}
	for _, c := range append(reqlog.HistogramColumns("lat"), reqlog.HistogramColumns("ttft")...) {
		parts = append(parts, "COALESCE(sum(rl."+c+"), 0)")
	}
	return strings.Join(parts, ",\n\t")
}()

func rollupMetricsDest(m *Metrics) ([]any, func()) {
	n := len(reqlog.LatencyBucketBounds) + 1
	lat, ttft := make([]int64, n), make([]int64, n)
	var estimated int64
	dest := []any{&m.Requests, &m.Success, &m.InputTokens, &m.OutputTokens, &m.CacheReadTokens, &m.ReasoningTokens,
		&m.RevenueMicro, &m.ListAmountMicro, &m.CostMicro, &estimated, &m.ActiveAccounts}
	for i := range lat {
		dest = append(dest, &lat[i])
	}
	for i := range ttft {
		dest = append(dest, &ttft[i])
	}
	return dest, func() {
		finishMetrics(m, estimated)
		m.P50LatencyMs = histogramPercentile(lat, 0.5)
		m.P95LatencyMs = histogramPercentile(lat, 0.95)
		m.P95TTFTMs = histogramPercentile(ttft, 0.95)
	}
}

// histogramPercentile 在固定分桶直方图上线性插值估算分位数；没有样本返回 nil。
// 最后一个桶（> 最大上界）没有上界，返回最大上界作为下限估计。
func histogramPercentile(counts []int64, p float64) *int64 {
	var total int64
	for _, c := range counts {
		total += c
	}
	if total == 0 {
		return nil
	}
	target := p * float64(total)
	var cum float64
	lower := 0.0
	for i, c := range counts {
		bounds := reqlog.LatencyBucketBounds
		if i == len(bounds) {
			v := bounds[len(bounds)-1]
			return &v
		}
		upper := float64(bounds[i])
		if c > 0 && cum+float64(c) >= target {
			v := int64(lower + (target-cum)/float64(c)*(upper-lower) + 0.5)
			return &v
		}
		cum += float64(c)
		lower = upper
	}
	return nil
}

// finishMetrics 计算派生指标（毛利、错误率、估算占比、毛利率）。
func finishMetrics(m *Metrics, estimated int64) {
	m.GrossProfitMicro = m.RevenueMicro - m.CostMicro
	if m.Requests > 0 {
		er := decimal.NewFromInt(m.Requests - m.Success).Div(decimal.NewFromInt(m.Requests)).Round(4)
		m.ErrorRate = &er
		es := decimal.NewFromInt(estimated).Div(decimal.NewFromInt(m.Requests)).Round(4)
		m.EstimatedRatio = &es
	}
	if m.RevenueMicro != 0 {
		gm := decimal.NewFromInt(m.GrossProfitMicro).Div(decimal.NewFromInt(m.RevenueMicro)).Round(4)
		m.GrossMargin = &gm
	}
}

// StatsFilter 是统计与日志共用的过滤条件；零值表示不限。
type StatsFilter struct {
	From, To     time.Time
	VirtualModel string
	ChannelID    int64
	ProviderID   int64
	AccountID    int64
	APIKeyID     int64
}

// where 生成 WHERE 子句与参数（从 $1 开始编号）。provider 过滤需要 channels / provider_accounts
// 的关联，调用方负责在 FROM 里 LEFT JOIN 出别名 pa。
func (f StatsFilter) where() (string, []any) {
	return f.whereFor(rawSource)
}

// whereFor 按数据源的列名生成 WHERE。汇总表按整点小时对齐：from 向下取整。
func (f StatsFilter) whereFor(src statsSource) (string, []any) {
	from := f.From
	if src.name == "rollup" {
		from = from.UTC().Truncate(time.Hour)
	}
	conds := []string{src.timeCol + " >= $1", src.timeCol + " < $2"}
	args := []any{from, f.To}
	add := func(cond string, v any) {
		args = append(args, v)
		conds = append(conds, fmt.Sprintf(cond, len(args)))
	}
	if f.VirtualModel != "" {
		add("rl.virtual_model = $%d", f.VirtualModel)
	}
	if f.ChannelID != 0 {
		add("rl.channel_id = $%d", f.ChannelID)
	}
	if f.ProviderID != 0 {
		add(src.providerCol+" = $%d", f.ProviderID)
	}
	if f.AccountID != 0 {
		add("rl.account_id = $%d", f.AccountID)
	}
	if f.APIKeyID != 0 {
		add("rl.api_key_id = $%d", f.APIKeyID)
	}
	return "WHERE " + strings.Join(conds, " AND "), args
}

const statsFrom = `FROM request_logs rl
	LEFT JOIN channels c ON c.id = rl.channel_id
	LEFT JOIN provider_accounts pa ON pa.id = c.provider_account_id`

// ---------- overview ----------

type StatsOverview struct {
	Source   string    `json:"source"` // raw / rollup，见包注释
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Current  Metrics   `json:"current"`
	Previous Metrics   `json:"previous"`
}

// Overview 计算 [from, to) 与"上一个等长时间窗"的指标，供工作台 KPI 环比。
func (s *Service) Overview(ctx context.Context, from, to time.Time) (*StatsOverview, error) {
	if !to.After(from) || to.Sub(from) > maxDailyRange {
		return nil, ErrInvalidStatsRange
	}
	src := sourceFor(from, to)
	out := &StatsOverview{Source: src.name, From: from, To: to}
	for _, w := range []struct {
		m        *Metrics
		from, to time.Time
	}{{&out.Current, from, to}, {&out.Previous, from.Add(-to.Sub(from)), from}} {
		where, args := StatsFilter{From: w.from, To: w.to}.whereFor(src)
		dest, finish := src.dest(w.m)
		if err := s.db(ctx).QueryRow(ctx, "SELECT "+src.metrics+" "+src.from+" "+where, args...).Scan(dest...); err != nil {
			return nil, fmt.Errorf("admin: query stats overview: %w", err)
		}
		finish()
	}
	return out, nil
}

// ---------- usage by dimension ----------

type UsageInput struct {
	StatsFilter
	// TZ 决定按小时/按天分桶的边界与桶标签的时区；nil = UTC（兼容旧行为）。
	TZ       *time.Location
	Interval string // hour / day / none
	GroupBy  string // none / virtual_model / channel / provider / account / api_key
	Top      int    // 默认 8，最大 50
	OrderBy  string // 排名指标，默认 revenue_micro
}

type UsageGroup struct {
	Key    string  `json:"key"`
	Label  string  `json:"label"`
	Totals Metrics `json:"totals"`
}

type UsagePoint struct {
	Bucket string `json:"bucket"` // interval=none 时为空
	Group  string `json:"group"`
	Metrics
}

type UsageResult struct {
	Source   string       `json:"source"` // raw / rollup，见包注释
	Interval string       `json:"interval"`
	GroupBy  string       `json:"group_by"`
	Totals   Metrics      `json:"totals"`
	Groups   []UsageGroup `json:"groups"`
	Series   []UsagePoint `json:"series"`
}

const otherGroup = "__other__"

var groupExprs = map[string]string{
	"none":          "''",
	"virtual_model": "rl.virtual_model",
	"channel":       "COALESCE(rl.channel_id::text, '')",
	"provider":      "COALESCE(pa.provider_id::text, '')",
	"account":       "rl.account_id::text",
	"api_key":       "rl.api_key_id::text",
}

var orderMetrics = map[string]string{
	"requests":      "count(*)",
	"revenue_micro": "COALESCE(sum(rl.charged_amount), 0)",
	"cost_micro":    "COALESCE(sum(rl.cost_amount), 0)",
	"gross_profit":  "COALESCE(sum(rl.charged_amount), 0) - COALESCE(sum(rl.cost_amount), 0)",
	"input_tokens":  "COALESCE(sum(rl.input_tokens) FILTER (WHERE rl.status = 'success'), 0)",
	"output_tokens": "COALESCE(sum(rl.output_tokens) FILTER (WHERE rl.status = 'success'), 0)",
	"errors":        "count(*) FILTER (WHERE rl.status <> 'success')",
}

func (s *Service) Usage(ctx context.Context, in UsageInput) (*UsageResult, error) {
	if in.Interval == "" {
		in.Interval = "day"
	}
	if in.GroupBy == "" {
		in.GroupBy = "none"
	}
	if in.OrderBy == "" {
		in.OrderBy = "revenue_micro"
	}
	if in.Top <= 0 || in.Top > 50 {
		in.Top = 8
	}
	src := sourceFor(in.From, in.To)
	groupExpr, ok := src.groupExprs[in.GroupBy]
	if !ok {
		return nil, fmt.Errorf("%w: group_by=%q", ErrInvalidFilterOrValue, in.GroupBy)
	}
	orderExpr, ok := src.orderMetrics[in.OrderBy]
	if !ok {
		return nil, fmt.Errorf("%w: order_by=%q", ErrInvalidSort, in.OrderBy)
	}
	loc := in.TZ
	if loc == nil {
		loc = time.UTC
	}
	// 时区名作为最后一个 SQL 参数传入（不拼接进 SQL）。桶标签在 SQL 里输出
	// 本地时间，按小时的桶在下面补上该时刻的 UTC 偏移（RFC3339）。
	var bucketExpr string
	tzArg := func(args []any) (string, []any) {
		args = append(append([]any{}, args...), loc.String())
		return fmt.Sprintf("$%d", len(args)), args
	}
	switch in.Interval {
	case "hour":
		if in.To.Sub(in.From) > maxHourlyRange {
			return nil, fmt.Errorf("%w: interval=hour supports at most 7 days", ErrInvalidStatsRange)
		}
		bucketExpr = "to_char(date_trunc('hour', " + src.timeCol + " AT TIME ZONE %s), 'YYYY-MM-DD\"T\"HH24:00:00')"
	case "day":
		bucketExpr = "to_char(date_trunc('day', " + src.timeCol + " AT TIME ZONE %s), 'YYYY-MM-DD')"
	case "none":
		bucketExpr = "''"
	default:
		return nil, fmt.Errorf("%w: interval=%q", ErrInvalidFilterOrValue, in.Interval)
	}
	if !in.To.After(in.From) || in.To.Sub(in.From) > maxDailyRange {
		return nil, fmt.Errorf("%w: from/to must be a non-empty range of at most 90 days", ErrInvalidStatsRange)
	}

	where, args := in.StatsFilter.whereFor(src)
	res := &UsageResult{Source: src.name, Interval: in.Interval, GroupBy: in.GroupBy, Groups: []UsageGroup{}, Series: []UsagePoint{}}

	// 1) 总计
	dest, finish := src.dest(&res.Totals)
	if err := s.db(ctx).QueryRow(ctx, "SELECT "+src.metrics+" "+src.from+" "+where, args...).Scan(dest...); err != nil {
		return nil, fmt.Errorf("admin: query usage totals: %w", err)
	}
	finish()

	// 2) 按维度汇总并排名，取 Top N；其余合并为 __other__
	var top []string
	if in.GroupBy != "none" {
		rows, err := s.db(ctx).Query(ctx, fmt.Sprintf("SELECT %s AS grp, %s %s %s GROUP BY grp ORDER BY %s DESC, grp",
			groupExpr, src.metrics, src.from, where, orderExpr), args...)
		if err != nil {
			return nil, fmt.Errorf("admin: query usage groups: %w", err)
		}
		var others Metrics
		var otherCount int
		for rows.Next() {
			var g UsageGroup
			d, fin := src.dest(&g.Totals)
			if err := rows.Scan(append([]any{&g.Key}, d...)...); err != nil {
				rows.Close()
				return nil, fmt.Errorf("admin: scan usage group: %w", err)
			}
			fin()
			if len(top) < in.Top {
				top = append(top, g.Key)
				res.Groups = append(res.Groups, g)
			} else {
				otherCount++
				others.Requests += g.Totals.Requests
				others.RevenueMicro += g.Totals.RevenueMicro
				others.CostMicro += g.Totals.CostMicro
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		if otherCount > 0 {
			// "其他"组的完整指标（含百分位）在下面的 series 查询里按 __other__ 聚合；
			// 这里只给排名表一个近似的汇总行。
			others.GrossProfitMicro = others.RevenueMicro - others.CostMicro
			res.Groups = append(res.Groups, UsageGroup{Key: otherGroup, Label: fmt.Sprintf("其他（%d 项）", otherCount), Totals: others})
		}
		if err := s.labelGroups(ctx, in.GroupBy, res.Groups); err != nil {
			return nil, err
		}
	}

	// 3) 时间序列（interval=none 时只有一个桶，等价于分组汇总，省略）
	if in.Interval != "none" {
		seriesGroup := groupExpr
		seriesArgs := args
		if in.GroupBy != "none" {
			seriesArgs = append(append([]any{}, args...), top)
			seriesGroup = fmt.Sprintf("CASE WHEN %s = ANY($%d) THEN %s ELSE '%s' END", groupExpr, len(seriesArgs), groupExpr, otherGroup)
		}
		var tzParam string
		tzParam, seriesArgs = tzArg(seriesArgs)
		bucketExpr = fmt.Sprintf(bucketExpr, tzParam)
		// 按序号分组：汇总表 usage_hourly 自带 bucket 列，GROUP BY bucket 会被 Postgres 解析成输入列（按小时），
		// 而不是这里的输出别名（按天），导致同一天出现多行。
		rows, err := s.db(ctx).Query(ctx, fmt.Sprintf("SELECT %s AS bucket, %s AS grp, %s %s %s GROUP BY 1, 2 ORDER BY 1, 2",
			bucketExpr, seriesGroup, src.metrics, src.from, where), seriesArgs...)
		if err != nil {
			return nil, fmt.Errorf("admin: query usage series: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var p UsagePoint
			d, fin := src.dest(&p.Metrics)
			if err := rows.Scan(append([]any{&p.Bucket, &p.Group}, d...)...); err != nil {
				return nil, fmt.Errorf("admin: scan usage point: %w", err)
			}
			fin()
			if in.Interval == "hour" {
				p.Bucket = hourBucketLabel(p.Bucket, loc)
			}
			res.Series = append(res.Series, p)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// labelGroups 给分组补上可读名称：模型用展示名、渠道用"上游账号/上游模型"、
// 供应商用 code、账户用名称、API Key 用名称。
func (s *Service) labelGroups(ctx context.Context, groupBy string, groups []UsageGroup) error {
	keys := make([]string, 0, len(groups))
	for _, g := range groups {
		if g.Key != otherGroup && g.Key != "" {
			keys = append(keys, g.Key)
		}
	}
	labels := map[string]string{}
	var q string
	switch groupBy {
	case "virtual_model":
		q = `SELECT vm.name, COALESCE(md.display_name, vm.name) FROM virtual_models vm
		     LEFT JOIN virtual_model_metadata md ON md.virtual_model_id = vm.id WHERE vm.name = ANY($1)`
	case "channel":
		q = `SELECT c.id::text, pa.name || ' / ' || c.upstream_model FROM channels c JOIN provider_accounts pa ON pa.id = c.provider_account_id
		     WHERE c.id::text = ANY($1)`
	case "provider":
		q = `SELECT id::text, name || ' (' || code || ')' FROM providers WHERE id::text = ANY($1)`
	case "account":
		q = `SELECT id::text, name FROM accounts WHERE id::text = ANY($1)`
	case "api_key":
		q = `SELECT id::text, name || ' (' || display_prefix || '…)' FROM api_keys WHERE id::text = ANY($1)`
	}
	if q != "" && len(keys) > 0 {
		rows, err := s.db(ctx).Query(ctx, q, keys)
		if err != nil {
			return fmt.Errorf("admin: query group labels: %w", err)
		}
		for rows.Next() {
			var k, v string
			if err := rows.Scan(&k, &v); err != nil {
				rows.Close()
				return fmt.Errorf("admin: scan group label: %w", err)
			}
			labels[k] = v
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	for i := range groups {
		if groups[i].Label != "" {
			continue
		}
		switch {
		case labels[groups[i].Key] != "":
			groups[i].Label = labels[groups[i].Key]
		case groups[i].Key == "":
			groups[i].Label = "（无）"
		default:
			groups[i].Label = groups[i].Key
		}
	}
	return nil
}

// hourBucketLabel 把 SQL 输出的本地小时 "2006-01-02T15:00:00" 补上该时区在这一刻的
// UTC 偏移（RFC3339）；UTC 输出 "…Z"，与旧格式一致。
func hourBucketLabel(local string, loc *time.Location) string {
	t, err := time.ParseInLocation("2006-01-02T15:04:05", local, loc)
	if err != nil {
		return local
	}
	return t.Format(time.RFC3339)
}
