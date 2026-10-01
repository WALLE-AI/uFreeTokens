package rankings

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/WALLE-AI/uFreeTokens/internal/catalog"
)

// Options 是公开榜单的口径参数。
type Options struct {
	// MinDistinctAccounts：统计期内独立账户数低于它的模型/作者不单独上榜（避免从
	// 榜单反推单个客户的用量），归入"其他"。
	MinDistinctAccounts int
	// MaxAccountShare：单个账户在某模型上的计入量上限（占该模型当期原始总量的比例）。
	MaxAccountShare float64
	// ShowAbsolute 为 false 时响应里不带绝对 token 数，只有份额与排名（冷启动期
	// 平台绝对量小，公开绝对值意义不大且容易被反推）。
	ShowAbsolute bool
}

// DefaultOptions 与方案 §3.4、§8.2 一致。
func DefaultOptions() Options {
	return Options{MinDistinctAccounts: 3, MaxAccountShare: 0.2}
}

// ErrInvalidPeriod 表示 period 参数不合法。
var ErrInvalidPeriod = errors.New("rankings: period must be one of day, week, month")

// Period 是统计期。窗口由完整自然日组成，截止到"今天 0 点"（不含今天）：今天还没
// 过完，和上一期的完整一天比环比没有意义。
type Period string

const (
	PeriodDay   Period = "day"
	PeriodWeek  Period = "week"
	PeriodMonth Period = "month"
)

func (p Period) days() int {
	switch p {
	case PeriodDay:
		return 1
	case PeriodWeek:
		return 7
	case PeriodMonth:
		return 30
	}
	return 0
}

// ParsePeriod 解析 ?period=，空字符串默认 week。
func ParsePeriod(s string) (Period, error) {
	if s == "" {
		return PeriodWeek, nil
	}
	p := Period(s)
	if p.days() == 0 {
		return "", ErrInvalidPeriod
	}
	return p, nil
}

// Window 返回统计期 [From, To)（Location 时区的自然日）。
type Window struct{ From, To time.Time }

func windowFor(p Period, now time.Time) (cur, prev Window) {
	to := Day(now)
	from := to.AddDate(0, 0, -p.days())
	return Window{from, to}, Window{from.AddDate(0, 0, -p.days()), from}
}

// RankingMethodology 随每个响应返回，前端"统计方法"区块据此展示真实口径。
type RankingMethodology struct {
	MinDistinctAccounts int     `json:"min_distinct_accounts"`
	MaxAccountShare     float64 `json:"max_account_share"`
	ExcludesInternal    bool    `json:"excludes_internal"`
	SuccessOnly         bool    `json:"success_only"`
	ShowsAbsolute       bool    `json:"shows_absolute"`
}

// RankingSeriesPoint 是趋势图上的一天。Share 是该模型当天占全平台公开 token 的比例。
type RankingSeriesPoint struct {
	Day    string  `json:"day"`
	Tokens *int64  `json:"tokens,omitempty"`
	Share  float64 `json:"share"`
}

// ModelRankingEntry 是模型榜的一行。Tokens 是计入排名的 token 数（已应用单账户上限），
// ShowAbsolute=false 时省略。Change 是与上一期相比的环比（本期/上期 − 1），上一期
// 为 0 时为 null。
type ModelRankingEntry struct {
	Rank            int                  `json:"rank"`
	Model           string               `json:"model"`
	DisplayName     string               `json:"display_name"`
	Author          string               `json:"author"`
	ProviderDisplay string               `json:"provider_display,omitempty"`
	Deprecated      bool                 `json:"deprecated"`
	Tokens          *int64               `json:"tokens,omitempty"`
	Share           float64              `json:"share"`
	Change          *float64             `json:"change"`
	Series          []RankingSeriesPoint `json:"series,omitempty"`
}

// RankingOthers 汇总没有单独上榜的部分（未达隐私阈值、非公开模型、超出 limit 的模型）。
type RankingOthers struct {
	Tokens *int64  `json:"tokens,omitempty"`
	Share  float64 `json:"share"`
}

// ModelRanking 是 GET /v1/rankings/models 的响应。
type ModelRanking struct {
	Period      Period              `json:"period"`
	From        string              `json:"from"`
	To          string              `json:"to"` // 不含
	TZ          string              `json:"tz"`
	TotalTokens *int64              `json:"total_tokens,omitempty"`
	Models      []ModelRankingEntry `json:"models"`
	Others      RankingOthers       `json:"others"`
	UpdatedAt   *time.Time          `json:"updated_at"`
	Methodology RankingMethodology  `json:"methodology"`
}

// AuthorRankingEntry 是厂商份额榜的一行。
type AuthorRankingEntry struct {
	Rank            int      `json:"rank"`
	Author          string   `json:"author"`
	ProviderDisplay string   `json:"provider_display"`
	Tokens          *int64   `json:"tokens,omitempty"`
	Share           float64  `json:"share"`
	Change          *float64 `json:"change"`
	Models          int      `json:"models"`
}

// AuthorRanking 是 GET /v1/rankings/authors 的响应。
type AuthorRanking struct {
	Period      Period               `json:"period"`
	From        string               `json:"from"`
	To          string               `json:"to"`
	TZ          string               `json:"tz"`
	TotalTokens *int64               `json:"total_tokens,omitempty"`
	Authors     []AuthorRankingEntry `json:"authors"`
	Others      RankingOthers        `json:"others"`
	UpdatedAt   *time.Time           `json:"updated_at"`
	Methodology RankingMethodology   `json:"methodology"`
}

// Service 读物化表并按公开口径计算榜单。不做缓存——网关的公开接口层统一缓存响应。
type Service struct {
	pool    *pgxpool.Pool
	catalog *catalog.Store
	opts    Options
	now     func() time.Time
}

func NewService(pool *pgxpool.Pool, cat *catalog.Store, opts Options) *Service {
	return &Service{pool: pool, catalog: cat, opts: opts, now: time.Now}
}

// SetClock 供测试固定"现在"。
func (s *Service) SetClock(now func() time.Time) { s.now = now }

func (s *Service) methodology() RankingMethodology {
	return RankingMethodology{MinDistinctAccounts: s.opts.MinDistinctAccounts, MaxAccountShare: s.opts.MaxAccountShare,
		ExcludesInternal: true, SuccessOnly: true, ShowsAbsolute: s.opts.ShowAbsolute}
}

// ---------- 数据装载与口径计算 ----------

// displayName / providerDisplay 取当前 catalog 的运营元数据（模型改名后展示新名称）。
func displayName(vm *catalog.VirtualModel) string {
	if vm.Metadata != nil && vm.Metadata.DisplayName != "" {
		return vm.Metadata.DisplayName
	}
	return vm.Name
}

func providerDisplay(vm *catalog.VirtualModel) string {
	if vm.Metadata != nil {
		return vm.Metadata.ProviderDisplay
	}
	return ""
}

// resolver 把物化表的 model_key（'id:<id>' / 'name:<name>'）映射到当前 catalog 里对
// free 等级可见的模型；映射不到的（hidden、非 free 可见、已删除）返回 nil。
type resolver struct {
	byID   map[int64]*catalog.VirtualModel
	byName map[string]*catalog.VirtualModel
}

func newResolver(snap *catalog.Snapshot) resolver {
	r := resolver{byID: map[int64]*catalog.VirtualModel{}, byName: map[string]*catalog.VirtualModel{}}
	for _, set := range []map[string]*catalog.VirtualModel{snap.Models, snap.DeprecatedModels} {
		for _, vm := range set {
			if !visibleToFree(vm) {
				continue
			}
			r.byID[vm.ID] = vm
			r.byName[vm.Name] = vm
		}
	}
	return r
}

func visibleToFree(vm *catalog.VirtualModel) bool {
	for _, t := range vm.VisibleTiers {
		if t == "free" {
			return true
		}
	}
	return false
}

func (r resolver) resolve(modelKey string) *catalog.VirtualModel {
	var id int64
	if _, err := fmt.Sscanf(modelKey, "id:%d", &id); err == nil {
		return r.byID[id]
	}
	if len(modelKey) > 5 && modelKey[:5] == "name:" {
		return r.byName[modelKey[5:]]
	}
	return nil
}

// usageData 是两个统计期内（本期 + 上期）物化表的原始数据。
type usageData struct {
	// accounts[window][model_key][account_id] = tokens；window 0 = 本期，1 = 上期。
	accounts [2]map[string]map[int64]int64
	// daily[model_key][day] = tokens（本期，用于趋势序列）。
	daily     map[string]map[string]int64
	updatedAt *time.Time
}

// metric 是账户分布表里参与排名的列：tokens（全部）、tool_tokens（有工具调用的请求）、
// image_tokens（含图片输入的请求）。由调用方传常量，不来自用户输入。
type metric string

const (
	metricTokens metric = "tokens"
	metricTools  metric = "tool_tokens"
	metricImages metric = "image_tokens"
)

func (s *Service) load(ctx context.Context, cur, prev Window, withDaily bool, m metric) (*usageData, error) {
	d := &usageData{accounts: [2]map[string]map[int64]int64{{}, {}}, daily: map[string]map[string]int64{}}
	rows, err := s.pool.Query(ctx,
		`SELECT day, model_key, account_id, `+string(m)+` FROM public_model_account_daily
		 WHERE day >= $1 AND day < $2 AND `+string(m)+` > 0`,
		prev.From.Format(time.DateOnly), cur.To.Format(time.DateOnly))
	if err != nil {
		return nil, fmt.Errorf("rankings: query account daily: %w", err)
	}
	defer rows.Close()
	curFrom := cur.From.Format(time.DateOnly)
	for rows.Next() {
		var day time.Time
		var key string
		var acct, tokens int64
		if err := rows.Scan(&day, &key, &acct, &tokens); err != nil {
			return nil, fmt.Errorf("rankings: scan account daily: %w", err)
		}
		w := 1
		if day.Format(time.DateOnly) >= curFrom {
			w = 0
		}
		m := d.accounts[w][key]
		if m == nil {
			m = map[int64]int64{}
			d.accounts[w][key] = m
		}
		m[acct] += tokens
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if withDaily {
		rows, err := s.pool.Query(ctx,
			`SELECT day, model_key, input_tokens + output_tokens FROM public_model_usage_daily WHERE day >= $1 AND day < $2`,
			curFrom, cur.To.Format(time.DateOnly))
		if err != nil {
			return nil, fmt.Errorf("rankings: query model daily: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var day time.Time
			var key string
			var tokens int64
			if err := rows.Scan(&day, &key, &tokens); err != nil {
				return nil, fmt.Errorf("rankings: scan model daily: %w", err)
			}
			m := d.daily[key]
			if m == nil {
				m = map[string]int64{}
				d.daily[key] = m
			}
			m[day.Format(time.DateOnly)] += tokens
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	var updated *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT max(updated_at) FROM public_model_usage_daily WHERE day < $1`,
		cur.To.Format(time.DateOnly)).Scan(&updated); err != nil {
		return nil, fmt.Errorf("rankings: query updated_at: %w", err)
	}
	d.updatedAt = updated
	return d, nil
}

// modelStat 是一个公开模型在某统计期的口径结果。
type modelStat struct {
	raw, counted int64
	accounts     map[int64]int64
}

// aggregate 把 model_key 归并到公开模型上（同一模型可能同时有 'id:' 与 'name:' 两种键），
// 返回 vm.ID → 账户分布；映射不到公开模型的 token 计入 hiddenTokens。
func aggregate(r resolver, byKey map[string]map[int64]int64) (perModel map[int64]map[int64]int64, models map[int64]*catalog.VirtualModel, hiddenTokens int64) {
	perModel, models = map[int64]map[int64]int64{}, map[int64]*catalog.VirtualModel{}
	for key, accts := range byKey {
		vm := r.resolve(key)
		if vm == nil {
			for _, t := range accts {
				hiddenTokens += t
			}
			continue
		}
		models[vm.ID] = vm
		m := perModel[vm.ID]
		if m == nil {
			m = map[int64]int64{}
			perModel[vm.ID] = m
		}
		for a, t := range accts {
			m[a] += t
		}
	}
	return perModel, models, hiddenTokens
}

// capped 计算应用单账户上限后的计入量：每个账户最多计入 maxShare × 原始总量。
func capped(accts map[int64]int64, maxShare float64) (raw, counted int64) {
	for _, t := range accts {
		raw += t
	}
	if maxShare <= 0 || maxShare >= 1 {
		return raw, raw
	}
	limit := int64(maxShare * float64(raw))
	for _, t := range accts {
		counted += min(t, limit)
	}
	return raw, counted
}

func change(cur, prev int64) *float64 {
	if prev <= 0 {
		return nil
	}
	v := float64(cur)/float64(prev) - 1
	return &v
}

func share(part, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(part) / float64(total)
}

func (s *Service) abs(v int64) *int64 {
	if !s.opts.ShowAbsolute {
		return nil
	}
	return &v
}

// Models 计算模型 token 用量榜。limit 取 1..100；withSeries 为 true 时每行带本期每日序列。
func (s *Service) Models(ctx context.Context, p Period, limit int, withSeries bool) (*ModelRanking, error) {
	return s.models(ctx, p, limit, withSeries, metricTokens)
}

// Tools 是工具调用榜：只统计响应里有工具调用的成功请求的 token，口径同 Models。
func (s *Service) Tools(ctx context.Context, p Period, limit int) (*ModelRanking, error) {
	return s.models(ctx, p, limit, false, metricTools)
}

// Multimodal 是多模态（图片输入）榜：只统计请求里含图片输入的成功请求的 token。
func (s *Service) Multimodal(ctx context.Context, p Period, limit int) (*ModelRanking, error) {
	return s.models(ctx, p, limit, false, metricImages)
}

func (s *Service) models(ctx context.Context, p Period, limit int, withSeries bool, m metric) (*ModelRanking, error) {
	limit = min(max(limit, 1), 100)
	snap, err := s.catalog.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("rankings: load catalog: %w", err)
	}
	cur, prev := windowFor(p, s.now())
	data, err := s.load(ctx, cur, prev, withSeries, m)
	if err != nil {
		return nil, err
	}
	r := newResolver(snap)
	curModels, vms, hidden := aggregate(r, data.accounts[0])
	prevModels, _, _ := aggregate(r, data.accounts[1])

	type row struct {
		vm             *catalog.VirtualModel
		counted, prevC int64
		raw            int64
	}
	var total, others int64 = hidden, hidden
	var rows []row
	for id, accts := range curModels {
		raw, counted := capped(accts, s.opts.MaxAccountShare)
		total += counted
		if len(accts) < s.opts.MinDistinctAccounts {
			others += counted
			continue
		}
		_, prevCounted := capped(prevModels[id], s.opts.MaxAccountShare)
		rows = append(rows, row{vm: vms[id], counted: counted, prevC: prevCounted, raw: raw})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].counted != rows[j].counted {
			return rows[i].counted > rows[j].counted
		}
		return rows[i].vm.Name < rows[j].vm.Name
	})
	if len(rows) > limit {
		for _, rw := range rows[limit:] {
			others += rw.counted
		}
		rows = rows[:limit]
	}

	// 每日序列：模型当天原始 token × 本期计入比例（单账户上限按期计算，序列按同一比例缩放，
	// 保证序列之和与榜单数值一致）；份额的分母是当天全平台公开 token（含"其他"）。
	var days []string
	dayTotals := map[string]int64{}
	if withSeries {
		for d := cur.From; d.Before(cur.To); d = d.AddDate(0, 0, 1) {
			days = append(days, d.Format(time.DateOnly))
		}
		for _, perDay := range data.daily {
			for day, t := range perDay {
				dayTotals[day] += t
			}
		}
	}
	keysByModel := map[int64][]string{}
	if withSeries {
		for key := range data.daily {
			if vm := r.resolve(key); vm != nil {
				keysByModel[vm.ID] = append(keysByModel[vm.ID], key)
			}
		}
	}

	resp := &ModelRanking{
		Period: p, From: cur.From.Format(time.DateOnly), To: cur.To.Format(time.DateOnly), TZ: Location.String(),
		TotalTokens: s.abs(total), Models: make([]ModelRankingEntry, 0, len(rows)),
		Others: RankingOthers{Tokens: s.abs(others), Share: share(others, total)}, UpdatedAt: data.updatedAt,
		Methodology: s.methodology(),
	}
	for i, rw := range rows {
		e := ModelRankingEntry{
			Rank: i + 1, Model: rw.vm.Name, DisplayName: displayName(rw.vm), Author: catalog.AuthorOf(rw.vm.Name),
			ProviderDisplay: providerDisplay(rw.vm), Deprecated: rw.vm.Status == "deprecated",
			Tokens: s.abs(rw.counted), Share: share(rw.counted, total), Change: change(rw.counted, rw.prevC),
		}
		if withSeries {
			ratio := share(rw.counted, rw.raw)
			e.Series = make([]RankingSeriesPoint, 0, len(days))
			for _, day := range days {
				var t int64
				for _, key := range keysByModel[rw.vm.ID] {
					t += data.daily[key][day]
				}
				scaled := int64(float64(t) * ratio)
				e.Series = append(e.Series, RankingSeriesPoint{Day: day, Tokens: s.abs(scaled), Share: share(scaled, dayTotals[day])})
			}
		}
		resp.Models = append(resp.Models, e)
	}
	return resp, nil
}

// maxAuthors 是厂商份额榜单独展示的数量，其余合并为"其他"（方案 §3.4：Top 9 + 其他）。
const maxAuthors = 9

// Authors 计算模型作者（厂商）的 token 份额榜。
func (s *Service) Authors(ctx context.Context, p Period) (*AuthorRanking, error) {
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

	type agg struct {
		author          string
		providerDisplay string
		topModelTokens  int64
		counted, prevC  int64
		accounts        map[int64]bool
		models          int
	}
	byAuthor := map[string]*agg{}
	get := func(vm *catalog.VirtualModel) *agg {
		a := catalog.AuthorOf(vm.Name)
		g := byAuthor[a]
		if g == nil {
			g = &agg{author: a, accounts: map[int64]bool{}}
			byAuthor[a] = g
		}
		return g
	}
	curModels, vms, hidden := aggregate(r, data.accounts[0])
	for id, accts := range curModels {
		_, counted := capped(accts, s.opts.MaxAccountShare)
		g := get(vms[id])
		g.counted += counted
		g.models++
		for a := range accts {
			g.accounts[a] = true
		}
		// 厂商展示名取该厂商用量最大的模型上运营录入的 provider_display。
		if pd := providerDisplay(vms[id]); pd != "" && counted >= g.topModelTokens {
			g.providerDisplay, g.topModelTokens = pd, counted
		}
	}
	prevModels, prevVMs, _ := aggregate(r, data.accounts[1])
	for id, accts := range prevModels {
		_, counted := capped(accts, s.opts.MaxAccountShare)
		if g := byAuthor[catalog.AuthorOf(prevVMs[id].Name)]; g != nil {
			g.prevC += counted
		}
	}

	var total, others int64 = hidden, hidden
	var list []*agg
	for _, g := range byAuthor {
		total += g.counted
		if len(g.accounts) < s.opts.MinDistinctAccounts {
			others += g.counted
			continue
		}
		list = append(list, g)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].counted != list[j].counted {
			return list[i].counted > list[j].counted
		}
		return list[i].author < list[j].author
	})
	if len(list) > maxAuthors {
		for _, g := range list[maxAuthors:] {
			others += g.counted
		}
		list = list[:maxAuthors]
	}
	resp := &AuthorRanking{
		Period: p, From: cur.From.Format(time.DateOnly), To: cur.To.Format(time.DateOnly), TZ: Location.String(),
		TotalTokens: s.abs(total), Authors: make([]AuthorRankingEntry, 0, len(list)),
		Others: RankingOthers{Tokens: s.abs(others), Share: share(others, total)}, UpdatedAt: data.updatedAt,
		Methodology: s.methodology(),
	}
	for i, g := range list {
		pd := g.providerDisplay
		if pd == "" {
			pd = g.author
		}
		resp.Authors = append(resp.Authors, AuthorRankingEntry{
			Rank: i + 1, Author: g.author, ProviderDisplay: pd, Tokens: s.abs(g.counted),
			Share: share(g.counted, total), Change: change(g.counted, g.prevC), Models: g.models,
		})
	}
	return resp, nil
}
