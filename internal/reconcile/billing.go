package reconcile

import (
	"context"
	"fmt"
	"time"
)

// billingDriftThreshold 是账单级对账的漂移容忍度（技术方案 §7.16.8：渠道 × 模型
// × 天粒度下，本地成本与上游账单差异超过 1% 就应该上报）。合同折扣、免费额度、
// 计价规则变更都可能造成几个百分点以内的正常波动，卡在 1% 是为了在"噪音"和
// "值得人工看一眼"之间取一个和技术方案一致的平衡点，不是本包发明的数字。
const billingDriftThreshold = 0.01

// BillingLineItem 是上游账单里"某个模型在某天被实际扣费多少"的一条明细，单位
// 和 request_logs.cost_amount 一致（平台内部计价的最小货币单位，通常是分）。
type BillingLineItem struct {
	UpstreamModel string
	AmountMicro   int64
}

// BillingFetcher 从某个上游拉取"某个 provider_account 在某天"的账单明细
// （技术方案 §7.16.2 的 L1 来源）。具体实现（真正调用各家账单/用量 API）留给
// 后续按需接入——每家认证方式、明细粒度、时区处理都不一样，在没有真实凭据能
// 验证的情况下写一个"看起来对"但没验证过的实现,风险比不做还大,见包注释。
// 本包只提供接口和比对逻辑,配合 MockBillingFetcher 做框架测试。
type BillingFetcher interface {
	FetchDailyBilling(ctx context.Context, providerAccountID int64, day time.Time) ([]BillingLineItem, error)
}

// ChannelBillingDrift 是"渠道 × 模型 × 天"粒度下,本地算出的成本
// (Σ request_logs.cost_amount) 与上游账单之间的对比结果。
type ChannelBillingDrift struct {
	ProviderAccountID int64
	UpstreamModel     string
	Day               time.Time
	LocalCostAmount   int64
	UpstreamBilled    int64
}

func (d ChannelBillingDrift) Diff() int64 { return d.LocalCostAmount - d.UpstreamBilled }

// DriftRatio 返回 |Diff()| / UpstreamBilled。UpstreamBilled 为 0 时返回 nil——
// 除以零没有意义,这种边界情况（本地有成本但上游账单为 0,或者反过来）由
// Flagged() 单独处理,不依赖这个比例。
func (d ChannelBillingDrift) DriftRatio() *float64 {
	if d.UpstreamBilled == 0 {
		return nil
	}
	ratio := float64(d.Diff()) / float64(d.UpstreamBilled)
	if ratio < 0 {
		ratio = -ratio
	}
	return &ratio
}

// Flagged 判断这一条是否值得人工关注:漂移比例超过 billingDriftThreshold,或者
// 上游账单是 0 但本地却算出了非零成本(反之亦然)——这种情况 DriftRatio 算不出
// 比例,但显然也不该被当成"一致"放过。
func (d ChannelBillingDrift) Flagged() bool {
	if ratio := d.DriftRatio(); ratio != nil {
		return *ratio > billingDriftThreshold
	}
	return d.LocalCostAmount != 0
}

// CheckBillingDrift 对某个 provider_account 在某一天做账单级对账(技术方案
// §7.16.8):按 upstream_model 分组比较本地成本(request_logs.cost_amount,
// 通过 channel_id 关联 channels 表拿到 provider_account_id/upstream_model)
// 与 fetcher 拉到的上游账单明细。只上报,不自动修正——账单差异的常见原因
// (合同折扣没配置、缓存计价规则变了、新增计量项)都需要人工排查,见包注释。
func (r *Reconciler) CheckBillingDrift(ctx context.Context, fetcher BillingFetcher, providerAccountID int64, day time.Time) ([]ChannelBillingDrift, error) {
	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	dayEnd := dayStart.Add(24 * time.Hour)

	rows, err := r.pool.Query(ctx, `
		SELECT c.upstream_model, COALESCE(SUM(rl.cost_amount), 0)
		FROM request_logs rl
		JOIN channels c ON c.id = rl.channel_id
		WHERE c.provider_account_id = $1
		  AND rl.created_at >= $2 AND rl.created_at < $3
		  AND rl.status = 'success'
		GROUP BY c.upstream_model
	`, providerAccountID, dayStart, dayEnd)
	if err != nil {
		return nil, fmt.Errorf("reconcile: query local cost by upstream_model: %w", err)
	}
	localByModel := map[string]int64{}
	for rows.Next() {
		var model string
		var total int64
		if err := rows.Scan(&model, &total); err != nil {
			rows.Close()
			return nil, fmt.Errorf("reconcile: scan local cost row: %w", err)
		}
		localByModel[model] = total
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reconcile: iterate local cost rows: %w", err)
	}

	items, err := fetcher.FetchDailyBilling(ctx, providerAccountID, dayStart)
	if err != nil {
		return nil, fmt.Errorf("reconcile: fetch upstream billing: %w", err)
	}
	billedByModel := map[string]int64{}
	for _, item := range items {
		billedByModel[item.UpstreamModel] += item.AmountMicro
	}

	models := make(map[string]struct{}, len(localByModel)+len(billedByModel))
	for m := range localByModel {
		models[m] = struct{}{}
	}
	for m := range billedByModel {
		models[m] = struct{}{}
	}

	out := make([]ChannelBillingDrift, 0, len(models))
	for model := range models {
		out = append(out, ChannelBillingDrift{
			ProviderAccountID: providerAccountID,
			UpstreamModel:     model,
			Day:               dayStart,
			LocalCostAmount:   localByModel[model],
			UpstreamBilled:    billedByModel[model],
		})
	}
	return out, nil
}
