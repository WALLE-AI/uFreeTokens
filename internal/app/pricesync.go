package app

import (
	"net/http"
	"time"

	"github.com/shopspring/decimal"

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
	httpx.WriteJSON(w, http.StatusCreated, result)
}

func (h *adminHandlers) listPendingChangeRequests(w http.ResponseWriter, r *http.Request) {
	if !h.requirePriceSync(w, r) {
		return
	}
	list, err := h.pricesync.ListPending(r.Context())
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"change_requests": list})
}

type decideChangeRequestBody struct {
	DecidedBy int64 `json:"decided_by"`
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
	bookID, err := h.pricesync.Approve(r.Context(), id, body.DecidedBy)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
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
	if err := h.pricesync.Reject(r.Context(), id, body.DecidedBy); err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
}
