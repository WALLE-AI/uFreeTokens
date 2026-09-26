package app

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
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
	Logger     *slog.Logger
	Admin      *admin.Service
	PriceSync  *pricesync.Engine // nil 时价格同步相关接口返回 503 not_implemented（见技术方案 §7.16）
	AdminToken string            // 见 NewAdminRouter 的鉴权说明；空字符串会拒绝所有受保护的请求，不会退化成不鉴权
	TestWebDir string            // 非空时在根路径同源提供 test_web/admin.html（手工联调用，见 staticweb.go）；空字符串（默认）不开启
}

// NewAdminRouter 组装控制面路由：账户/API Key/Provider/渠道/虚拟模型/价格管理
// （技术方案 §7 相关章节）。
//
// 鉴权：所有业务路由都要求 Authorization: Bearer <AdminToken>
// （httpx.RequireBearerToken，只有 /healthz 不需要）。这不是完整的管理员登录/
// RBAC——没有"是谁在操作"的概念，知道这一个共享密钥的人能做任何事：创建账户、
// 调整余额、添加/篡改上游 Key、改价格。真正的多用户登录 + 按角色收权限，
// 需要先把 users/account_members 那套用户体系接起来（见 internal/admin 包文档），
// 这里只是把"完全没有鉴权"升级成"至少不是任何人都能连上"，作为部署前的最低
// 要求，不是最终形态。
func NewAdminRouter(d AdminDeps) http.Handler {
	r := chi.NewRouter()
	r.Use(httpx.RequestID)
	r.Use(httpx.Recover(d.Logger))
	r.Use(httpx.AccessLog(d.Logger))

	r.Get("/healthz", healthzHandler)
	// 根路径给手工联调用的测试页面用，故意放在鉴权分组外面——它只是个静态
	// HTML 文件，浏览器直接打开首页不应该先被 401 挡住；管理员令牌是页面内
	// JS 调后续接口时才用到。TestWebDir 为空时这里返回 404，行为和不注册
	// 这个路由完全一样。
	r.Get("/", serveStaticHTML(d.TestWebDir, "admin.html"))

	h := &adminHandlers{svc: d.Admin, log: d.Logger, pricesync: d.PriceSync}

	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireBearerToken(d.AdminToken))

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
		r.Get("/provider-accounts/{providerAccountID}/upstream-models", h.listUpstreamModels)

		r.Post("/virtual-models", h.createVirtualModel)
		r.Get("/virtual-models", h.getVirtualModelByName)
		r.Post("/virtual-models/{virtualModelID}/sell-price", h.setSellPrice)
		r.Post("/channels", h.createChannel)
		r.Get("/channels", h.findChannel)
		r.Post("/channels/{channelID}/cost-price", h.setCostPrice)
		r.Post("/channels/{channelID}/price-observations", h.ingestPriceObservation)
		r.Post("/fx-rates", h.setFXRate)

		r.Post("/price-sources", h.createPriceSource)
		r.Post("/providers/{providerID}/price-observations", h.ingestUnmappedPriceObservation)
		r.Get("/price-change-requests", h.listPendingChangeRequests)
		r.Post("/price-change-requests/{changeRequestID}/approve", h.approveChangeRequest)
		r.Post("/price-change-requests/{changeRequestID}/reject", h.rejectChangeRequest)

		r.Get("/pending-model-listings", h.listPendingModelListings)
		r.Post("/pending-model-listings/{listingID}/publish", h.publishPendingModelListing)
		r.Post("/pending-model-listings/{listingID}/dismiss", h.dismissPendingModelListing)

		r.Get("/audit-logs", h.listAuditLogs)
	})

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
	case errors.Is(err, admin.ErrAccountNotFound), errors.Is(err, admin.ErrAPIKeyNotFound),
		errors.Is(err, admin.ErrProviderAccountNotFound),
		errors.Is(err, admin.ErrVirtualModelNotFound), errors.Is(err, admin.ErrChannelNotFound),
		errors.Is(err, pricesync.ErrChangeRequestNotFound), errors.Is(err, pricesync.ErrListingNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, pricesync.ErrChangeRequestNotPending), errors.Is(err, pricesync.ErrListingNotPending),
		errors.Is(err, admin.ErrNoActiveProviderKey):
		httpx.WriteError(w, r, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, admin.ErrUpstreamUnavailable):
		// 上游 /models 调不通/返回非预期内容——这是上游那边的问题，不是调用方
		// 请求参数有问题，不应该归进默认的 400。
		httpx.WriteError(w, r, http.StatusBadGateway, "upstream_unavailable", err.Error())
	default:
		log.Warn("admin request failed", "error", err)
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
	}
}

// actorIDFromRequest 从 X-Actor-ID 头读出发起操作的管理员 ID——cmd/admin 还没有
// 鉴权（见 internal/admin 包文档），没有"当前登录管理员"这个概念，这个头完全
// 靠调用方自觉填写，读不到或解析失败就记 0（未知/系统操作）。等真正的管理员
// 登录做出来了，这里应该换成从鉴权中间件解析出的身份，调用方不用变。
func actorIDFromRequest(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.Header.Get("X-Actor-ID"), 10, 64)
	return id
}

// requestIP 尽量拿到调用方地址（去掉端口），拿不到就原样返回 RemoteAddr。
func requestIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// recordAudit 是审计日志写入失败时的统一处理：只记日志，不影响已经成功的业务
// 操作的响应——审计不应该成为主流程的单点故障（技术方案 Phase 4 的审计导出，
// 见 internal/admin/audit.go 的取舍说明）。
func (h *adminHandlers) recordAudit(r *http.Request, action, targetType, targetID string, before, after any) {
	if _, err := h.svc.RecordAudit(r.Context(), admin.AuditLogInput{
		ActorID: actorIDFromRequest(r), Action: action, TargetType: targetType, TargetID: targetID,
		Before: before, After: after, IP: requestIP(r),
	}); err != nil {
		h.log.Error("record audit log failed", "action", action, "target_type", targetType, "target_id", targetID, "error", err)
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
	h.recordAudit(r, "wallet.adjust", "account", strconv.FormatInt(id, 10), nil, map[string]any{"amount": in.Amount, "ref_id": in.RefID, "receipt": receipt})
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
	h.recordAudit(r, "wallet.credit_grant", "account", strconv.FormatInt(id, 10), nil, granted)
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
	// key（ProviderKeySummary）从来不包含明文/密文，只有末 4 位——安全地记进审计日志。
	h.recordAudit(r, "provider_key.add", "provider_account", strconv.FormatInt(providerAccountID, 10), nil, key)
	httpx.WriteJSON(w, http.StatusCreated, key)
}

// listUpstreamModels 是"选一个供应商、录入 API Key 之后，真的问一遍它支持
// 哪些模型"这个操作的 HTTP 入口——调用 admin.Service.ListUpstreamModels，
// 后者会真的对上游发一次 GET /models（技术方案 §7.16 之外的手工联调场景，
// 不是价格同步流水线的一部分）。
func (h *adminHandlers) listUpstreamModels(w http.ResponseWriter, r *http.Request) {
	providerAccountID, ok := pathInt64(r, "providerAccountID")
	if !ok {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid provider account id")
		return
	}
	models, err := h.svc.ListUpstreamModels(r.Context(), providerAccountID)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": models})
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

// getVirtualModelByName 是"这个名字的虚拟模型是不是已经存在了"的查询入口——
// 主要给"先查、不存在才创建"的幂等导入流程用（比如 test_web 反复点"导入"
// 不应该每次都撞 virtual_models.name 的唯一约束）。?name= 是必填的，这里
// 没有做成通用的"列出全部虚拟模型"接口。
func (h *adminHandlers) getVirtualModelByName(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "query param 'name' is required")
		return
	}
	vm, err := h.svc.GetVirtualModelByName(r.Context(), name)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, vm)
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

// findChannel 是"这个 (虚拟模型, 上游账号, 上游模型) 三元组是不是已经有渠道了"
// 的查询入口，三个查询参数都必填——同 getVirtualModelByName，服务幂等导入
// 流程，不是通用的渠道列表接口。
func (h *adminHandlers) findChannel(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	vmID, err1 := strconv.ParseInt(q.Get("virtual_model_id"), 10, 64)
	paID, err2 := strconv.ParseInt(q.Get("provider_account_id"), 10, 64)
	upstreamModel := q.Get("upstream_model")
	if err1 != nil || err2 != nil || upstreamModel == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request",
			"query params 'virtual_model_id', 'provider_account_id' (both integers) and 'upstream_model' are required")
		return
	}
	ch, err := h.svc.FindChannel(r.Context(), vmID, paID, upstreamModel)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ch)
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
	h.recordAudit(r, "sell_price.set", "virtual_model", strconv.FormatInt(vmID, 10), nil, map[string]any{"price_book_id": bookID, "tier": body.Tier, "components": body.Components})
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
	h.recordAudit(r, "cost_price.set", "channel", strconv.FormatInt(channelID, 10), nil, map[string]any{"price_book_id": bookID, "currency": body.Currency, "components": body.Components})
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
	h.recordAudit(r, "fx_rate.set", "fx_rate", body.Base, nil, in)
	httpx.WriteJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
}

// listAuditLogs 是审计导出的查询入口（技术方案 Phase 4）：?target_type=&target_id=
// 都可以留空，留空表示不按那个维度过滤；?limit= 留空用默认值 100。
func (h *adminHandlers) listAuditLogs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	entries, err := h.svc.ListAuditLogs(r.Context(), admin.ListAuditLogsInput{
		TargetType: r.URL.Query().Get("target_type"), TargetID: r.URL.Query().Get("target_id"), Limit: limit,
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"audit_logs": entries})
}
