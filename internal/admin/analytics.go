package admin

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// 分析查询（《运营后台全局助手执行方案》P2）：全局助手 query_analytics 工具背后的语义指标层。
// 指标、分组维度、时间粒度都走白名单，SQL 全部参数化：
//   - usage（默认）：调用量、Token、收入、成本、毛利、错误率、延迟、活跃账户。复用 Usage
//     （≤48 小时读 request_logs，更长读 usage_hourly），week / month 由按天的结果在内存中合并；
//   - wallet：钱包流水中的充值、退款、赠金、赠金过期、人工调账（不含逐请求的 consume，消费看 usage 的收入）；
//   - balance：钱包余额快照（与时间范围无关）。
// 输出统一为 Dataset：列定义 + 行 + 合计。金额换算为元（数值），比率为 0~1 的小数；compare=previous_period
// 时附上一等长周期的值与变化，派生计算全部在服务端完成，助手不需要自己算。

const (
	maxAnalyticsTop    = 50
	maxWalletRange     = 400 * 24 * time.Hour
	defaultAnalyticTop = 10
)

// AnalyticsQuery 是 POST /analytics/query 的请求体。
type AnalyticsQuery struct {
	Subject  string           `json:"subject,omitempty"`  // usage（默认）/ wallet / balance
	Metrics  []string         `json:"metrics,omitempty"`  // 空 = 该主题的默认指标
	GroupBy  string           `json:"group_by,omitempty"` // usage：none / virtual_model / channel / provider / account / api_key；wallet：none / account；balance：none / account / tier / account_type / status
	Interval string           `json:"interval,omitempty"` // none / hour / day / week / month（hour 只用于 usage 且不超过 7 天）
	From     string           `json:"from,omitempty"`     // RFC3339 或 YYYY-MM-DD；缺省 to 往前 7 天
	To       string           `json:"to,omitempty"`       // RFC3339 或 YYYY-MM-DD（含当天）；缺省现在
	TZ       string           `json:"tz,omitempty"`       // 分桶时区（IANA），缺省为平台运营时区
	Filters  AnalyticsFilters `json:"filters,omitempty"`
	Compare  string           `json:"compare,omitempty"`  // "" / previous_period
	Top      int              `json:"top,omitempty"`      // 分组时最多返回的组数，默认 10，最大 50
	OrderBy  string           `json:"order_by,omitempty"` // 分组排序指标（降序），默认该主题的第一个指标
}

// AnalyticsFilters 是分析查询的过滤条件；零值表示不限。
type AnalyticsFilters struct {
	VirtualModel string `json:"virtual_model,omitempty"`
	ChannelID    int64  `json:"channel_id,omitempty"`
	ProviderID   int64  `json:"provider_id,omitempty"`
	AccountID    int64  `json:"account_id,omitempty"`
	APIKeyID     int64  `json:"api_key_id,omitempty"`
}

// AnalyticsColumn 描述数据集的一列。Type：string / time / integer / number / money（元）/ percent（0~1）/
// pp（百分点差，0~1）/ ms（毫秒）。
type AnalyticsColumn struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Type  string `json:"type"`
}

// AnalyticsDataset 是分析查询的结果（也是助手图表、报表引用的数据单元）。
type AnalyticsDataset struct {
	Title    string            `json:"title"`
	Subject  string            `json:"subject"`
	Source   string            `json:"source"` // raw / rollup / ledger / wallets
	From     *time.Time        `json:"from,omitempty"`
	To       *time.Time        `json:"to,omitempty"`
	Interval string            `json:"interval"`
	GroupBy  string            `json:"group_by"`
	Compare  string            `json:"compare,omitempty"`
	Currency string            `json:"currency"`
	Columns  []AnalyticsColumn `json:"columns"`
	Rows     []map[string]any  `json:"rows"`
	Totals   map[string]any    `json:"totals"`
	Previous map[string]any    `json:"previous,omitempty"` // compare=previous_period 时上一周期的合计
	Notes    []string          `json:"notes"`
}

// AnalyticsRange 是解析后的时间范围与分桶时区（由 HTTP 层解析 From/To/TZ 后传入）。
type AnalyticsRange struct {
	From, To time.Time
	Loc      *time.Location
}

type metricDef struct {
	label string
	typ   string
	// additive=false 的指标无法由更细的桶合并（百分位、去重计数），week / month 粒度下为空。
	additive bool
	// order 是 Usage 支持的服务端排序键；空 = 取回后在内存中排序。
	order string
	value func(m *Metrics) any
}

func yuan(micro int64) float64 {
	f, _ := decimal.New(micro, -6).Round(4).Float64()
	return f
}

func decOrNil(d *decimal.Decimal) any {
	if d == nil {
		return nil
	}
	f, _ := d.Float64()
	return f
}

func intOrNil(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

var usageMetrics = map[string]metricDef{
	"requests":        {"请求数", "integer", true, "requests", func(m *Metrics) any { return m.Requests }},
	"success":         {"成功请求", "integer", true, "", func(m *Metrics) any { return m.Success }},
	"errors":          {"失败请求", "integer", true, "errors", func(m *Metrics) any { return m.Requests - m.Success }},
	"error_rate":      {"错误率", "percent", true, "", func(m *Metrics) any { return decOrNil(m.ErrorRate) }},
	"input_tokens":    {"输入 Token", "integer", true, "input_tokens", func(m *Metrics) any { return m.InputTokens }},
	"output_tokens":   {"输出 Token", "integer", true, "output_tokens", func(m *Metrics) any { return m.OutputTokens }},
	"total_tokens":    {"总 Token", "integer", true, "", func(m *Metrics) any { return m.InputTokens + m.OutputTokens }},
	"revenue":         {"收入（元）", "money", true, "revenue_micro", func(m *Metrics) any { return yuan(m.RevenueMicro) }},
	"cost":            {"成本（元）", "money", true, "cost_micro", func(m *Metrics) any { return yuan(m.CostMicro) }},
	"gross_profit":    {"毛利（元）", "money", true, "gross_profit", func(m *Metrics) any { return yuan(m.GrossProfitMicro) }},
	"gross_margin":    {"毛利率", "percent", true, "", func(m *Metrics) any { return decOrNil(m.GrossMargin) }},
	"p50_latency_ms":  {"P50 延迟", "ms", false, "", func(m *Metrics) any { return intOrNil(m.P50LatencyMs) }},
	"p95_latency_ms":  {"P95 延迟", "ms", false, "", func(m *Metrics) any { return intOrNil(m.P95LatencyMs) }},
	"p95_ttft_ms":     {"P95 首字延迟", "ms", false, "", func(m *Metrics) any { return intOrNil(m.P95TTFTMs) }},
	"active_accounts": {"活跃账户", "integer", false, "", func(m *Metrics) any { return m.ActiveAccounts }},
}

var usageDefaultMetrics = []string{"requests", "revenue", "cost", "gross_profit", "gross_margin", "error_rate"}

var groupLabels = map[string]string{
	"virtual_model": "虚拟模型", "channel": "渠道", "provider": "供应商", "account": "账户", "api_key": "API Key",
	"tier": "等级", "account_type": "账户类型", "status": "状态",
}

var intervalLabels = map[string]string{"hour": "按小时", "day": "按天", "week": "按周", "month": "按月"}

// Analytics 执行一次分析查询。
func (s *Service) Analytics(ctx context.Context, q AnalyticsQuery, r AnalyticsRange) (*AnalyticsDataset, error) {
	if r.Loc == nil {
		r.Loc = time.UTC
	}
	q.Subject = strings.TrimSpace(q.Subject)
	if q.Subject == "" {
		q.Subject = "usage"
	}
	if q.GroupBy == "" {
		q.GroupBy = "none"
	}
	if q.Top <= 0 {
		q.Top = defaultAnalyticTop
	}
	if q.Top > maxAnalyticsTop {
		q.Top = maxAnalyticsTop
	}
	if q.Compare != "" && q.Compare != "previous_period" {
		return nil, fmt.Errorf("%w: compare must be empty or previous_period", ErrInvalidFilterOrValue)
	}
	var (
		ds  *AnalyticsDataset
		err error
	)
	switch q.Subject {
	case "usage":
		ds, err = s.usageAnalytics(ctx, q, r)
	case "wallet":
		ds, err = s.walletAnalytics(ctx, q, r)
	case "balance":
		ds, err = s.balanceAnalytics(ctx, q)
	default:
		return nil, fmt.Errorf("%w: subject must be usage / wallet / balance", ErrInvalidFilterOrValue)
	}
	if err != nil {
		return nil, err
	}
	ds.Subject, ds.GroupBy, ds.Compare = q.Subject, q.GroupBy, q.Compare
	ds.Currency = "CNY"
	if ds.Notes == nil {
		ds.Notes = []string{}
	}
	ds.Title = analyticsTitle(ds, r)
	return ds, nil
}

func analyticsTitle(ds *AnalyticsDataset, r AnalyticsRange) string {
	parts := []string{map[string]string{"usage": "用量", "wallet": "钱包流水", "balance": "钱包余额"}[ds.Subject]}
	if ds.GroupBy != "none" {
		parts = append(parts, "按"+groupLabels[ds.GroupBy])
	}
	if l := intervalLabels[ds.Interval]; l != "" {
		parts = append(parts, l)
	}
	if ds.From != nil && ds.To != nil {
		// To 是开区间，展示时取前一刻所在的日期。
		parts = append(parts, ds.From.In(r.Loc).Format("2006-01-02")+" ~ "+ds.To.Add(-time.Nanosecond).In(r.Loc).Format("2006-01-02"))
	}
	if ds.Compare == "previous_period" {
		parts = append(parts, "对比上一周期")
	}
	return strings.Join(parts, " · ")
}

// pickMetrics 校验并返回指标列表（保持调用方给定的顺序，去重）。
func pickMetrics(req []string, defs map[string]metricDef, defaults []string) ([]string, error) {
	if len(req) == 0 {
		return defaults, nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(req))
	for _, m := range req {
		m = strings.TrimSpace(m)
		if _, ok := defs[m]; !ok {
			names := make([]string, 0, len(defs))
			for k := range defs {
				names = append(names, k)
			}
			sort.Strings(names)
			return nil, fmt.Errorf("%w: unknown metric %q (supported: %s)", ErrInvalidFilterOrValue, m, strings.Join(names, ", "))
		}
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out, nil
}

// ---------- usage ----------

func (s *Service) usageAnalytics(ctx context.Context, q AnalyticsQuery, r AnalyticsRange) (*AnalyticsDataset, error) {
	metrics, err := pickMetrics(q.Metrics, usageMetrics, usageDefaultMetrics)
	if err != nil {
		return nil, err
	}
	if _, ok := groupExprs[q.GroupBy]; !ok {
		return nil, fmt.Errorf("%w: group_by for usage must be none / virtual_model / channel / provider / account / api_key", ErrInvalidFilterOrValue)
	}
	interval := q.Interval
	if interval == "" {
		interval = map[bool]string{true: "day", false: "none"}[q.GroupBy == "none"]
	}
	usageInterval := interval
	switch interval {
	case "none", "hour", "day":
	case "week", "month":
		usageInterval = "day"
	default:
		return nil, fmt.Errorf("%w: interval must be none / hour / day / week / month", ErrInvalidFilterOrValue)
	}
	orderBy := q.OrderBy
	if orderBy == "" {
		orderBy = metrics[0]
	}
	od, ok := usageMetrics[orderBy]
	if !ok {
		return nil, fmt.Errorf("%w: order_by must be one of the usage metrics", ErrInvalidFilterOrValue)
	}
	serverOrder := od.order
	if serverOrder == "" {
		serverOrder = "revenue_micro"
	}
	f := q.Filters
	base := UsageInput{
		StatsFilter: StatsFilter{VirtualModel: f.VirtualModel, ChannelID: f.ChannelID, ProviderID: f.ProviderID, AccountID: f.AccountID, APIKeyID: f.APIKeyID},
		TZ:          r.Loc, Interval: usageInterval, GroupBy: q.GroupBy, Top: q.Top, OrderBy: serverOrder,
	}
	run := func(from, to time.Time) (*UsageResult, error) {
		in := base
		in.From, in.To = from, to
		return s.Usage(ctx, in)
	}
	cur, err := run(r.From, r.To)
	if err != nil {
		return nil, err
	}
	var prev *UsageResult
	if q.Compare == "previous_period" {
		span := r.To.Sub(r.From)
		if prev, err = run(r.From.Add(-span), r.From); err != nil {
			return nil, err
		}
	}

	from, to := r.From, r.To
	ds := &AnalyticsDataset{Source: cur.Source, From: &from, To: &to, Interval: interval}
	if serverOrder != od.order && q.GroupBy != "none" {
		ds.Notes = append(ds.Notes, fmt.Sprintf("按 %s 排序时，前 %d 组先按收入选出，再按 %s 重新排序。", orderBy, q.Top, orderBy))
	}
	if cur.Source == "rollup" {
		ds.Notes = append(ds.Notes, "时间范围超过 48 小时，读小时汇总表：最近约 5 分钟的数据可能尚未汇总，延迟分位数为近似值。")
	}
	if interval == "week" || interval == "month" {
		for _, m := range metrics {
			if !usageMetrics[m].additive {
				ds.Notes = append(ds.Notes, "按周/按月时，P50/P95 延迟与活跃账户数无法由按天数据合并，显示为空。")
				break
			}
		}
	}
	metricValues := func(m *Metrics, merged bool) map[string]any {
		out := make(map[string]any, len(metrics))
		for _, k := range metrics {
			d := usageMetrics[k]
			if merged && !d.additive {
				out[k] = nil
				continue
			}
			out[k] = d.value(m)
		}
		return out
	}
	ds.Totals = metricValues(&cur.Totals, false)
	if prev != nil {
		ds.Previous = metricValues(&prev.Totals, false)
	}

	cols := []AnalyticsColumn{}
	metricCols := func() []AnalyticsColumn {
		out := make([]AnalyticsColumn, 0, len(metrics))
		for _, k := range metrics {
			out = append(out, AnalyticsColumn{Key: k, Label: usageMetrics[k].label, Type: usageMetrics[k].typ})
		}
		return out
	}
	groupLabel := map[string]string{}
	for _, g := range cur.Groups {
		groupLabel[g.Key] = g.Label
	}

	switch {
	case interval == "none":
		// 分组汇总（group_by=none 时只有一行合计）
		if q.GroupBy == "none" {
			cols = append(cols, metricCols()...)
			ds.Rows = []map[string]any{metricValues(&cur.Totals, false)}
			break
		}
		cols = append(cols, AnalyticsColumn{Key: "key", Label: groupLabels[q.GroupBy] + " ID", Type: "string"},
			AnalyticsColumn{Key: "label", Label: groupLabels[q.GroupBy], Type: "string"})
		cols = append(cols, metricCols()...)
		prevByKey := map[string]*Metrics{}
		if prev != nil {
			for i := range prev.Groups {
				prevByKey[prev.Groups[i].Key] = &prev.Groups[i].Totals
			}
		}
		for i := range cur.Groups {
			g := &cur.Groups[i]
			row := metricValues(&g.Totals, false)
			row["key"], row["label"] = g.Key, g.Label
			if g.Key == otherGroup {
				// "其他"组只有请求数、收入、成本是准确的汇总（见 Usage）
				for _, k := range metrics {
					if k != "requests" && k != "revenue" && k != "cost" && k != "gross_profit" {
						row[k] = nil
					}
				}
			}
			if prev != nil {
				addCompare(row, metrics, prevByKey[g.Key], usageMetrics)
			}
			ds.Rows = append(ds.Rows, row)
		}
		if serverOrder != od.order {
			sortRows(ds.Rows, orderBy)
		}
	default:
		// 时间序列（可带分组，长表：每个 桶×组 一行）
		cols = append(cols, AnalyticsColumn{Key: "bucket", Label: "时间", Type: "time"})
		if q.GroupBy != "none" {
			cols = append(cols, AnalyticsColumn{Key: "key", Label: groupLabels[q.GroupBy] + " ID", Type: "string"},
				AnalyticsColumn{Key: "label", Label: groupLabels[q.GroupBy], Type: "string"})
		}
		cols = append(cols, metricCols()...)
		merged := interval == "week" || interval == "month"
		points := bucketPoints(cur.Series, interval, r.Loc)
		var prevPoints []UsagePoint
		if prev != nil {
			prevPoints = bucketPoints(prev.Series, interval, r.Loc)
		}
		// 上一周期按「同一组内的第 i 个桶」对齐
		prevIdx := map[string][]*Metrics{}
		for i := range prevPoints {
			p := &prevPoints[i]
			prevIdx[p.Group] = append(prevIdx[p.Group], &p.Metrics)
		}
		seen := map[string]int{}
		for i := range points {
			p := &points[i]
			row := metricValues(&p.Metrics, merged)
			row["bucket"] = p.Bucket
			if q.GroupBy != "none" {
				row["key"] = p.Group
				row["label"] = groupLabel[p.Group]
				if row["label"] == "" {
					row["label"] = p.Group
				}
			}
			if prev != nil {
				var pm *Metrics
				if list := prevIdx[p.Group]; seen[p.Group] < len(list) {
					pm = list[seen[p.Group]]
				}
				seen[p.Group]++
				addCompare(row, metrics, pm, usageMetrics)
			}
			ds.Rows = append(ds.Rows, row)
		}
	}
	if prev != nil {
		cols = append(cols, compareColumns(metrics, usageMetrics)...)
	}
	if ds.Rows == nil {
		ds.Rows = []map[string]any{}
	}
	ds.Columns = cols
	return ds, nil
}

// bucketPoints 把按天的点合并成按周（周一起始）/按月的点；hour / day 原样返回。
func bucketPoints(series []UsagePoint, interval string, loc *time.Location) []UsagePoint {
	if interval != "week" && interval != "month" {
		return series
	}
	type key struct{ bucket, group string }
	idx := map[key]int{}
	var out []UsagePoint
	for _, p := range series {
		d, err := time.ParseInLocation("2006-01-02", p.Bucket, loc)
		if err != nil {
			continue
		}
		var b string
		if interval == "week" {
			offset := (int(d.Weekday()) + 6) % 7 // 周一 = 0
			b = d.AddDate(0, 0, -offset).Format("2006-01-02")
		} else {
			b = d.Format("2006-01")
		}
		k := key{b, p.Group}
		i, ok := idx[k]
		if !ok {
			idx[k] = len(out)
			out = append(out, UsagePoint{Bucket: b, Group: p.Group})
			i = len(out) - 1
		}
		mergeMetrics(&out[i].Metrics, &p.Metrics)
	}
	for i := range out {
		m := &out[i].Metrics
		m.GrossProfitMicro = m.RevenueMicro - m.CostMicro
		m.ErrorRate, m.GrossMargin = nil, nil
		if m.Requests > 0 {
			er := decimal.NewFromInt(m.Requests - m.Success).Div(decimal.NewFromInt(m.Requests)).Round(4)
			m.ErrorRate = &er
		}
		if m.RevenueMicro != 0 {
			gm := decimal.NewFromInt(m.GrossProfitMicro).Div(decimal.NewFromInt(m.RevenueMicro)).Round(4)
			m.GrossMargin = &gm
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Bucket != out[b].Bucket {
			return out[a].Bucket < out[b].Bucket
		}
		return out[a].Group < out[b].Group
	})
	return out
}

// mergeMetrics 累加可相加的计数与金额（百分位、去重计数不可相加，由调用方置空）。
func mergeMetrics(dst, src *Metrics) {
	dst.Requests += src.Requests
	dst.Success += src.Success
	dst.InputTokens += src.InputTokens
	dst.OutputTokens += src.OutputTokens
	dst.CacheReadTokens += src.CacheReadTokens
	dst.ReasoningTokens += src.ReasoningTokens
	dst.RevenueMicro += src.RevenueMicro
	dst.ListAmountMicro += src.ListAmountMicro
	dst.CostMicro += src.CostMicro
}

// addCompare 给一行补上上一周期的值与变化：比率类给百分点差（pp），其余给相对变化（上一周期为 0 时为空）。
func addCompare(row map[string]any, metrics []string, prev *Metrics, defs map[string]metricDef) {
	for _, k := range metrics {
		d := defs[k]
		var pv any
		if prev != nil {
			pv = d.value(prev)
		}
		row[k+"_prev"] = pv
		row[k+"_change"] = change(row[k], pv, d.typ)
	}
}

func addCompareValues(row map[string]any, metrics []string, prev map[string]any, defs map[string]metricDef) {
	for _, k := range metrics {
		pv := prev[k]
		row[k+"_prev"] = pv
		row[k+"_change"] = change(row[k], pv, defs[k].typ)
	}
}

func compareColumns(metrics []string, defs map[string]metricDef) []AnalyticsColumn {
	out := make([]AnalyticsColumn, 0, 2*len(metrics))
	for _, k := range metrics {
		d := defs[k]
		out = append(out, AnalyticsColumn{Key: k + "_prev", Label: d.label + "（上期）", Type: d.typ})
		if d.typ == "percent" {
			out = append(out, AnalyticsColumn{Key: k + "_change", Label: d.label + "变化（百分点）", Type: "pp"})
		} else {
			out = append(out, AnalyticsColumn{Key: k + "_change", Label: d.label + "环比", Type: "percent"})
		}
	}
	return out
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case int64:
		return float64(t), true
	case int:
		return float64(t), true
	case float64:
		return t, true
	}
	return 0, false
}

func change(cur, prev any, typ string) any {
	c, ok1 := toFloat(cur)
	p, ok2 := toFloat(prev)
	if !ok1 || !ok2 {
		return nil
	}
	if typ == "percent" {
		f, _ := decimal.NewFromFloat(c - p).Round(4).Float64()
		return f
	}
	if p == 0 {
		return nil
	}
	f, _ := decimal.NewFromFloat((c - p) / absf(p)).Round(4).Float64()
	return f
}

func absf(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// sortRows 按指标降序排列（空值排在最后，"其他"组固定在末尾）。
func sortRows(rows []map[string]any, metric string) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i]["key"] == otherGroup || rows[j]["key"] == otherGroup {
			return rows[j]["key"] == otherGroup && rows[i]["key"] != otherGroup
		}
		a, okA := toFloat(rows[i][metric])
		b, okB := toFloat(rows[j][metric])
		if okA != okB {
			return okA
		}
		return a > b
	})
}

// ---------- wallet（钱包流水） ----------

// walletMetrics 的取值来自 walletRow.values（value 字段不用）。
var walletMetrics = map[string]metricDef{
	"recharge_amount":      {"充值金额（元）", "money", true, "", nil},
	"recharge_count":       {"充值笔数", "integer", true, "", nil},
	"paying_accounts":      {"充值账户数", "integer", true, "", nil},
	"refund_amount":        {"退款金额（元）", "money", true, "", nil},
	"grant_amount":         {"赠金发放（元）", "money", true, "", nil},
	"grant_expired_amount": {"赠金过期（元）", "money", true, "", nil},
	"adjust_amount":        {"人工调账（元）", "money", true, "", nil},
}

var walletSelect = `COALESCE(sum(le.amount) FILTER (WHERE le.type = 'recharge'), 0),
	count(*) FILTER (WHERE le.type = 'recharge'),
	count(DISTINCT le.account_id) FILTER (WHERE le.type = 'recharge'),
	COALESCE(sum(le.amount) FILTER (WHERE le.type = 'refund'), 0),
	COALESCE(sum(le.amount) FILTER (WHERE le.type = 'grant'), 0),
	COALESCE(sum(le.amount) FILTER (WHERE le.type = 'grant_expire'), 0),
	COALESCE(sum(le.amount) FILTER (WHERE le.type = 'adjust'), 0)`

type walletRow [7]int64

func (w walletRow) values(metrics []string) map[string]any {
	all := map[string]any{
		"recharge_amount": yuan(w[0]), "recharge_count": w[1], "paying_accounts": w[2], "refund_amount": yuan(w[3]),
		"grant_amount": yuan(w[4]), "grant_expired_amount": yuan(w[5]), "adjust_amount": yuan(w[6]),
	}
	out := make(map[string]any, len(metrics))
	for _, k := range metrics {
		out[k] = all[k]
	}
	return out
}

func (w *walletRow) dest() []any {
	return []any{&w[0], &w[1], &w[2], &w[3], &w[4], &w[5], &w[6]}
}

var walletOrderExprs = map[string]string{
	"recharge_amount": "1", "recharge_count": "2", "paying_accounts": "3", "refund_amount": "4",
	"grant_amount": "5", "grant_expired_amount": "6", "adjust_amount": "7",
}

func (s *Service) walletAnalytics(ctx context.Context, q AnalyticsQuery, r AnalyticsRange) (*AnalyticsDataset, error) {
	metrics, err := pickMetrics(q.Metrics, walletMetrics, []string{"recharge_amount", "recharge_count", "paying_accounts", "grant_amount", "adjust_amount"})
	if err != nil {
		return nil, err
	}
	if !r.To.After(r.From) || r.To.Sub(r.From) > maxWalletRange {
		return nil, fmt.Errorf("%w: from/to must be a non-empty range of at most 400 days", ErrInvalidStatsRange)
	}
	if q.GroupBy != "none" && q.GroupBy != "account" {
		return nil, fmt.Errorf("%w: group_by for wallet must be none / account", ErrInvalidFilterOrValue)
	}
	interval := q.Interval
	if interval == "" {
		interval = map[bool]string{true: "day", false: "none"}[q.GroupBy == "none"]
	}
	var truncUnit, bucketFmt string
	switch interval {
	case "none":
	case "day":
		truncUnit, bucketFmt = "day", "YYYY-MM-DD"
	case "week":
		truncUnit, bucketFmt = "week", "YYYY-MM-DD"
	case "month":
		truncUnit, bucketFmt = "month", "YYYY-MM"
	default:
		return nil, fmt.Errorf("%w: interval for wallet must be none / day / week / month", ErrInvalidFilterOrValue)
	}
	if q.GroupBy == "account" && interval != "none" {
		return nil, fmt.Errorf("%w: group_by=account only supports interval=none for wallet", ErrInvalidFilterOrValue)
	}
	orderBy := q.OrderBy
	if orderBy == "" {
		orderBy = metrics[0]
	}
	orderCol, ok := walletOrderExprs[orderBy]
	if !ok {
		return nil, fmt.Errorf("%w: order_by must be one of the wallet metrics", ErrInvalidFilterOrValue)
	}

	where := func(from, to time.Time) (string, []any) {
		conds := []string{"le.type <> 'consume'", "le.created_at >= $1", "le.created_at < $2"}
		args := []any{from, to}
		if q.Filters.AccountID != 0 {
			args = append(args, q.Filters.AccountID)
			conds = append(conds, fmt.Sprintf("le.account_id = $%d", len(args)))
		}
		return "WHERE " + strings.Join(conds, " AND "), args
	}
	totals := func(from, to time.Time) (walletRow, error) {
		var w walletRow
		wh, args := where(from, to)
		err := s.db(ctx).QueryRow(ctx, "SELECT "+walletSelect+" FROM ledger_entries le "+wh, args...).Scan(w.dest()...)
		if err != nil {
			return w, fmt.Errorf("admin: query wallet totals: %w", err)
		}
		return w, nil
	}
	// grouped 返回 (bucket 或账户 ID) → 指标，按 SQL 的顺序。
	grouped := func(from, to time.Time) ([]string, map[string]walletRow, error) {
		wh, args := where(from, to)
		var sql string
		if q.GroupBy == "account" {
			args = append(args, q.Top)
			sql = fmt.Sprintf("SELECT le.account_id::text AS k, %s FROM ledger_entries le %s GROUP BY k ORDER BY %s DESC, k LIMIT $%d",
				walletSelect, wh, walletOrderOrdinal(orderCol), len(args))
		} else {
			args = append(args, r.Loc.String())
			sql = fmt.Sprintf("SELECT to_char(date_trunc('%s', le.created_at AT TIME ZONE $%d), '%s') AS k, %s FROM ledger_entries le %s GROUP BY k ORDER BY k",
				truncUnit, len(args), bucketFmt, walletSelect, wh)
		}
		rows, err := s.db(ctx).Query(ctx, sql, args...)
		if err != nil {
			return nil, nil, fmt.Errorf("admin: query wallet groups: %w", err)
		}
		defer rows.Close()
		var keys []string
		out := map[string]walletRow{}
		for rows.Next() {
			var k string
			var w walletRow
			if err := rows.Scan(append([]any{&k}, w.dest()...)...); err != nil {
				return nil, nil, fmt.Errorf("admin: scan wallet group: %w", err)
			}
			keys = append(keys, k)
			out[k] = w
		}
		return keys, out, rows.Err()
	}

	from, to := r.From, r.To
	ds := &AnalyticsDataset{Source: "ledger", From: &from, To: &to, Interval: interval,
		Notes: []string{"金额按账本原始符号汇总：充值、赠金为正，赠金过期为负，人工调账有正有负；消费流水不在此统计（看用量的收入）。"}}
	tot, err := totals(r.From, r.To)
	if err != nil {
		return nil, err
	}
	ds.Totals = tot.values(metrics)
	span := r.To.Sub(r.From)
	if q.Compare == "previous_period" {
		pt, err := totals(r.From.Add(-span), r.From)
		if err != nil {
			return nil, err
		}
		ds.Previous = pt.values(metrics)
	}
	cols := []AnalyticsColumn{}
	metricCols := make([]AnalyticsColumn, 0, len(metrics))
	for _, k := range metrics {
		metricCols = append(metricCols, AnalyticsColumn{Key: k, Label: walletMetrics[k].label, Type: walletMetrics[k].typ})
	}
	if q.GroupBy == "none" && interval == "none" {
		cols = append(cols, metricCols...)
		ds.Rows = []map[string]any{tot.values(metrics)}
		if ds.Previous != nil {
			addCompareValues(ds.Rows[0], metrics, ds.Previous, walletMetrics)
		}
	} else {
		keys, rows, err := grouped(r.From, r.To)
		if err != nil {
			return nil, err
		}
		var prevKeys []string
		var prevRows map[string]walletRow
		if q.Compare == "previous_period" {
			if prevKeys, prevRows, err = grouped(r.From.Add(-span), r.From); err != nil {
				return nil, err
			}
		}
		var labels map[string]string
		if q.GroupBy == "account" {
			cols = append(cols, AnalyticsColumn{Key: "key", Label: "账户 ID", Type: "string"}, AnalyticsColumn{Key: "label", Label: "账户", Type: "string"})
			if labels, err = s.accountLabels(ctx, keys); err != nil {
				return nil, err
			}
		} else {
			cols = append(cols, AnalyticsColumn{Key: "bucket", Label: "时间", Type: "time"})
		}
		cols = append(cols, metricCols...)
		for i, k := range keys {
			row := rows[k].values(metrics)
			if q.GroupBy == "account" {
				row["key"], row["label"] = k, labels[k]
			} else {
				row["bucket"] = k
			}
			if q.Compare == "previous_period" {
				var pv map[string]any
				if q.GroupBy == "account" {
					if w, ok := prevRows[k]; ok {
						pv = w.values(metrics)
					}
				} else if i < len(prevKeys) {
					pv = prevRows[prevKeys[i]].values(metrics)
				}
				addCompareValues(row, metrics, pv, walletMetrics)
			}
			ds.Rows = append(ds.Rows, row)
		}
	}
	if q.Compare == "previous_period" {
		cols = append(cols, compareColumns(metrics, walletMetrics)...)
	}
	if ds.Rows == nil {
		ds.Rows = []map[string]any{}
	}
	ds.Columns = cols
	return ds, nil
}

// walletOrderOrdinal 把指标在 SELECT 中的序号（1 起）换成带分组列偏移的 ORDER BY 序号。
func walletOrderOrdinal(col string) string {
	var n int
	_, _ = fmt.Sscan(col, &n)
	return fmt.Sprint(n + 1)
}

// accountLabels 返回账户 ID → 名称。
func (s *Service) accountLabels(ctx context.Context, ids []string) (map[string]string, error) {
	groups := make([]UsageGroup, len(ids))
	for i, id := range ids {
		groups[i] = UsageGroup{Key: id}
	}
	if err := s.labelGroups(ctx, "account", groups); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(groups))
	for _, g := range groups {
		out[g.Key] = g.Label
	}
	return out, nil
}

// ---------- balance（钱包余额快照） ----------

// balanceMetrics 的取值来自 balanceRow.values（value 字段不用）。
var balanceMetrics = map[string]metricDef{
	"cash_balance":    {"现金余额（元）", "money", true, "", nil},
	"bonus_balance":   {"赠金余额（元）", "money", true, "", nil},
	"total_balance":   {"总余额（元）", "money", true, "", nil},
	"frozen_amount":   {"冻结金额（元）", "money", true, "", nil},
	"accounts":        {"账户数", "integer", true, "", nil},
	"funded_accounts": {"有现金余额的账户", "integer", true, "", nil},
}

var balanceOrderExprs = map[string]string{
	"cash_balance": "sum(w.cash_balance)", "bonus_balance": "sum(w.bonus_balance)",
	"total_balance": "sum(w.cash_balance + w.bonus_balance)", "frozen_amount": "sum(w.frozen)",
	"accounts": "count(*)", "funded_accounts": "count(*) FILTER (WHERE w.cash_balance > 0)",
}

var balanceGroupExprs = map[string]string{
	"none": "''", "account": "a.id::text", "tier": "a.tier", "account_type": "a.type", "status": "a.status",
}

const balanceSelect = `COALESCE(sum(w.cash_balance), 0), COALESCE(sum(w.bonus_balance), 0), COALESCE(sum(w.frozen), 0),
	count(*), count(*) FILTER (WHERE w.cash_balance > 0)`

type balanceRow [5]int64

func (b balanceRow) values(metrics []string) map[string]any {
	all := map[string]any{
		"cash_balance": yuan(b[0]), "bonus_balance": yuan(b[1]), "total_balance": yuan(b[0] + b[1]),
		"frozen_amount": yuan(b[2]), "accounts": b[3], "funded_accounts": b[4],
	}
	out := make(map[string]any, len(metrics))
	for _, k := range metrics {
		out[k] = all[k]
	}
	return out
}

func (s *Service) balanceAnalytics(ctx context.Context, q AnalyticsQuery) (*AnalyticsDataset, error) {
	metrics, err := pickMetrics(q.Metrics, balanceMetrics, []string{"total_balance", "cash_balance", "bonus_balance", "accounts"})
	if err != nil {
		return nil, err
	}
	grp, ok := balanceGroupExprs[q.GroupBy]
	if !ok {
		return nil, fmt.Errorf("%w: group_by for balance must be none / account / tier / account_type / status", ErrInvalidFilterOrValue)
	}
	if q.Interval != "" && q.Interval != "none" {
		return nil, fmt.Errorf("%w: balance is a current snapshot and only supports interval=none", ErrInvalidFilterOrValue)
	}
	if q.Compare != "" {
		return nil, fmt.Errorf("%w: balance is a current snapshot and does not support compare", ErrInvalidFilterOrValue)
	}
	orderBy := q.OrderBy
	if orderBy == "" {
		orderBy = metrics[0]
	}
	orderExpr, ok := balanceOrderExprs[orderBy]
	if !ok {
		return nil, fmt.Errorf("%w: order_by must be one of the balance metrics", ErrInvalidFilterOrValue)
	}
	where, args := "", []any{}
	if q.Filters.AccountID != 0 {
		args = append(args, q.Filters.AccountID)
		where = "WHERE a.id = $1"
	}
	const from = "FROM wallets w JOIN accounts a ON a.id = w.account_id"
	ds := &AnalyticsDataset{Source: "wallets", Interval: "none", Notes: []string{"余额为当前快照，与时间范围无关。"}}
	var tot balanceRow
	if err := s.db(ctx).QueryRow(ctx, "SELECT "+balanceSelect+" "+from+" "+where, args...).Scan(&tot[0], &tot[1], &tot[2], &tot[3], &tot[4]); err != nil {
		return nil, fmt.Errorf("admin: query balance totals: %w", err)
	}
	ds.Totals = tot.values(metrics)
	cols := []AnalyticsColumn{}
	if q.GroupBy == "none" {
		ds.Rows = []map[string]any{tot.values(metrics)}
	} else {
		gargs := append(append([]any{}, args...), q.Top)
		rows, err := s.db(ctx).Query(ctx, fmt.Sprintf("SELECT %s AS k, %s %s %s GROUP BY k ORDER BY %s DESC, k LIMIT $%d",
			grp, balanceSelect, from, where, orderExpr, len(gargs)), gargs...)
		if err != nil {
			return nil, fmt.Errorf("admin: query balance groups: %w", err)
		}
		var keys []string
		vals := map[string]balanceRow{}
		for rows.Next() {
			var k string
			var b balanceRow
			if err := rows.Scan(&k, &b[0], &b[1], &b[2], &b[3], &b[4]); err != nil {
				rows.Close()
				return nil, fmt.Errorf("admin: scan balance group: %w", err)
			}
			keys = append(keys, k)
			vals[k] = b
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		labels := map[string]string{}
		if q.GroupBy == "account" {
			if labels, err = s.accountLabels(ctx, keys); err != nil {
				return nil, err
			}
		}
		cols = append(cols, AnalyticsColumn{Key: "key", Label: groupLabels[q.GroupBy] + map[bool]string{true: " ID", false: ""}[q.GroupBy == "account"], Type: "string"})
		if q.GroupBy == "account" {
			cols = append(cols, AnalyticsColumn{Key: "label", Label: "账户", Type: "string"})
		}
		for _, k := range keys {
			row := vals[k].values(metrics)
			row["key"] = k
			if q.GroupBy == "account" {
				row["label"] = labels[k]
			}
			ds.Rows = append(ds.Rows, row)
		}
	}
	for _, k := range metrics {
		cols = append(cols, AnalyticsColumn{Key: k, Label: balanceMetrics[k].label, Type: balanceMetrics[k].typ})
	}
	if ds.Rows == nil {
		ds.Rows = []map[string]any{}
	}
	ds.Columns = cols
	return ds, nil
}
