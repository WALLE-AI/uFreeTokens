package offers

import (
	"github.com/shopspring/decimal"
)

// PricePoint 是检测器需要的最小价格信息：默认档、输入长度 0 起步的输入 / 输出单价（每百万 token）。
// 缺失的计量项为 nil。
type PricePoint struct {
	Input  *decimal.Decimal
	Output *decimal.Decimal
}

// IsFree：输入、输出都有报价且都为 0。只有一项报价（如纯按次计费的模型）不算免费模型。
func (p PricePoint) IsFree() bool {
	return p.Input != nil && p.Output != nil && p.Input.IsZero() && p.Output.IsZero()
}

// Blended 返回 3:1 加权的混合单价（行业常用口径：输入 token 约为输出的 3 倍）；缺项时用另一项。
func (p PricePoint) Blended() (decimal.Decimal, bool) {
	switch {
	case p.Input != nil && p.Output != nil:
		return p.Input.Mul(decimal.NewFromInt(3)).Add(*p.Output).Div(decimal.NewFromInt(4)), true
	case p.Input != nil:
		return *p.Input, true
	case p.Output != nil:
		return *p.Output, true
	}
	return decimal.Zero, false
}

// PriceCutThreshold：新混合价 ≤ 旧混合价 × 0.8 才算降价情报（小幅波动 / 汇率噪音不报）。
var PriceCutThreshold = decimal.NewFromFloat(0.8)

// PriceObservation 是一个模型本次与上一次的价格。
type PriceObservation struct {
	Model    string
	Current  PricePoint
	Previous *PricePoint // nil = 第一次看到
}

// DetectFromPrices 从一批价格观测里识别免费模型与降价，返回待入库的候选。
// sourceID / providerCode / evidenceURL 原样写进候选；它是纯函数，方便单测。
func DetectFromPrices(sourceID int64, providerCode, evidenceURL string, obs []PriceObservation) []Candidate {
	var out []Candidate
	zero := decimal.Zero
	for _, o := range obs {
		sid := sourceID
		if o.Current.IsFree() {
			out = append(out, Candidate{
				SourceID: &sid, ProviderCode: providerCode, UpstreamModel: o.Model, OfferType: TypeFreeModel,
				DiscountRatio: &zero, EvidenceURL: evidenceURL, Detection: "structured",
				EvidenceExcerpt: "input=0, output=0 (per 1M tokens)",
			})
			continue
		}
		if o.Previous == nil || o.Previous.IsFree() {
			continue
		}
		cur, ok1 := o.Current.Blended()
		prev, ok2 := o.Previous.Blended()
		if !ok1 || !ok2 || prev.IsZero() || cur.IsZero() {
			continue
		}
		ratio := cur.Div(prev)
		if ratio.GreaterThan(PriceCutThreshold) {
			continue
		}
		r := ratio.Round(5)
		out = append(out, Candidate{
			SourceID: &sid, ProviderCode: providerCode, UpstreamModel: o.Model, OfferType: TypePriceCut,
			DiscountRatio: &r, EvidenceURL: evidenceURL, Detection: "price_diff",
			EvidenceExcerpt: "blended price (3:1) " + prev.StringFixed(4) + " -> " + cur.StringFixed(4) + " per 1M tokens",
		})
	}
	return out
}
