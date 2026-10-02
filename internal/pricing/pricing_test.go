package pricing

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func dec(s string) decimal.Decimal {
	d, err := decimal.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return d
}

func intPtr16(v int16) *int16 { return &v }
func intPtr(v int) *int       { return &v }

// TestCharge_V1WorkedExample 复算技术方案原 V1 文档给出的例子：
// 输入 1250 token、输出 870 token，售价 4.635 / 18.54 元/百万 token。
// 1250×4.635/1e6 + 870×18.54/1e6 = 0.02192355 元 = 21923.55 微元，向上取整 21924。
func TestCharge_V1WorkedExample(t *testing.T) {
	book := Book{Components: []Component{
		{Meter: MeterInput, Unit: UnitPer1MTokens, ServiceTier: "default", UnitPrice: dec("4.635")},
		{Meter: MeterOutput, Unit: UnitPer1MTokens, ServiceTier: "default", UnitPrice: dec("18.54")},
	}}
	u := Usage{InputTokens: 1250, OutputTokens: 870}

	got, matched := Charge(book, u, "", time.Now(), RoundCeil)
	if !matched {
		t.Fatal("expected components to match")
	}
	if got != 21924 {
		t.Errorf("Charge() = %d micro-CNY, want 21924", got)
	}
}

func TestCharge_ZeroUsageIsZeroCost(t *testing.T) {
	book := Book{Components: []Component{
		{Meter: MeterInput, Unit: UnitPer1MTokens, UnitPrice: dec("4.635")},
		{Meter: MeterOutput, Unit: UnitPer1MTokens, UnitPrice: dec("18.54")},
	}}
	got, matched := Charge(book, Usage{}, "", time.Now(), RoundCeil)
	if matched {
		t.Error("expected no components matched for all-zero usage")
	}
	if got != 0 {
		t.Errorf("Charge() = %d, want 0", got)
	}
}

func TestCharge_CacheReadCheaperThanInput(t *testing.T) {
	book := Book{Components: []Component{
		{Meter: MeterInput, Unit: UnitPer1MTokens, UnitPrice: dec("10")},
		{Meter: MeterInputCacheRead, Unit: UnitPer1MTokens, UnitPrice: dec("1")}, // 10% of input
	}}
	// 100万 token 全部命中缓存 vs 全部未命中，费用应相差 10 倍
	cached, _ := Charge(book, Usage{CacheReadTokens: 1_000_000}, "", time.Now(), RoundCeil)
	uncached, _ := Charge(book, Usage{InputTokens: 1_000_000}, "", time.Now(), RoundCeil)
	if cached != 1_000_000 { // 1 元 = 1,000,000 微元
		t.Errorf("cached charge = %d, want 1_000_000", cached)
	}
	if uncached != 10_000_000 {
		t.Errorf("uncached charge = %d, want 10_000_000", uncached)
	}
}

func TestCharge_TieredByInputTokens(t *testing.T) {
	book := Book{Components: []Component{
		{Meter: MeterInput, Unit: UnitPer1MTokens, TierMinInput: 0, TierMaxInput: intPtr(200_000), UnitPrice: dec("1")},
		{Meter: MeterInput, Unit: UnitPer1MTokens, TierMinInput: 200_000, TierMaxInput: nil, UnitPrice: dec("2")},
	}}

	below, _ := Charge(book, Usage{InputTokens: 100_000}, "", time.Now(), RoundCeil)
	if below != 100_000 { // 100,000 × 1元/1e6 = 0.1元 = 100,000 微元
		t.Errorf("below-tier charge = %d, want 100000", below)
	}

	above, _ := Charge(book, Usage{InputTokens: 300_000}, "", time.Now(), RoundCeil)
	if above != 600_000 { // 300,000 × 2元/1e6 = 0.6元 = 600,000 微元
		t.Errorf("above-tier charge = %d, want 600000", above)
	}
}

func TestCharge_ServiceTierIsolation(t *testing.T) {
	book := Book{Components: []Component{
		{Meter: MeterInput, Unit: UnitPer1MTokens, ServiceTier: "default", UnitPrice: dec("10")},
		{Meter: MeterInput, Unit: UnitPer1MTokens, ServiceTier: "batch", UnitPrice: dec("5")},
	}}
	def, _ := Charge(book, Usage{InputTokens: 1_000_000}, "default", time.Now(), RoundCeil)
	batch, _ := Charge(book, Usage{InputTokens: 1_000_000}, "batch", time.Now(), RoundCeil)
	if def != 10_000_000 {
		t.Errorf("default tier charge = %d, want 10_000_000", def)
	}
	if batch != 5_000_000 {
		t.Errorf("batch tier charge = %d, want 5_000_000", batch)
	}
}

func TestCharge_TimeWindowDiscount(t *testing.T) {
	// 闲时折扣：00:00-08:00 UTC 半价，其余时间原价。
	book := Book{Components: []Component{
		{Meter: MeterInput, Unit: UnitPer1MTokens, WindowStartMin: intPtr16(0), WindowEndMin: intPtr16(480), UnitPrice: dec("5")},
		{Meter: MeterInput, Unit: UnitPer1MTokens, WindowStartMin: intPtr16(480), WindowEndMin: intPtr16(0), UnitPrice: dec("10")},
	}}
	offPeak := time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC) // 03:00 UTC -> 落在 [0,480)
	onPeak := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC) // 12:00 UTC -> 落在 [480,0) 跨零点区间

	off, _ := Charge(book, Usage{InputTokens: 1_000_000}, "", offPeak, RoundCeil)
	on, _ := Charge(book, Usage{InputTokens: 1_000_000}, "", onPeak, RoundCeil)
	if off != 5_000_000 {
		t.Errorf("off-peak charge = %d, want 5_000_000", off)
	}
	if on != 10_000_000 {
		t.Errorf("on-peak charge = %d, want 10_000_000", on)
	}
}

func TestCharge_RoundingCeilsTotal(t *testing.T) {
	// 1 token × 0.000001 元/百万 token 会产生远小于 1 微元的小数，必须向上取整为 1，
	// 不能因为"太小"而被舍去（否则可以被刷成免费）。
	book := Book{Components: []Component{
		{Meter: MeterInput, Unit: UnitPer1MTokens, UnitPrice: dec("0.000001")},
	}}
	got, _ := Charge(book, Usage{InputTokens: 1}, "", time.Now(), RoundCeil)
	if got != 1 {
		t.Errorf("Charge() = %d, want 1 (ceil of a tiny positive amount)", got)
	}

	gotFloor, _ := Charge(book, Usage{InputTokens: 1}, "", time.Now(), RoundFloor)
	if gotFloor != 0 {
		t.Errorf("Charge() with RoundFloor = %d, want 0", gotFloor)
	}
}

func TestCharge_UnmatchedMeterIsIgnoredNotError(t *testing.T) {
	// 价格表只配置了 input，若用量里出现 output，不应 panic 或报错，只是不计费该部分。
	book := Book{Components: []Component{
		{Meter: MeterInput, Unit: UnitPer1MTokens, UnitPrice: dec("10")},
	}}
	got, matched := Charge(book, Usage{InputTokens: 1_000_000, OutputTokens: 500_000}, "", time.Now(), RoundCeil)
	if !matched {
		t.Fatal("expected at least the input component to match")
	}
	if got != 10_000_000 {
		t.Errorf("Charge() = %d, want 10_000_000 (output ignored, no matching component)", got)
	}
}

// TestCharge_MediaMeters 覆盖多模态计量项：按张、按百万字符、按秒（毫秒折算成小数秒，
// 不按整秒向上取整），以及与按次计费叠加。
func TestCharge_MediaMeters(t *testing.T) {
	book := Book{Components: []Component{
		{Meter: MeterImage, Unit: UnitPerImage, UnitPrice: dec("0.1")},
		{Meter: MeterInputChar, Unit: UnitPer1MChars, UnitPrice: dec("50")},
		{Meter: MeterAudioSecond, Unit: UnitPerSecond, UnitPrice: dec("0.001")},
		{Meter: MeterRequest, Unit: UnitPerRequest, UnitPrice: dec("0.002")},
	}}
	cases := []struct {
		name string
		u    Usage
		want int64
	}{
		{"2 images", Usage{Images: 2}, 200_000},
		{"1000 chars", Usage{InputChars: 1000}, 50_000},
		{"2.5 seconds", Usage{AudioMillis: 2500}, 2_500},
		{"seconds + request", Usage{AudioMillis: 1000, RequestCount: 1}, 3_000},
	}
	for _, c := range cases {
		got, matched := Charge(book, c.u, "", time.Now(), RoundCeil)
		if !matched || got != c.want {
			t.Errorf("%s: Charge() = %d (matched=%v), want %d", c.name, got, matched, c.want)
		}
	}
	// 价格表里没有对应计量项时不计费、也不算匹配
	if got, matched := Charge(Book{Components: []Component{{Meter: MeterInput, Unit: UnitPer1MTokens, UnitPrice: dec("1")}}},
		Usage{Images: 3}, "", time.Now(), RoundCeil); got != 0 || matched {
		t.Errorf("unpriced meter: Charge() = %d matched=%v, want 0 false", got, matched)
	}
}
