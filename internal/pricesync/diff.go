package pricesync

import (
	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
)

type Direction string

const (
	DirectionUp      Direction = "up"
	DirectionDown    Direction = "down"
	DirectionMixed   Direction = "mixed"
	DirectionNew     Direction = "new"
	DirectionRemoved Direction = "removed"
)

// ComponentDiff 是一个计量项的旧价/新价对比。OldPrice==nil 表示这是新增的计量项；
// NewPrice==nil 表示这个计量项在提案里消失了（不代表整个模型下线，见 ChangeDiff
// 的 Direction 说明）。
type ComponentDiff struct {
	Meter        pricing.Meter
	ServiceTier  string
	TierMinInput int
	OldPrice     *decimal.Decimal
	NewPrice     *decimal.Decimal
	ChangeRatio  *decimal.Decimal // (new-old)/old；只有当新旧价格都存在且旧价非零时才有值
}

type ChangeDiff struct {
	Components []ComponentDiff
	// Direction 是新旧价格集合之间的整体方向：
	//   new     = 之前完全没有价格（新渠道/新模型）
	//   removed = 提案里一个计量项都没有（来源认为这个模型已经没有价格信息了）
	//   up/down = 所有发生了实际变化的重叠计量项都同向变化
	//   mixed   = 涨跌互现，或者只有新增/移除计量项而没有任何重叠项发生变化
	//             （保守起见走人工审批，而不是擅自归到 up 或 down）
	Direction Direction
	// MaxChangeRatio 是所有重叠计量项里变化幅度绝对值最大的一个，供 Policy 判断
	// "涨价 <= 20%" 这类阈值（技术方案 §7.16.7）。
	MaxChangeRatio decimal.Decimal
}

// HasChanges 判断这个 diff 是不是"什么都没变"（比如重复抓到一模一样的价格）——
// 这种情况 Engine 不应该生成一条毫无意义的 change request。
func (d ChangeDiff) HasChanges() bool {
	for _, c := range d.Components {
		if c.OldPrice == nil || c.NewPrice == nil {
			return true // 新增或移除了计量项
		}
		if c.ChangeRatio != nil && !c.ChangeRatio.IsZero() {
			return true
		}
	}
	return false
}

// Diff 比较当前生效的成本价分量与提案的新 PriceSpec，产出逐计量项的对比和
// 整体方向判定（技术方案 §7.16.4 price_change_requests.diff / direction）。
func Diff(current []Component, proposed PriceSpec) ChangeDiff {
	curByKey := indexComponents(current)
	seen := make(map[string]bool, len(proposed.Components))

	var diffs []ComponentDiff
	increased, decreased := false, false
	maxRatio := decimal.Zero

	for _, c := range proposed.Components {
		key := componentKey(c.Meter, c.ServiceTier, c.TierMinInput)
		seen[key] = true
		newPrice := c.UnitPrice
		d := ComponentDiff{Meter: c.Meter, ServiceTier: tierOrDefault(c.ServiceTier), TierMinInput: c.TierMinInput, NewPrice: &newPrice}

		if old, ok := curByKey[key]; ok {
			oldPrice := old.UnitPrice
			d.OldPrice = &oldPrice
			if !oldPrice.IsZero() {
				ratio := newPrice.Sub(oldPrice).Div(oldPrice)
				d.ChangeRatio = &ratio
				switch {
				case ratio.IsPositive():
					increased = true
				case ratio.IsNegative():
					decreased = true
				}
				if abs := ratio.Abs(); abs.GreaterThan(maxRatio) {
					maxRatio = abs
				}
			} else if !newPrice.IsZero() {
				increased = true // 从 0（之前免费）变成收费，风险方向等同涨价
			}
		}
		diffs = append(diffs, d)
	}

	for key, old := range curByKey {
		if seen[key] {
			continue
		}
		oldPrice := old.UnitPrice
		diffs = append(diffs, ComponentDiff{
			Meter: old.Meter, ServiceTier: tierOrDefault(old.ServiceTier), TierMinInput: old.TierMinInput, OldPrice: &oldPrice,
		})
	}

	var direction Direction
	switch {
	case len(current) == 0:
		direction = DirectionNew
	case len(proposed.Components) == 0:
		direction = DirectionRemoved
	case increased && decreased:
		direction = DirectionMixed
	case increased:
		direction = DirectionUp
	case decreased:
		direction = DirectionDown
	default:
		direction = DirectionMixed
	}

	return ChangeDiff{Components: diffs, Direction: direction, MaxChangeRatio: maxRatio}
}
