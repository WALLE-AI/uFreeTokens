package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// 外部数据采集的运营接口（docs/外部数据采集模块（价格情报与评测榜单）技术方案.md §3.3、§4.4）：
// 数据源"立即运行"与运行历史、评测榜单的模型名映射工作台、比价看板。调度与抓取本身在
// internal/datasync / pricesync / offers / benchsync，由 worker 执行。

var (
	ErrModelAliasNotFound = errors.New("admin: model alias not found")
)

// RunPriceSourceNow 把来源的 next_run_at 置为现在：worker 下一轮（≤1 分钟）就会执行它。
// 停用的来源不会被调度，需要先启用。
func (s *Service) RunPriceSourceNow(ctx context.Context, id int64) (*PriceSourceInfo, error) {
	var enabled bool
	err := s.db(ctx).QueryRow(ctx, `UPDATE price_sources SET next_run_at = now() WHERE id = $1 RETURNING enabled`, id).Scan(&enabled)
	if isNoRows(err) {
		return nil, ErrPriceSourceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("admin: schedule price source: %w", err)
	}
	if !enabled {
		return nil, invalid("source is disabled; enable it first")
	}
	return s.GetPriceSource(ctx, id)
}

// DataSourceRun 是一次采集执行记录。
type DataSourceRun struct {
	ID           int64           `json:"id"`
	SourceID     int64           `json:"source_id"`
	StartedAt    time.Time       `json:"started_at"`
	FinishedAt   *time.Time      `json:"finished_at"`
	Status       string          `json:"status"`
	ItemsFetched *int            `json:"items_fetched"`
	ItemsChanged *int            `json:"items_changed"`
	Error        *string         `json:"error"`
	Detail       json.RawMessage `json:"detail"`
}

// ListDataSourceRuns 返回某来源最近的执行记录（新的在前）。
func (s *Service) ListDataSourceRuns(ctx context.Context, sourceID int64, limit int) ([]DataSourceRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if _, err := s.GetPriceSource(ctx, sourceID); err != nil {
		return nil, err
	}
	rows, err := s.db(ctx).Query(ctx,
		`SELECT id, source_id, started_at, finished_at, status, items_fetched, items_changed, error, detail
		 FROM data_source_runs WHERE source_id = $1 ORDER BY started_at DESC, id DESC LIMIT $2`, sourceID, limit)
	if err != nil {
		return nil, fmt.Errorf("admin: list data source runs: %w", err)
	}
	defer rows.Close()
	out := []DataSourceRun{}
	for rows.Next() {
		var r DataSourceRun
		var detail []byte
		if err := rows.Scan(&r.ID, &r.SourceID, &r.StartedAt, &r.FinishedAt, &r.Status, &r.ItemsFetched, &r.ItemsChanged, &r.Error, &detail); err != nil {
			return nil, fmt.Errorf("admin: scan data source run: %w", err)
		}
		if len(detail) > 0 {
			r.Detail = detail
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---------- 模型名映射 ----------

// ModelAliasStatuses 与 model_aliases.status 一致。
var ModelAliasStatuses = []string{"auto", "suggested", "confirmed", "ignored", "unmatched"}

// ModelAlias 是榜单模型名的一条映射。status=suggested 时 VirtualModelID 是模糊匹配给出的候选，
// 尚未生效；auto / confirmed 才会关联到榜单结果。
type ModelAlias struct {
	Namespace      string     `json:"namespace"`
	ExternalLabel  string     `json:"external_label"`
	VirtualModelID *int64     `json:"virtual_model_id"`
	VirtualModel   *string    `json:"virtual_model"`
	Status         string     `json:"status"`
	Method         string     `json:"method"`
	Confidence     *float64   `json:"confidence"`
	Variant        *string    `json:"variant"`
	SeenCount      int        `json:"seen_count"`
	FirstSeenAt    time.Time  `json:"first_seen_at"`
	LastSeenAt     time.Time  `json:"last_seen_at"`
	DecidedByName  *string    `json:"decided_by_name"`
	DecidedAt      *time.Time `json:"decided_at"`
}

type ListModelAliasesInput struct {
	Namespace string
	Status    string
	Query     string
	Limit     int
	Offset    int
}

// ListModelAliases 分页列出映射；默认按"最近出现"排序，方便处理新上榜的模型。
func (s *Service) ListModelAliases(ctx context.Context, in ListModelAliasesInput) ([]ModelAlias, int, error) {
	if in.Status != "" {
		if err := oneOf("status", in.Status, ModelAliasStatuses...); err != nil {
			return nil, 0, err
		}
	}
	if in.Limit <= 0 || in.Limit > 500 {
		in.Limit = 100
	}
	where := `WHERE ($1 = '' OR a.namespace = $1) AND ($2 = '' OR a.status = $2)
		AND ($3 = '' OR a.external_label ILIKE '%' || $3 || '%' OR vm.name ILIKE '%' || $3 || '%')`
	args := []any{in.Namespace, in.Status, strings.TrimSpace(in.Query)}
	var total int
	if err := s.db(ctx).QueryRow(ctx,
		`SELECT count(*) FROM model_aliases a LEFT JOIN virtual_models vm ON vm.id = a.virtual_model_id `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("admin: count model aliases: %w", err)
	}
	rows, err := s.db(ctx).Query(ctx,
		`SELECT a.namespace, a.external_label, a.virtual_model_id, vm.name, a.status, a.method, a.confidence::float8, a.variant,
		   a.seen_count, a.first_seen_at, a.last_seen_at, a.decided_by_name, a.decided_at
		 FROM model_aliases a LEFT JOIN virtual_models vm ON vm.id = a.virtual_model_id `+where+`
		 ORDER BY a.last_seen_at DESC, a.namespace, a.external_label LIMIT $4 OFFSET $5`, append(args, in.Limit, in.Offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("admin: list model aliases: %w", err)
	}
	defer rows.Close()
	out := []ModelAlias{}
	for rows.Next() {
		var a ModelAlias
		if err := rows.Scan(&a.Namespace, &a.ExternalLabel, &a.VirtualModelID, &a.VirtualModel, &a.Status, &a.Method, &a.Confidence,
			&a.Variant, &a.SeenCount, &a.FirstSeenAt, &a.LastSeenAt, &a.DecidedByName, &a.DecidedAt); err != nil {
			return nil, 0, fmt.Errorf("admin: scan model alias: %w", err)
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}

// ModelAliasNamespaces 返回已有的命名空间（下拉框用）。
func (s *Service) ModelAliasNamespaces(ctx context.Context) ([]string, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT DISTINCT namespace FROM model_aliases ORDER BY 1`)
	if err != nil {
		return nil, fmt.Errorf("admin: list alias namespaces: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var ns string
		if err := rows.Scan(&ns); err != nil {
			return nil, err
		}
		out = append(out, ns)
	}
	return out, rows.Err()
}

type SetModelAliasInput struct {
	Namespace     string `json:"namespace"`
	ExternalLabel string `json:"external_label"`
	// Status：confirmed（关联到 VirtualModelID / VirtualModel）、ignored（确认平台没有这个模型）、
	// auto（撤销人工决定，交回自动匹配，下次导入时重新匹配）。
	Status         string  `json:"status"`
	VirtualModelID *int64  `json:"virtual_model_id"`
	VirtualModel   *string `json:"virtual_model"`
}

// SetModelAliasResult 汇总一次人工映射的影响。
type SetModelAliasResult struct {
	Alias           *ModelAlias `json:"alias"`
	RelinkedResults int64       `json:"relinked_results"`
	ReprojectedRuns int         `json:"reprojected_runs"`
}

// SetModelAlias 人工确认 / 忽略一条映射，并立即把已导入的榜单结果重新关联（同命名空间、同模型名），
// 已发布的受影响 run 重新投影 scores。整体在一个事务里。
func (s *Service) SetModelAlias(ctx context.Context, in SetModelAliasInput, by string) (*SetModelAliasResult, error) {
	in.Namespace, in.ExternalLabel = strings.TrimSpace(in.Namespace), strings.TrimSpace(in.ExternalLabel)
	if in.Namespace == "" || in.ExternalLabel == "" {
		return nil, invalid("namespace and external_label are required")
	}
	if err := oneOf("status", in.Status, "confirmed", "ignored", "auto"); err != nil {
		return nil, err
	}
	var vmID *int64
	if in.Status == "confirmed" {
		switch {
		case in.VirtualModelID != nil:
			if _, err := s.GetVirtualModel(ctx, *in.VirtualModelID); err != nil {
				return nil, err
			}
			vmID = in.VirtualModelID
		case in.VirtualModel != nil && strings.TrimSpace(*in.VirtualModel) != "":
			vm, err := s.GetVirtualModelByName(ctx, strings.TrimSpace(*in.VirtualModel))
			if err != nil {
				return nil, err
			}
			vmID = &vm.ID
		default:
			return nil, invalid("virtual_model_id or virtual_model is required when confirming")
		}
	}
	res := &SetModelAliasResult{}
	err := s.RunInTx(ctx, func(ctx context.Context) error {
		method := "manual"
		if in.Status == "auto" {
			method = "none"
		}
		status := in.Status
		if status == "auto" {
			status = "unmatched" // 交回自动匹配：下次导入时重新匹配并覆盖
		}
		if _, err := s.db(ctx).Exec(ctx,
			`INSERT INTO model_aliases (namespace, external_label, virtual_model_id, status, method, decided_by_name, decided_at)
			 VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), now())
			 ON CONFLICT (namespace, external_label) DO UPDATE SET virtual_model_id = EXCLUDED.virtual_model_id,
			   status = EXCLUDED.status, method = EXCLUDED.method, confidence = NULL,
			   decided_by_name = EXCLUDED.decided_by_name, decided_at = now()`,
			in.Namespace, in.ExternalLabel, vmID, status, method, by); err != nil {
			return fmt.Errorf("admin: upsert model alias: %w", err)
		}
		if in.Status == "auto" {
			return nil // 已导入的结果保持原样，等下次导入重新匹配
		}
		tag, err := s.db(ctx).Exec(ctx,
			`UPDATE benchmark_results x SET virtual_model_id = $3
			 FROM benchmark_runs r JOIN benchmarks b ON b.id = r.benchmark_id
			 WHERE x.run_id = r.id AND b.alias_namespace = $1 AND x.model_label = $2
			   AND x.virtual_model_id IS DISTINCT FROM $3`, in.Namespace, in.ExternalLabel, vmID)
		if err != nil {
			return fmt.Errorf("admin: relink benchmark results: %w", err)
		}
		res.RelinkedResults = tag.RowsAffected()
		rows, err := s.db(ctx).Query(ctx,
			`SELECT r.id FROM benchmark_runs r JOIN benchmarks b ON b.id = r.benchmark_id
			 WHERE r.published AND b.alias_namespace = $1 AND b.score_key IS NOT NULL
			   AND EXISTS (SELECT 1 FROM benchmark_results x WHERE x.run_id = r.id AND x.model_label = $2)`, in.Namespace, in.ExternalLabel)
		if err != nil {
			return fmt.Errorf("admin: find affected runs: %w", err)
		}
		var runIDs []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			runIDs = append(runIDs, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, id := range runIDs {
			if _, err := s.ApplyScoreProjection(ctx, id); err != nil {
				return err
			}
		}
		res.ReprojectedRuns = len(runIDs)
		return nil
	})
	if err != nil {
		return nil, err
	}
	list, _, err := s.ListModelAliases(ctx, ListModelAliasesInput{Namespace: in.Namespace, Query: in.ExternalLabel, Limit: 500})
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ExternalLabel == in.ExternalLabel {
			res.Alias = &list[i]
		}
	}
	if res.Alias == nil {
		return nil, ErrModelAliasNotFound
	}
	return res, nil
}

// ---------- 比价看板 ----------

// MarketPrice 是某来源对某模型的最新报价（每百万 token）。
type MarketPrice struct {
	SourceID      int64            `json:"source_id"`
	SourceName    string           `json:"source_name"`
	Level         string           `json:"level"`
	UpstreamModel string           `json:"upstream_model"`
	Currency      string           `json:"currency"`
	Input         *decimal.Decimal `json:"input"`
	Output        *decimal.Decimal `json:"output"`
	InputCNY      *decimal.Decimal `json:"input_cny"`
	OutputCNY     *decimal.Decimal `json:"output_cny"`
	ObservedAt    time.Time        `json:"observed_at"`
}

// ChannelCost 是一个渠道当前生效的成本价。
type ChannelCost struct {
	ChannelID     int64            `json:"channel_id"`
	ProviderCode  string           `json:"provider_code"`
	UpstreamModel string           `json:"upstream_model"`
	Currency      *string          `json:"currency"`
	Input         *decimal.Decimal `json:"input"`
	Output        *decimal.Decimal `json:"output"`
	InputCNY      *decimal.Decimal `json:"input_cny"`
	OutputCNY     *decimal.Decimal `json:"output_cny"`
}

// PriceComparisonRow 是比价看板的一行（一个虚拟模型）。
type PriceComparisonRow struct {
	VirtualModelID int64            `json:"virtual_model_id"`
	VirtualModel   string           `json:"virtual_model"`
	SellCurrency   *string          `json:"sell_currency"`
	SellInput      *decimal.Decimal `json:"sell_input"`
	SellOutput     *decimal.Decimal `json:"sell_output"`
	Channels       []ChannelCost    `json:"channels"`
	Market         []MarketPrice    `json:"market"`
	// 售价相对最低成本价的毛利率（按 3:1 混合价，CNY 计）；缺数据为 null。
	MarginRatio *decimal.Decimal `json:"margin_ratio"`
	// 售价相对市场最低价的倍数（3:1 混合价，CNY 计）；>1 表示比市场最低价贵。
	VsMarketLowest *decimal.Decimal `json:"vs_market_lowest"`
}

type PriceComparisonInput struct {
	Query  string
	Limit  int
	Offset int
}

type priceRow struct {
	currency      string
	input, output *decimal.Decimal
}

// PriceComparison 汇总虚拟模型的售价、各渠道成本价与外部来源最近 30 天的最新报价。
// 外部报价按 upstream_model 匹配：完全相同，或形如 "厂商/模型" 的聚合来源写法。
func (s *Service) PriceComparison(ctx context.Context, in PriceComparisonInput) ([]PriceComparisonRow, int, error) {
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 50
	}
	q := strings.TrimSpace(in.Query)
	var total int
	if err := s.db(ctx).QueryRow(ctx,
		`SELECT count(*) FROM virtual_models WHERE status <> 'deprecated' AND ($1 = '' OR name ILIKE '%' || $1 || '%')`, q).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("admin: count price comparison: %w", err)
	}
	fx, err := s.fxRatesToCNY(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.db(ctx).Query(ctx,
		`SELECT vm.id, vm.name, sb.currency,
		   (SELECT unit_price FROM price_components c WHERE c.price_book_id = sb.id AND c.meter = 'input' AND c.service_tier = 'default' AND c.tier_min_input = 0 AND c.window_start_min IS NULL),
		   (SELECT unit_price FROM price_components c WHERE c.price_book_id = sb.id AND c.meter = 'output' AND c.service_tier = 'default' AND c.tier_min_input = 0 AND c.window_start_min IS NULL)
		 FROM virtual_models vm
		 LEFT JOIN LATERAL (
		   SELECT pb.id, pb.currency FROM price_books pb
		   WHERE pb.kind = 'sell' AND pb.virtual_model_id = vm.id AND pb.effective_from <= now()
		     AND (pb.effective_to IS NULL OR pb.effective_to > now())
		   ORDER BY (pb.tier IS NULL) DESC, pb.effective_from DESC LIMIT 1) sb ON true
		 WHERE vm.status <> 'deprecated' AND ($1 = '' OR vm.name ILIKE '%' || $1 || '%')
		 ORDER BY vm.name LIMIT $2 OFFSET $3`, q, in.Limit, in.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("admin: query price comparison: %w", err)
	}
	var out []PriceComparisonRow
	for rows.Next() {
		var r PriceComparisonRow
		if err := rows.Scan(&r.VirtualModelID, &r.VirtualModel, &r.SellCurrency, &r.SellInput, &r.SellOutput); err != nil {
			rows.Close()
			return nil, 0, fmt.Errorf("admin: scan price comparison: %w", err)
		}
		r.Channels, r.Market = []ChannelCost{}, []MarketPrice{}
		out = append(out, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	for i := range out {
		r := &out[i]
		chRows, err := s.db(ctx).Query(ctx,
			`SELECT c.id, p.code, c.upstream_model, cb.currency,
			   (SELECT unit_price FROM price_components x WHERE x.price_book_id = cb.id AND x.meter = 'input' AND x.service_tier = 'default' AND x.tier_min_input = 0 AND x.window_start_min IS NULL),
			   (SELECT unit_price FROM price_components x WHERE x.price_book_id = cb.id AND x.meter = 'output' AND x.service_tier = 'default' AND x.tier_min_input = 0 AND x.window_start_min IS NULL)
			 FROM channels c JOIN provider_accounts pa ON pa.id = c.provider_account_id JOIN providers p ON p.id = pa.provider_id
			 LEFT JOIN LATERAL (
			   SELECT pb.id, pb.currency FROM price_books pb WHERE pb.kind = 'cost' AND pb.channel_id = c.id AND pb.effective_from <= now()
			     AND (pb.effective_to IS NULL OR pb.effective_to > now()) ORDER BY pb.effective_from DESC LIMIT 1) cb ON true
			 WHERE c.virtual_model_id = $1 AND c.status = 'active' ORDER BY c.id`, r.VirtualModelID)
		if err != nil {
			return nil, 0, fmt.Errorf("admin: query channel costs: %w", err)
		}
		upstreams := map[string]bool{}
		for chRows.Next() {
			var c ChannelCost
			if err := chRows.Scan(&c.ChannelID, &c.ProviderCode, &c.UpstreamModel, &c.Currency, &c.Input, &c.Output); err != nil {
				chRows.Close()
				return nil, 0, fmt.Errorf("admin: scan channel cost: %w", err)
			}
			if c.Currency != nil {
				c.InputCNY, c.OutputCNY = toCNY(c.Input, *c.Currency, fx), toCNY(c.Output, *c.Currency, fx)
			}
			upstreams[c.UpstreamModel] = true
			r.Channels = append(r.Channels, c)
		}
		chRows.Close()
		if err := chRows.Err(); err != nil {
			return nil, 0, err
		}
		names := []string{r.VirtualModel}
		for u := range upstreams {
			names = append(names, u)
		}
		mRows, err := s.db(ctx).Query(ctx,
			`SELECT DISTINCT ON (o.source_id, o.upstream_model) o.source_id, ps.name, ps.level, o.upstream_model, o.spec, o.observed_at
			 FROM price_observations o JOIN price_sources ps ON ps.id = o.source_id
			 WHERE o.observed_at > now() - interval '30 days'
			   AND (o.upstream_model = ANY($1) OR split_part(o.upstream_model, '/', 2) = ANY($1))
			 ORDER BY o.source_id, o.upstream_model, o.observed_at DESC`, names)
		if err != nil {
			return nil, 0, fmt.Errorf("admin: query market prices: %w", err)
		}
		for mRows.Next() {
			var m MarketPrice
			var spec []byte
			if err := mRows.Scan(&m.SourceID, &m.SourceName, &m.Level, &m.UpstreamModel, &spec, &m.ObservedAt); err != nil {
				mRows.Close()
				return nil, 0, fmt.Errorf("admin: scan market price: %w", err)
			}
			pr := specInputOutput(spec)
			m.Currency, m.Input, m.Output = pr.currency, pr.input, pr.output
			m.InputCNY, m.OutputCNY = toCNY(m.Input, m.Currency, fx), toCNY(m.Output, m.Currency, fx)
			r.Market = append(r.Market, m)
		}
		mRows.Close()
		if err := mRows.Err(); err != nil {
			return nil, 0, err
		}

		var sellCNY *decimal.Decimal
		if r.SellCurrency != nil {
			sellCNY = blended(toCNY(r.SellInput, *r.SellCurrency, fx), toCNY(r.SellOutput, *r.SellCurrency, fx))
		}
		var minCost, minMarket *decimal.Decimal
		for _, c := range r.Channels {
			if b := blended(c.InputCNY, c.OutputCNY); b != nil && (minCost == nil || b.LessThan(*minCost)) {
				minCost = b
			}
		}
		for _, m := range r.Market {
			if b := blended(m.InputCNY, m.OutputCNY); b != nil && b.IsPositive() && (minMarket == nil || b.LessThan(*minMarket)) {
				minMarket = b
			}
		}
		if sellCNY != nil && sellCNY.IsPositive() && minCost != nil {
			v := sellCNY.Sub(*minCost).Div(*sellCNY).Round(4)
			r.MarginRatio = &v
		}
		if sellCNY != nil && minMarket != nil {
			v := sellCNY.Div(*minMarket).Round(4)
			r.VsMarketLowest = &v
		}
	}
	if out == nil {
		out = []PriceComparisonRow{}
	}
	return out, total, nil
}

// fxRatesToCNY 返回各币种到 CNY 的最新汇率（CNY 自身为 1）。
func (s *Service) fxRatesToCNY(ctx context.Context) (map[string]decimal.Decimal, error) {
	rows, err := s.db(ctx).Query(ctx,
		`SELECT DISTINCT ON (base) base, rate FROM fx_rates WHERE quote = 'CNY' ORDER BY base, effective_date DESC`)
	if err != nil {
		return nil, fmt.Errorf("admin: load fx rates: %w", err)
	}
	defer rows.Close()
	out := map[string]decimal.Decimal{"CNY": decimal.NewFromInt(1)}
	for rows.Next() {
		var base string
		var rate decimal.Decimal
		if err := rows.Scan(&base, &rate); err != nil {
			return nil, err
		}
		out[base] = rate
	}
	return out, rows.Err()
}

func toCNY(v *decimal.Decimal, currency string, fx map[string]decimal.Decimal) *decimal.Decimal {
	if v == nil {
		return nil
	}
	rate, ok := fx[currency]
	if !ok {
		return nil
	}
	x := v.Mul(rate).Round(6)
	return &x
}

func blended(in, out *decimal.Decimal) *decimal.Decimal {
	switch {
	case in != nil && out != nil:
		v := in.Mul(decimal.NewFromInt(3)).Add(*out).Div(decimal.NewFromInt(4))
		return &v
	case in != nil:
		return in
	case out != nil:
		return out
	}
	return nil
}

// specInputOutput 从 pricesync.PriceSpec 的 JSON 里取默认档的输入 / 输出单价（不 import pricesync，避免循环依赖）。
func specInputOutput(raw []byte) priceRow {
	var spec struct {
		Currency   string
		Components []struct {
			Meter          string
			Unit           string
			ServiceTier    string
			TierMinInput   int
			WindowStartMin *int16
			UnitPrice      decimal.Decimal
		}
	}
	var pr priceRow
	if err := json.Unmarshal(raw, &spec); err != nil {
		return pr
	}
	pr.currency = spec.Currency
	for _, c := range spec.Components {
		if (c.ServiceTier != "" && c.ServiceTier != "default") || c.TierMinInput != 0 || c.WindowStartMin != nil || c.Unit != "per_1m_tokens" {
			continue
		}
		v := c.UnitPrice
		switch c.Meter {
		case "input":
			pr.input = &v
		case "output":
			pr.output = &v
		}
	}
	return pr
}
