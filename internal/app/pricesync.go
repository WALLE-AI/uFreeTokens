package app

import (
	"context"
	"net/http"
	"strings"

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
	var body createPriceSourceRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	if body.Schedule != "" && !admin.ValidSchedule(body.Schedule) {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "schedule must be a 5-field cron expression or @hourly/@daily/@every <duration>")
		return
	}
	if err := admin.CheckSourceConfig(body.Config); err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	id, err := audited(h, r, func(ctx context.Context) (int64, auditEntry, error) {
		id, err := h.pricesync.CreateSource(ctx, pricesync.CreateSourceInput{
			ProviderID: body.ProviderID, Level: pricesync.Level(body.Level), Kind: body.Kind, Fetcher: body.Fetcher, URL: body.URL,
			Domain: body.Domain, Name: body.Name, Schedule: body.Schedule, Config: body.Config, Enabled: body.Enabled,
			License: body.License, Attribution: body.Attribution, PublicDisplay: body.PublicDisplay, AutoPublish: body.AutoPublish,
		})
		if err != nil {
			return 0, auditEntry{}, err
		}
		return id, auditEntry{"price_source.create", "price_source", idStr(id), nil, body}, nil
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
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
	var body priceObservationRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	components := make([]pricesync.Component, 0, len(body.Components))
	for _, c := range body.Components {
		components = append(components, c.toComponent())
	}

	// 观测可能触发自动发布成本价，必须留审计（此前这条路径没有审计）。
	out, err := audited(h, r, func(ctx context.Context) (ingestResultDTO, auditEntry, error) {
		result, err := h.pricesync.Ingest(ctx, pricesync.IngestInput{
			ChannelID: channelID, SourceID: body.SourceID, Level: pricesync.Level(body.Level), UpstreamModel: body.UpstreamModel,
			Spec:      pricesync.PriceSpec{Currency: body.Currency, Components: components, EffectiveFrom: body.EffectiveFrom, ExpiresAt: body.ExpiresAt},
			RawObject: body.RawObject,
		})
		if err != nil {
			return ingestResultDTO{}, auditEntry{}, err
		}
		out := toIngestResultDTO(*result)
		return out, auditEntry{"price_observation.ingest", "channel", idStr(channelID), nil,
			map[string]any{"source_id": body.SourceID, "level": body.Level, "upstream_model": body.UpstreamModel, "result": out}}, nil
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	invalidateTodoCache()
	httpx.WriteJSON(w, http.StatusCreated, out)
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
	var body unmappedObservationRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	components := make([]pricesync.Component, 0, len(body.Components))
	for _, c := range body.Components {
		components = append(components, c.toComponent())
	}

	out, err := audited(h, r, func(ctx context.Context) (unmappedIngestResultDTO, auditEntry, error) {
		result, err := h.pricesync.IngestUnmapped(ctx, pricesync.UnmappedObservationInput{
			ProviderID: providerID, SourceID: body.SourceID, Level: pricesync.Level(body.Level), UpstreamModel: body.UpstreamModel,
			Spec:      pricesync.PriceSpec{Currency: body.Currency, Components: components, EffectiveFrom: body.EffectiveFrom, ExpiresAt: body.ExpiresAt},
			RawObject: body.RawObject,
		})
		if err != nil {
			return unmappedIngestResultDTO{}, auditEntry{}, err
		}
		out := toUnmappedIngestResultDTO(*result)
		return out, auditEntry{"price_observation.ingest", "provider", idStr(providerID), nil,
			map[string]any{"source_id": body.SourceID, "level": body.Level, "upstream_model": body.UpstreamModel, "result": out}}, nil
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	invalidateTodoCache()
	httpx.WriteJSON(w, http.StatusCreated, out)
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
	var body dismissListingRequest
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &body); err != nil {
			httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
			return
		}
	}
	if _, err := audited(h, r, func(ctx context.Context) (struct{}, auditEntry, error) {
		if err := h.pricesync.DismissListing(ctx, id); err != nil {
			return struct{}{}, auditEntry{}, err
		}
		return struct{}{}, auditEntry{"listing.dismiss", "pending_model_listing", idStr(id), map[string]string{"status": "pending"},
			map[string]string{"status": "dismissed", "reason": body.Reason}}, nil
	}); err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	invalidateTodoCache()
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
	var body publishListingRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	out, err := audited(h, r, func(ctx context.Context) (publishListingResultDTO, auditEntry, error) {
		result, err := h.pricesync.PublishListing(ctx, id, pricesync.PublishListingInput{
			VirtualModel: admin.CreateVirtualModelInput{
				Name: body.VirtualModel.Name, Family: body.VirtualModel.Family, Type: body.VirtualModel.Type,
				ContextWindow: body.VirtualModel.ContextWindow, MaxOutput: body.VirtualModel.MaxOutput,
				Capabilities: body.VirtualModel.Capabilities, VisibleTiers: body.VirtualModel.VisibleTiers,
			},
			ProviderAccountID: body.ProviderAccountID, SellMarkup: body.SellMarkup,
		})
		if err != nil {
			return publishListingResultDTO{}, auditEntry{}, err
		}
		out := toPublishListingResultDTO(*result)
		return out, auditEntry{"listing.publish", "pending_model_listing", idStr(id), map[string]string{"status": "pending"},
			map[string]any{"request": body, "result": out}}, nil
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	invalidateTodoCache()
	httpx.WriteJSON(w, http.StatusCreated, out)
}

// decideChangeRequestBody 是审批/驳回的请求体。审批人一律取已认证的管理员身份；
// DecidedBy 只为兼容旧调用方而保留解析，值被忽略。ConfirmBlocked 只对批准有效。
type decideChangeRequestBody struct {
	DecidedBy      int64  `json:"decided_by"`
	Reason         string `json:"reason"`
	ConfirmBlocked bool   `json:"confirm_blocked"`
}

func (b decideChangeRequestBody) meta(r *http.Request) pricesync.DecisionMeta {
	p := actor(r)
	return pricesync.DecisionMeta{By: p.AdminID, ByName: p.Name, Reason: b.Reason, ConfirmBlocked: b.ConfirmBlocked}
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
	bookID, err := audited(h, r, func(ctx context.Context) (int64, auditEntry, error) {
		bookID, err := h.pricesync.Approve(ctx, id, body.meta(r))
		if err != nil {
			return 0, auditEntry{}, err
		}
		return bookID, auditEntry{"price_change.approve", "price_change_request", idStr(id), nil,
			map[string]any{"applied_book_id": bookID, "reason": body.Reason, "confirm_blocked": body.ConfirmBlocked}}, nil
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	invalidateTodoCache()
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
	if strings.TrimSpace(body.Reason) == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "reason is required when rejecting a price change")
		return
	}
	if _, err := audited(h, r, func(ctx context.Context) (struct{}, auditEntry, error) {
		if err := h.pricesync.Reject(ctx, id, body.meta(r)); err != nil {
			return struct{}{}, auditEntry{}, err
		}
		return struct{}{}, auditEntry{"price_change.reject", "price_change_request", idStr(id), nil,
			map[string]any{"status": "rejected", "reason": body.Reason}}, nil
	}); err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	invalidateTodoCache()
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
}
