package pricesync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/store"
)

// ListingPublisher 是 PublishListing 需要的发布能力，在 CostPricePublisher
// 之外再加上创建虚拟模型/渠道/售价——同样刻意保持窄接口。*admin.Service 满足
// 这个接口。
type ListingPublisher interface {
	CostPricePublisher
	CreateVirtualModel(ctx context.Context, in admin.CreateVirtualModelInput) (*admin.VirtualModel, error)
	CreateChannel(ctx context.Context, in admin.CreateChannelInput) (*admin.Channel, error)
	SetSellPrice(ctx context.Context, in admin.SetSellPriceInput) (int64, error)
}

// UnmappedObservationInput 描述一条还不知道该挂到哪个渠道的观测——技术方案
// §7.16.3 Mapper 阶段的输入：只知道"哪个 provider、上游模型 ID 叫什么"，
// 具体路由到哪个 provider_account/channel 需要现有配置里已经有对应渠道才能
// 确定。
type UnmappedObservationInput struct {
	ProviderID    int64
	SourceID      int64
	Level         Level
	UpstreamModel string
	Spec          PriceSpec
	RawObject     string
}

// UnmappedIngestResult 汇总 IngestUnmapped 实际做了什么：如果找到了匹配的
// 现有渠道，会对每一个都跑一遍正常的 Ingest 流程（一个 upstream_model 可能
// 通过多个 provider_account 被接了不止一个渠道）；一个都没找到时会新建/更新
// 一条"待上架"候选，ListingID 非 nil。两者不会同时发生。
type UnmappedIngestResult struct {
	MappedResults []IngestResult
	ListingID     *int64
}

// IngestUnmapped 实现 Mapper 阶段：按 (provider_id, upstream_model) 查找现有
// 渠道——找到了就对每个渠道正常走 Ingest；一个都找不到就记入/更新
// pending_model_listings，不自动创建任何配置（技术方案 §7.16.3："匹配不到 ->
// 新模型发现队列，不自动上架"）。
func (e *Engine) IngestUnmapped(ctx context.Context, in UnmappedObservationInput) (*UnmappedIngestResult, error) {
	channelIDs, err := e.resolveChannels(ctx, in.ProviderID, in.UpstreamModel)
	if err != nil {
		return nil, err
	}

	if len(channelIDs) > 0 {
		results := make([]IngestResult, 0, len(channelIDs))
		for _, chID := range channelIDs {
			r, err := e.Ingest(ctx, IngestInput{
				ChannelID: chID, SourceID: in.SourceID, Level: in.Level, UpstreamModel: in.UpstreamModel,
				Spec: in.Spec, RawObject: in.RawObject,
			})
			if err != nil {
				return nil, fmt.Errorf("pricesync: ingest for mapped channel %d: %w", chID, err)
			}
			results = append(results, *r)
		}
		return &UnmappedIngestResult{MappedResults: results}, nil
	}

	specJSON, err := json.Marshal(in.Spec)
	if err != nil {
		return nil, fmt.Errorf("pricesync: marshal spec: %w", err)
	}
	var listingID int64
	if err := e.db(ctx).QueryRow(ctx,
		`INSERT INTO pending_model_listings (provider_id, upstream_model, source_id, observed_spec)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (provider_id, upstream_model) DO UPDATE
		   SET observed_spec = EXCLUDED.observed_spec, source_id = EXCLUDED.source_id, last_observed_at = now()
		 RETURNING id`,
		in.ProviderID, in.UpstreamModel, in.SourceID, specJSON,
	).Scan(&listingID); err != nil {
		return nil, fmt.Errorf("pricesync: upsert pending_model_listing: %w", err)
	}
	return &UnmappedIngestResult{ListingID: &listingID}, nil
}

// resolveChannels 查找某个 provider 下、以 upstream_model 命名的所有活跃渠道
// （可能不止一个——同一个模型可以通过不同的 provider_account 接入多次）。
func (e *Engine) resolveChannels(ctx context.Context, providerID int64, upstreamModel string) ([]int64, error) {
	rows, err := e.db(ctx).Query(ctx,
		`SELECT c.id FROM channels c
		 JOIN provider_accounts pa ON pa.id = c.provider_account_id
		 WHERE pa.provider_id = $1 AND c.upstream_model = $2 AND c.status = 'active'`,
		providerID, upstreamModel,
	)
	if err != nil {
		return nil, fmt.Errorf("pricesync: resolve channels: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("pricesync: scan channel id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// PendingListingSummary 是 ListPendingListings 的返回行。
type PendingListingSummary struct {
	ID              int64
	ProviderID      int64
	UpstreamModel   string
	ObservedSpec    PriceSpec
	FirstObservedAt time.Time
	LastObservedAt  time.Time
}

// ListPendingListings 列出所有还没决定（既没发布也没驳回）的候选模型。
func (e *Engine) ListPendingListings(ctx context.Context) ([]PendingListingSummary, error) {
	rows, err := e.db(ctx).Query(ctx,
		`SELECT id, provider_id, upstream_model, observed_spec, first_observed_at, last_observed_at
		 FROM pending_model_listings WHERE status = 'pending' ORDER BY first_observed_at`)
	if err != nil {
		return nil, fmt.Errorf("pricesync: query pending_model_listings: %w", err)
	}
	defer rows.Close()

	var out []PendingListingSummary
	for rows.Next() {
		var s PendingListingSummary
		var specJSON []byte
		if err := rows.Scan(&s.ID, &s.ProviderID, &s.UpstreamModel, &specJSON, &s.FirstObservedAt, &s.LastObservedAt); err != nil {
			return nil, fmt.Errorf("pricesync: scan pending_model_listing: %w", err)
		}
		if err := json.Unmarshal(specJSON, &s.ObservedSpec); err != nil {
			return nil, fmt.Errorf("pricesync: unmarshal observed_spec: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

var (
	ErrListingNotFound   = errors.New("pricesync: pending model listing not found")
	ErrListingNotPending = errors.New("pricesync: pending model listing is not pending")
)

// Dismiss 驳回一条候选——运营看过了，决定不上架这个模型。不删除记录，留作
// 审计（下次这个来源再报告同一个 upstream_model 时，ON CONFLICT 只会更新
// observed_spec，不会把已经 dismissed 的记录重新变回 pending，避免一个已经
// 明确拒绝过的模型反复出现在待办列表里）。
func (e *Engine) DismissListing(ctx context.Context, listingID int64) error {
	return store.RunInTx(ctx, e.pool, func(ctx context.Context) error {
		var status string
		err := e.db(ctx).QueryRow(ctx, `SELECT status FROM pending_model_listings WHERE id = $1 FOR UPDATE`, listingID).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrListingNotFound
		}
		if err != nil {
			return fmt.Errorf("pricesync: load pending_model_listing: %w", err)
		}
		if status != "pending" {
			return ErrListingNotPending
		}
		if _, err := e.db(ctx).Exec(ctx,
			`UPDATE pending_model_listings SET status = 'dismissed', decided_at = now() WHERE id = $1`, listingID,
		); err != nil {
			return fmt.Errorf("pricesync: dismiss pending_model_listing: %w", err)
		}
		return nil
	})
}

// PublishListingInput 是运营"一键上架"时必须补齐的部分——这些字段没法从价格
// 观测里自动推断，需要人工决定（技术方案 §7.16.3："人工确认后发布"）：
// 虚拟模型怎么命名/归到哪个能力档位、走哪个 provider_account（带真实凭据）、
// 加多少毛利率。
type PublishListingInput struct {
	VirtualModel      admin.CreateVirtualModelInput
	ProviderAccountID int64
	SellMarkup        decimal.Decimal // 售价(CNY) = 成本 × 汇率 × cost_multiplier × (1 + SellMarkup)；0.3 = 加价 30%
}

// PublishListingResult 是一键上架实际创建出来的东西。
type PublishListingResult struct {
	VirtualModelID int64
	ChannelID      int64
	CostBookID     int64
	SellBookID     int64
}

// PublishListing 把一条待上架候选变成真实可用的配置：新建虚拟模型 -> 新建渠道
// （挂到运营指定的 provider_account）-> 用观测到的价格发布成本价 -> 按
// (1+SellMarkup) 算出售价并发布 -> 标记候选为 published。
//
// 全部步骤在一个数据库事务里（store.RunInTx，admin 的各个方法会加入这个事务），
// 并先锁住候选行：任何一步失败整体回滚，不会留下"虚拟模型建好了、渠道没建"
// 的半成品；两个运营同时上架同一候选，第二个会看到 status 已不是 pending。
func (e *Engine) PublishListing(ctx context.Context, listingID int64, in PublishListingInput) (*PublishListingResult, error) {
	if !in.SellMarkup.IsPositive() && !in.SellMarkup.IsZero() {
		return nil, errors.New("pricesync: sell_markup must not be negative")
	}
	if in.SellMarkup.GreaterThan(decimal.NewFromInt(10)) {
		return nil, errors.New("pricesync: sell_markup must not exceed 10 (i.e. +1000%)")
	}
	var result *PublishListingResult
	err := store.RunInTx(ctx, e.pool, func(ctx context.Context) error {
		var err error
		result, err = e.publishListingTx(ctx, listingID, in)
		return err
	})
	return result, err
}

func (e *Engine) publishListingTx(ctx context.Context, listingID int64, in PublishListingInput) (*PublishListingResult, error) {
	var status string
	var upstreamModel string
	var specJSON []byte
	err := e.db(ctx).QueryRow(ctx,
		`SELECT status, upstream_model, observed_spec FROM pending_model_listings WHERE id = $1 FOR UPDATE`, listingID,
	).Scan(&status, &upstreamModel, &specJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrListingNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("pricesync: load pending_model_listing: %w", err)
	}
	if status != "pending" {
		return nil, ErrListingNotPending
	}
	var spec PriceSpec
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		return nil, fmt.Errorf("pricesync: unmarshal observed_spec: %w", err)
	}

	// 售价以人民币发布，必须先把观测到的成本折成 CNY（汇率 × 上游账号的合同倍率）
	// 再加价——否则一个 USD 报价的模型会按"美元数字当人民币"上架，直接亏本。
	// 两项查询都放在创建任何对象之前：缺汇率时整体失败，不留下半上架的虚拟模型。
	costToCNY, err := e.costToCNYFactor(ctx, spec.Currency, in.ProviderAccountID)
	if err != nil {
		return nil, err
	}

	vm, err := e.publisher.CreateVirtualModel(ctx, in.VirtualModel)
	if err != nil {
		return nil, fmt.Errorf("pricesync: create virtual model: %w", err)
	}
	ch, err := e.publisher.CreateChannel(ctx, admin.CreateChannelInput{
		VirtualModelID: vm.ID, ProviderAccountID: in.ProviderAccountID, UpstreamModel: upstreamModel,
	})
	if err != nil {
		return nil, fmt.Errorf("pricesync: create channel: %w", err)
	}

	costComponents := toAdminComponents(spec.Components)
	costBookID, err := e.publisher.SetCostPrice(ctx, admin.SetCostPriceInput{
		ChannelID: ch.ID, Currency: spec.Currency, Components: costComponents,
	})
	if err != nil {
		return nil, fmt.Errorf("pricesync: publish cost price: %w", err)
	}

	sellComponents := make([]admin.PriceComponentInput, len(costComponents))
	markupFactor := decimal.NewFromInt(1).Add(in.SellMarkup)
	for i, c := range costComponents {
		sellComponents[i] = c
		sellComponents[i].UnitPrice = c.UnitPrice.Mul(costToCNY).Mul(markupFactor).Round(6)
	}
	sellBookID, err := e.publisher.SetSellPrice(ctx, admin.SetSellPriceInput{
		VirtualModelID: vm.ID, Components: sellComponents,
	})
	if err != nil {
		return nil, fmt.Errorf("pricesync: publish sell price: %w", err)
	}

	if _, err := e.db(ctx).Exec(ctx,
		`UPDATE pending_model_listings
		 SET status = 'published', published_virtual_model_id = $2, published_channel_id = $3, decided_at = now()
		 WHERE id = $1`,
		listingID, vm.ID, ch.ID,
	); err != nil {
		return nil, fmt.Errorf("pricesync: mark pending_model_listing published: %w", err)
	}

	return &PublishListingResult{VirtualModelID: vm.ID, ChannelID: ch.ID, CostBookID: costBookID, SellBookID: sellBookID}, nil
}

// ErrMissingFXRate 表示观测价格的币种没有可用的人民币汇率，无法折算售价。
var ErrMissingFXRate = errors.New("pricesync: no CNY exchange rate for the listing currency; set one via POST /fx-rates first")

// costToCNYFactor 返回"1 单位观测成本 = 多少人民币成本"：汇率（quote=CNY、
// effective_date <= 今天的最新一条，与 internal/catalog 口径一致）× 上游账号的
// cost_multiplier。
func (e *Engine) costToCNYFactor(ctx context.Context, currency string, providerAccountID int64) (decimal.Decimal, error) {
	var multiplier decimal.Decimal
	err := e.db(ctx).QueryRow(ctx, `SELECT cost_multiplier FROM provider_accounts WHERE id = $1`, providerAccountID).Scan(&multiplier)
	if errors.Is(err, pgx.ErrNoRows) {
		return decimal.Zero, admin.ErrProviderAccountNotFound
	}
	if err != nil {
		return decimal.Zero, fmt.Errorf("pricesync: load cost_multiplier: %w", err)
	}
	if currency == "" || currency == "CNY" {
		return multiplier, nil
	}
	var rate decimal.Decimal
	err = e.db(ctx).QueryRow(ctx,
		`SELECT rate FROM fx_rates WHERE base = $1 AND quote = 'CNY' AND effective_date <= CURRENT_DATE
		 ORDER BY effective_date DESC LIMIT 1`, currency).Scan(&rate)
	if errors.Is(err, pgx.ErrNoRows) {
		return decimal.Zero, fmt.Errorf("%w (currency %s)", ErrMissingFXRate, currency)
	}
	if err != nil {
		return decimal.Zero, fmt.Errorf("pricesync: load fx rate: %w", err)
	}
	return rate.Mul(multiplier), nil
}

func toAdminComponents(components []Component) []admin.PriceComponentInput {
	out := make([]admin.PriceComponentInput, len(components))
	for i, c := range components {
		out[i] = admin.PriceComponentInput{
			Meter: string(c.Meter), Unit: string(c.Unit), ServiceTier: c.ServiceTier,
			TierMinInput: c.TierMinInput, TierMaxInput: c.TierMaxInput,
			WindowStartMin: c.WindowStartMin, WindowEndMin: c.WindowEndMin, UnitPrice: c.UnitPrice,
		}
	}
	return out
}
