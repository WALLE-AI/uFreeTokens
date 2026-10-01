package admin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/store"
	"github.com/shopspring/decimal"
)

// 售价/毛利预览与目录计数（B7），把原来散落在前端的浮点计算收回服务端，
// 口径与 pricectx.go 的列表毛利完全一致（decimal 计算，售价保留 4 位小数，
// 毛利率保留 4 位小数，取 input/output 中较差的一项）。

type PricingPreviewItem struct {
	Key        string           `json:"key"`
	CostInput  *decimal.Decimal `json:"cost_input"`
	CostOutput *decimal.Decimal `json:"cost_output"`
	// SellInput/SellOutput 非空时视为手工指定的售价，只计算毛利；为空时按 markup 自动计算。
	SellInput     *decimal.Decimal `json:"sell_input"`
	SellOutput    *decimal.Decimal `json:"sell_output"`
	MarkupPercent *decimal.Decimal `json:"markup_percent"` // 覆盖全局 markup
}

type PricingPreviewInput struct {
	Currency       string               `json:"currency"`        // 成本价币种，默认 USD
	CostMultiplier *decimal.Decimal     `json:"cost_multiplier"` // 默认 1
	MarkupPercent  decimal.Decimal      `json:"markup_percent"`  // 全局加价百分比
	Items          []PricingPreviewItem `json:"items"`
}

type PricingPreviewResultItem struct {
	Key            string           `json:"key"`
	CostInputCNY   *decimal.Decimal `json:"cost_input_cny"`
	CostOutputCNY  *decimal.Decimal `json:"cost_output_cny"`
	SellInput      *decimal.Decimal `json:"sell_input"`
	SellOutput     *decimal.Decimal `json:"sell_output"`
	MarginRatio    *decimal.Decimal `json:"margin_ratio"`
	NegativeMargin bool             `json:"negative_margin"`
}

type PricingPreviewResult struct {
	Currency  string                     `json:"currency"`
	FXRate    *decimal.Decimal           `json:"fx_rate"`
	FXDate    *time.Time                 `json:"fx_date"`
	FXMissing bool                       `json:"fx_missing"`
	Items     []PricingPreviewResultItem `json:"items"`
}

const maxPreviewItems = 500

// SellFromCost 按 markup 百分比从人民币成本算售价（保留 4 位小数），供预览与
// 批量导入共用，保证两边口径一致。
func SellFromCost(costCNY decimal.Decimal, markupPercent decimal.Decimal) decimal.Decimal {
	return costCNY.Mul(decimal.NewFromInt(1).Add(markupPercent.Div(decimal.NewFromInt(100)))).Round(4)
}

// latestFXToCNY 取 base→CNY 当前生效的最新汇率；base 为 CNY 时返回 1。
func (s *Service) latestFXToCNY(ctx context.Context, q store.Querier, base string) (*decimal.Decimal, *time.Time, error) {
	if base == "CNY" {
		one := decimal.NewFromInt(1)
		return &one, nil, nil
	}
	var (
		rate decimal.Decimal
		date time.Time
	)
	err := q.QueryRow(ctx,
		`SELECT rate, effective_date FROM fx_rates WHERE base = $1 AND quote = 'CNY' AND effective_date <= CURRENT_DATE
		 ORDER BY effective_date DESC LIMIT 1`, base).Scan(&rate, &date)
	if isNoRows(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("admin: query fx rate: %w", err)
	}
	return &rate, &date, nil
}

func (s *Service) PricingPreview(ctx context.Context, in PricingPreviewInput) (*PricingPreviewResult, error) {
	cur := strings.ToUpper(strings.TrimSpace(in.Currency))
	if cur == "" {
		cur = "USD"
	}
	if len(in.Items) == 0 || len(in.Items) > maxPreviewItems {
		return nil, invalid("items must contain 1-%d entries", maxPreviewItems)
	}
	mult := decimal.NewFromInt(1)
	if in.CostMultiplier != nil {
		if !in.CostMultiplier.IsPositive() {
			return nil, invalid("cost_multiplier must be > 0")
		}
		mult = *in.CostMultiplier
	}
	if in.MarkupPercent.IsNegative() || in.MarkupPercent.GreaterThan(decimal.NewFromInt(1000)) {
		return nil, invalid("markup_percent must be within [0, 1000]")
	}
	rate, date, err := s.latestFXToCNY(ctx, s.db(ctx), cur)
	if err != nil {
		return nil, err
	}
	out := &PricingPreviewResult{Currency: cur, FXRate: rate, FXDate: date, FXMissing: rate == nil, Items: make([]PricingPreviewResultItem, 0, len(in.Items))}
	for _, it := range in.Items {
		for _, p := range []*decimal.Decimal{it.CostInput, it.CostOutput, it.SellInput, it.SellOutput} {
			if p != nil && p.IsNegative() {
				return nil, invalid("prices must be >= 0 (item %q)", it.Key)
			}
		}
		markup := in.MarkupPercent
		if it.MarkupPercent != nil {
			if it.MarkupPercent.IsNegative() || it.MarkupPercent.GreaterThan(decimal.NewFromInt(1000)) {
				return nil, invalid("markup_percent must be within [0, 1000] (item %q)", it.Key)
			}
			markup = *it.MarkupPercent
		}
		res := PricingPreviewResultItem{Key: it.Key, SellInput: it.SellInput, SellOutput: it.SellOutput}
		conv := func(c *decimal.Decimal) *decimal.Decimal {
			if c == nil || rate == nil {
				return nil
			}
			v := c.Mul(*rate).Mul(mult).Round(6)
			return &v
		}
		res.CostInputCNY, res.CostOutputCNY = conv(it.CostInput), conv(it.CostOutput)
		auto := func(c *decimal.Decimal) *decimal.Decimal {
			if c == nil {
				return nil
			}
			v := SellFromCost(*c, markup)
			return &v
		}
		if res.SellInput == nil {
			res.SellInput = auto(res.CostInputCNY)
		}
		if res.SellOutput == nil {
			res.SellOutput = auto(res.CostOutputCNY)
		}
		res.MarginRatio = marginRatio(
			&PriceBrief{Currency: "CNY", Input: res.SellInput, Output: res.SellOutput},
			&CostCNY{Input: res.CostInputCNY, Output: res.CostOutputCNY, Missing: rate == nil})
		res.NegativeMargin = res.MarginRatio != nil && res.MarginRatio.IsNegative()
		out.Items = append(out.Items, res)
	}
	return out, nil
}

// CatalogCounts 是模型库/渠道列表顶部 KPI 需要的计数，一次请求算完（原来前端
// 为此各发 4 个 page_size=1 的列表请求）。
type CatalogCounts struct {
	Models struct {
		Total           int `json:"total"`
		MissingSell     int `json:"missing_sell_price"`
		MissingMetadata int `json:"missing_metadata"`
		NoActiveChannel int `json:"no_active_channel"`
		NegativeMargin  int `json:"negative_margin"`
	} `json:"models"`
	Channels struct {
		Total          int `json:"total"`
		Active         int `json:"active"`
		NegativeMargin int `json:"negative_margin"`
		MissingCost    int `json:"missing_cost"`
		Dedicated      int `json:"dedicated"`
	} `json:"channels"`
}

func (s *Service) CatalogCounts(ctx context.Context) (*CatalogCounts, error) {
	var out CatalogCounts
	m, c := &out.Models, &out.Channels
	if err := s.db(ctx).QueryRow(ctx,
		`SELECT count(*), count(*) FILTER (WHERE NOT has_sell_price), count(*) FILTER (WHERE NOT has_metadata),
		   count(*) FILTER (WHERE active_channel_count = 0), count(*) FILTER (WHERE min_margin_ratio < 0)
		 FROM v_admin_model_summary`,
	).Scan(&m.Total, &m.MissingSell, &m.MissingMetadata, &m.NoActiveChannel, &m.NegativeMargin); err != nil {
		return nil, fmt.Errorf("admin: count models: %w", err)
	}
	if err := s.db(ctx).QueryRow(ctx,
		`SELECT count(*), count(*) FILTER (WHERE ch.status = 'active'), count(*) FILTER (WHERE m.margin_ratio < 0),
		   count(*) FILTER (WHERE m.cost_book_id IS NULL), count(*) FILTER (WHERE cardinality(ch.allowed_account_ids) > 0)
		 FROM channels ch JOIN v_admin_channel_margin m ON m.channel_id = ch.id`,
	).Scan(&c.Total, &c.Active, &c.NegativeMargin, &c.MissingCost, &c.Dedicated); err != nil {
		return nil, fmt.Errorf("admin: count channels: %w", err)
	}
	return &out, nil
}
