package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// 运营后台列表/详情里的"当前价格与毛利"（运营后台接口方案 §1.3、§1.4）。
//
// 口径与 internal/catalog 的快照加载器保持一致：
//   - 当前生效的价格版本 = 同一 channel（成本价）/ 同一 virtual model（售价）下
//     effective_from <= now() 且未过期的最新一个 price_book。注意售价**不区分
//     tier**——catalog 按 virtual_model_id 取最新一本，price_books.tier 目前在
//     运行时不参与计价，这里如实反映运行时行为，不假装有分档售价。
//   - 非 CNY 成本价按 fx_rates 里 quote='CNY'、effective_date <= 今天 的最新汇率折算，
//     再乘以 provider_accounts.cost_multiplier。
//   - 毛利率只看"基础"计量项：input / output（unit=per_1m_tokens）以及多模态计量项
//     image / input_char / audio_second / request（mediaMeterSQL），service_tier=default，
//     tier_min_input=0，不带时段窗口；售价与成本按 meter+unit 配对，取最差的一项
//     （保守口径，任一项亏钱都要暴露）。

// mediaMeterSQL 是参与毛利计算的非 token 计量项（多模态技术方案 §5），与迁移 00031
// 里 v_admin_channel_margin 的口径一致。
const mediaMeterSQL = `('image','input_char','audio_second','request')`

// PriceBrief 是列表里展示的价格摘要：基础 input/output 单价，以及多模态计量项。
type PriceBrief struct {
	PriceBookID   int64            `json:"price_book_id"`
	Currency      string           `json:"currency"`
	Input         *decimal.Decimal `json:"input"`
	Output        *decimal.Decimal `json:"output"`
	Media         []MeterPrice     `json:"media,omitempty"`
	EffectiveFrom time.Time        `json:"effective_from"`
}

// MeterPrice 是一个非 token 计量项的单价（如 image / per_image）。
type MeterPrice struct {
	Meter string          `json:"meter"`
	Unit  string          `json:"unit"`
	Price decimal.Decimal `json:"price"`
}

// CostCNY 是折算成人民币（含 cost_multiplier）后的成本单价。
type CostCNY struct {
	Input   *decimal.Decimal `json:"input"`
	Output  *decimal.Decimal `json:"output"`
	Media   []MeterPrice     `json:"media,omitempty"`
	FXRate  decimal.Decimal  `json:"fx_rate"`
	FXDate  *time.Time       `json:"fx_date"`
	Missing bool             `json:"fx_missing"` // 非 CNY 且没有可用汇率：无法折算
}

type fxRate struct {
	rate decimal.Decimal
	date time.Time
}

// priceContext 一次性加载一批渠道/虚拟模型当前生效的价格与汇率，供列表计算毛利。
type priceContext struct {
	costByChannel map[int64]*PriceBrief
	sellByVM      map[int64]*PriceBrief
	fx            map[string]fxRate
}

func (s *Service) loadPriceContext(ctx context.Context, channelIDs, vmIDs []int64) (*priceContext, error) {
	pc := &priceContext{costByChannel: map[int64]*PriceBrief{}, sellByVM: map[int64]*PriceBrief{}, fx: map[string]fxRate{}}
	var err error
	if len(channelIDs) > 0 {
		if pc.costByChannel, err = s.loadCurrentBriefs(ctx, "cost", "channel_id", channelIDs); err != nil {
			return nil, err
		}
	}
	if len(vmIDs) > 0 {
		if pc.sellByVM, err = s.loadCurrentBriefs(ctx, "sell", "virtual_model_id", vmIDs); err != nil {
			return nil, err
		}
	}
	rows, err := s.db(ctx).Query(ctx,
		`SELECT DISTINCT ON (base) base, rate, effective_date
		 FROM fx_rates WHERE quote = 'CNY' AND effective_date <= CURRENT_DATE
		 ORDER BY base, effective_date DESC`)
	if err != nil {
		return nil, fmt.Errorf("admin: query fx_rates: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var base string
		var r fxRate
		if err := rows.Scan(&base, &r.rate, &r.date); err != nil {
			return nil, fmt.Errorf("admin: scan fx_rate: %w", err)
		}
		pc.fx[base] = r
	}
	return pc, rows.Err()
}

// loadCurrentBriefs 取每个 key 当前生效的价格版本及其基础 input/output 单价。
func (s *Service) loadCurrentBriefs(ctx context.Context, kind, keyColumn string, ids []int64) (map[int64]*PriceBrief, error) {
	rows, err := s.db(ctx).Query(ctx, fmt.Sprintf(
		`WITH cur AS (
		   SELECT DISTINCT ON (pb.%[1]s) pb.id, pb.%[1]s AS key, pb.currency, pb.effective_from
		   FROM price_books pb
		   WHERE pb.kind = $1 AND pb.%[1]s = ANY($2)
		     AND pb.effective_from <= now() AND (pb.effective_to IS NULL OR pb.effective_to > now())
		   ORDER BY pb.%[1]s, pb.effective_from DESC
		 )
		 SELECT cur.key, cur.id, cur.currency, cur.effective_from,
		   (SELECT unit_price FROM price_components c WHERE c.price_book_id = cur.id AND c.meter = 'input'
		      AND c.unit = 'per_1m_tokens' AND c.service_tier = 'default' AND c.tier_min_input = 0 AND c.window_start_min IS NULL LIMIT 1),
		   (SELECT unit_price FROM price_components c WHERE c.price_book_id = cur.id AND c.meter = 'output'
		      AND c.unit = 'per_1m_tokens' AND c.service_tier = 'default' AND c.tier_min_input = 0 AND c.window_start_min IS NULL LIMIT 1),
		   (SELECT json_agg(json_build_object('meter', c.meter, 'unit', c.unit, 'price', c.unit_price) ORDER BY c.meter, c.unit)
		      FROM price_components c WHERE c.price_book_id = cur.id AND c.meter IN %[2]s
		      AND c.service_tier = 'default' AND c.tier_min_input = 0 AND c.window_start_min IS NULL)
		 FROM cur`, keyColumn, mediaMeterSQL),
		kind, ids)
	if err != nil {
		return nil, fmt.Errorf("admin: query current %s price books: %w", kind, err)
	}
	defer rows.Close()
	out := map[int64]*PriceBrief{}
	for rows.Next() {
		var key int64
		var media []byte
		b := &PriceBrief{}
		if err := rows.Scan(&key, &b.PriceBookID, &b.Currency, &b.EffectiveFrom, &b.Input, &b.Output, &media); err != nil {
			return nil, fmt.Errorf("admin: scan price brief: %w", err)
		}
		if len(media) > 0 {
			if err := json.Unmarshal(media, &b.Media); err != nil {
				return nil, fmt.Errorf("admin: decode media prices: %w", err)
			}
		}
		out[key] = b
	}
	return out, rows.Err()
}

// costInCNY 把成本价折成人民币（含合同倍率）。
func (pc *priceContext) costInCNY(cost *PriceBrief, multiplier decimal.Decimal) *CostCNY {
	if cost == nil {
		return nil
	}
	out := &CostCNY{FXRate: decimal.NewFromInt(1)}
	if cost.Currency != "" && cost.Currency != "CNY" {
		r, ok := pc.fx[cost.Currency]
		if !ok {
			out.Missing = true
			return out
		}
		out.FXRate = r.rate
		d := r.date
		out.FXDate = &d
	}
	conv := func(p *decimal.Decimal) *decimal.Decimal {
		if p == nil {
			return nil
		}
		v := p.Mul(out.FXRate).Mul(multiplier).Round(6)
		return &v
	}
	out.Input, out.Output = conv(cost.Input), conv(cost.Output)
	for _, m := range cost.Media {
		out.Media = append(out.Media, MeterPrice{Meter: m.Meter, Unit: m.Unit, Price: *conv(&m.Price)})
	}
	return out
}

// marginRatio = min over {input, output, 配对的多模态计量项} of (1 - 成本CNY/售价)。都缺失返回 nil。
func marginRatio(sell *PriceBrief, cost *CostCNY) *decimal.Decimal {
	if sell == nil || cost == nil || cost.Missing || (sell.Currency != "" && sell.Currency != "CNY") {
		return nil
	}
	var worst *decimal.Decimal
	check := func(s, c *decimal.Decimal) {
		if s == nil || c == nil || s.IsZero() {
			return
		}
		m := decimal.NewFromInt(1).Sub(c.Div(*s)).Round(4)
		if worst == nil || m.LessThan(*worst) {
			worst = &m
		}
	}
	check(sell.Input, cost.Input)
	check(sell.Output, cost.Output)
	for _, sm := range sell.Media {
		for _, cm := range cost.Media {
			if sm.Meter == cm.Meter && sm.Unit == cm.Unit {
				sp, cp := sm.Price, cm.Price
				check(&sp, &cp)
			}
		}
	}
	return worst
}
