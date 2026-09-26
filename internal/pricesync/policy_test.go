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

func TestDecidePolicy_MixedNewRemoved_AlwaysPending(t *testing.T) {
	for _, dir := range []Direction{DirectionMixed, DirectionNew, DirectionRemoved} {
		got := DecidePolicy(LevelL2, ChangeDiff{Direction: dir}, nil)
		if got != DecisionPending {
			t.Errorf("Decision(%s) = %q, want pending", dir, got)
		}
	}
}
