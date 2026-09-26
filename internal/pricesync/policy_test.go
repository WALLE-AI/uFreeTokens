package pricesync

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestDecidePolicy_BlockingIssueAlwaysWins(t *testing.T) {
	issues := []ValidationIssue{{Rule: "magnitude", Severity: IssueBlocking}}
	got := DecidePolicy(LevelL2, ChangeDiff{Direction: DirectionDown}, issues)
	if got != DecisionBlocked {
		t.Errorf("Decision = %q, want blocked (even though L2+down would normally auto-approve)", got)
	}
}

func TestDecidePolicy_ForcesReviewOverridesAutoApprove(t *testing.T) {
	issues := []ValidationIssue{{Rule: "zero_price_without_expiry", Severity: IssueForcesReview}}
	got := DecidePolicy(LevelL2, ChangeDiff{Direction: DirectionDown}, issues)
	if got != DecisionPending {
		t.Errorf("Decision = %q, want pending", got)
	}
}

func TestDecidePolicy_L2Decrease_AutoApproved(t *testing.T) {
	got := DecidePolicy(LevelL2, ChangeDiff{Direction: DirectionDown}, nil)
	if got != DecisionAutoApproved {
		t.Errorf("Decision = %q, want auto_approved", got)
	}
}

func TestDecidePolicy_L5Decrease_AutoApproved(t *testing.T) {
	got := DecidePolicy(LevelL5, ChangeDiff{Direction: DirectionDown}, nil)
	if got != DecisionAutoApproved {
		t.Errorf("Decision = %q, want auto_approved", got)
	}
}

func TestDecidePolicy_L3Decrease_Pending(t *testing.T) {
	got := DecidePolicy(LevelL3, ChangeDiff{Direction: DirectionDown}, nil)
	if got != DecisionPending {
		t.Errorf("Decision = %q, want pending (L3 decreases still need approval)", got)
	}
}

func TestDecidePolicy_L2SmallIncrease_AutoApproved(t *testing.T) {
	got := DecidePolicy(LevelL2, ChangeDiff{Direction: DirectionUp, MaxChangeRatio: decimal.NewFromFloat(0.15)}, nil)
	if got != DecisionAutoApproved {
		t.Errorf("Decision = %q, want auto_approved (15%% <= 20%% threshold)", got)
	}
}

func TestDecidePolicy_L2IncreaseExactlyAtThreshold_AutoApproved(t *testing.T) {
	got := DecidePolicy(LevelL2, ChangeDiff{Direction: DirectionUp, MaxChangeRatio: decimal.NewFromFloat(0.20)}, nil)
	if got != DecisionAutoApproved {
		t.Errorf("Decision = %q, want auto_approved (exactly at the 20%% threshold, inclusive)", got)
	}
}

func TestDecidePolicy_L2LargeIncrease_Pending(t *testing.T) {
	got := DecidePolicy(LevelL2, ChangeDiff{Direction: DirectionUp, MaxChangeRatio: decimal.NewFromFloat(0.21)}, nil)
	if got != DecisionPending {
		t.Errorf("Decision = %q, want pending (over the 20%% threshold)", got)
	}
}

func TestDecidePolicy_L3Increase_AlwaysPending(t *testing.T) {
	got := DecidePolicy(LevelL3, ChangeDiff{Direction: DirectionUp, MaxChangeRatio: decimal.NewFromFloat(0.01)}, nil)
	if got != DecisionPending {
		t.Errorf("Decision = %q, want pending (L3 never auto-approves regardless of magnitude)", got)
	}
}

// TestDecidePolicy_L4NeverAutoApproves 验证技术方案 §7.16.2 对 L4（社区数据集）
// 来源的定位——"只做交叉校验，永不单独生效"：不管涨价、降价、金额大小，L4 都
// 不应该落进 DecisionAutoApproved。
func TestDecidePolicy_L4NeverAutoApproves(t *testing.T) {
	cases := []struct {
		name string
		diff ChangeDiff
	}{
		{"tiny decrease", ChangeDiff{Direction: DirectionDown, MaxChangeRatio: decimal.NewFromFloat(0.01)}},
		{"tiny increase", ChangeDiff{Direction: DirectionUp, MaxChangeRatio: decimal.NewFromFloat(0.01)}},
		{"new model", ChangeDiff{Direction: DirectionNew}},
	}
	for _, c := range cases {
		got := DecidePolicy(LevelL4, c.diff, nil)
		if got == DecisionAutoApproved {
			t.Errorf("%s: Decision = %q, want anything but auto_approved (L4 must never single-handedly apply a change)", c.name, got)
		}
	}
}

func TestDecidePolicy_MixedNewRemoved_AlwaysPending(t *testing.T) {
	for _, dir := range []Direction{DirectionMixed, DirectionNew, DirectionRemoved} {
		got := DecidePolicy(LevelL2, ChangeDiff{Direction: dir}, nil)
		if got != DecisionPending {
			t.Errorf("Decision(%s) = %q, want pending", dir, got)
		}
	}
}
