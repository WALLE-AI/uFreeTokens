package catalog

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
)

func priceComponent(meter pricing.Meter, tier string, price float64) pricing.Component {
	return pricing.Component{Meter: meter, Unit: pricing.UnitPer1MTokens, ServiceTier: tier, UnitPrice: decimal.NewFromFloat(price)}
}

func TestHasNegativeMargin_SellBelowCost_True(t *testing.T) {
	sell := pricing.Book{Components: []pricing.Component{priceComponent(pricing.MeterInput, "", 5)}}
	cost := pricing.Book{Components: []pricing.Component{priceComponent(pricing.MeterInput, "", 8)}}
	if !hasNegativeMargin(sell, cost, decimal.NewFromInt(1), decimal.NewFromInt(1)) {
		t.Error("expected negative margin when sell price < cost price")
	}
}

func TestHasNegativeMargin_SellAboveCost_False(t *testing.T) {
	sell := pricing.Book{Components: []pricing.Component{priceComponent(pricing.MeterInput, "", 10)}}
	cost := pricing.Book{Components: []pricing.Component{priceComponent(pricing.MeterInput, "", 5)}}
	if hasNegativeMargin(sell, cost, decimal.NewFromInt(1), decimal.NewFromInt(1)) {
		t.Error("expected no negative margin when sell price > cost price")
	}
}

func TestHasNegativeMargin_EqualPrices_False(t *testing.T) {
	sell := pricing.Book{Components: []pricing.Component{priceComponent(pricing.MeterInput, "", 5)}}
	cost := pricing.Book{Components: []pricing.Component{priceComponent(pricing.MeterInput, "", 5)}}
	if hasNegativeMargin(sell, cost, decimal.NewFromInt(1), decimal.NewFromInt(1)) {
		t.Error("equal sell and cost price is break-even, not negative margin")
	}
}

func TestHasNegativeMargin_OnlyOneMeterNegative_True(t *testing.T) {
	sell := pricing.Book{Components: []pricing.Component{
		priceComponent(pricing.MeterInput, "", 10),
		priceComponent(pricing.MeterOutput, "", 5), // output 亏钱，input 不亏
	}}
	cost := pricing.Book{Components: []pricing.Component{
		priceComponent(pricing.MeterInput, "", 5),
		priceComponent(pricing.MeterOutput, "", 8),
	}}
	if !hasNegativeMargin(sell, cost, decimal.NewFromInt(1), decimal.NewFromInt(1)) {
		t.Error("expected negative margin when any single meter is underwater")
	}
}

func TestHasNegativeMargin_NoOverlapMeter_False(t *testing.T) {
	sell := pricing.Book{Components: []pricing.Component{priceComponent(pricing.MeterInput, "", 1)}}
	cost := pricing.Book{Components: []pricing.Component{priceComponent(pricing.MeterOutput, "", 100)}}
	if hasNegativeMargin(sell, cost, decimal.NewFromInt(1), decimal.NewFromInt(1)) {
		t.Error("no comparable meter between sell and cost, should not be judged negative")
	}
}

func TestHasNegativeMargin_AppliesFXRateAndCostMultiplier(t *testing.T) {
	sell := pricing.Book{Components: []pricing.Component{priceComponent(pricing.MeterInput, "", 10)}} // 10 CNY
	cost := pricing.Book{Components: []pricing.Component{priceComponent(pricing.MeterInput, "", 1)}}  // 1 USD
	// 1 USD * fx 7.2 = 7.2 CNY，还没亏；乘上 2 倍的合同折扣（cost_multiplier=2）变成 14.4，亏了。
	if hasNegativeMargin(sell, cost, decimal.NewFromFloat(7.2), decimal.NewFromInt(1)) {
		t.Error("1 USD * 7.2 fx = 7.2 CNY < 10 CNY sell price, should not be negative yet")
	}
	if !hasNegativeMargin(sell, cost, decimal.NewFromFloat(7.2), decimal.NewFromInt(2)) {
		t.Error("1 USD * 7.2 fx * 2x cost_multiplier = 14.4 CNY > 10 CNY sell price, should be negative")
	}
}

func TestComputeNegativeMargins_SkipsChannelsWithoutSellOrCostPrice(t *testing.T) {
	snap := &Snapshot{
		Models:           map[string]*VirtualModel{},
		ChannelsByVM:     map[int64][]*Channel{1: {{ID: 1, VirtualModelID: 1, ProviderAccountID: 1}}},
		ProviderAccounts: map[int64]*ProviderAccount{1: {ID: 1, CostMultiplier: decimal.NewFromInt(1)}},
		SellPriceBooks:   map[int64]pricing.Book{},
		CostPriceBooks:   map[int64]pricing.Book{},
		FXRates:          map[string]decimal.Decimal{},
	}
	computeNegativeMargins(snap)
	if snap.ChannelsByVM[1][0].NegativeMargin {
		t.Error("a channel with no sell or cost price configured must not be flagged negative-margin")
	}
}

func TestComputeNegativeMargins_SkipsWhenFXRateMissing(t *testing.T) {
	ch := &Channel{ID: 1, VirtualModelID: 1, ProviderAccountID: 1}
	snap := &Snapshot{
		Models:           map[string]*VirtualModel{},
		ChannelsByVM:     map[int64][]*Channel{1: {ch}},
		ProviderAccounts: map[int64]*ProviderAccount{1: {ID: 1, CostMultiplier: decimal.NewFromInt(1)}},
		SellPriceBooks:   map[int64]pricing.Book{1: {Components: []pricing.Component{priceComponent(pricing.MeterInput, "", 1)}}},
		CostPriceBooks:   map[int64]pricing.Book{1: {Currency: "USD", Components: []pricing.Component{priceComponent(pricing.MeterInput, "", 100)}}},
		FXRates:          map[string]decimal.Decimal{}, // 没有 USD 汇率
	}
	computeNegativeMargins(snap)
	if ch.NegativeMargin {
		t.Error("without an FX rate to convert USD cost, the channel must not be judged (missing data isn't the same as losing money)")
	}
}
