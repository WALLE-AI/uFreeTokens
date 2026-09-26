package app

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
	"github.com/WALLE-AI/uFreeTokens/internal/pricesync"
)

// AdminDeps 是构造控制面路由所需的全部依赖。
type AdminDeps struct {
	Logger    *slog.Logger
	Admin     *admin.Service
	PriceSync *pricesync.Engine // nil 时价格同步相关接口返回 503 not_implemented（见技术方案 §7.16）
}

// NewAdminRouter 组装控制面路由：账户/API Key/Provider/渠道/虚拟模型/价格管理
// （技术方案 §7 相关章节）。
//
// 重要：这里完全没有鉴权/权限控制（见 internal/admin 包文档的范围限制说明）。
// 这组接口只应该部署在内网、或者放在一个自己做了鉴权的反向代理后面，
// 绝不能直接暴露到公网——任何能连上它的人都可以创建账户、调整余额、
// 添加/篡改上游 Key、改价格。这是部署前必须解决的安全缺口，不是可以忽略的细节。
func NewAdminRouter(d AdminDeps) http.Handler {
	r := chi.NewRouter()
	r.Use(httpx.RequestID)
	r.Use(httpx.Recover(d.Logger))
	r.Use(httpx.AccessLog(d.Logger))

	r.Get("/healthz", healthzHandler)

	h := &adminHandlers{svc: d.Admin, log: d.Logger, pricesync: d.PriceSync}

	r.Route("/accounts", func(r chi.Router) {
		r.Post("/", h.createAccount)
		r.Get("/{accountID}", h.getAccount)
		r.Post("/{accountID}/api-keys", h.createAPIKey)
		r.Get("/{accountID}/api-keys", h.listAPIKeys)
		r.Post("/{accountID}/wallet/adjust", h.adjustWallet)
		r.Post("/{accountID}/credit-grants", h.grantCredit)
	})
	r.Post("/api-keys/{apiKeyID}/revoke", h.revokeAPIKey)

	r.Post("/providers", h.createProvider)
	r.Post("/provider-accounts", h.createProviderAccount)
	r.Post("/provider-accounts/{providerAccountID}/keys", h.addProviderKey)

	r.Post("/virtual-models", h.createVirtualModel)
	r.Post("/virtual-models/{virtualModelID}/sell-price", h.setSellPrice)
	r.Post("/channels", h.createChannel)
	r.Post("/channels/{channelID}/cost-price", h.setCostPrice)
	r.Post("/channels/{channelID}/price-observations", h.ingestPriceObservation)
	r.Post("/fx-rates", h.setFXRate)

	r.Post("/price-sources", h.createPriceSource)
	r.Get("/price-change-requests", h.listPendingChangeRequests)
	r.Post("/price-change-requests/{changeRequestID}/approve", h.approveChangeRequest)
	r.Post("/price-change-requests/{changeRequestID}/reject", h.rejectChangeRequest)

	return r
}

type adminHandlers struct {
	svc       *admin.Service
	log       *slog.Logger
	pricesync *pricesync.Engine
}

// requirePriceSync 是价格同步相关接口共用的前置检查：PriceSync 未装配时统一
// 返回 503，而不是让每个 handler 各自判断、各自措辞。
func (h *adminHandlers) requirePriceSync(w http.ResponseWriter, r *http.Request) bool {
	if h.pricesync == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "not_implemented", "Price sync is not configured on this server.")
		return false
	}
	return true
}

// --- 请求体解析与错误映射的小工具 ---

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// writeAdminError 把 internal/admin 返回的错误映射成合适的状态码：已知的
// "not found" 类错误映射 404，其余（多数是输入校验失败）一律 400——
// 这层接口的调用方是运营人员/内部工具，不需要像面向最终用户的网关那样
// 精细区分错误码，能定位问题就够了。
func writeAdminError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	switch {
	case errors.Is(err, admin.ErrAccountNotFound), errors.Is(err, admin.ErrAPIKeyNotFound), errors.Is(err, pricesync.ErrChangeRequestNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, pricesync.ErrChangeRequestNotPending):
		httpx.WriteError(w, r, http.StatusConflict, "conflict", err.Error())
	default:
		log.Warn("admin request failed", "error", err)
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
	}
}

func pathInt64(r *http.Request, key string) (int64, bool) {
	v, err := strconv.ParseInt(chi.URLParam(r, key), 10, 64)
	return v, err == nil
}

// --- accounts ---

func (h *adminHandlers) createAccount(w http.ResponseWriter, r *http.Request) {
	var in admin.CreateAccountInput
	if err := decodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	acct, err := h.svc.CreateAccount(r.Context(), in)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, acct)
}

func (h *adminHandlers) getAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt64(r, "accountID")
	if !ok {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid account id")
		return
	}
	acct, wal, err := h.svc.GetAccount(r.Context(), id)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"account": acct, "wallet": wal})
}

type adjustWalletRequest struct {
	Amount int64  `json:"amount"`
	RefID  string `json:"ref_id"`
}

func (h *adminHandlers) adjustWallet(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt64(r, "accountID")
	if !ok {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid account id")
		return
	}
	var in adjustWalletRequest
	if err := decodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	// internal/wallet 是唯一能写钱包表的模块，这里直接调用它（经由 admin.Service
	// 暴露出来的实例）而不是让 admin 包自己拥有钱包写入逻辑。
	receipt, err := h.svc.Wallet().Adjust(r.Context(), id, in.Amount, in.RefID)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, receipt)
}

type grantCreditRequest struct {
	Source     string     `json:"source"`
	Amount     int64      `json:"amount"`
	ExpiresAt  *time.Time `json:"expires_at"`
	ModelScope []string   `json:"model_scope"`
	RefID      string     `json:"ref_id"`
}

func (h *adminHandlers) grantCredit(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt64(r, "accountID")
	if !ok {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid account id")
		return
	}
	var in grantCreditRequest
	if err := decodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	granted, err := h.svc.GrantCredit(r.Context(), admin.GrantCreditInput{
		AccountID: id, Source: in.Source, Amount: in.Amount,
		ExpiresAt: in.ExpiresAt, ModelScope: in.ModelScope, RefID: in.RefID,
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, granted)
}

// --- api keys ---

func (h *adminHandlers) createAPIKey(w http.ResponseWriter, r *http.Request) {
	accountID, ok := pathInt64(r, "accountID")
	if !ok {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid account id")
		return
	}
	var body struct {
		Name             string   `json:"name"`
		AllowedModels    []string `json:"allowed_models"`
		RPMLimit         *int     `json:"rpm_limit"`
		TPMLimit         *int     `json:"tpm_limit"`
		ConcurrencyLimit *int     `json:"concurrency_limit"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	created, err := h.svc.CreateAPIKey(r.Context(), admin.CreateAPIKeyInput{
		AccountID: accountID, Name: body.Name, AllowedModels: body.AllowedModels,
		RPMLimit: body.RPMLimit, TPMLimit: body.TPMLimit, ConcurrencyLimit: body.ConcurrencyLimit,
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, created)
}

func (h *adminHandlers) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	accountID, ok := pathInt64(r, "accountID")
	if !ok {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid account id")
		return
	}
	keys, err := h.svc.ListAPIKeys(r.Context(), accountID)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": keys})
}

func (h *adminHandlers) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt64(r, "apiKeyID")
	if !ok {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid api key id")
		return
	}
	if err := h.svc.RevokeAPIKey(r.Context(), id); err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

// --- provider catalog ---

func (h *adminHandlers) createProvider(w http.ResponseWriter, r *http.Request) {
	var in admin.CreateProviderInput
	if err := decodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	p, err := h.svc.CreateProvider(r.Context(), in)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, p)
}

func (h *adminHandlers) createProviderAccount(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProviderID     int64            `json:"provider_id"`
		Name           string           `json:"name"`
		BaseURL        string           `json:"base_url"`
		CostMultiplier *decimal.Decimal `json:"cost_multiplier"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	acc, err := h.svc.CreateProviderAccount(r.Context(), admin.CreateProviderAccountInput{
		ProviderID: body.ProviderID, Name: body.Name, BaseURL: body.BaseURL, CostMultiplier: body.CostMultiplier,
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, acc)
}

func (h *adminHandlers) addProviderKey(w http.ResponseWriter, r *http.Request) {
	providerAccountID, ok := pathInt64(r, "providerAccountID")
	if !ok {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid provider account id")
		return
	}
	var body struct {
		Secret string `json:"secret"`
		Weight int    `json:"weight"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	key, err := h.svc.AddProviderKey(r.Context(), admin.AddProviderKeyInput{
		ProviderAccountID: providerAccountID, Secret: body.Secret, Weight: body.Weight,
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, key)
}

func (h *adminHandlers) createVirtualModel(w http.ResponseWriter, r *http.Request) {
	var in admin.CreateVirtualModelInput
	if err := decodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	vm, err := h.svc.CreateVirtualModel(r.Context(), in)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, vm)
}

func (h *adminHandlers) createChannel(w http.ResponseWriter, r *http.Request) {
	var in admin.CreateChannelInput
	if err := decodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	ch, err := h.svc.CreateChannel(r.Context(), in)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, ch)
}

func (h *adminHandlers) setSellPrice(w http.ResponseWriter, r *http.Request) {
	vmID, ok := pathInt64(r, "virtualModelID")
	if !ok {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid virtual model id")
		return
	}
	var body struct {
		Tier       string                      `json:"tier"`
		Components []admin.PriceComponentInput `json:"components"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	bookID, err := h.svc.SetSellPrice(r.Context(), admin.SetSellPriceInput{
		VirtualModelID: vmID, Tier: body.Tier, Components: body.Components,
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]int64{"price_book_id": bookID})
}

func (h *adminHandlers) setCostPrice(w http.ResponseWriter, r *http.Request) {
	channelID, ok := pathInt64(r, "channelID")
	if !ok {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid channel id")
		return
	}
	var body struct {
		Currency   string                      `json:"currency"`
		Components []admin.PriceComponentInput `json:"components"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	bookID, err := h.svc.SetCostPrice(r.Context(), admin.SetCostPriceInput{
		ChannelID: channelID, Currency: body.Currency, Components: body.Components,
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]int64{"price_book_id": bookID})
}

func (h *adminHandlers) setFXRate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Base          string          `json:"base"`
		Quote         string          `json:"quote"`
		Rate          decimal.Decimal `json:"rate"`
		Source        string          `json:"source"`
		EffectiveDate *time.Time      `json:"effective_date"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	in := admin.SetFXRateInput{Base: body.Base, Quote: body.Quote, Rate: body.Rate, Source: body.Source}
	if body.EffectiveDate != nil {
		in.EffectiveDate = *body.EffectiveDate
	}
	if err := h.svc.SetFXRate(r.Context(), in); err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
}
