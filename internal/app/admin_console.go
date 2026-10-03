package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"golang.org/x/sync/singleflight"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/adminauth"
	"github.com/WALLE-AI/uFreeTokens/internal/health"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
	"github.com/WALLE-AI/uFreeTokens/internal/offers"
	"github.com/WALLE-AI/uFreeTokens/internal/pricesync"
)

// 运营后台（frontend/admin）的列表 / 详情 / 编辑接口，见
// docs/cmd-admin 运营后台接口补全技术方案.md §1、§2、§5、§7。
// 与 admin.go 里的创建类接口共用同一个鉴权分组。

// 路由与权限在 admin_routes.go 的 adminRouteTable 里集中声明。

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

// boolean 解析 true/false/1/0，缺省为 false；其他取值记为参数错误（此前静默当 false）。
func (p *queryParser) boolean(key string) bool {
	b := p.optBool(key)
	return b != nil && *b
}

func (p *queryParser) optBool(key string) *bool {
	var b bool
	switch p.str(key) {
	case "":
		return nil
	case "true", "1":
		b = true
	case "false", "0":
		b = false
	default:
		if p.err == nil {
			p.err = errors.New("invalid boolean query param '" + key + "', want true or false")
		}
		return nil
	}
	return &b
}

// enum 读一个枚举过滤参数：空表示不过滤；不在 allowed 里记为参数错误（此前静默
// 返回空结果，运营会误以为"没有数据"）。
func (p *queryParser) enum(key string, allowed ...string) string {
	v := p.str(key)
	if v != "" && !slices.Contains(allowed, v) && p.err == nil {
		p.err = errors.New("invalid value for '" + key + "', want one of " + strings.Join(allowed, "/"))
	}
	return v
}

// enumCSV 同 enum，但接受逗号分隔的多个值；extra 是额外允许的特殊值（如 "all"）。
func (p *queryParser) enumCSV(key string, allowed []string, extra ...string) []string {
	vals := p.csv(key)
	for _, v := range vals {
		if !slices.Contains(allowed, v) && !slices.Contains(extra, v) && p.err == nil {
			p.err = errors.New("invalid value '" + v + "' for '" + key + "', want one of " + strings.Join(append(allowed, extra...), "/"))
		}
	}
	return vals
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

// clearKeyCooldown 清除网关为某个上游 Key 设置的冷却（没有 Redis 时跳过）。失败只记日志：
// 冷却本身有 TTL，最坏情况是 Key 晚几分钟恢复可选。
func (h *adminHandlers) clearKeyCooldown(ctx context.Context, keyID int64) {
	if err := health.ClearKeyCooldown(ctx, h.redis, keyID); err != nil {
		h.log.Warn("clear provider key cooldown failed", "provider_key_id", keyID, "error", err)
	}
}

// requireFieldPermission 在 PATCH 请求体包含 field 时额外要求 perm；请求体会被
// 读出后原样放回，后续 handler 仍能正常解析。
func requireFieldPermission(w http.ResponseWriter, r *http.Request, field string, perm adminauth.Permission) bool {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "unreadable request body")
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	var probe map[string]json.RawMessage
	if json.Unmarshal(raw, &probe) == nil {
		if _, ok := probe[field]; ok && !can(r, perm) {
			forbid(w, r, perm)
			return false
		}
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

// patchHandler 是所有 PATCH 接口的共同流程：解析 body → 核对 If-Match（乐观锁）
// → 在一个事务里执行更新并写审计（before/after 只含改动字段）→ 返回更新后的
// 对象与新的 ETag。
//
// If-Match 缺省时不做版本核对（兼容旧调用方）；带了但版本不一致返回 412。
// 更新已提交而回读失败时不能再返回错误（调用方会以为没改成而重试），改为返回
// 200 + 本次改动的字段。
func patchHandler[In any, Out any](h *adminHandlers, w http.ResponseWriter, r *http.Request, id int64, action, targetType, table string,
	update func(context.Context, int64, In) (*admin.Change, error), reload func(context.Context, int64) (Out, error)) {
	var in In
	if err := decodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	ctx := r.Context()
	if v, ok, err := ifMatchVersion(r); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	} else if ok {
		ctx = admin.WithExpectedVersion(ctx, v)
	} else if ifMatchRequiredTables[table] {
		if h.requireIfMatch {
			httpx.WriteError(w, r, http.StatusPreconditionRequired, "precondition_required",
				"If-Match is required: send the ETag returned by the detail endpoint.")
			return
		}
		h.log.Warn("PATCH without If-Match (lost-update protection skipped)", "table", table, "id", id)
	}
	change, err := audited(h, r.WithContext(ctx), func(ctx context.Context) (*admin.Change, auditEntry, error) {
		change, err := update(ctx, id, in)
		if err != nil {
			return nil, auditEntry{}, err
		}
		return change, auditEntry{action, targetType, idStr(id), change.Before, change.After}, nil
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	invalidateTodoCache()
	w.Header().Set("ETag", etag(change.Version))
	out, err := reload(r.Context(), id)
	if err != nil {
		h.log.Error("reload after committed update failed", "table", table, "id", id, "error", err)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"id": id, "updated": change.After, "version": change.Version})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// ifMatchRequiredTables 是有详情接口（返回 ETag）的可编辑资源；RequireIfMatch 打开时
// 它们的 PATCH 必须带 If-Match。上游密钥、API Key、价格源没有单独的详情页，前端拿
// 不到 ETag，暂不强制（仍然支持 If-Match）。
var ifMatchRequiredTables = map[string]bool{
	"providers": true, "provider_accounts": true, "virtual_models": true, "channels": true, "accounts": true,
}

func etag(version int) string { return `W/"` + strconv.Itoa(version) + `"` }

// ifMatchVersion 解析 If-Match: W/"12"、"12" 或 12；缺省或 "*" 返回 ok=false。
func ifMatchVersion(r *http.Request) (int, bool, error) {
	v := strings.TrimSpace(r.Header.Get("If-Match"))
	if v == "" || v == "*" {
		return 0, false, nil
	}
	v = strings.Trim(strings.TrimPrefix(v, "W/"), `"`)
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, false, errors.New("invalid If-Match header, want the ETag returned by the detail endpoint")
	}
	return n, true, nil
}

// setETag 给详情响应加上 ETag（行版本），供之后的 PATCH 带 If-Match。
func (h *adminHandlers) setETag(w http.ResponseWriter, r *http.Request, table string, id int64) {
	if v, err := h.svc.RowVersion(r.Context(), table, id); err == nil {
		w.Header().Set("ETag", etag(v))
	}
}

// ---------- providers ----------

func (h *adminHandlers) listProviders(w http.ResponseWriter, r *http.Request) {
	q := &queryParser{r: r}
	en := admin.EnumValues()
	in := admin.ListProvidersInput{Q: q.str("q"), Status: q.enum("status", en.ProviderStatuses...), Protocol: q.enum("protocol", en.Protocols...), Sort: q.str("sort"), PageRequest: q.page()}
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
	h.setETag(w, r, "providers", id)
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
	// 域名白名单决定上游密钥能被发往哪里，改它需要写密钥的权限。
	if !requireFieldPermission(w, r, "allowed_hosts", adminauth.PermProviderKeyWrite) {
		return
	}
	patchHandler(h, w, r, id, "provider.update", "provider", "providers", h.svc.UpdateProvider,
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
	in := admin.ListProviderAccountsInput{ProviderID: q.int64("provider_id"), Q: q.str("q"), Status: q.enum("status", admin.EnumValues().ProviderStatuses...), PageRequest: q.page()}
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
	h.setETag(w, r, "provider_accounts", id)
	httpx.WriteJSON(w, http.StatusOK, a)
}

func (h *adminHandlers) updateProviderAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "providerAccountID", "provider account")
	if !ok {
		return
	}
	// base_url 决定网关流量和上游密钥发往哪里，改它需要写密钥的权限。
	if !requireFieldPermission(w, r, "base_url", adminauth.PermProviderKeyWrite) {
		return
	}
	patchHandler(h, w, r, id, "provider_account.update", "provider_account", "provider_accounts", h.svc.UpdateProviderAccount, h.svc.GetProviderAccount)
}

func (h *adminHandlers) updateProviderKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "providerKeyID", "provider key")
	if !ok {
		return
	}
	patchHandler(h, w, r, id, "provider_key.update", "provider_key", "provider_keys", h.svc.UpdateProviderKey,
		func(ctx context.Context, id int64) (*admin.ProviderKeyInfo, error) {
			k, err := h.svc.GetProviderKey(ctx, id)
			// 运营手动把 Key 重新启用：清掉网关留下的冷却记录，立即恢复可选
			if err == nil && k.Status == "active" {
				h.clearKeyCooldown(ctx, id)
			}
			return k, err
		})
}

func (h *adminHandlers) revokeProviderKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "providerKeyID", "provider key")
	if !ok {
		return
	}
	if _, err := audited(h, r, func(ctx context.Context) (*admin.Change, auditEntry, error) {
		change, err := h.svc.RevokeProviderKey(ctx, id)
		if err != nil {
			return nil, auditEntry{}, err
		}
		return change, auditEntry{"provider_key.revoke", "provider_key", idStr(id), change.Before, change.After}, nil
	}); err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	// 吊销后冷却记录已无意义，一并清掉，避免健康页把它算作"冷却中的 Key"
	h.clearKeyCooldown(r.Context(), id)
	k, err := h.svc.GetProviderKey(r.Context(), id)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, k)
}

// ---------- virtual models ----------

// virtualModels 返回列表。带 name 参数时兼容旧行为做精确查找（返回单个对象或
// 404）——已废弃，请改用 GET /virtual-models/lookup?name=，响应带 Deprecation 头。
func (h *adminHandlers) virtualModels(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Has("name") {
		w.Header().Set("Deprecation", "true")
		w.Header().Set("Link", `</virtual-models/lookup>; rel="successor-version"`)
		h.getVirtualModelByName(w, r)
		return
	}
	q := &queryParser{r: r}
	in := admin.ListVirtualModelsInput{
		Q: q.str("q"), Statuses: q.enumCSV("status", admin.EnumValues().ModelStatuses), Type: q.enum("type", admin.EnumValues().ModelTypes...),
		Family: q.str("family"), Tier: q.enum("tier", admin.EnumValues().Tiers...),
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
	h.setETag(w, r, "virtual_models", id)
	httpx.WriteJSON(w, http.StatusOK, vm)
}

func (h *adminHandlers) updateVirtualModel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "virtualModelID", "virtual model")
	if !ok {
		return
	}
	patchHandler(h, w, r, id, "virtual_model.update", "virtual_model", "virtual_models", h.svc.UpdateVirtualModel, h.svc.GetVirtualModel)
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

// channels 返回列表。virtual_model_id + provider_account_id + upstream_model 三个
// 参数同时出现时兼容旧行为做精确查找——已废弃，请改用 GET /channels/lookup。
func (h *adminHandlers) channels(w http.ResponseWriter, r *http.Request) {
	qv := r.URL.Query()
	if qv.Has("virtual_model_id") && qv.Has("provider_account_id") && qv.Has("upstream_model") {
		w.Header().Set("Deprecation", "true")
		w.Header().Set("Link", `</channels/lookup>; rel="successor-version"`)
		h.findChannel(w, r)
		return
	}
	q := &queryParser{r: r}
	in := admin.ListChannelsInput{
		VirtualModelID: q.int64("virtual_model_id"), ProviderAccountID: q.int64("provider_account_id"), ProviderID: q.int64("provider_id"),
		Status: q.enum("status", admin.EnumValues().ChannelStatuses...), Q: q.str("q"),
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
	h.setETag(w, r, "channels", id)
	httpx.WriteJSON(w, http.StatusOK, ch)
}

func (h *adminHandlers) updateChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "channelID", "channel")
	if !ok {
		return
	}
	patchHandler(h, w, r, id, "channel.update", "channel", "channels", h.svc.UpdateChannel, h.svc.GetChannel)
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
	in := admin.ListPriceSourcesInput{ProviderID: q.int64("provider_id"), Enabled: q.optBool("enabled"),
		Domain: q.enum("domain", admin.EnumValues().SourceDomains...)}
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
	patchHandler(h, w, r, id, "price_source.update", "price_source", "price_sources", h.svc.UpdatePriceSource, h.svc.GetPriceSource)
}

func (h *adminHandlers) updateAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "accountID", "account")
	if !ok {
		return
	}
	patchHandler(h, w, r, id, "account.update", "account", "accounts", h.svc.UpdateAccount,
		func(ctx context.Context, id int64) (accountWithWallet, error) {
			acct, wal, err := h.svc.GetAccount(ctx, id)
			return accountWithWallet{Account: acct, Wallet: wal}, err
		})
}

// ---------- 调价审批 / 待上架 ----------

func (h *adminHandlers) listChangeRequests(w http.ResponseWriter, r *http.Request) {
	if !h.requirePriceSync(w, r) {
		return
	}
	q := &queryParser{r: r}
	in := admin.ListChangeRequestsInput{
		Statuses: q.enumCSV("status", admin.EnumValues().ChangeStatuses, "all"), Direction: q.enum("direction", admin.EnumValues().ChangeDirections...), ChannelID: q.int64("channel_id"), ProviderID: q.int64("provider_id"),
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
	var body batchApproveRequest
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
	briefs, err := h.svc.ChangeRequestBriefs(r.Context(), body.IDs)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	for _, id := range body.IDs {
		d, found := briefs[id]
		if !found {
			fail(id, "not_found", "change request not found")
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
		// 每条独立一个事务：审批与审计同成败，失败的条目不影响其他条目。
		bookID, err := audited(h, r, func(ctx context.Context) (int64, auditEntry, error) {
			bookID, err := h.pricesync.Approve(ctx, id, meta)
			if err != nil {
				return 0, auditEntry{}, err
			}
			return bookID, auditEntry{"price_change.approve", "price_change_request", idStr(id), map[string]any{"status": d.Status},
				map[string]any{"applied_book_id": bookID, "reason": body.Reason, "batch": true}}, nil
		})
		if err != nil {
			fail(id, "conflict", err.Error())
			continue
		}
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
	in := admin.ListPendingListingsInput{Status: q.enum("status", admin.EnumValues().ListingStatuses...), ProviderID: q.int64("provider_id"),
		Origin: q.enum("origin", "price_source", "free_offer"), PageRequest: q.page()}
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
	at    time.Time
	val   *admin.TodoCounts
	group singleflight.Group
}

// invalidateTodoCache 在会改变待办数量的写操作（审批、上架、忽略、改价、改渠道）
// 之后调用，让前端紧接着的刷新拿到新数字，而不是 30 秒内的旧缓存。
func invalidateTodoCache() {
	todoCache.Lock()
	todoCache.val = nil
	todoCache.Unlock()
}

// getTodoCounts 在锁外查库（此前持有全局锁查库，慢查询会阻塞所有运营的轮询），
// 并发未命中用 singleflight 合并成一次查询。
func (h *adminHandlers) getTodoCounts(w http.ResponseWriter, r *http.Request) {
	todoCache.Lock()
	val, fresh := todoCache.val, todoCache.val != nil && time.Since(todoCache.at) <= 30*time.Second
	todoCache.Unlock()
	if !fresh {
		v, err, _ := todoCache.group.Do("todo", func() (any, error) { return h.svc.GetTodoCounts(context.WithoutCancel(r.Context())) })
		if err != nil {
			writeAdminError(w, r, h.log, err)
			return
		}
		val = v.(*admin.TodoCounts)
		todoCache.Lock()
		todoCache.val, todoCache.at = val, time.Now()
		todoCache.Unlock()
	}
	out := *val
	// 智能体提案按调用者权限计数（不进共享缓存）：只数“我有权限处理的”待审提案。
	if h.agent.Enabled() && can(r, adminauth.PermAgentUse) {
		if n, err := h.agent.Store.CountPending(r.Context(), permsOf(actor(r))); err == nil {
			out.AgentPendingApprovals = n
		} else {
			h.log.Warn("count agent proposals failed", "error", err)
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// isAdminNotFound / isAdminConflict 集中列出 admin 包新增的哨兵错误，供 writeAdminError 使用。
func isAdminNotFound(err error) bool {
	return errors.Is(err, admin.ErrProviderNotFound) || errors.Is(err, admin.ErrProviderKeyNotFound) ||
		errors.Is(err, admin.ErrPriceSourceNotFound) || errors.Is(err, admin.ErrChangeRequestNotFound) ||
		errors.Is(err, pricesync.ErrChangeRequestNotFound) || errors.Is(err, admin.ErrRequestLogNotFound) ||
		errors.Is(err, admin.ErrUserNotFound) || errors.Is(err, admin.ErrMemberNotFound) ||
		errors.Is(err, admin.ErrBenchmarkNotFound) || errors.Is(err, admin.ErrBenchmarkRunNotFound) ||
		errors.Is(err, admin.ErrPublicAppRuleNotFound) || errors.Is(err, admin.ErrModelAliasNotFound) ||
		errors.Is(err, offers.ErrNotFound)
}

func isAdminConflict(err error) bool {
	return errors.Is(err, admin.ErrLastActiveChannel) || errors.Is(err, admin.ErrProviderKeyRevoked) ||
		errors.Is(err, admin.ErrLastAccountOwner) || errors.Is(err, admin.ErrAPIKeyRevokedFinal) ||
		errors.Is(err, admin.ErrBenchmarkRunPublished) || errors.Is(err, offers.ErrInvalidStatus)
}
