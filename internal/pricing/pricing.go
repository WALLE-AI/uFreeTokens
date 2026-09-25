// Package pricing 是纯计算层：给定价格分量与用量，算出费用。不做任何 IO。
// 对应技术方案 §6.4、§7.9.2。所有金额输入/输出单位为"微元"（1 元 = 1_000_000）。
package pricing

import (
	"time"

	"github.com/shopspring/decimal"
)

// million 用于把 "元/百万 token" 的单价换算成每 token 单价。
var million = decimal.NewFromInt(1_000_000)

// Meter 与 migrations/00004_pricing.sql 中 price_components.meter 的取值一致。
type Meter string

const (
	MeterInput           Meter = "input"
	MeterInputCacheRead  Meter = "input_cache_read"
	MeterInputCacheWrite Meter = "input_cache_write"
	MeterOutput          Meter = "output"
	MeterOutputReasoning Meter = "output_reasoning"
	MeterRequest         Meter = "request" // 按次计费（如联网搜索）
)

// Unit 与 price_components.unit 的取值一致。
type Unit string

const (
	UnitPer1MTokens Unit = "per_1m_tokens"
	UnitPerRequest  Unit = "per_request"
	UnitPerImage    Unit = "per_image"
	UnitPerSecond   Unit = "per_second"
)

// Component 对应一条 price_components 记录。
type Component struct {
	Meter          Meter
	Unit           Unit
	ServiceTier    string // "" 或 "default" 表示默认档
	TierMinInput   int
	TierMaxInput   *int   // nil = 无上限
	WindowStartMin *int16 // UTC 当日分钟数，nil = 全天生效
	WindowEndMin   *int16
	UnitPrice      decimal.Decimal
}

// Book 是某个价格版本下的全部计量分量（对应一条 price_books + 其下的 price_components）。
type Book struct {
	Components []Component
}

// Usage 是一次请求的用量，字段含义见技术方案 §7.4 中 schema.Usage 的适配器映射说明。
type Usage struct {
	InputTokens      int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	OutputTokens     int64
	ReasoningTokens  int64
	RequestCount     int64 // 通常为 0 或 1，用于按次计费的 meter（如 request_web_search）
}

// meterQuantity 把 Usage 拆成 (meter, 数量) 序列。ReasoningTokens 只有在价格表里
// 显式配置了 output_reasoning 分量时才会被单独计价；否则按 OutputTokens 计入 output
// （即推理 token 已包含在 output 里，不重复计费——由调用方保证 Usage.OutputTokens
// 的口径与是否存在 output_reasoning 分量匹配，适配器层负责，见 §7.4）。
func meterQuantities(u Usage) []struct {
	Meter Meter
	Qty   int64
} {
	return []struct {
		Meter Meter
		Qty   int64
	}{
		{MeterInput, u.InputTokens},
		{MeterInputCacheRead, u.CacheReadTokens},
		{MeterInputCacheWrite, u.CacheWriteTokens},
		{MeterOutput, u.OutputTokens},
		{MeterOutputReasoning, u.ReasoningTokens},
		{MeterRequest, u.RequestCount},
	}
}

// RoundingMode 决定 Charge 最终把小数金额转换成整数微元时的取整方向。
// 技术方案 §7.9.2 规定默认"向上取整"，因为这是要收取用户的钱，向下取整
// 会导致平台长期系统性少收（对应支出侧则相反，见 CostRoundFloor）。
type RoundingMode int

const (
	RoundCeil RoundingMode = iota
	RoundFloor
)

// Charge 计算一次请求的费用（微元，向上取整，一次性对总额取整而非逐项取整——
// 逐项取整会在多计量项场景下产生累积误差）。at 用于匹配按时段计价的分量
// （闲时折扣等），inputTokensForTier 用于匹配按上下文长度分档的分量。
// serviceTier 为空时按 "default" 处理。
func Charge(book Book, u Usage, serviceTier string, at time.Time, mode RoundingMode) (amountMicro int64, matched bool) {
	if serviceTier == "" {
		serviceTier = "default"
	}
	inputTokensForTier := int(u.InputTokens + u.CacheReadTokens + u.CacheWriteTokens)
	minuteOfDay := int16(at.UTC().Hour()*60 + at.UTC().Minute())

	total := decimal.Zero
	anyMatched := false

	for _, mq := range meterQuantities(u) {
		if mq.Qty == 0 {
			continue
		}
		c, ok := selectComponent(book.Components, mq.Meter, serviceTier, inputTokensForTier, minuteOfDay)
		if !ok {
			// 该 meter 在价格表中没有对应分量：说明这个计量项对该虚拟模型/渠道不计价
			// （例如缓存写入未单独计价），跳过，不视为错误。
			continue
		}
		anyMatched = true
		qty := decimal.NewFromInt(mq.Qty)

		switch c.Unit {
		case UnitPer1MTokens:
			total = total.Add(c.UnitPrice.Mul(qty).Div(million))
		case UnitPerRequest, UnitPerImage, UnitPerSecond:
			total = total.Add(c.UnitPrice.Mul(qty))
		}
	}

	return roundMicro(total, mode), anyMatched
}

// selectComponent 按"服务等级精确匹配 → 时段匹配 → 分档匹配"的优先级选出唯一分量
// （见技术方案 §6.4 price_components 表注释）。同一 meter 下如有多个候选，
// 优先选服务等级精确匹配、再选分档下限最大（离当前用量最近）的一条。
func selectComponent(components []Component, meter Meter, serviceTier string, inputTokens int, minuteOfDay int16) (Component, bool) {
	var best Component
	found := false

	for _, c := range components {
		if c.Meter != meter {
			continue
		}
		tier := c.ServiceTier
		if tier == "" {
			tier = "default"
		}
		if tier != serviceTier {
			continue
		}
		if inputTokens < c.TierMinInput {
			continue
		}
		if c.TierMaxInput != nil && inputTokens >= *c.TierMaxInput {
			continue
		}
		if !windowMatches(c.WindowStartMin, c.WindowEndMin, minuteOfDay) {
			continue
		}
		if !found || c.TierMinInput > best.TierMinInput {
			best = c
			found = true
		}
	}
	return best, found
}

// windowMatches 判断 minuteOfDay 是否落在 [start, end) 内，支持跨零点
// （如 start=1600 end=0 表示 16:00 到次日 00:00... 实际语义按各来源定义，
// 这里统一按 "start <= end 视为不跨零点，否则跨零点" 处理）。
func windowMatches(start, end *int16, minuteOfDay int16) bool {
	if start == nil || end == nil {
		return true // 未配置时段 = 全天生效
	}
	s, e := *start, *end
	if s == e {
		return true // 0..1440 全天的一种表达方式
	}
	if s < e {
		return minuteOfDay >= s && minuteOfDay < e
	}
	// 跨零点：例如 s=1320(22:00) e=480(08:00)
	return minuteOfDay >= s || minuteOfDay < e
}

func roundMicro(total decimal.Decimal, mode RoundingMode) int64 {
	// total 单位是"元"，微元 = 元 × 1_000_000。
	micro := total.Mul(million)
	switch mode {
	case RoundFloor:
		return micro.Floor().IntPart()
	default:
		return micro.Ceil().IntPart()
	}
}
