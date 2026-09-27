package app

import (
	"net/http"
	"strconv"
	"time"

	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
	"github.com/WALLE-AI/uFreeTokens/internal/pricesync"
	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
)

func (h *adminHandlers) createPriceSource(w http.ResponseWriter, r *http.Request) {
	if !h.requirePriceSync(w, r) {
		return
	}
	var body struct {
		ProviderID *int64 `json:"provider_id"`
		Level      string `json:"level"`
		Kind       string `json:"kind"`
		Fetcher    string `json:"fetcher"`
		URL        string `json:"url"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	id, err := h.pricesync.CreateSource(r.Context(), pricesync.CreateSourceInput{
		ProviderID: body.ProviderID, Level: pricesync.Level(body.Level), Kind: body.Kind, Fetcher: body.Fetcher, URL: body.URL,
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	h.recordAudit(r, "price_source.create", "price_source", strconv.FormatInt(id, 10), nil, body)
	httpx.WriteJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

// priceComponentJSON 是 PriceSpec.Components 的 HTTP 请求体形状，字段名和
// admin.PriceComponentInput 保持一致，方便调用方复用同一套心智模型。
type priceComponentJSON struct {
	Meter          string          `json:"meter"`
	Unit           string          `json:"unit"`
	ServiceTier    string          `json:"service_tier"`
	TierMinInput   int             `json:"tier_min_input"`
	TierMaxInput   *int            `json:"tier_max_input"`
	WindowStartMin *int16          `json:"window_start_min"`
	WindowEndMin   *int16          `json:"window_end_min"`
	UnitPrice      decimal.Decimal `json:"unit_price"`
}

func (c priceComponentJSON) toComponent() pricesync.Component {
	return pricesync.Component{
		Meter: pricing.Meter(c.Meter), Unit: pricing.Unit(c.Unit), ServiceTier: c.ServiceTier,
		TierMinInput: c.TierMinInput, TierMaxInput: c.TierMaxInput,
		WindowStartMin: c.WindowStartMin, WindowEndMin: c.WindowEndMin, UnitPrice: c.UnitPrice,
	}
}

// ingestPriceObservation 是技术方案 §7.16 同步流水线里"来了一条观测"的入口——
// 本阶段没有实现真正的调度/抓取（见 internal/pricesync 包级注释），这个接口
// 替代了那个角色：运营或者一个外部脚本把归一化好的价格 POST 到这里，走完
// 校验 -> diff -> 生效策略 -> （自动通过时）直接发布的完整流程。
func (h *adminHandlers) ingestPriceObservation(w http.ResponseWriter, r *http.Request) {
	if !h.requirePriceSync(w, r) {
		return
	}
	channelID, ok := pathInt64(r, "channelID")
	if !ok {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid channel id")
		return
	}
	var body struct {
		SourceID      int64                `json:"source_id"`
		Level         string               `json:"level"`
		UpstreamModel string               `json:"upstream_model"`
		Currency      string               `json:"currency"`
		Components    []priceComponentJSON `json:"components"`
		EffectiveFrom *time.Time           `json:"effective_from"`
		ExpiresAt     *time.Time           `json:"expires_at"`
		RawObject     string               `json:"raw_object"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	components := make([]pricesync.Component, 0, len(body.Components))
	for _, c := range body.Components {
		components = append(components, c.toComponent())
	}

	result, err := h.pricesync.Ingest(r.Context(), pricesync.IngestInput{
		ChannelID: channelID, SourceID: body.SourceID, Level: pricesync.Level(body.Level), UpstreamModel: body.UpstreamModel,
		Spec:      pricesync.PriceSpec{Currency: body.Currency, Components: components, EffectiveFrom: body.EffectiveFrom, ExpiresAt: body.ExpiresAt},
		RawObject: body.RawObject,
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	invalidateTodoCache()
	httpx.WriteJSON(w, http.StatusCreated, toIngestResultDTO(*result))
}

// ingestUnmappedPriceObservation 是技术方案 §7.16.3 Mapper 阶段的入口：不知道
// 具体渠道，只知道 provider + upstream_model。找到匹配渠道就对每个都走正常的
// Ingest 流程；一个都找不到就记入"新模型发现"队列（Phase 3），不自动上架。
func (h *adminHandlers) ingestUnmappedPriceObservation(w http.ResponseWriter, r *http.Request) {
	if !h.requirePriceSync(w, r) {
		return
	}
	providerID, ok := pathInt64(r, "providerID")
	if !ok {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid provider id")
		return
	}
	var body struct {
		SourceID      int64                `json:"source_id"`
		Level         string               `json:"level"`
		UpstreamModel string               `json:"upstream_model"`
		Currency      string               `json:"currency"`
		Components    []priceComponentJSON `json:"components"`
		EffectiveFrom *time.Time           `json:"effective_from"`
		ExpiresAt     *time.Time           `json:"expires_at"`
		RawObject     string               `json:"raw_object"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	components := make([]pricesync.Component, 0, len(body.Components))
	for _, c := range body.Components {
		components = append(components, c.toComponent())
	}

	result, err := h.pricesync.IngestUnmapped(r.Context(), pricesync.UnmappedObservationInput{
		ProviderID: providerID, SourceID: body.SourceID, Level: pricesync.Level(body.Level), UpstreamModel: body.UpstreamModel,
		Spec:      pricesync.PriceSpec{Currency: body.Currency, Components: components, EffectiveFrom: body.EffectiveFrom, ExpiresAt: body.ExpiresAt},
		RawObject: body.RawObject,
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	invalidateTodoCache()
	httpx.WriteJSON(w, http.StatusCreated, toUnmappedIngestResultDTO(*result))
}

func (h *adminHandlers) dismissPendingModelListing(w http.ResponseWriter, r *http.Request) {
	if !h.requirePriceSync(w, r) {
		return
	}
	id, ok := pathInt64(r, "listingID")
	if !ok {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid listing id")
		return
	}
	// body 可选（旧调用方不传 body）：只有 reason 一个字段，写进审计。
	var body struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &body); err != nil {
			httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
			return
		}
	}
	if err := h.pricesync.DismissListing(r.Context(), id); err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	invalidateTodoCache()
	h.recordAudit(r, "listing.dismiss", "pending_model_listing", strconv.FormatInt(id, 10), nil, map[string]string{"status": "dismissed", "reason": body.Reason})
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "dismissed"})
}

// publishPendingModelListing 是"一键上架"：运营补齐虚拟模型元数据、指定真正
// 用哪个 provider_account 路由、给个加价比例，一次调用建出虚拟模型 + 渠道 +
// 成本价 + 售价（技术方案 Phase 3"新模型自动发现与一键上架"）。
func (h *adminHandlers) publishPendingModelListing(w http.ResponseWriter, r *http.Request) {
	if !h.requirePriceSync(w, r) {
		return
	}
	id, ok := pathInt64(r, "listingID")
	if !ok {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid listing id")
		return
	}
	var body struct {
		VirtualModel struct {
			Name          string   `json:"name"`
			Family        string   `json:"family"`
			Type          string   `json:"type"`
			ContextWindow int      `json:"context_window"`
			MaxOutput     int      `json:"max_output"`
			Capabilities  []string `json:"capabilities"`
			VisibleTiers  []string `json:"visible_tiers"`
		} `json:"virtual_model"`
		ProviderAccountID int64           `json:"provider_account_id"`
		SellMarkup        decimal.Decimal `json:"sell_markup"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	result, err := h.pricesync.PublishListing(r.Context(), id, pricesync.PublishListingInput{
		VirtualModel: admin.CreateVirtualModelInput{
			Name: body.VirtualModel.Name, Family: body.VirtualModel.Family, Type: body.VirtualModel.Type,
			ContextWindow: body.VirtualModel.ContextWindow, MaxOutput: body.VirtualModel.MaxOutput,
			Capabilities: body.VirtualModel.Capabilities, VisibleTiers: body.VirtualModel.VisibleTiers,
		},
		ProviderAccountID: body.ProviderAccountID, SellMarkup: body.SellMarkup,
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	out := toPublishListingResultDTO(*result)
	invalidateTodoCache()
	h.recordAudit(r, "listing.publish", "pending_model_listing", strconv.FormatInt(id, 10), nil, map[string]any{"request": body, "result": out})
	httpx.WriteJSON(w, http.StatusCreated, out)
}

// decideChangeRequestBody 是审批/驳回的请求体。DecidedBy 保留以兼容旧调用方，
// 但优先使用 X-Actor-ID（运营后台接口方案 §5.3）；ConfirmBlocked 只对批准有效。
type decideChangeRequestBody struct {
	DecidedBy      int64  `json:"decided_by"`
	Reason         string `json:"reason"`
	ConfirmBlocked bool   `json:"confirm_blocked"`
}

func (b decideChangeRequestBody) meta(r *http.Request) pricesync.DecisionMeta {
	by := actorIDFromRequest(r)
	if by == 0 {
		by = b.DecidedBy
	}
	return pricesync.DecisionMeta{By: by, ByName: actorNameFromRequest(r), Reason: b.Reason, ConfirmBlocked: b.ConfirmBlocked}
}

func (h *adminHandlers) approveChangeRequest(w http.ResponseWriter, r *http.Request) {
	if !h.requirePriceSync(w, r) {
		return
	}
	id, ok := pathInt64(r, "changeRequestID")
	if !ok {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid change request id")
		return
	}
	var body decideChangeRequestBody
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	bookID, err := h.pricesync.Approve(r.Context(), id, body.meta(r))
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	invalidateTodoCache()
	h.recordAudit(r, "price_change.approve", "price_change_request", strconv.FormatInt(id, 10), nil,
		map[string]any{"applied_book_id": bookID, "reason": body.Reason, "confirm_blocked": body.ConfirmBlocked})
	httpx.WriteJSON(w, http.StatusOK, map[string]int64{"applied_book_id": bookID})
}

func (h *adminHandlers) rejectChangeRequest(w http.ResponseWriter, r *http.Request) {
	if !h.requirePriceSync(w, r) {
		return
	}
	id, ok := pathInt64(r, "changeRequestID")
	if !ok {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid change request id")
		return
	}
	var body decideChangeRequestBody
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	if err := h.pricesync.Reject(r.Context(), id, body.meta(r)); err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	invalidateTodoCache()
	h.recordAudit(r, "price_change.reject", "price_change_request", strconv.FormatInt(id, 10), nil,
		map[string]any{"status": "rejected", "reason": body.Reason})
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
}
