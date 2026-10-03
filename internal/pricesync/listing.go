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
	EnsureVirtualModelMetadata(ctx context.Context, vmID int64) (bool, error)
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
	Meta          *ModelMeta
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

	listingID, err := e.upsertListing(ctx, in.ProviderID, in.UpstreamModel, in.SourceID, in.Spec, in.Meta, nil)
	if err != nil {
		return nil, err
	}
	return &UnmappedIngestResult{ListingID: &listingID}, nil
}

// upsertListing 新建 / 刷新一条待上架候选。offerID 非 nil 表示免费模型候选（origin=free_offer）：
// 已过期的候选重新变回 pending（上游又免费了）；dismissed / published 的不动状态。
// 没带参数的观测不覆盖已有参数（同一模型可能先后被带参数和不带参数的来源报告）。
func (e *Engine) upsertListing(ctx context.Context, providerID int64, upstreamModel string, sourceID int64, spec PriceSpec, meta *ModelMeta, offerID *int64) (int64, error) {
	specJSON, err := json.Marshal(spec)
	if err != nil {
		return 0, fmt.Errorf("pricesync: marshal spec: %w", err)
	}
	var metaJSON []byte
	if meta != nil {
		if metaJSON, err = json.Marshal(meta); err != nil {
			return 0, fmt.Errorf("pricesync: marshal meta: %w", err)
		}
	}
	origin := "price_source"
	if offerID != nil {
		origin = "free_offer"
	}
	var listingID int64
	if err := e.db(ctx).QueryRow(ctx,
		`INSERT INTO pending_model_listings (provider_id, upstream_model, source_id, observed_spec, observed_meta, origin, offer_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (provider_id, upstream_model) DO UPDATE
		   SET observed_spec = EXCLUDED.observed_spec, source_id = EXCLUDED.source_id, last_observed_at = now(),
		       observed_meta = COALESCE(EXCLUDED.observed_meta, pending_model_listings.observed_meta),
		       origin = CASE WHEN EXCLUDED.offer_id IS NULL THEN pending_model_listings.origin ELSE 'free_offer' END,
		       offer_id = COALESCE(EXCLUDED.offer_id, pending_model_listings.offer_id),
		       status = CASE WHEN pending_model_listings.status = 'expired' AND EXCLUDED.offer_id IS NOT NULL
		                     THEN 'pending' ELSE pending_model_listings.status END
		 RETURNING id`,
		providerID, upstreamModel, sourceID, specJSON, metaJSON, origin, offerID,
	).Scan(&listingID); err != nil {
		return 0, fmt.Errorf("pricesync: upsert pending_model_listing: %w", err)
	}
	return listingID, nil
}

// FreeListingInput 是一条被识别为免费的上游模型（offers 已经为它写了 free_model 情报）。
type FreeListingInput struct {
	ProviderID    int64
	SourceID      int64
	UpstreamModel string
	Spec          PriceSpec
	Meta          *ModelMeta
	OfferID       int64
}

// UpsertFreeListing 让免费模型直接进待上架队列（不受来源 discover_listings 开关限制——免费模型
// 数量有限，不会像聚合来源那样刷出几百条）。平台上已经有该模型的渠道时不建候选，返回 ok=false。
func (e *Engine) UpsertFreeListing(ctx context.Context, in FreeListingInput) (listingID int64, ok bool, err error) {
	channels, err := e.resolveChannels(ctx, in.ProviderID, in.UpstreamModel)
	if err != nil || len(channels) > 0 {
		return 0, false, err
	}
	offerID := in.OfferID
	listingID, err = e.upsertListing(ctx, in.ProviderID, in.UpstreamModel, in.SourceID, in.Spec, in.Meta, &offerID)
	return listingID, err == nil, err
}

// ProviderIDByCode 把优惠情报里的厂商 code 映射成本平台 providers.id；平台没有这个供应商时返回 0。
func (e *Engine) ProviderIDByCode(ctx context.Context, code string) (int64, error) {
	var id int64
	err := e.db(ctx).QueryRow(ctx, `SELECT id FROM providers WHERE code = $1`, code).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("pricesync: load provider by code: %w", err)
	}
	return id, nil
}

// freeOfferGone：候选关联的免费情报已过期，且同一厂商 + 模型没有其它仍有效的免费情报
// （同一个免费模型可能被多个来源报告，只要还有一个来源说它免费，就不算结束）。
const freeOfferGone = `EXISTS (SELECT 1 FROM upstream_offers o WHERE o.id = l.offer_id AND o.status = 'expired')
	AND NOT EXISTS (SELECT 1 FROM upstream_offers o JOIN upstream_offers o2
	                  ON o2.provider_code = o.provider_code AND o2.upstream_model = o.upstream_model
	                WHERE o.id = l.offer_id AND o2.offer_type = 'free_model' AND o2.status IN ('new','confirmed','adopted'))`

// FreeLifecycleResult 汇总 SyncFreeListings 做了什么。
type FreeLifecycleResult struct {
	Expired          int64   // 还没上架就不再免费、置为 expired 的候选
	RetiredChannels  []int64 // 已上架、因免费结束被停用的渠道
	DeprecatedModels []int64 // 随之被标为 deprecated 的虚拟模型（由候选新建、且已没有其它可用渠道）
}

// SyncFreeListings 处理免费模型的生命周期。上游免费结束（情报 expired）后：
//   - 待上架的候选置为 expired，不再出现在待办里（上游重新免费时 upsertListing 会把它变回 pending）；
//   - 已上架的停用对应渠道——否则会继续以 0 元售价卖一个已经收费的上游，或路由到已下线的 :free 模型；
//     候选新建的虚拟模型若已没有其它可用渠道，一并标为 deprecated；挂到已有虚拟模型的只停渠道。
//
// 每条候选只处理一次（retired_at 标记）：运营之后手动恢复的渠道不会再被停掉。幂等，每次抓取后调用。
func (e *Engine) SyncFreeListings(ctx context.Context) (*FreeLifecycleResult, error) {
	res := &FreeLifecycleResult{}
	err := store.RunInTx(ctx, e.pool, func(ctx context.Context) error {
		tag, err := e.db(ctx).Exec(ctx,
			`UPDATE pending_model_listings l SET status = 'expired', decided_at = now()
			 WHERE l.origin = 'free_offer' AND l.status = 'pending' AND `+freeOfferGone)
		if err != nil {
			return fmt.Errorf("pricesync: expire free listings: %w", err)
		}
		res.Expired = tag.RowsAffected()

		type retired struct {
			channelID int64
			vmID      *int64
			attached  bool
		}
		rows, err := e.db(ctx).Query(ctx,
			`UPDATE pending_model_listings l SET retired_at = now()
			 WHERE l.origin = 'free_offer' AND l.status = 'published' AND l.retired_at IS NULL
			   AND l.published_channel_id IS NOT NULL AND `+freeOfferGone+`
			 RETURNING l.published_channel_id, l.published_virtual_model_id, l.attached`)
		if err != nil {
			return fmt.Errorf("pricesync: retire free listings: %w", err)
		}
		list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (retired, error) {
			var r retired
			err := row.Scan(&r.channelID, &r.vmID, &r.attached)
			return r, err
		})
		if err != nil {
			return fmt.Errorf("pricesync: scan retired listings: %w", err)
		}
		for _, r := range list {
			tag, err := e.db(ctx).Exec(ctx,
				`UPDATE channels SET status = 'disabled', version = version + 1, updated_at = now() WHERE id = $1 AND status = 'active'`, r.channelID)
			if err != nil {
				return fmt.Errorf("pricesync: disable channel %d: %w", r.channelID, err)
			}
			if tag.RowsAffected() > 0 {
				res.RetiredChannels = append(res.RetiredChannels, r.channelID)
			}
			if r.attached || r.vmID == nil {
				continue
			}
			tag, err = e.db(ctx).Exec(ctx,
				`UPDATE virtual_models SET status = 'deprecated', version = version + 1, updated_at = now()
				 WHERE id = $1 AND status = 'active'
				   AND NOT EXISTS (SELECT 1 FROM channels WHERE virtual_model_id = $1 AND status = 'active')`, *r.vmID)
			if err != nil {
				return fmt.Errorf("pricesync: deprecate virtual model %d: %w", *r.vmID, err)
			}
			if tag.RowsAffected() > 0 {
				res.DeprecatedModels = append(res.DeprecatedModels, *r.vmID)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
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
//
// 虚拟模型名一律等于上游原始模型名（VirtualModel.Name 被忽略）。已有同名虚拟模型时不新建：
// 新渠道直接挂上去、只发布成本价，售价不变（VirtualModel 其余字段与 SellMarkup 也被忽略）。
type PublishListingInput struct {
	VirtualModel      admin.CreateVirtualModelInput
	ProviderAccountID int64
	SellMarkup        decimal.Decimal // 售价(CNY) = 成本 × 汇率 × cost_multiplier × (1 + SellMarkup)；0.3 = 加价 30%
	DecidedByName     string          // 免费候选上架时，把关联的优惠情报标为"已确认"所记的处理人
}

// PublishListingResult 是一键上架实际创建出来的东西。复用已有同名虚拟模型时 SellBookID 为 0（没有发布售价）。
type PublishListingResult struct {
	VirtualModelID int64
	ChannelID      int64
	CostBookID     int64
	SellBookID     int64
	// MetadataCreated：虚拟模型原本没有展示元数据，这次按外部目录 / 模型名自动生成了一条。
	MetadataCreated bool
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
	var offerID *int64
	err := e.db(ctx).QueryRow(ctx,
		`SELECT status, upstream_model, observed_spec, offer_id FROM pending_model_listings WHERE id = $1 FOR UPDATE`, listingID,
	).Scan(&status, &upstreamModel, &specJSON, &offerID)
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
	// 虚拟模型名 = 上游原始模型名。同名虚拟模型在用（active / hidden）时复用：只挂渠道、不动售价；
	// 已废弃的（比如上次免费结束被系统废弃）重新启用，并按本次观测重新定价。
	var vmID int64
	var vmStatus string
	err = e.db(ctx).QueryRow(ctx, `SELECT id, status FROM virtual_models WHERE name = $1 FOR UPDATE`, upstreamModel).Scan(&vmID, &vmStatus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("pricesync: load virtual model %q: %w", upstreamModel, err)
	}
	reuse := vmID != 0 && vmStatus != "deprecated"

	// 不发布售价（复用已有虚拟模型）或成本全为 0（免费模型，售价恒为 0）时用不到汇率，缺了也放行。
	costToCNY, err := e.costToCNYFactor(ctx, spec.Currency, in.ProviderAccountID)
	if err != nil && !(errors.Is(err, ErrMissingFXRate) && (reuse || spec.isFree())) {
		return nil, err
	}

	switch {
	case reuse:
	case vmID != 0:
		if _, err := e.db(ctx).Exec(ctx,
			`UPDATE virtual_models SET status = 'active', version = version + 1, updated_at = now() WHERE id = $1`, vmID); err != nil {
			return nil, fmt.Errorf("pricesync: reactivate virtual model: %w", err)
		}
	default:
		vmIn := in.VirtualModel
		vmIn.Name = upstreamModel
		vm, err := e.publisher.CreateVirtualModel(ctx, vmIn)
		if err != nil {
			return nil, fmt.Errorf("pricesync: create virtual model: %w", err)
		}
		vmID = vm.ID
	}
	ch, err := e.publisher.CreateChannel(ctx, admin.CreateChannelInput{
		VirtualModelID: vmID, ProviderAccountID: in.ProviderAccountID, UpstreamModel: upstreamModel,
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

	result := &PublishListingResult{VirtualModelID: vmID, ChannelID: ch.ID, CostBookID: costBookID}
	// 展示元数据还没有时按建议值补一条（名称、厂商、介绍、标签），省得上架后再手工录入；
	// 已有的（运营录过的）一律不动。
	if result.MetadataCreated, err = e.publisher.EnsureVirtualModelMetadata(ctx, vmID); err != nil {
		return nil, fmt.Errorf("pricesync: ensure virtual model metadata: %w", err)
	}
	if !reuse {
		if result.SellBookID, err = e.publishMarkupSellPrice(ctx, vmID, costComponents, costToCNY, in.SellMarkup); err != nil {
			return nil, err
		}
	}

	if _, err := e.db(ctx).Exec(ctx,
		`UPDATE pending_model_listings
		 SET status = 'published', published_virtual_model_id = $2, published_channel_id = $3, attached = $4, decided_at = now()
		 WHERE id = $1`,
		listingID, vmID, ch.ID, reuse,
	); err != nil {
		return nil, fmt.Errorf("pricesync: mark pending_model_listing published: %w", err)
	}
	// 免费候选：上架即视为运营已核实这条免费情报。
	if offerID != nil {
		if _, err := e.db(ctx).Exec(ctx,
			`UPDATE upstream_offers SET status = 'confirmed', decided_by_name = NULLIF($2, ''), decided_at = now()
			 WHERE id = $1 AND status = 'new'`, *offerID, in.DecidedByName); err != nil {
			return nil, fmt.Errorf("pricesync: confirm free offer: %w", err)
		}
	}
	return result, nil
}

// publishMarkupSellPrice 按 成本 × 折人民币系数 × (1+markup) 发布售价。免费模型成本为 0，售价也就是 0。
func (e *Engine) publishMarkupSellPrice(ctx context.Context, vmID int64, costComponents []admin.PriceComponentInput, costToCNY, markup decimal.Decimal) (int64, error) {
	sellComponents := make([]admin.PriceComponentInput, len(costComponents))
	markupFactor := decimal.NewFromInt(1).Add(markup)
	for i, c := range costComponents {
		sellComponents[i] = c
		sellComponents[i].UnitPrice = c.UnitPrice.Mul(costToCNY).Mul(markupFactor).Round(6)
	}
	sellBookID, err := e.publisher.SetSellPrice(ctx, admin.SetSellPriceInput{
		VirtualModelID: vmID, Components: sellComponents,
	})
	if err != nil {
		return 0, fmt.Errorf("pricesync: publish sell price: %w", err)
	}
	return sellBookID, nil
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
