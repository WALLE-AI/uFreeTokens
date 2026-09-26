package pricesync

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
)

func comp(meter pricing.Meter, tier string, tierMin int, price float64) Component {
	return Component{Meter: meter, ServiceTier: tier, TierMinInput: tierMin, Unit: pricing.UnitPer1MTokens, UnitPrice: decimal.NewFromFloat(price)}
}

func hasSeverity(issues []ValidationIssue, rule string, sev IssueSeverity) bool {
	for _, i := range issues {
		if i.Rule == rule && i.Severity == sev {
			return true
		}
	}
	return false
}

func TestValidate_NoCurrentPrice_NoMagnitudeCheck(t *testing.T) {
	proposed := PriceSpec{Components: []Component{comp(pricing.MeterInput, "", 0, 1000)}}
	issues := Validate(nil, proposed)
	if hasSeverity(issues, "magnitude", IssueBlocking) {
		t.Error("no current price to compare against, should not flag magnitude")
	}
}

func TestValidate_TenXIncrease_Blocks(t *testing.T) {
	current := []Component{comp(pricing.MeterInput, "", 0, 1)}
	proposed := PriceSpec{Components: []Component{comp(pricing.MeterInput, "", 0, 10)}}
	issues := Validate(current, proposed)
	if !hasSeverity(issues, "magnitude", IssueBlocking) {
		t.Errorf("expected a blocking magnitude issue for a 10x increase, got %+v", issues)
	}
}

func TestValidate_TenthDecrease_Blocks(t *testing.T) {
	current := []Component{comp(pricing.MeterInput, "", 0, 10)}
	proposed := PriceSpec{Components: []Component{comp(pricing.MeterInput, "", 0, 1)}}
	issues := Validate(current, proposed)
	if !hasSeverity(issues, "magnitude", IssueBlocking) {
		t.Errorf("expected a blocking magnitude issue for a 1/10 decrease, got %+v", issues)
	}
}

func TestValidate_ModerateChange_DoesNotBlock(t *testing.T) {
	current := []Component{comp(pricing.MeterInput, "", 0, 10)}
	proposed := PriceSpec{Components: []Component{comp(pricing.MeterInput, "", 0, 11)}}
	issues := Validate(current, proposed)
	if hasSeverity(issues, "magnitude", IssueBlocking) {
		t.Errorf("a 10%% increase should not trip the 10x/0.1x magnitude check, got %+v", issues)
	}
}

func TestValidate_ZeroPriceWithoutExpiry_ForcesReview(t *testing.T) {
	current := []Component{comp(pricing.MeterInput, "", 0, 10)}
	proposed := PriceSpec{Components: []Component{comp(pricing.MeterInput, "", 0, 0)}}
	issues := Validate(current, proposed)
	if !hasSeverity(issues, "zero_price_without_expiry", IssueForcesReview) {
		t.Errorf("expected a forces_review issue for a zero price with no expiry, got %+v", issues)
	}
}

func TestValidate_ZeroPriceWithExpiry_NoForcedReview(t *testing.T) {
	expiry := time.Now().Add(24 * time.Hour)
	current := []Component{comp(pricing.MeterInput, "", 0, 10)}
	proposed := PriceSpec{ExpiresAt: &expiry, Components: []Component{comp(pricing.MeterInput, "", 0, 0)}}
	issues := Validate(current, proposed)
	if hasSeverity(issues, "zero_price_without_expiry", IssueForcesReview) {
		t.Errorf("a zero price with an explicit expiry should not force review, got %+v", issues)
	}
}

func TestValidate_CacheReadExceedsInput_Warns(t *testing.T) {
	proposed := PriceSpec{Components: []Component{
		comp(pricing.MeterInput, "default", 0, 5),
		comp(pricing.MeterInputCacheRead, "default", 0, 10), // 缓存读价比输入价还贵，不合理
	}}
	issues := Validate(nil, proposed)
	if !hasSeverity(issues, "cache_read_exceeds_input", IssueWarning) {
		t.Errorf("expected a warning for cache_read > input, got %+v", issues)
	}
}

func TestValidate_CacheReadBelowInput_NoWarning(t *testing.T) {
	proposed := PriceSpec{Components: []Component{
		comp(pricing.MeterInput, "default", 0, 10),
		comp(pricing.MeterInputCacheRead, "default", 0, 1),
	}}
	issues := Validate(nil, proposed)
	if hasSeverity(issues, "cache_read_exceeds_input", IssueWarning) {
		t.Errorf("cache_read < input should not warn, got %+v", issues)
	}
}

func TestValidate_NonMonotonicTier_Warns(t *testing.T) {
	proposed := PriceSpec{Components: []Component{
		comp(pricing.MeterInput, "default", 0, 5),
		comp(pricing.MeterInput, "default", 128000, 3), // 更高档位反而更便宜，不单调
	}}
	issues := Validate(nil, proposed)
	if !hasSeverity(issues, "non_monotonic_tier", IssueWarning) {
		t.Errorf("expected a non_monotonic_tier warning, got %+v", issues)
	}
}

func TestValidate_MonotonicTier_NoWarning(t *testing.T) {
	proposed := PriceSpec{Components: []Component{
		comp(pricing.MeterInput, "default", 0, 5),
		comp(pricing.MeterInput, "default", 128000, 8),
	}}
	issues := Validate(nil, proposed)
	if hasSeverity(issues, "non_monotonic_tier", IssueWarning) {
		t.Errorf("a monotonically increasing tier should not warn, got %+v", issues)
	}
}
