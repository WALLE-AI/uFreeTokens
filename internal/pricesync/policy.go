package pricesync

import "github.com/shopspring/decimal"

type Decision string

const (
	DecisionAutoApproved Decision = "auto_approved"
	DecisionPending      Decision = "pending"
	DecisionBlocked      Decision = "blocked"
)

// maxAutoApproveIncreaseRatio 对应技术方案 §7.16.7："涨价 <= 20% | L2 | 自动生效"。
var maxAutoApproveIncreaseRatio = decimal.NewFromFloat(0.20)

// DecidePolicy 实现技术方案 §7.16.7 的成本价生效策略表：
//
//	降价（所有计量项都不升）  | L2/L5 | 自动生效
//	降价                      | L3    | 审批
//	涨价 <= 20%               | L2    | 自动生效
//	涨价 > 20% 或来源为 L3    | —     | 审批
//	mixed / new / removed     | —     | 审批
//
// issues 里任何 IssueBlocking 直接短路成 blocked；任何 IssueForcesReview
// 短路成 pending——两者都优先于下面的方向判断。
func DecidePolicy(level Level, diff ChangeDiff, issues []ValidationIssue) Decision {
	for _, i := range issues {
		if i.Severity == IssueBlocking {
			return DecisionBlocked
		}
	}
	for _, i := range issues {
		if i.Severity == IssueForcesReview {
			return DecisionPending
		}
	}

	switch diff.Direction {
	case DirectionDown:
		if level == LevelL2 || level == LevelL5 {
			return DecisionAutoApproved
		}
		return DecisionPending
	case DirectionUp:
		if level == LevelL2 && diff.MaxChangeRatio.LessThanOrEqual(maxAutoApproveIncreaseRatio) {
			return DecisionAutoApproved
		}
		return DecisionPending
	default: // mixed / new / removed：技术方案没有给出对应的"自动生效"格，一律审批
		return DecisionPending
	}
}
