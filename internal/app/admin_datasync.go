package app

import (
	"context"
	"net/http"
	"time"

	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
	"github.com/WALLE-AI/uFreeTokens/internal/offers"
)

// 外部数据采集的运营接口（docs/外部数据采集模块（价格情报与评测榜单）技术方案.md §3.3、§4.4）：
// 数据源立即运行 / 运行历史、上游优惠情报（优惠雷达）、比价看板、评测榜单模型名映射工作台。
// 抓取本身由 worker 执行，这里只读写状态。

// pageWindow 把 page/page_size 归一化成 limit/offset（与 admin.PageRequest 同样的默认值与上限）。
func pageWindow(p admin.PageRequest) (limit, offset, page int) {
	if p.Page < 1 {
		p.Page = 1
	}
	if p.PageSize < 1 {
		p.PageSize = 20
	}
	if p.PageSize > 100 {
		p.PageSize = 100
	}
	return p.PageSize, (p.Page - 1) * p.PageSize, p.Page
}

// POST /price-sources/{id}/run
func (h *adminHandlers) runPriceSourceNow(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "priceSourceID", "price source")
	if !ok {
		return
	}
	src, err := audited(h, r, func(ctx context.Context) (*admin.PriceSourceInfo, auditEntry, error) {
		src, err := h.svc.RunPriceSourceNow(ctx, id)
		if err != nil {
			return nil, auditEntry{}, err
		}
		return src, auditEntry{"price_source.run", "price_source", idStr(id), nil, map[string]any{"next_run_at": src.NextRunAt}}, nil
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, src)
}

// GET /price-sources/{id}/runs?limit=
func (h *adminHandlers) listDataSourceRuns(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "priceSourceID", "price source")
	if !ok {
		return
	}
	q := &queryParser{r: r}
	limit := q.int("limit")
	if !q.ok(w) {
		return
	}
	runs, err := h.svc.ListDataSourceRuns(r.Context(), id, limit)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": runs})
}

// ---------- 优惠雷达 ----------

func (h *adminHandlers) requireOffers(w http.ResponseWriter, r *http.Request) bool {
	if h.offers == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "not_implemented", "Upstream offers are not configured on this server.")
		return false
	}
	return true
}

// GET /upstream-offers?status=&offer_type=&provider_code=&q=&page=&page_size=
func (h *adminHandlers) listUpstreamOffers(w http.ResponseWriter, r *http.Request) {
	if !h.requireOffers(w, r) {
		return
	}
	q := &queryParser{r: r}
	in := offers.ListInput{
		Status: q.enum("status", offers.Statuses...), OfferType: q.enum("offer_type", offers.Types...),
		ProviderCode: q.str("provider_code"), Query: q.str("q"),
	}
	p := q.page()
	if !q.ok(w) {
		return
	}
	var page int
	in.Limit, in.Offset, page = pageWindow(p)
	list, total, err := h.offers.List(r.Context(), in)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, admin.Page[offers.Offer]{Data: list, Total: total, Page: page, PageSize: in.Limit})
}

// GET /upstream-offers/{offerID}
func (h *adminHandlers) getUpstreamOffer(w http.ResponseWriter, r *http.Request) {
	if !h.requireOffers(w, r) {
		return
	}
	id, ok := pathID(w, r, "offerID", "offer")
	if !ok {
		return
	}
	o, err := h.offers.Get(r.Context(), id)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, o)
}

type setOfferStatusRequest struct {
	Status string `json:"status"` // confirmed / ignored / new
}

// POST /upstream-offers/{offerID}/status
func (h *adminHandlers) setUpstreamOfferStatus(w http.ResponseWriter, r *http.Request) {
	if !h.requireOffers(w, r) {
		return
	}
	id, ok := pathID(w, r, "offerID", "offer")
	if !ok {
		return
	}
	var body setOfferStatusRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	o, err := audited(h, r, func(ctx context.Context) (*offers.Offer, auditEntry, error) {
		before, err := h.offers.Get(ctx, id)
		if err != nil {
			return nil, auditEntry{}, err
		}
		o, err := h.offers.SetStatus(ctx, id, body.Status, actor(r).Name)
		if err != nil {
			return nil, auditEntry{}, err
		}
		return o, auditEntry{"upstream_offer.status", "upstream_offer", idStr(id),
			map[string]string{"status": before.Status}, map[string]string{"status": o.Status}}, nil
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	invalidateTodoCache()
	httpx.WriteJSON(w, http.StatusOK, o)
}

type adoptOfferRequest struct {
	Side          string           `json:"side"` // cost / sell
	ChannelID     *int64           `json:"channel_id"`
	VirtualModel  string           `json:"virtual_model"`
	Name          string           `json:"name"`
	DiscountRatio *decimal.Decimal `json:"discount_ratio"`
	StartsAt      *time.Time       `json:"starts_at"`
	EndsAt        *time.Time       `json:"ends_at"`
	Priority      int              `json:"priority"`
	BudgetTotal   *int64           `json:"budget_total"`
}

type adoptOfferResponse struct {
	PromotionID int64 `json:"promotion_id"`
}

// POST /upstream-offers/{offerID}/adopt：把情报采用为一条 promotions（side=sell 会立即参与计费）。
func (h *adminHandlers) adoptUpstreamOffer(w http.ResponseWriter, r *http.Request) {
	if !h.requireOffers(w, r) {
		return
	}
	id, ok := pathID(w, r, "offerID", "offer")
	if !ok {
		return
	}
	var body adoptOfferRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	promotionID, err := audited(h, r, func(ctx context.Context) (int64, auditEntry, error) {
		pid, err := h.offers.Adopt(ctx, id, offers.AdoptInput{
			Side: body.Side, ChannelID: body.ChannelID, VirtualModel: body.VirtualModel, Name: body.Name,
			DiscountRatio: body.DiscountRatio, StartsAt: body.StartsAt, EndsAt: body.EndsAt, Priority: body.Priority,
			BudgetTotal: body.BudgetTotal, DecidedByName: actor(r).Name,
		})
		if err != nil {
			return 0, auditEntry{}, err
		}
		return pid, auditEntry{"upstream_offer.adopt", "upstream_offer", idStr(id), nil,
			map[string]any{"request": body, "promotion_id": pid}}, nil
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	invalidateTodoCache()
	httpx.WriteJSON(w, http.StatusCreated, adoptOfferResponse{PromotionID: promotionID})
}

// ---------- 比价看板 ----------

// GET /pricesync/price-comparison?q=&page=&page_size=
func (h *adminHandlers) priceComparison(w http.ResponseWriter, r *http.Request) {
	q := &queryParser{r: r}
	query := q.str("q")
	p := q.page()
	if !q.ok(w) {
		return
	}
	limit, offset, page := pageWindow(p)
	rows, total, err := h.svc.PriceComparison(r.Context(), admin.PriceComparisonInput{Query: query, Limit: limit, Offset: offset})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, admin.Page[admin.PriceComparisonRow]{Data: rows, Total: total, Page: page, PageSize: limit})
}

// ---------- 模型名映射 ----------

// GET /model-aliases?namespace=&status=&q=&page=&page_size=
func (h *adminHandlers) listModelAliases(w http.ResponseWriter, r *http.Request) {
	q := &queryParser{r: r}
	in := admin.ListModelAliasesInput{Namespace: q.str("namespace"), Status: q.enum("status", admin.ModelAliasStatuses...), Query: q.str("q")}
	p := q.page()
	if !q.ok(w) {
		return
	}
	var page int
	in.Limit, in.Offset, page = pageWindow(p)
	list, total, err := h.svc.ListModelAliases(r.Context(), in)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, admin.Page[admin.ModelAlias]{Data: list, Total: total, Page: page, PageSize: in.Limit})
}

// GET /model-aliases/namespaces
func (h *adminHandlers) listModelAliasNamespaces(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.ModelAliasNamespaces(r.Context())
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": list})
}

// PUT /model-aliases：人工确认 / 忽略 / 交回自动匹配，并重新关联已导入的榜单结果。
func (h *adminHandlers) setModelAlias(w http.ResponseWriter, r *http.Request) {
	var body admin.SetModelAliasInput
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	res, err := audited(h, r, func(ctx context.Context) (*admin.SetModelAliasResult, auditEntry, error) {
		res, err := h.svc.SetModelAlias(ctx, body, actor(r).Name)
		if err != nil {
			return nil, auditEntry{}, err
		}
		return res, auditEntry{"model_alias.set", "model_alias", body.Namespace + ":" + body.ExternalLabel, nil, res}, nil
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	invalidateTodoCache()
	httpx.WriteJSON(w, http.StatusOK, res)
}
