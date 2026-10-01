package app

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/adminauth"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
	"github.com/WALLE-AI/uFreeTokens/internal/pricesync"
)

// 字典、计数、价格预览与批量操作（B7），替代前端硬编码枚举、多次计数请求、
// 浏览器里的浮点价格计算和逐条循环调用。

type metaEnumsResponse struct {
	admin.Enums
	Permissions []adminauth.Permission `json:"permissions"`
}

func (h *adminHandlers) metaEnums(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, metaEnumsResponse{Enums: admin.EnumValues(), Permissions: adminauth.AllPermissions})
}

func (h *adminHandlers) catalogCounts(w http.ResponseWriter, r *http.Request) {
	c, err := h.svc.CatalogCounts(r.Context())
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c)
}

func (h *adminHandlers) pricingPreview(w http.ResponseWriter, r *http.Request) {
	var in admin.PricingPreviewInput
	if err := decodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	res, err := h.svc.PricingPreview(r.Context(), in)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *adminHandlers) getPriceSource(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "priceSourceID", "price source")
	if !ok {
		return
	}
	ps, err := h.svc.GetPriceSource(r.Context(), id)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	h.setETag(w, r, "price_sources", id)
	httpx.WriteJSON(w, http.StatusOK, ps)
}

type batchItemResult struct {
	ID    int64     `json:"id"`
	OK    bool      `json:"ok"`
	Error *apiError `json:"error,omitempty"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// batchDismissListings 批量忽略待上架模型：逐条独立处理，部分失败不影响其余，
// 每条结果单独返回（与 batch-approve 的语义一致）。
func (h *adminHandlers) batchDismissListings(w http.ResponseWriter, r *http.Request) {
	if !h.requirePriceSync(w, r) {
		return
	}
	var body batchDismissRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	if len(body.IDs) == 0 || len(body.IDs) > 200 {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "ids must contain 1-200 items")
		return
	}
	results := make([]batchItemResult, 0, len(body.IDs))
	for _, id := range body.IDs {
		_, err := audited(h, r, func(ctx context.Context) (struct{}, auditEntry, error) {
			if err := h.pricesync.DismissListing(ctx, id); err != nil {
				return struct{}{}, auditEntry{}, err
			}
			return struct{}{}, auditEntry{"listing.dismiss", "pending_model_listing", idStr(id), map[string]any{"status": "pending"},
				map[string]any{"status": "dismissed", "reason": body.Reason, "batch": true}}, nil
		})
		if err != nil {
			code := "conflict"
			if errors.Is(err, pricesync.ErrListingNotFound) {
				code = "not_found"
			}
			results = append(results, batchItemResult{ID: id, Error: &apiError{Code: code, Message: err.Error()}})
			continue
		}
		results = append(results, batchItemResult{ID: id, OK: true})
	}
	invalidateTodoCache()
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"results": results})
}

// channelHealth 是 GET /channels/health（方案 G9）：active 渠道最近 ?window_minutes=（默认 15，
// 最长 1440）的请求量、错误率、P95，加上网关熔断与 Key 冷却状态和最近的健康事件。
func (h *adminHandlers) channelHealth(w http.ResponseWriter, r *http.Request) {
	q := &queryParser{r: r}
	minutes := q.int("window_minutes")
	if !q.ok(w) {
		return
	}
	if minutes <= 0 {
		minutes = 15
	}
	if minutes > 1440 {
		minutes = 1440
	}
	rep, err := h.svc.ChannelHealth(r.Context(), h.redis, time.Duration(minutes)*time.Minute, admin.DefaultHealthThresholds)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rep)
}
