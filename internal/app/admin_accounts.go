package app

import (
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
)

// 账户检索、资金流水、赠送余额、全局 API Key 检索（运营后台接口方案 §4）。
// 调账 / 赠送两个写接口在 admin.go，这里只放读接口。

func (h *adminHandlers) listAccounts(w http.ResponseWriter, r *http.Request) {
	q := &queryParser{r: r}
	in := admin.ListAccountsInput{
		Q: q.str("q"), Status: q.enum("status", admin.EnumValues().AccountStatuses...), Tier: q.enum("tier", admin.EnumValues().Tiers...),
		Type: q.enum("type", admin.EnumValues().AccountTypes...), Sort: q.str("sort"), PageRequest: q.page(),
	}
	if !q.ok(w) {
		return
	}
	page, err := h.svc.ListAccounts(r.Context(), in)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

func (h *adminHandlers) listLedger(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "accountID", "account")
	if !ok {
		return
	}
	q := &queryParser{r: r}
	in := admin.ListLedgerInput{AccountID: id, Type: q.enum("type", admin.EnumValues().LedgerTypes...), BalanceKind: q.enum("balance_kind", admin.EnumValues().LedgerBalanceKinds...), Before: q.str("before"), Limit: q.int("limit")}
	if !q.ok(w) {
		return
	}
	var err error
	if in.From, err = parseTimeParam(q.str("from"), false); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid 'from': "+err.Error())
		return
	}
	if in.To, err = parseTimeParam(q.str("to"), true); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid 'to': "+err.Error())
		return
	}
	entries, next, err := h.svc.ListLedger(r.Context(), in)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": entries, "next_cursor": next})
}

func (h *adminHandlers) listCreditGrants(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "accountID", "account")
	if !ok {
		return
	}
	q := &queryParser{r: r}
	active := q.boolean("active")
	if !q.ok(w) {
		return
	}
	grants, truncated, err := h.svc.ListCreditGrants(r.Context(), id, active)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": grants, "truncated": truncated})
}

func (h *adminHandlers) searchAPIKeys(w http.ResponseWriter, r *http.Request) {
	q := &queryParser{r: r}
	in := admin.ListAPIKeysInput{Q: q.str("q"), AccountID: q.int64("account_id"), Status: q.enum("status", admin.EnumValues().APIKeyStatuses...), PageRequest: q.page()}
	if !q.ok(w) {
		return
	}
	page, err := h.svc.SearchAPIKeys(r.Context(), in)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

// requireReason 校验资金操作必填的原因（≤200 字），失败时直接写 400。
func requireReason(w http.ResponseWriter, r *http.Request, reason string) (string, bool) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "'reason' is required for wallet operations")
		return "", false
	}
	if utf8.RuneCountInString(reason) > 200 {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "'reason' must be at most 200 characters")
		return "", false
	}
	return reason, true
}

func accountIDString(id int64) string { return strconv.FormatInt(id, 10) }
