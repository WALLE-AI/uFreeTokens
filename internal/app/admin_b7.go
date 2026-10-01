package app

import (
	"context"
	"net/http"
	"strings"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/adminauth"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
)

// B7 新增的写接口：批量导入上游模型、API Key 编辑、账户成员管理。

type importModelResultItem struct {
	admin.ImportModelPlan
	OK     bool                     `json:"ok"`
	Result *admin.ImportModelResult `json:"result,omitempty"`
	Error  *apiError                `json:"error,omitempty"`
}

// importModels 是 POST /provider-accounts/{id}/import-models：
//   - dry_run=true：只返回每个模型的导入计划（平台现状、人民币成本、售价、毛利、
//     错误），不写库，需要 catalog:read；
//   - 否则逐个模型各自在一个事务里导入并写审计（与向导"部分失败只重试失败项"
//     的交互一致），需要 catalog:write + pricing:write。
func (h *adminHandlers) importModels(w http.ResponseWriter, r *http.Request) {
	paID, ok := pathID(w, r, "providerAccountID", "provider account")
	if !ok {
		return
	}
	var body importModelsRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	in := body.ImportModelsInput
	in.ProviderAccountID = paID
	if !body.DryRun {
		for _, perm := range []adminauth.Permission{adminauth.PermCatalogWrite, adminauth.PermPricingWrite} {
			if !can(r, perm) {
				forbid(w, r, perm)
				return
			}
		}
	}
	plans, preview, err := h.svc.PlanImport(r.Context(), in)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	items := make([]importModelResultItem, len(plans))
	for i, p := range plans {
		items[i] = importModelResultItem{ImportModelPlan: p}
		if body.DryRun {
			items[i].OK = len(p.Errors) == 0
			continue
		}
		it := in.Items[i]
		res, err := audited(h, r, func(ctx context.Context) (*admin.ImportModelResult, auditEntry, error) {
			res, err := h.svc.ImportOne(ctx, paID, in.Currency, it, p)
			if err != nil {
				return nil, auditEntry{}, err
			}
			return res, auditEntry{"model.import", "provider_account", idStr(paID), nil, map[string]any{"item": it, "plan": p, "result": res}}, nil
		})
		if err != nil {
			items[i].Error = &apiError{Code: "import_failed", Message: err.Error()}
			continue
		}
		items[i].OK, items[i].Result = true, res
	}
	if !body.DryRun {
		invalidateTodoCache()
	}
	httpx.WriteJSON(w, http.StatusOK, importModelsResponse{
		DryRun: body.DryRun, Currency: preview.Currency, FXRate: preview.FXRate, FXDate: preview.FXDate,
		FXMissing: preview.FXMissing, Items: items,
	})
}

func (h *adminHandlers) updateAPIKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "apiKeyID", "api key")
	if !ok {
		return
	}
	patchHandler(h, w, r, id, "api_key.update", "api_key", "api_keys", h.svc.UpdateAPIKey, h.svc.GetAPIKey)
}

func (h *adminHandlers) addAccountMember(w http.ResponseWriter, r *http.Request) {
	accountID, ok := pathID(w, r, "accountID", "account")
	if !ok {
		return
	}
	var body addMemberRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	m, err := audited(h, r, func(ctx context.Context) (*admin.AccountMember, auditEntry, error) {
		m, err := h.svc.AddAccountMember(ctx, accountID, strings.TrimSpace(body.Email), body.Role)
		if err != nil {
			return nil, auditEntry{}, err
		}
		return m, auditEntry{"account_member.add", "account", idStr(accountID), nil, m}, nil
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, m)
}

func (h *adminHandlers) updateAccountMember(w http.ResponseWriter, r *http.Request) {
	accountID, ok := pathID(w, r, "accountID", "account")
	if !ok {
		return
	}
	userID, ok := pathID(w, r, "userID", "user")
	if !ok {
		return
	}
	var body updateMemberRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	m, err := audited(h, r, func(ctx context.Context) (*admin.AccountMember, auditEntry, error) {
		before, after, err := h.svc.UpdateAccountMember(ctx, accountID, userID, body.Role)
		if err != nil {
			return nil, auditEntry{}, err
		}
		return after, auditEntry{"account_member.update", "account", idStr(accountID), before, after}, nil
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, m)
}

func (h *adminHandlers) removeAccountMember(w http.ResponseWriter, r *http.Request) {
	accountID, ok := pathID(w, r, "accountID", "account")
	if !ok {
		return
	}
	userID, ok := pathID(w, r, "userID", "user")
	if !ok {
		return
	}
	if _, err := audited(h, r, func(ctx context.Context) (struct{}, auditEntry, error) {
		before, err := h.svc.RemoveAccountMember(ctx, accountID, userID)
		if err != nil {
			return struct{}{}, auditEntry{}, err
		}
		return struct{}{}, auditEntry{"account_member.remove", "account", idStr(accountID), before, nil}, nil
	}); err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
