package app

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
	"github.com/WALLE-AI/uFreeTokens/internal/pricesync"
)

// 运营后台（frontend/admin）的列表 / 详情 / 编辑接口，见
// docs/cmd-admin 运营后台接口补全技术方案.md §1、§2、§5、§7。
// 与 admin.go 里的创建类接口共用同一个鉴权分组。

func (h *adminHandlers) registerConsoleRoutes(r chi.Router) {
	r.Get("/providers", h.listProviders)
	r.Get("/providers/{providerID}", h.getProvider)
	r.Patch("/providers/{providerID}", h.updateProvider)

	r.Get("/provider-accounts", h.listProviderAccounts)
	r.Get("/provider-accounts/{providerAccountID}", h.getProviderAccount)
	r.Patch("/provider-accounts/{providerAccountID}", h.updateProviderAccount)
	r.Patch("/provider-keys/{providerKeyID}", h.updateProviderKey)
	r.Post("/provider-keys/{providerKeyID}/revoke", h.revokeProviderKey)

	r.Get("/virtual-models/{virtualModelID}", h.getVirtualModel)
	r.Patch("/virtual-models/{virtualModelID}", h.updateVirtualModel)
	r.Get("/virtual-models/{virtualModelID}/price-books", h.listSellPriceBooks)

	r.Get("/channels/{channelID}", h.getChannel)
	r.Patch("/channels/{channelID}", h.updateChannel)
	r.Get("/channels/{channelID}/price-books", h.listCostPriceBooks)

	r.Get("/fx-rates", h.listFXRates)
	r.Get("/fx-rates/latest", h.listLatestFXRates)
	r.Get("/price-sources", h.listPriceSources)
	r.Patch("/price-sources/{priceSourceID}", h.updatePriceSource)

	r.Patch("/accounts/{accountID}", h.updateAccount)

	r.Get("/price-change-requests/{changeRequestID}", h.getChangeRequest)
	r.Post("/price-change-requests/batch-approve", h.batchApproveChangeRequests)

	r.Get("/todo-counts", h.getTodoCounts)
}

// ---------- 查询参数解析 ----------

// queryParser 累积第一个解析错误，handler 在读完全部参数后统一检查一次。
type queryParser struct {
	r   *http.Request
	err error
}

func (p *queryParser) str(key string) string { return strings.TrimSpace(p.r.URL.Query().Get(key)) }

func (p *queryParser) int64(key string) int64 {
	v := p.str(key)
	if v == "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil && p.err == nil {
		p.err = errors.New("invalid integer query param '" + key + "'")
	}
	return n
}

func (p *queryParser) int(key string) int { return int(p.int64(key)) }

func (p *queryParser) boolean(key string) bool {
	v := p.str(key)
	return v == "true" || v == "1"
}

func (p *queryParser) optBool(key string) *bool {
	v := p.str(key)
	if v == "" {
		return nil
	}
	b := v == "true" || v == "1"
	return &b
}

func (p *queryParser) csv(key string) []string {
	v := p.str(key)
	if v == "" {
		return nil
	}
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (p *queryParser) page() admin.PageRequest {
	return admin.PageRequest{Page: p.int("page"), PageSize: p.int("page_size")}
}

func (p *queryParser) ok(w http.ResponseWriter) bool {
	if p.err != nil {
		httpx.WriteError(w, p.r, http.StatusBadRequest, "invalid_request", p.err.Error())
		return false
	}
	return true
}

// pathID 读路径参数，失败时直接写 400 并返回 false。
func pathID(w http.ResponseWriter, r *http.Request, key, what string) (int64, bool) {
	id, ok := pathInt64(r, key)
	if !ok {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid "+what+" id")
	}
	return id, ok
}

// patchHandler 是所有 PATCH 接口的共同流程：解析 body → 执行更新 → 写审计
// （before/after 只含改动字段）→ 返回更新后的对象。
func patchHandler[In any, Out any](h *adminHandlers, w http.ResponseWriter, r *http.Request, id int64, action, targetType string,
	update func(context.Context, int64, In) (*admin.Change, error), reload func(context.Context, int64) (Out, error)) {
	var in In
	if err := decodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	change, err := update(r.Context(), id, in)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	h.recordAudit(r, action, targetType, strconv.FormatInt(id, 10), change.Before, change.After)
	invalidateTodoCache()
	out, err := reload(r.Context(), id)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// ---------- providers ----------

func (h *adminHandlers) listProviders(w http.ResponseWriter, r *http.Request) {
	q := &queryParser{r: r}
	in := admin.ListProvidersInput{Q: q.str("q"), Status: q.str("status"), Protocol: q.str("protocol"), Sort: q.str("sort"), PageRequest: q.page()}
	if !q.ok(w) {
		return
	}
	page, err := h.svc.ListProviders(r.Context(), in)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

func (h *adminHandlers) getProvider(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "providerID", "provider")
	if !ok {
		return
	}
	p, err := h.svc.GetProvider(r.Context(), id)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

// providerUpdateResult 在返回最新供应商信息的同时，告诉前端停用后还有多少
// active 渠道仍在使用它（停用供应商不级联停用渠道，接口方案 §2）。
type providerUpdateResult struct {
	*admin.ProviderDetail
	AffectedActiveChannels int `json:"affected_active_channels"`
}

func (h *adminHandlers) updateProvider(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "providerID", "provider")
	if !ok {
		return
	}
	patchHandler(h, w, r, id, "provider.update", "provider", h.svc.UpdateProvider,
		func(ctx context.Context, id int64) (providerUpdateResult, error) {
			p, err := h.svc.GetProvider(ctx, id)
			if err != nil {
				return providerUpdateResult{}, err
			}
			n, err := h.svc.ActiveChannelCountForProvider(ctx, id)
			return providerUpdateResult{ProviderDetail: p, AffectedActiveChannels: n}, err
		})
}

func (h *adminHandlers) listProviderAccounts(w http.ResponseWriter, r *http.Request) {
	q := &queryParser{r: r}
	in := admin.ListProviderAccountsInput{ProviderID: q.int64("provider_id"), Q: q.str("q"), Status: q.str("status"), PageRequest: q.page()}
	if !q.ok(w) {
		return
	}
	page, err := h.svc.ListProviderAccounts(r.Context(), in)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

func (h *adminHandlers) getProviderAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "providerAccountID", "provider account")
	if !ok {
		return
	}
	a, err := h.svc.GetProviderAccount(r.Context(), id)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, a)
}

func (h *adminHandlers) updateProviderAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "providerAccountID", "provider account")
	if !ok {
		return
	}
	patchHandler(h, w, r, id, "provider_account.update", "provider_account", h.svc.UpdateProviderAccount, h.svc.GetProviderAccount)
}

func (h *adminHandlers) updateProviderKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "providerKeyID", "provider key")
	if !ok {
		return
	}
	patchHandler(h, w, r, id, "provider_key.update", "provider_key", h.svc.UpdateProviderKey, h.svc.GetProviderKey)
}

func (h *adminHandlers) revokeProviderKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "providerKeyID", "provider key")
	if !ok {
		return
	}
	change, err := h.svc.RevokeProviderKey(r.Context(), id)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	h.recordAudit(r, "provider_key.revoke", "provider_key", strconv.FormatInt(id, 10), change.Before, change.After)
	k, err := h.svc.GetProviderKey(r.Context(), id)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, k)
}

// ---------- virtual models ----------

// virtualModels 兼容旧行为：带 name 参数时是精确查找（返回单个对象或 404），
// frontend/test_web/admin.html 的幂等导入依赖这一点；否则返回列表。
func (h *adminHandlers) virtualModels(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Has("name") {
		h.getVirtualModelByName(w, r)
		return
	}
	q := &queryParser{r: r}
	in := admin.ListVirtualModelsInput{
		Q: q.str("q"), Statuses: q.csv("status"), Type: q.str("type"), Family: q.str("family"), Tier: q.str("tier"),
		Missing: q.str("missing"), NegativeMargin: q.str("margin") == "negative", Sort: q.str("sort"), PageRequest: q.page(),
	}
	if m := q.str("margin"); m != "" && m != "negative" && q.err == nil {
		q.err = errors.New("margin only supports 'negative'")
	}
	if !q.ok(w) {
		return
	}
	page, err := h.svc.ListVirtualModels(r.Context(), in)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

func (h *adminHandlers) getVirtualModel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "virtualModelID", "virtual model")
	if !ok {
		return
	}
	vm, err := h.svc.GetVirtualModel(r.Context(), id)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, vm)
}

func (h *adminHandlers) updateVirtualModel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "virtualModelID", "virtual model")
	if !ok {
		return
	}
	patchHandler(h, w, r, id, "virtual_model.update", "virtual_model", h.svc.UpdateVirtualModel, h.svc.GetVirtualModel)
}

func (h *adminHandlers) listSellPriceBooks(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "virtualModelID", "virtual model")
	if !ok {
		return
	}
	q := &queryParser{r: r}
	limit := q.int("limit")
	if !q.ok(w) {
		return
	}
	books, err := h.svc.ListPriceBooks(r.Context(), admin.ListPriceBooksInput{Kind: "sell", VirtualModelID: id, Limit: limit})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": books})
}

// ---------- channels ----------

// channels 兼容旧行为：virtual_model_id + provider_account_id + upstream_model
// 三个参数同时出现时是精确查找（返回单个对象或 404）；否则返回列表。
func (h *adminHandlers) channels(w http.ResponseWriter, r *http.Request) {
	qv := r.URL.Query()
	if qv.Has("virtual_model_id") && qv.Has("provider_account_id") && qv.Has("upstream_model") {
		h.findChannel(w, r)
		return
	}
	q := &queryParser{r: r}
	in := admin.ListChannelsInput{
		VirtualModelID: q.int64("virtual_model_id"), ProviderAccountID: q.int64("provider_account_id"), ProviderID: q.int64("provider_id"),
		Status: q.str("status"), Q: q.str("q"),
		NegativeMargin: q.str("margin") == "negative", MissingCost: q.boolean("missing_cost"), Dedicated: q.boolean("dedicated"),
		Sort: q.str("sort"), PageRequest: q.page(),
	}
	if m := q.str("margin"); m != "" && m != "negative" && q.err == nil {
		q.err = errors.New("margin only supports 'negative'")
	}
	if !q.ok(w) {
		return
	}
	page, err := h.svc.ListChannels(r.Context(), in)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

func (h *adminHandlers) getChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "channelID", "channel")
	if !ok {
		return
	}
	ch, err := h.svc.GetChannel(r.Context(), id)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ch)
}

func (h *adminHandlers) updateChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "channelID", "channel")
	if !ok {
		return
	}
	patchHandler(h, w, r, id, "channel.update", "channel", h.svc.UpdateChannel, h.svc.GetChannel)
}

func (h *adminHandlers) listCostPriceBooks(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "channelID", "channel")
	if !ok {
		return
	}
	q := &queryParser{r: r}
	limit := q.int("limit")
	if !q.ok(w) {
		return
	}
	books, err := h.svc.ListPriceBooks(r.Context(), admin.ListPriceBooksInput{Kind: "cost", ChannelID: id, Limit: limit})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": books})
}

// ---------- fx rates / price sources / accounts ----------

func (h *adminHandlers) listFXRates(w http.ResponseWriter, r *http.Request) {
	q := &queryParser{r: r}
	base, quote, limit := strings.ToUpper(q.str("base")), strings.ToUpper(q.str("quote")), q.int("limit")
	if !q.ok(w) {
		return
	}
	rates, err := h.svc.ListFXRates(r.Context(), base, quote, false, limit)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": rates})
}

func (h *adminHandlers) listLatestFXRates(w http.ResponseWriter, r *http.Request) {
	rates, err := h.svc.ListFXRates(r.Context(), "", "", true, 1)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": rates})
}

func (h *adminHandlers) listPriceSources(w http.ResponseWriter, r *http.Request) {
	q := &queryParser{r: r}
	in := admin.ListPriceSourcesInput{ProviderID: q.int64("provider_id"), Enabled: q.optBool("enabled")}
	if !q.ok(w) {
		return
	}
	list, err := h.svc.ListPriceSources(r.Context(), in)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": list})
}

func (h *adminHandlers) updatePriceSource(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "priceSourceID", "price source")
	if !ok {
		return
	}
	patchHandler(h, w, r, id, "price_source.update", "price_source", h.svc.UpdatePriceSource, h.svc.GetPriceSource)
}

func (h *adminHandlers) updateAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "accountID", "account")
	if !ok {
		return
	}
	patchHandler(h, w, r, id, "account.update", "account", h.svc.UpdateAccount,
		func(ctx context.Context, id int64) (map[string]any, error) {
			acct, wal, err := h.svc.GetAccount(ctx, id)
			return map[string]any{"account": acct, "wallet": wal}, err
		})
}

// ---------- 调价审批 / 待上架 ----------

func (h *adminHandlers) listChangeRequests(w http.ResponseWriter, r *http.Request) {
	if !h.requirePriceSync(w, r) {
		return
	}
	q := &queryParser{r: r}
	in := admin.ListChangeRequestsInput{
		Statuses: q.csv("status"), Direction: q.str("direction"), ChannelID: q.int64("channel_id"), ProviderID: q.int64("provider_id"),
		Sort: q.str("sort"), PageRequest: q.page(),
	}
	if !q.ok(w) {
		return
	}
	page, err := h.svc.ListChangeRequests(r.Context(), in)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

func (h *adminHandlers) getChangeRequest(w http.ResponseWriter, r *http.Request) {
	if !h.requirePriceSync(w, r) {
		return
	}
	id, ok := pathID(w, r, "changeRequestID", "change request")
	if !ok {
		return
	}
	d, err := h.svc.GetChangeRequest(r.Context(), id)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

// maxBatchApproveRatio 是批量批准允许的最大变化幅度上限（接口方案 §5.4）。
var maxBatchApproveRatio = decimal.RequireFromString("0.2")

type batchApproveResult struct {
	ID            int64  `json:"id"`
	OK            bool   `json:"ok"`
	AppliedBookID *int64 `json:"applied_book_id,omitempty"`
	Error         *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// batchApproveChangeRequests 逐条独立批准：只允许 pending（不允许 blocked），且
// |max_change_ratio| 不超过请求给出的阈值（阈值本身上限 0.2）。部分失败不回滚
// 已成功的条目，每条的结果单独返回。
func (h *adminHandlers) batchApproveChangeRequests(w http.ResponseWriter, r *http.Request) {
	if !h.requirePriceSync(w, r) {
		return
	}
	var body struct {
		IDs               []int64         `json:"ids"`
		Reason            string          `json:"reason"`
		MaxAbsChangeRatio decimal.Decimal `json:"max_abs_change_ratio"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	if len(body.IDs) == 0 || len(body.IDs) > 100 {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "ids must contain 1-100 items")
		return
	}
	if !body.MaxAbsChangeRatio.IsPositive() || body.MaxAbsChangeRatio.GreaterThan(maxBatchApproveRatio) {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "max_abs_change_ratio must be in (0, 0.2]")
		return
	}
	meta := decideChangeRequestBody{Reason: body.Reason}.meta(r)
	results := make([]batchApproveResult, 0, len(body.IDs))
	fail := func(id int64, code, msg string) {
		res := batchApproveResult{ID: id}
		res.Error = &struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}{code, msg}
		results = append(results, res)
	}
	for _, id := range body.IDs {
		d, err := h.svc.GetChangeRequest(r.Context(), id)
		if err != nil {
			fail(id, "not_found", err.Error())
			continue
		}
		if d.Status != "pending" {
			fail(id, "conflict", "only pending change requests can be batch-approved (status is "+d.Status+")")
			continue
		}
		if d.MaxChangeRatio.Abs().GreaterThan(body.MaxAbsChangeRatio) {
			fail(id, "conflict", "change ratio exceeds max_abs_change_ratio")
			continue
		}
		bookID, err := h.pricesync.Approve(r.Context(), id, meta)
		if err != nil {
			fail(id, "conflict", err.Error())
			continue
		}
		h.recordAudit(r, "price_change.approve", "price_change_request", strconv.FormatInt(id, 10), nil,
			map[string]any{"applied_book_id": bookID, "reason": body.Reason, "batch": true})
		results = append(results, batchApproveResult{ID: id, OK: true, AppliedBookID: &bookID})
	}
	invalidateTodoCache()
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (h *adminHandlers) listPendingListings(w http.ResponseWriter, r *http.Request) {
	if !h.requirePriceSync(w, r) {
		return
	}
	q := &queryParser{r: r}
	in := admin.ListPendingListingsInput{Status: q.str("status"), ProviderID: q.int64("provider_id"), PageRequest: q.page()}
	if !q.ok(w) {
		return
	}
	page, err := h.svc.ListPendingListings(r.Context(), in)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

// ---------- 待办计数 ----------

// todoCache：侧栏每 60 秒轮询一次，多个运营同时在线时，后三项（需要全量算毛利）
// 不必每次都重算，缓存 30 秒。
var todoCache struct {
	sync.Mutex
	at  time.Time
	val *admin.TodoCounts
}

// invalidateTodoCache 在会改变待办数量的写操作（审批、上架、忽略、改价、改渠道）
// 之后调用，让前端紧接着的刷新拿到新数字，而不是 30 秒内的旧缓存。
func invalidateTodoCache() {
	todoCache.Lock()
	todoCache.val = nil
	todoCache.Unlock()
}

func (h *adminHandlers) getTodoCounts(w http.ResponseWriter, r *http.Request) {
	todoCache.Lock()
	defer todoCache.Unlock()
	if todoCache.val == nil || time.Since(todoCache.at) > 30*time.Second {
		t, err := h.svc.GetTodoCounts(r.Context())
		if err != nil {
			writeAdminError(w, r, h.log, err)
			return
		}
		todoCache.val, todoCache.at = t, time.Now()
	}
	httpx.WriteJSON(w, http.StatusOK, todoCache.val)
}

// isAdminNotFound / isAdminConflict 集中列出 admin 包新增的哨兵错误，供 writeAdminError 使用。
func isAdminNotFound(err error) bool {
	return errors.Is(err, admin.ErrProviderNotFound) || errors.Is(err, admin.ErrProviderKeyNotFound) ||
		errors.Is(err, admin.ErrPriceSourceNotFound) || errors.Is(err, admin.ErrChangeRequestNotFound) ||
		errors.Is(err, pricesync.ErrChangeRequestNotFound) || errors.Is(err, admin.ErrRequestLogNotFound)
}

func isAdminConflict(err error) bool {
	return errors.Is(err, admin.ErrLastActiveChannel) || errors.Is(err, admin.ErrProviderKeyRevoked)
}
