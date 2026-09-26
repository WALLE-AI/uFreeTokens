package pricesync

import (
	"fmt"
	"sort"

	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
)

// IssueSeverity 决定一条校验问题如何影响 Engine.Ingest 的最终状态
// （技术方案 §7.16.6）。
type IssueSeverity string

const (
	// IssueBlocking：拦截，生成的 change request 状态直接是 blocked，不管 Policy
	// 本来会判定成什么——大概率是单位/解析错误，需要人工排查而不是走正常审批。
	IssueBlocking IssueSeverity = "blocking"
	// IssueForcesReview：不拦截，但不管 Policy 本来会不会自动通过，强制变成
	// pending（比如"价格变成 0 但没有显式标注免费+结束时间"）。
	IssueForcesReview IssueSeverity = "forces_review"
	// IssueWarning：只记录在 diff 里给人看，不影响状态判定。
	IssueWarning IssueSeverity = "warning"
)

type ValidationIssue struct {
	Rule     string
	Message  string
	Severity IssueSeverity
}

func tierOrDefault(t string) string {
	if t == "" {
		return "default"
	}
	return t
}

func componentKey(meter pricing.Meter, tier string, tierMin int) string {
	return fmt.Sprintf("%s|%s|%d", meter, tierOrDefault(tier), tierMin)
}

func indexComponents(cs []Component) map[string]Component {
	m := make(map[string]Component, len(cs))
	for _, c := range cs {
		m[componentKey(c.Meter, c.ServiceTier, c.TierMinInput)] = c
	}
	return m
}

// Validate 实现技术方案 §7.16.6 里不需要跨来源/跨时间历史数据的校验规则
// （数量级异常、零价无到期时间、结构性检查）。需要历史观测数据的规则
// （L3 连续两次确认、多来源冲突）在 engine.go 里实现，因为那两条需要查
// price_observations 表，不是纯函数。
func Validate(current []Component, proposed PriceSpec) []ValidationIssue {
	var issues []ValidationIssue
	curByKey := indexComponents(current)

	for _, c := range proposed.Components {
		if old, ok := curByKey[componentKey(c.Meter, c.ServiceTier, c.TierMinInput)]; ok && !old.UnitPrice.IsZero() {
			ratio := c.UnitPrice.Div(old.UnitPrice)
			if ratio.GreaterThanOrEqual(decimal.NewFromInt(10)) || ratio.LessThanOrEqual(decimal.NewFromFloat(0.1)) {
				issues = append(issues, ValidationIssue{
					Rule:     "magnitude",
					Severity: IssueBlocking,
					Message: fmt.Sprintf("%s (tier=%s) changed %sx (from %s to %s); looks like a unit or parsing error",
						c.Meter, tierOrDefault(c.ServiceTier), ratio.StringFixed(2), old.UnitPrice, c.UnitPrice),
				})
			}
		}
		if c.UnitPrice.IsZero() && proposed.ExpiresAt == nil {
			issues = append(issues, ValidationIssue{
				Rule:     "zero_price_without_expiry",
				Severity: IssueForcesReview,
				Message:  fmt.Sprintf("%s priced at 0 with no expiry; must be confirmed as an intentional free promotion", c.Meter),
			})
		}
	}

	issues = append(issues, structuralIssues(proposed.Components)...)
	return issues
}

// structuralIssues：缓存读价应 <= 输入价；同一 (meter, service_tier) 按
// tier_min_input 分档时价格应单调不减。两条都只告警，不影响生效状态——真实
// 世界里偶尔会有厂商定价本身就不严格单调，不应该因此拦截整条提案。
func structuralIssues(components []Component) []ValidationIssue {
	var issues []ValidationIssue

	inputByTier := map[string]decimal.Decimal{}
	for _, c := range components {
		if c.Meter == pricing.MeterInput {
			inputByTier[tierOrDefault(c.ServiceTier)] = c.UnitPrice
		}
	}
	for _, c := range components {
		if c.Meter != pricing.MeterInputCacheRead {
			continue
		}
		if inputPrice, ok := inputByTier[tierOrDefault(c.ServiceTier)]; ok && c.UnitPrice.GreaterThan(inputPrice) {
			issues = append(issues, ValidationIssue{
				Rule:     "cache_read_exceeds_input",
				Severity: IssueWarning,
				Message:  fmt.Sprintf("input_cache_read price %s exceeds input price %s for tier %s", c.UnitPrice, inputPrice, tierOrDefault(c.ServiceTier)),
			})
		}
	}

	type groupKey struct {
		meter pricing.Meter
		tier  string
	}
	groups := map[groupKey][]Component{}
	for _, c := range components {
		k := groupKey{c.Meter, tierOrDefault(c.ServiceTier)}
		groups[k] = append(groups[k], c)
	}
	for k, g := range groups {
		if len(g) < 2 {
			continue
		}
		sort.Slice(g, func(i, j int) bool { return g[i].TierMinInput < g[j].TierMinInput })
		for i := 1; i < len(g); i++ {
			if g[i].UnitPrice.LessThan(g[i-1].UnitPrice) {
				issues = append(issues, ValidationIssue{
					Rule:     "non_monotonic_tier",
					Severity: IssueWarning,
					Message: fmt.Sprintf("%s tier starting at %d input tokens (%s) is cheaper than the tier below it starting at %d (%s)",
						k.meter, g[i].TierMinInput, g[i].UnitPrice, g[i-1].TierMinInput, g[i-1].UnitPrice),
				})
			}
		}
	}
	return issues
}
