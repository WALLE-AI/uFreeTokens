package rankings

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/catalog"
	"github.com/WALLE-AI/uFreeTokens/internal/reqlog"
)

// 阶段 3 的两个榜单：最快推理速度、热门应用（docs/基准测试与排行榜数据服务技术方案.md §3.4）。

// minSpeedSamples 是上速度榜所需的最少流式成功请求数：样本太少时吞吐量波动大，没有参考意义。
const minSpeedSamples = 10

// SpeedRankingEntry 是速度榜的一行。只展示模型维度，不暴露上游渠道 / 供应商账户（商业信息）。
type SpeedRankingEntry struct {
	Rank            int    `json:"rank"`
	Model           string `json:"model"`
	DisplayName     string `json:"display_name"`
	Author          string `json:"author"`
	ProviderDisplay string `json:"provider_display,omitempty"`
	Deprecated      bool   `json:"deprecated"`
	// TokensPerSecond 是单请求输出吞吐的中位数（P50，直方图桶内线性插值）；
	// MeanTokensPerSecond 是按 token 加权的平均吞吐（输出 token 之和 ÷ 生成耗时之和）。
	TokensPerSecond     float64 `json:"tokens_per_second"`
	MeanTokensPerSecond float64 `json:"mean_tokens_per_second"`
}

// SpeedRanking 是 GET /v1/rankings/speed 的响应。
type SpeedRanking struct {
	Period      Period              `json:"period"`
	From        string              `json:"from"`
	To          string              `json:"to"`
	TZ          string              `json:"tz"`
	Models      []SpeedRankingEntry `json:"models"`
	UpdatedAt   *time.Time          `json:"updated_at"`
	Methodology RankingMethodology  `json:"methodology"`
}

// Speed 计算输出吞吐（tok/s）：统计期内每个成功流式请求的"输出 token ÷ 生成耗时"的中位数，
// 按中位数从快到慢排序；同时给出按 token 加权的平均吞吐。非流式请求无法区分排队与生成，
// 不参与。模型需同时满足隐私阈值（独立账户数）与最少样本数。
func (s *Service) Speed(ctx context.Context, p Period, limit int) (*SpeedRanking, error) {
	limit = min(max(limit, 1), 100)
	snap, err := s.catalog.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("rankings: load catalog: %w", err)
	}
	cur, prev := windowFor(p, s.now())
	data, err := s.load(ctx, cur, prev, false, metricTokens)
	if err != nil {
		return nil, err
	}
	r := newResolver(snap)
	accounts, vms, _ := aggregate(r, data.accounts[0])

	spd := reqlog.HistogramColumns("spd")
	sums := make([]string, len(spd))
	for i, c := range spd {
		sums[i] = "sum(" + c + ")"
	}
	rows, err := s.pool.Query(ctx,
		`SELECT model_key, sum(speed_requests), sum(speed_output_tokens), sum(speed_gen_ms), `+strings.Join(sums, ", ")+`
		 FROM public_model_usage_daily WHERE day >= $1 AND day < $2 GROUP BY model_key`,
		cur.From.Format(time.DateOnly), cur.To.Format(time.DateOnly))
	if err != nil {
		return nil, fmt.Errorf("rankings: query speed: %w", err)
	}
	defer rows.Close()
	type speedAgg struct {
		requests, tokens, genMs int64
		hist                    []int64
	}
	byModel := map[int64]*speedAgg{}
	for rows.Next() {
		var key string
		a := speedAgg{hist: make([]int64, len(spd))}
		dest := []any{&key, &a.requests, &a.tokens, &a.genMs}
		for i := range a.hist {
			dest = append(dest, &a.hist[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("rankings: scan speed: %w", err)
		}
		vm := r.resolve(key)
		if vm == nil {
			continue
		}
		g := byModel[vm.ID]
		if g == nil {
			g = &speedAgg{hist: make([]int64, len(spd))}
			byModel[vm.ID] = g
		}
		g.requests, g.tokens, g.genMs = g.requests+a.requests, g.tokens+a.tokens, g.genMs+a.genMs
		for i, n := range a.hist {
			g.hist[i] += n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var list []SpeedRankingEntry
	for id, g := range byModel {
		if g.requests < minSpeedSamples || g.genMs <= 0 || len(accounts[id]) < s.opts.MinDistinctAccounts {
			continue
		}
		vm := vms[id]
		mean := float64(g.tokens) * 1000 / float64(g.genMs)
		p50, ok := histogramMedian(g.hist, reqlog.SpeedBucketBounds)
		if !ok { // 00028 之前物化的历史数据没有直方图：退回加权平均
			p50 = mean
		}
		list = append(list, SpeedRankingEntry{
			Model: vm.Name, DisplayName: displayName(vm), Author: catalog.AuthorOf(vm.Name), ProviderDisplay: providerDisplay(vm),
			Deprecated: vm.Status == "deprecated", TokensPerSecond: p50, MeanTokensPerSecond: mean,
		})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].TokensPerSecond != list[j].TokensPerSecond {
			return list[i].TokensPerSecond > list[j].TokensPerSecond
		}
		return list[i].Model < list[j].Model
	})
	if len(list) > limit {
		list = list[:limit]
	}
	for i := range list {
		list[i].Rank = i + 1
	}
	if list == nil {
		list = []SpeedRankingEntry{}
	}
	return &SpeedRanking{Period: p, From: cur.From.Format(time.DateOnly), To: cur.To.Format(time.DateOnly), TZ: Location.String(),
		Models: list, UpdatedAt: data.updatedAt, Methodology: s.methodology()}, nil
}

// histogramMedian 用分桶计数估算中位数：找到累计计数跨过一半的桶，在桶内线性插值；
// 落在最后一个（无上界）桶时返回它的下界。没有任何计数时 ok=false。
func histogramMedian(counts []int64, bounds []int64) (float64, bool) {
	var total int64
	for _, n := range counts {
		total += n
	}
	if total == 0 {
		return 0, false
	}
	target := float64(total) / 2
	var cum float64
	lower := 0.0
	for i, n := range counts {
		if i >= len(bounds) {
			return lower, true
		}
		upper := float64(bounds[i])
		if n > 0 && cum+float64(n) >= target {
			return lower + (target-cum)/float64(n)*(upper-lower), true
		}
		cum += float64(n)
		lower = upper
	}
	return lower, true
}

// AppRankingEntry 是应用榜的一行。AppURL 只有 scheme://host，可能为空。
type AppRankingEntry struct {
	Rank    int      `json:"rank"`
	AppName string   `json:"app_name"`
	AppURL  string   `json:"app_url"`
	Tokens  *int64   `json:"tokens,omitempty"`
	Share   float64  `json:"share"`
	Change  *float64 `json:"change"`
}

// AppRanking 是 GET /v1/rankings/apps 的响应。
type AppRanking struct {
	Period      Period             `json:"period"`
	From        string             `json:"from"`
	To          string             `json:"to"`
	TZ          string             `json:"tz"`
	TotalTokens *int64             `json:"total_tokens,omitempty"`
	Apps        []AppRankingEntry  `json:"apps"`
	Others      RankingOthers      `json:"others"`
	UpdatedAt   *time.Time         `json:"updated_at"`
	Methodology RankingMethodology `json:"methodology"`
}

// AppRule 是运营维护的应用规则（public_app_rules）：block 不上榜；merge 并入 MergeInto；
// rename（以及带 DisplayName 的 merge/block）覆盖展示名。
type AppRule struct {
	Action      string
	MergeInto   string
	DisplayName string
}

func (s *Service) appRules(ctx context.Context) (map[string]AppRule, error) {
	rows, err := s.pool.Query(ctx, `SELECT app_key, action, COALESCE(merge_into, ''), COALESCE(display_name, '') FROM public_app_rules`)
	if err != nil {
		return nil, fmt.Errorf("rankings: query app rules: %w", err)
	}
	defer rows.Close()
	rules := map[string]AppRule{}
	for rows.Next() {
		var key string
		var r AppRule
		if err := rows.Scan(&key, &r.Action, &r.MergeInto, &r.DisplayName); err != nil {
			return nil, fmt.Errorf("rankings: scan app rule: %w", err)
		}
		rules[key] = r
	}
	return rules, rows.Err()
}

// canonicalApp 按规则把 app_key 归并到最终的 key（最多跟随 5 层 merge，防止配置成环）；
// blocked 为 true 表示该应用被屏蔽。
func canonicalApp(rules map[string]AppRule, key string) (string, bool) {
	for i := 0; i < 5; i++ {
		r, ok := rules[key]
		if !ok {
			return key, false
		}
		switch r.Action {
		case "block":
			return key, true
		case "merge":
			key = r.MergeInto
		default:
			return key, false
		}
	}
	return key, false
}

// Apps 计算应用 token 榜，口径同模型榜（内部账户排除、独立账户阈值、单账户上限）。
// 只包含主动声明了 X-Title 的请求；同一域名（HTTP-Referer）下的请求归为同一应用。
func (s *Service) Apps(ctx context.Context, p Period, limit int) (*AppRanking, error) {
	limit = min(max(limit, 1), 100)
	cur, prev := windowFor(p, s.now())
	rules, err := s.appRules(ctx)
	if err != nil {
		return nil, err
	}
	curFrom := cur.From.Format(time.DateOnly)
	var accts [2]map[string]map[int64]int64
	accts[0], accts[1] = map[string]map[int64]int64{}, map[string]map[int64]int64{}
	blocked := int64(0)
	rows, err := s.pool.Query(ctx,
		`SELECT day, app_key, account_id, tokens FROM public_app_account_daily WHERE day >= $1 AND day < $2`,
		prev.From.Format(time.DateOnly), cur.To.Format(time.DateOnly))
	if err != nil {
		return nil, fmt.Errorf("rankings: query app daily: %w", err)
	}
	for rows.Next() {
		var day time.Time
		var key string
		var acct, tokens int64
		if err := rows.Scan(&day, &key, &acct, &tokens); err != nil {
			rows.Close()
			return nil, fmt.Errorf("rankings: scan app daily: %w", err)
		}
		w := 1
		if day.Format(time.DateOnly) >= curFrom {
			w = 0
		}
		key, isBlocked := canonicalApp(rules, key)
		if isBlocked {
			if w == 0 {
				blocked += tokens
			}
			continue
		}
		m := accts[w][key]
		if m == nil {
			m = map[int64]int64{}
			accts[w][key] = m
		}
		m[acct] += tokens
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 展示名 / 地址：取本期该 key 下请求最多的那一天的名称；合并目标自己的名称优先于被并入的别名。
	type label struct {
		name, url string
		own       bool
		requests  int64
	}
	labels := map[string]label{}
	rows, err = s.pool.Query(ctx,
		`SELECT app_key, app_name, app_url, requests, updated_at FROM public_app_usage_daily WHERE day >= $1 AND day < $2`,
		curFrom, cur.To.Format(time.DateOnly))
	if err != nil {
		return nil, fmt.Errorf("rankings: query app names: %w", err)
	}
	var updatedAt *time.Time
	for rows.Next() {
		var key, name, url string
		var requests int64
		var at time.Time
		if err := rows.Scan(&key, &name, &url, &requests, &at); err != nil {
			rows.Close()
			return nil, fmt.Errorf("rankings: scan app names: %w", err)
		}
		if updatedAt == nil || at.After(*updatedAt) {
			updatedAt = &at
		}
		canon, _ := canonicalApp(rules, key)
		own := canon == key
		if l, ok := labels[canon]; !ok || (own && !l.own) || (own == l.own && requests > l.requests) {
			labels[canon] = label{name: name, url: url, own: own, requests: requests}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	type row struct {
		key            string
		counted, prevC int64
	}
	var total, others int64 = blocked, blocked
	var list []row
	for key, a := range accts[0] {
		_, counted := capped(a, s.opts.MaxAccountShare)
		total += counted
		if len(a) < s.opts.MinDistinctAccounts {
			others += counted
			continue
		}
		_, prevCounted := capped(accts[1][key], s.opts.MaxAccountShare)
		list = append(list, row{key: key, counted: counted, prevC: prevCounted})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].counted != list[j].counted {
			return list[i].counted > list[j].counted
		}
		return list[i].key < list[j].key
	})
	if len(list) > limit {
		for _, rw := range list[limit:] {
			others += rw.counted
		}
		list = list[:limit]
	}
	resp := &AppRanking{
		Period: p, From: curFrom, To: cur.To.Format(time.DateOnly), TZ: Location.String(),
		TotalTokens: s.abs(total), Apps: make([]AppRankingEntry, 0, len(list)),
		Others: RankingOthers{Tokens: s.abs(others), Share: share(others, total)}, UpdatedAt: updatedAt,
		Methodology: s.methodology(),
	}
	for i, rw := range list {
		l := labels[rw.key]
		name, url := l.name, l.url
		if name == "" { // 理论上不会发生：账户表与名称表同一事务写入
			name = strings.TrimPrefix(strings.TrimPrefix(rw.key, "name:"), "url:")
		}
		if r, ok := rules[rw.key]; ok && r.DisplayName != "" {
			name = r.DisplayName
		}
		if url == "" && strings.HasPrefix(rw.key, "url:") {
			url = strings.TrimPrefix(rw.key, "url:")
		}
		resp.Apps = append(resp.Apps, AppRankingEntry{Rank: i + 1, AppName: name, AppURL: url, Tokens: s.abs(rw.counted),
			Share: share(rw.counted, total), Change: change(rw.counted, rw.prevC)})
	}
	return resp, nil
}
