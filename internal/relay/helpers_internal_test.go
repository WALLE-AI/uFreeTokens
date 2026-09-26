// computeCostAmount 是未导出函数，测试必须放在 package relay 内部（而不是
// 外部测试包 relay_test，见 relay_test.go 顶部注释）。这是本文件唯一存在的理由，
// 其它测试都应该继续放在 relay_test.go 里，走公开 API + 真实基础设施的路线。
package relay

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
	"github.com/WALLE-AI/uFreeTokens/internal/schema"
)

func TestComputeCostAmount_NoComponentsReturnsNil(t *testing.T) {
	got := computeCostAmount(pricing.Book{}, decimal.NewFromInt(1), schema.Usage{InputTokens: 1000}, nil)
	if got != nil {
		t.Errorf("computeCostAmount() = %v, want nil (no cost price configured)", got)
	}
}

func TestComputeCostAmount_NonCNYCurrencyWithoutFXRateReturnsNil(t *testing.T) {
	book := pricing.Book{
		Currency:   "USD",
		Components: []pricing.Component{{Meter: pricing.MeterInput, Unit: pricing.UnitPer1MTokens, UnitPrice: decimal.NewFromInt(1)}},
	}
	got := computeCostAmount(book, decimal.NewFromInt(1), schema.Usage{InputTokens: 1000}, nil)
	if got != nil {
		t.Errorf("computeCostAmount() = %v, want nil (no fx_rate for USD)", got)
	}
	// 有汇率表，但里面没有这个币种：同样应该是 nil，不能悄悄当成 1:1。
	got = computeCostAmount(book, decimal.NewFromInt(1), schema.Usage{InputTokens: 1000}, map[string]decimal.Decimal{"EUR": decimal.NewFromInt(8)})
	if got != nil {
		t.Errorf("computeCostAmount() = %v, want nil (fx_rates has EUR but not USD)", got)
	}
}

func TestComputeCostAmount_NonCNYCurrencyConvertsUsingFXRate(t *testing.T) {
	book := pricing.Book{
		Currency:   "USD",
		Components: []pricing.Component{{Meter: pricing.MeterInput, Unit: pricing.UnitPer1MTokens, UnitPrice: decimal.NewFromInt(1)}},
	}
	// 1,000,000 input token × 1 美元/百万 = 1 美元 = 1,000,000 微美元；汇率 7.2 -> 7,200,000 微元。
	got := computeCostAmount(book, decimal.NewFromInt(1), schema.Usage{InputTokens: 1_000_000}, map[string]decimal.Decimal{"USD": decimal.NewFromFloat(7.2)})
	if got == nil {
		t.Fatal("computeCostAmount() = nil, want a computed value")
	}
	if *got != 7_200_000 {
		t.Errorf("computeCostAmount() = %d, want 7200000", *got)
	}
}

func TestComputeCostAmount_ComputesAndAppliesMultiplier(t *testing.T) {
	book := pricing.Book{
		Currency:   "CNY",
		Components: []pricing.Component{{Meter: pricing.MeterInput, Unit: pricing.UnitPer1MTokens, UnitPrice: decimal.NewFromInt(10)}},
	}
	// 1,000,000 input token × 10 元/百万 = 10 元 = 10,000,000 微元，再乘以 0.8 折扣 = 8,000,000。
	got := computeCostAmount(book, decimal.NewFromFloat(0.8), schema.Usage{InputTokens: 1_000_000}, nil)
	if got == nil {
		t.Fatal("computeCostAmount() = nil, want a computed value")
	}
	if *got != 8_000_000 {
		t.Errorf("computeCostAmount() = %d, want 8_000_000", *got)
	}
}

func TestComputeCostAmount_ZeroMultiplierMeansZeroCost(t *testing.T) {
	// cost_multiplier=0 是一个合法配置（比如整月免费的渠道），不应该被当成
	// "没设置"悄悄改回 1 倍——这是曾经真实写出来过的一个 bug。
	book := pricing.Book{
		Currency:   "CNY",
		Components: []pricing.Component{{Meter: pricing.MeterInput, Unit: pricing.UnitPer1MTokens, UnitPrice: decimal.NewFromInt(10)}},
	}
	got := computeCostAmount(book, decimal.NewFromInt(0), schema.Usage{InputTokens: 1_000_000}, nil)
	if got == nil {
		t.Fatal("computeCostAmount() = nil, want a computed zero value (not nil)")
	}
	if *got != 0 {
		t.Errorf("computeCostAmount() = %d, want 0", *got)
	}
}

func TestComputeCostAmount_NoMatchingMeterReturnsNil(t *testing.T) {
	// 成本价只配了 input，这次请求只有 output 用量：没有任何分量命中，
	// 返回 nil 而不是一个具有欺骗性的 0。
	book := pricing.Book{
		Currency:   "CNY",
		Components: []pricing.Component{{Meter: pricing.MeterInput, Unit: pricing.UnitPer1MTokens, UnitPrice: decimal.NewFromInt(10)}},
	}
	got := computeCostAmount(book, decimal.NewFromInt(1), schema.Usage{OutputTokens: 1000}, nil)
	if got != nil {
		t.Errorf("computeCostAmount() = %v, want nil (no meter matched this usage)", got)
	}
}
