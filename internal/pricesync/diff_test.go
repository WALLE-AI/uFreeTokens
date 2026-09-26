package pricesync

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
)

func TestDiff_NoCurrentPrice_DirectionNew(t *testing.T) {
	proposed := PriceSpec{Components: []Component{comp(pricing.MeterInput, "", 0, 10)}}
	d := Diff(nil, proposed)
	if d.Direction != DirectionNew {
		t.Errorf("Direction = %q, want new", d.Direction)
	}
	if !d.HasChanges() {
		t.Error("HasChanges() = false, want true (brand new price)")
	}
}

func TestDiff_EmptyProposed_DirectionRemoved(t *testing.T) {
	current := []Component{comp(pricing.MeterInput, "", 0, 10)}
	d := Diff(current, PriceSpec{})
	if d.Direction != DirectionRemoved {
		t.Errorf("Direction = %q, want removed", d.Direction)
	}
}

func TestDiff_AllPricesDown_DirectionDown(t *testing.T) {
	current := []Component{
		comp(pricing.MeterInput, "", 0, 10),
		comp(pricing.MeterOutput, "", 0, 20),
	}
	proposed := PriceSpec{Components: []Component{
		comp(pricing.MeterInput, "", 0, 8),
		comp(pricing.MeterOutput, "", 0, 15),
	}}
	d := Diff(current, proposed)
	if d.Direction != DirectionDown {
		t.Errorf("Direction = %q, want down", d.Direction)
	}
	if !d.HasChanges() {
		t.Error("HasChanges() = false, want true")
	}
}

func TestDiff_AllPricesUp_DirectionUpAndMaxRatio(t *testing.T) {
	current := []Component{
		comp(pricing.MeterInput, "", 0, 10),  // +10%
		comp(pricing.MeterOutput, "", 0, 20), // +50%
	}
	proposed := PriceSpec{Components: []Component{
		comp(pricing.MeterInput, "", 0, 11),
		comp(pricing.MeterOutput, "", 0, 30),
	}}
	d := Diff(current, proposed)
	if d.Direction != DirectionUp {
		t.Errorf("Direction = %q, want up", d.Direction)
	}
	if !d.MaxChangeRatio.Equal(decimal.NewFromFloat(0.5)) {
		t.Errorf("MaxChangeRatio = %s, want 0.5 (the larger of the two increases)", d.MaxChangeRatio)
	}
}

func TestDiff_MixedUpAndDown_DirectionMixed(t *testing.T) {
	current := []Component{
		comp(pricing.MeterInput, "", 0, 10),
		comp(pricing.MeterOutput, "", 0, 20),
	}
	proposed := PriceSpec{Components: []Component{
		comp(pricing.MeterInput, "", 0, 20),  // up
		comp(pricing.MeterOutput, "", 0, 10), // down
	}}
	d := Diff(current, proposed)
	if d.Direction != DirectionMixed {
		t.Errorf("Direction = %q, want mixed", d.Direction)
	}
}

func TestDiff_NoActualChange_HasChangesFalse(t *testing.T) {
	current := []Component{comp(pricing.MeterInput, "", 0, 10)}
	proposed := PriceSpec{Components: []Component{comp(pricing.MeterInput, "", 0, 10)}}
	d := Diff(current, proposed)
	if d.HasChanges() {
		t.Error("HasChanges() = true, want false (identical price, nothing to propose)")
	}
}

func TestDiff_NewComponentAdded_RecordedButNotUpOrDown(t *testing.T) {
	current := []Component{comp(pricing.MeterInput, "", 0, 10)}
	proposed := PriceSpec{Components: []Component{
		comp(pricing.MeterInput, "", 0, 10),         // 没变
		comp(pricing.MeterInputCacheRead, "", 0, 1), // 新增
	}}
	d := Diff(current, proposed)
	if d.Direction != DirectionMixed {
		t.Errorf("Direction = %q, want mixed (a new meter with no overlapping change is conservatively not auto-classified)", d.Direction)
	}
	if !d.HasChanges() {
		t.Error("HasChanges() = false, want true (a new component was added)")
	}
	found := false
	for _, c := range d.Components {
		if c.Meter == pricing.MeterInputCacheRead && c.OldPrice == nil && c.NewPrice != nil {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a diff entry for the newly added input_cache_read meter, got %+v", d.Components)
	}
}

func TestDiff_ComponentRemoved_RecordedWithNilNewPrice(t *testing.T) {
	current := []Component{
		comp(pricing.MeterInput, "", 0, 10),
		comp(pricing.MeterOutput, "", 0, 20),
	}
	proposed := PriceSpec{Components: []Component{comp(pricing.MeterInput, "", 0, 10)}}
	d := Diff(current, proposed)
	found := false
	for _, c := range d.Components {
		if c.Meter == pricing.MeterOutput && c.NewPrice == nil && c.OldPrice != nil {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a diff entry for the removed output meter, got %+v", d.Components)
	}
}

func TestDiff_FreeToPaid_TreatedAsIncrease(t *testing.T) {
	current := []Component{comp(pricing.MeterInput, "", 0, 0)}
	proposed := PriceSpec{Components: []Component{comp(pricing.MeterInput, "", 0, 5)}}
	d := Diff(current, proposed)
	if d.Direction != DirectionUp {
		t.Errorf("Direction = %q, want up (going from free to paid is a price increase)", d.Direction)
	}
}
