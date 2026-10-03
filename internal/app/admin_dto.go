package app

import (
	"github.com/WALLE-AI/uFreeTokens/internal/pricesync"
	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

// 控制面响应 DTO（运营后台接口方案 §0.1、§9）。
//
// internal/admin 的结构体直接带 snake_case json tag；这里只放**不能**直接加
// tag 的类型：pricesync.PriceSpec/Component/ComponentDiff 同时被序列化进
// price_change_requests.proposed_spec/diff、pending_model_listings.observed_spec
// 等 JSONB 列（历史数据是 PascalCase 键），给它们加 tag 会让读取历史行时
// UnitPrice、TierMinInput 这类多词字段静默丢失；wallet.Receipt 属于计费主链路，
// 不为控制面的展示需求改它。

type ingestResultDTO struct {
	ObservationID   int64  `json:"observation_id"`
	ChangeRequestID *int64 `json:"change_request_id"`
	Decision        string `json:"decision"`
	AppliedBookID   *int64 `json:"applied_book_id"`
}

func toIngestResultDTO(r pricesync.IngestResult) ingestResultDTO {
	return ingestResultDTO{ObservationID: r.ObservationID, ChangeRequestID: r.ChangeRequestID, Decision: string(r.Decision), AppliedBookID: r.AppliedBookID}
}

type unmappedIngestResultDTO struct {
	MappedResults []ingestResultDTO `json:"mapped_results"`
	ListingID     *int64            `json:"listing_id"`
}

func toUnmappedIngestResultDTO(r pricesync.UnmappedIngestResult) unmappedIngestResultDTO {
	out := unmappedIngestResultDTO{ListingID: r.ListingID, MappedResults: make([]ingestResultDTO, 0, len(r.MappedResults))}
	for _, m := range r.MappedResults {
		out.MappedResults = append(out.MappedResults, toIngestResultDTO(m))
	}
	return out
}

type publishListingResultDTO struct {
	VirtualModelID int64 `json:"virtual_model_id"`
	ChannelID      int64 `json:"channel_id"`
	CostBookID     int64 `json:"cost_book_id"`
	SellBookID     int64 `json:"sell_book_id"`
	// MetadataCreated：本次上架为虚拟模型自动生成了展示元数据（原本没有）。
	MetadataCreated bool `json:"metadata_created"`
}

func toPublishListingResultDTO(r pricesync.PublishListingResult) publishListingResultDTO {
	return publishListingResultDTO{VirtualModelID: r.VirtualModelID, ChannelID: r.ChannelID, CostBookID: r.CostBookID, SellBookID: r.SellBookID, MetadataCreated: r.MetadataCreated}
}

// walletAdjustDTO 是人工调账的回执。wallet.Adjust 复用了计费回执结构，
// ChargedAmount 是"扣了多少"（= -调账金额），这里翻回调账方向，避免运营看到
// "充值 100 元，amount = -100"这种反直觉的数字。
type walletAdjustDTO struct {
	RefID           string `json:"ref_id"`
	AccountID       int64  `json:"account_id"`
	AmountMicro     int64  `json:"amount_micro"`
	CashAfterMicro  int64  `json:"cash_after_micro"`
	BonusAfterMicro int64  `json:"bonus_after_micro"`
}

func toWalletAdjustDTO(r wallet.Receipt) walletAdjustDTO {
	return walletAdjustDTO{RefID: r.RequestID, AccountID: r.AccountID, AmountMicro: -r.ChargedAmount, CashAfterMicro: r.CashAfter, BonusAfterMicro: r.BonusAfter}
}
