package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/adminauth"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
	"github.com/WALLE-AI/uFreeTokens/internal/offers"
	"github.com/WALLE-AI/uFreeTokens/internal/pricesync"
	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

// AdminDeps 是构造控制面路由所需的全部依赖。
type AdminDeps struct {
	Logger    *slog.Logger
	Admin     *admin.Service
	PriceSync *pricesync.Engine // nil 时价格同步相关接口返回 503 not_implemented（见技术方案 §7.16）
	// Offers 是上游优惠情报库（docs/外部数据采集模块（价格情报与评测榜单）技术方案.md §3.2）；nil 时优惠雷达接口返回 503。
	Offers *offers.Store
	// Auth 是管理员账号/会话服务；nil 时只接受应急令牌（测试与未迁移的环境）。
	Auth *adminauth.Service
	// AdminToken 是应急（break-glass）共享令牌：匹配时身份为 system、拥有全部
	// 权限。空字符串表示关闭应急通道，只能用管理员会话登录。
	AdminToken string
	// TrustedProxies 是可信反向代理网段：只有来自这些地址的请求才读取
	// X-Forwarded-For 作为审计 IP。
	TrustedProxies []netip.Prefix
	// ReferencePriceURLs 覆盖参考价格查询的两个来源地址（测试用）；零值用官方地址。
	ReferencePriceURLs ReferencePriceURLs
	// RequireIfMatch 为 true 时，有详情接口（返回 ETag）的资源 PATCH 必须带 If-Match，
	// 缺省返回 428；false 时缺省只记 WARN（过渡期，见 docs/admin-api.md §2）。
	RequireIfMatch bool
	// StatsTZ 是统计接口未带 ?tz= 时的默认时区；nil = UTC。
	StatsTZ *time.Location
	// Redis 可选：用于读取网关的熔断/冷却状态（/channels/health）、吊销或重新启用
	// 上游 Key 时清除冷却记录。nil 时这些能力降级（健康页的熔断状态为 unknown）。
	Redis *redis.Client
	// PublicMinAccounts 是公开排行榜的隐私阈值（config public.rankings_min_accounts），
	// GET /public-apps 原样返回给后台页面做提示；0 = 默认 3。
	PublicMinAccounts int
	TestWebDir        string // 非空时在根路径同源提供 test_web/admin.html（手工联调用，见 staticweb.go）；空字符串（默认）不开启
}

// NewAdminRouter 组装控制面路由：账户/API Key/Provider/渠道/虚拟模型/价格管理
// （技术方案 §7 相关章节）。
//
// 鉴权：除 /healthz、/auth/login 外，所有路由都要求 Authorization: Bearer，
// 可以是管理员会话令牌（POST /auth/login 获得）或应急共享令牌；每个路由在
// adminRouteTable 里声明所需权限点，无权限返回 403 permission_denied。
// 审计日志的操作人来自已认证身份，不再读取客户端请求头。
func NewAdminRouter(d AdminDeps) http.Handler {
	r := chi.NewRouter()
	r.Use(httpx.RequestID)
	r.Use(httpx.Recover(d.Logger))
	r.Use(httpx.AccessLog(d.Logger))
	r.Use(httpx.MaxBodyBytes(maxAdminBodyBytes))

	r.Get("/healthz", healthzHandler)
	// 根路径给手工联调用的测试页面用，故意放在鉴权分组外面——它只是个静态
	// HTML 文件，浏览器直接打开首页不应该先被 401 挡住；管理员令牌是页面内
	// JS 调后续接口时才用到。TestWebDir 为空时这里返回 404，行为和不注册
	// 这个路由完全一样。
	r.Get("/", serveStaticHTML(d.TestWebDir, "admin.html"))

	h := &adminHandlers{
		svc: d.Admin, log: d.Logger, pricesync: d.PriceSync, offers: d.Offers, auth: d.Auth,
		legacyToken: d.AdminToken, trustedProxies: d.TrustedProxies, refURLs: d.ReferencePriceURLs.withDefaults(),
		requireIfMatch: d.RequireIfMatch, defaultTZ: d.StatsTZ, redis: d.Redis,
		publicMinAccounts: d.PublicMinAccounts,
	}
	if h.publicMinAccounts <= 0 {
		h.publicMinAccounts = 3
	}
	r.Post("/auth/login", h.login)

	r.Group(func(r chi.Router) {
		r.Use(h.authenticate)
		r.Use(h.idempotency)
		h.registerRoutes(r)
	})

	return r
}

// maxAdminBodyBytes 是管理接口请求体上限；批量导入等大请求也远小于这个值。
const maxAdminBodyBytes = 4 << 20

type adminHandlers struct {
	svc            *admin.Service
	log            *slog.Logger
	pricesync      *pricesync.Engine
	offers         *offers.Store
	auth           *adminauth.Service
	legacyToken    string
	trustedProxies []netip.Prefix
	refURLs        ReferencePriceURLs
	requireIfMatch bool
	defaultTZ      *time.Location
	redis          *redis.Client
	// publicMinAccounts 见 AdminDeps.PublicMinAccounts。
	publicMinAccounts int
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
	case isAdminNotFound(err):
		httpx.WriteError(w, r, http.StatusNotFound, "not_found", err.Error())
	case isAdminConflict(err):
		httpx.WriteError(w, r, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, wallet.ErrBalanceChanged):
		httpx.WriteError(w, r, http.StatusConflict, "balance_changed", err.Error())
	case errors.Is(err, admin.ErrAccountNotFound), errors.Is(err, admin.ErrAPIKeyNotFound),
		errors.Is(err, admin.ErrProviderAccountNotFound),
		errors.Is(err, admin.ErrVirtualModelNotFound), errors.Is(err, admin.ErrChannelNotFound),
		errors.Is(err, pricesync.ErrChangeRequestNotFound), errors.Is(err, pricesync.ErrListingNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, pricesync.ErrChangeRequestNotPending), errors.Is(err, pricesync.ErrListingNotPending),
		errors.Is(err, pricesync.ErrBlockedNeedsConfirm), errors.Is(err, admin.ErrNoActiveProviderKey):
		httpx.WriteError(w, r, http.StatusConflict, "conflict", err.Error())
	case isUniqueViolation(err), errors.Is(err, wallet.ErrDuplicateAdjustRef), errors.Is(err, wallet.ErrDuplicateGrantRef):
		// 唯一约束冲突（重复的 ref_id、重复的 provider code……）是"重复提交/已存在"，
		// 不是参数格式错误——前端据此提示"该单号已调过账"而不是"参数错误"
		// （运营后台接口方案 §0.5）。
		httpx.WriteError(w, r, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, admin.ErrUpstreamUnavailable):
		// 上游 /models 调不通/返回非预期内容——这是上游那边的问题，不是调用方
		// 请求参数有问题，不应该归进默认的 400。上游响应内容只进日志。
		log.Warn("upstream unavailable", "request_id", httpx.RequestIDFromContext(r.Context()), "error", err)
		httpx.WriteError(w, r, http.StatusBadGateway, "upstream_unavailable", "The upstream endpoint is unavailable or returned an unexpected response.")
	case errors.Is(err, admin.ErrKEKNotConfigured):
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "kek_not_configured", "Server has no key-encryption key configured; upstream secrets are unavailable.")
	case errors.Is(err, admin.ErrUnsafeUpstreamURL):
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, "unsafe_upstream_url", err.Error())
	case errors.Is(err, admin.ErrVersionConflict):
		httpx.WriteError(w, r, http.StatusPreconditionFailed, "version_conflict", "The resource was modified by someone else; reload and retry.")
	case errors.Is(err, adminauth.ErrAdminNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, adminauth.ErrEmailTaken), errors.Is(err, adminauth.ErrLastSuperAdmin):
		httpx.WriteError(w, r, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, adminauth.ErrInvalidInput):
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
	default:
		writeUnclassifiedError(w, r, log, err)
	}
}

// writeUnclassifiedError 处理没有哨兵错误的情况：
//   - 约束违反是调用方输入的问题（引用了不存在的对象、违反 CHECK/排他约束），
//     返回 422/409，只给出约束名，不透出 SQL 原文；
//   - 其他数据库/网络/超时错误是服务端问题，返回 500，原文只进日志；
//   - 剩下的是业务层自己构造的校验错误（errors.New/fmt.Errorf 的说明文字），
//     返回 400 并原样给出说明。
func writeUnclassifiedError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	reqID := httpx.RequestIDFromContext(r.Context())
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23503":
			httpx.WriteError(w, r, http.StatusUnprocessableEntity, "invalid_reference", "Referenced object does not exist or is still in use ("+pgErr.ConstraintName+").")
			return
		case "23514", "23502", "22001", "22003", "22P02", "22007", "22008":
			httpx.WriteError(w, r, http.StatusUnprocessableEntity, "constraint_violation", "Value violates a data constraint ("+pgErr.ConstraintName+").")
			return
		case "23P01":
			httpx.WriteError(w, r, http.StatusConflict, "conflict", "Conflicts with an existing record ("+pgErr.ConstraintName+").")
			return
		case "40001", "40P01":
			httpx.WriteError(w, r, http.StatusConflict, "retry", "Concurrent modification detected, please retry.")
			return
		}
		log.Error("admin request failed (database)", "request_id", reqID, "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Internal server error.")
		return
	}
	var netErr net.Error
	var connErr *pgconn.ConnectError
	if errors.As(err, &netErr) || errors.As(err, &connErr) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled) || pgconn.Timeout(err) {
		log.Error("admin request failed (infrastructure)", "request_id", reqID, "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Internal server error.")
		return
	}
	log.Warn("admin request failed", "request_id", reqID, "error", err)
	httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// actor 返回当前已认证管理员（鉴权中间件保证非 nil）。
func actor(r *http.Request) *adminauth.Principal {
	if p := adminauth.FromContext(r.Context()); p != nil {
		return p
	}
	return adminauth.SystemPrincipal()
}

// auditInput 由已认证身份和请求上下文构造审计记录。
func (h *adminHandlers) auditInput(r *http.Request, action, targetType, targetID string, before, after any) admin.AuditLogInput {
	p := actor(r)
	return admin.AuditLogInput{
		ActorID: p.AdminID, ActorName: p.Name, SessionID: p.SessionID,
		Action: action, TargetType: targetType, TargetID: targetID,
		Before: before, After: after, IP: h.clientIP(r), UserAgent: r.UserAgent(),
		RequestID: httpx.RequestIDFromContext(r.Context()),
	}
}

// auditEntry 描述一次写操作的审计内容。
type auditEntry struct {
	action, targetType, targetID string
	before, after                any
}

func idStr(id int64) string { return strconv.FormatInt(id, 10) }

// audited 在同一个数据库事务里执行写操作 op 并写入审计：任一步失败整体回滚，
// 保证"改了就一定有审计、有审计就一定改了"（审计不再是事后补写、失败只记日志）。
// op 里调用的 admin / wallet / pricesync 方法都会加入这个事务（store.RunInTx）。
func audited[T any](h *adminHandlers, r *http.Request, op func(ctx context.Context) (T, auditEntry, error)) (T, error) {
	var out T
	ctx := admin.WithActor(r.Context(), actor(r).AdminID)
	err := h.svc.RunInTx(ctx, func(ctx context.Context) error {
		res, e, err := op(ctx)
		if err != nil {
			return err
		}
		out = res
		_, err = h.svc.RecordAudit(ctx, h.auditInput(r, e.action, e.targetType, e.targetID, e.before, e.after))
		return err
	})
	return out, err
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
	acct, err := audited(h, r, func(ctx context.Context) (*admin.Account, auditEntry, error) {
		acct, err := h.svc.CreateAccount(ctx, in)
		if err != nil {
			return nil, auditEntry{}, err
		}
		return acct, auditEntry{"account.create", "account", idStr(acct.ID), nil, acct}, nil
	})
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
	extras, err := h.svc.GetAccountExtras(r.Context(), id)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	h.setETag(w, r, "accounts", id)
	httpx.WriteJSON(w, http.StatusOK, accountDetailResponse{
		Account: acct, Wallet: wal, Members: extras.Members, ActiveGrantsSummary: extras.ActiveGrantsSummary,
	})
}

type adjustWalletRequest struct {
	// AmountMicro 是调账金额（微元）；Amount 是旧字段名，保留兼容一个版本。
	AmountMicro int64  `json:"amount_micro"`
	Amount      int64  `json:"amount"`
	RefID       string `json:"ref_id"`
	Reason      string `json:"reason"`
	// ExpectedCashBalance 非空时，与事务内的实际现金余额核对，不等返回 409 balance_changed。
	ExpectedCashBalance *int64 `json:"expected_cash_balance_micro"`
}

// amount 优先取 amount_micro，兼容旧字段 amount。
func (in adjustWalletRequest) amount() int64 {
	if in.AmountMicro != 0 {
		return in.AmountMicro
	}
	return in.Amount
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
	reason, ok := requireReason(w, r, in.Reason)
	if !ok {
		return
	}
	out, err := audited(h, r, func(ctx context.Context) (walletAdjustDTO, auditEntry, error) {
		receipt, cashBefore, err := h.svc.Wallet().AdjustChecked(ctx, id, in.amount(), in.RefID, in.ExpectedCashBalance)
		if err != nil {
			return walletAdjustDTO{}, auditEntry{}, err
		}
		out := toWalletAdjustDTO(*receipt)
		return out, auditEntry{"wallet.adjust", "account", accountIDString(id),
			map[string]any{"cash_balance_micro": cashBefore},
			map[string]any{"cash_balance_micro": out.CashAfterMicro, "amount_micro": out.AmountMicro, "ref_id": out.RefID, "reason": reason}}, nil
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

type grantCreditRequest struct {
	Source      string     `json:"source"`
	AmountMicro int64      `json:"amount_micro"`
	Amount      int64      `json:"amount"` // 旧字段名，保留兼容一个版本
	ExpiresAt   *time.Time `json:"expires_at"`
	ModelScope  []string   `json:"model_scope"`
	RefID       string     `json:"ref_id"`
	Reason      string     `json:"reason"`
}

// amount 优先取 amount_micro，兼容旧字段 amount。
func (in grantCreditRequest) amount() int64 {
	if in.AmountMicro != 0 {
		return in.AmountMicro
	}
	return in.Amount
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
	reason, ok := requireReason(w, r, in.Reason)
	if !ok {
		return
	}
	amount := in.amount()
	granted, err := audited(h, r, func(ctx context.Context) (*admin.GrantedCredit, auditEntry, error) {
		granted, err := h.svc.GrantCredit(ctx, admin.GrantCreditInput{
			AccountID: id, Source: in.Source, Amount: amount,
			ExpiresAt: in.ExpiresAt, ModelScope: in.ModelScope, RefID: in.RefID,
		})
		if err != nil {
			return nil, auditEntry{}, err
		}
		return granted, auditEntry{"wallet.credit_grant", "account", accountIDString(id), nil, map[string]any{
			"grant": granted, "source": in.Source, "amount_micro": amount, "expires_at": in.ExpiresAt,
			"model_scope": in.ModelScope, "ref_id": in.RefID, "reason": reason,
		}}, nil
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
	var body createAPIKeyRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	created, err := audited(h, r, func(ctx context.Context) (*admin.CreatedAPIKey, auditEntry, error) {
		created, err := h.svc.CreateAPIKey(ctx, admin.CreateAPIKeyInput{
			AccountID: accountID, Name: body.Name, AllowedModels: body.AllowedModels,
			RPMLimit: body.RPMLimit, TPMLimit: body.TPMLimit, ConcurrencyLimit: body.ConcurrencyLimit,
			BudgetLimitMicro: body.BudgetLimitMicro, BudgetPeriod: body.BudgetPeriod, ExpiresAt: body.ExpiresAt,
		})
		if err != nil {
			return nil, auditEntry{}, err
		}
		// 只审计 APIKey 部分，明文 RawKey 绝不能进审计日志。
		return created, auditEntry{"api_key.create", "account", idStr(accountID), nil, created.APIKey}, nil
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
	// 分页返回（此前一次返回全部 Key、不分页）；不传分页参数时默认第一页 20 条。
	q := &queryParser{r: r}
	in := admin.ListAPIKeysInput{AccountID: accountID, Status: q.enum("status", admin.EnumValues().APIKeyStatuses...), PageRequest: q.page()}
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

func (h *adminHandlers) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt64(r, "apiKeyID")
	if !ok {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid api key id")
		return
	}
	if _, err := audited(h, r, func(ctx context.Context) (struct{}, auditEntry, error) {
		if err := h.svc.RevokeAPIKey(ctx, id); err != nil {
			return struct{}{}, auditEntry{}, err
		}
		return struct{}{}, auditEntry{"api_key.revoke", "api_key", idStr(id), nil, map[string]string{"status": "revoked"}}, nil
	}); err != nil {
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
	p, err := audited(h, r, func(ctx context.Context) (*admin.Provider, auditEntry, error) {
		p, err := h.svc.CreateProvider(ctx, in)
		if err != nil {
			return nil, auditEntry{}, err
		}
		return p, auditEntry{"provider.create", "provider", idStr(p.ID), nil, map[string]any{"provider": p, "allowed_hosts": in.AllowedHosts}}, nil
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, p)
}

func (h *adminHandlers) createProviderAccount(w http.ResponseWriter, r *http.Request) {
	var body createProviderAccountRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	acc, err := audited(h, r, func(ctx context.Context) (*admin.ProviderAccount, auditEntry, error) {
		acc, err := h.svc.CreateProviderAccount(ctx, admin.CreateProviderAccountInput{
			ProviderID: body.ProviderID, Name: body.Name, BaseURL: body.BaseURL, CostMultiplier: body.CostMultiplier,
		})
		if err != nil {
			return nil, auditEntry{}, err
		}
		return acc, auditEntry{"provider_account.create", "provider_account", idStr(acc.ID), nil, acc}, nil
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
	var body addProviderKeyRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	key, err := audited(h, r, func(ctx context.Context) (*admin.ProviderKeySummary, auditEntry, error) {
		key, err := h.svc.AddProviderKey(ctx, admin.AddProviderKeyInput{
			ProviderAccountID: providerAccountID, Secret: body.Secret, Weight: body.Weight,
		})
		if err != nil {
			return nil, auditEntry{}, err
		}
		// key（ProviderKeySummary）从来不包含明文/密文，只有末 4 位——安全地记进审计日志。
		return key, auditEntry{"provider_key.add", "provider_account", idStr(providerAccountID), nil, key}, nil
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
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
	// base_url 刚被改过（24 小时内）时，只有超级管理员能让服务端带着解密后的密钥
	// 去请求这个新地址：防止"先改地址、再立即调用"把密钥外带（方案 §3 B5）。
	if recent, err := h.svc.BaseURLRecentlyChanged(r.Context(), providerAccountID, 24*time.Hour); err != nil {
		writeAdminError(w, r, h.log, err)
		return
	} else if recent && !can(r, adminauth.PermAll) {
		httpx.WriteError(w, r, http.StatusForbidden, "permission_denied",
			"base_url was changed within the last 24 hours; only a super_admin can call the upstream with this account's key now.")
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
	vm, err := audited(h, r, func(ctx context.Context) (*admin.VirtualModel, auditEntry, error) {
		vm, err := h.svc.CreateVirtualModel(ctx, in)
		if err != nil {
			return nil, auditEntry{}, err
		}
		return vm, auditEntry{"virtual_model.create", "virtual_model", idStr(vm.ID), nil, vm}, nil
	})
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

// setVirtualModelMetadata 是 PUT /virtual-models/{id}/metadata 的入口
// （技术方案迭代5）：运营录入公开目录 GET /v1/catalog 的展示层文案与评分。
func (h *adminHandlers) setVirtualModelMetadata(w http.ResponseWriter, r *http.Request) {
	vmID, ok := pathInt64(r, "virtualModelID")
	if !ok {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid virtual model id")
		return
	}
	var body setMetadataRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	in := admin.SetVirtualModelMetadataInput{
		VirtualModelID: vmID, DisplayName: body.DisplayName, Description: body.Description,
		ProviderDisplay: body.ProviderDisplay, Tags: body.Tags, Scores: body.Scores,
	}
	if _, err := audited(h, r, func(ctx context.Context) (struct{}, auditEntry, error) {
		before, err := h.svc.GetVirtualModelMetadata(ctx, vmID)
		if err != nil {
			return struct{}{}, auditEntry{}, err
		}
		if err := h.svc.SetVirtualModelMetadata(ctx, in); err != nil {
			return struct{}{}, auditEntry{}, err
		}
		return struct{}{}, auditEntry{"virtual_model_metadata.set", "virtual_model", idStr(vmID), before, in}, nil
	}); err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *adminHandlers) createChannel(w http.ResponseWriter, r *http.Request) {
	var in admin.CreateChannelInput
	if err := decodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	ch, err := audited(h, r, func(ctx context.Context) (*admin.Channel, auditEntry, error) {
		ch, err := h.svc.CreateChannel(ctx, in)
		if err != nil {
			return nil, auditEntry{}, err
		}
		return ch, auditEntry{"channel.create", "channel", idStr(ch.ID), nil, ch}, nil
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	invalidateTodoCache()
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
	var body setSellPriceRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	bookID, err := audited(h, r, func(ctx context.Context) (int64, auditEntry, error) {
		before, err := h.svc.CurrentPriceBook(ctx, "sell", vmID)
		if err != nil {
			return 0, auditEntry{}, err
		}
		bookID, err := h.svc.SetSellPrice(ctx, admin.SetSellPriceInput{
			VirtualModelID: vmID, Tier: body.Tier, Components: body.Components,
		})
		if err != nil {
			return 0, auditEntry{}, err
		}
		return bookID, auditEntry{"sell_price.set", "virtual_model", idStr(vmID), before,
			map[string]any{"price_book_id": bookID, "tier": body.Tier, "components": body.Components}}, nil
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	invalidateTodoCache()
	httpx.WriteJSON(w, http.StatusCreated, map[string]int64{"price_book_id": bookID})
}

func (h *adminHandlers) setCostPrice(w http.ResponseWriter, r *http.Request) {
	channelID, ok := pathInt64(r, "channelID")
	if !ok {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid channel id")
		return
	}
	var body setCostPriceRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	bookID, err := audited(h, r, func(ctx context.Context) (int64, auditEntry, error) {
		before, err := h.svc.CurrentPriceBook(ctx, "cost", channelID)
		if err != nil {
			return 0, auditEntry{}, err
		}
		bookID, err := h.svc.SetCostPrice(ctx, admin.SetCostPriceInput{
			ChannelID: channelID, Currency: body.Currency, Components: body.Components,
		})
		if err != nil {
			return 0, auditEntry{}, err
		}
		return bookID, auditEntry{"cost_price.set", "channel", idStr(channelID), before,
			map[string]any{"price_book_id": bookID, "currency": body.Currency, "components": body.Components}}, nil
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	invalidateTodoCache()
	httpx.WriteJSON(w, http.StatusCreated, map[string]int64{"price_book_id": bookID})
}

func (h *adminHandlers) setFXRate(w http.ResponseWriter, r *http.Request) {
	var body setFXRateRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	in := admin.SetFXRateInput{Base: body.Base, Quote: body.Quote, Rate: body.Rate, Source: body.Source}
	if body.EffectiveDate != nil {
		in.EffectiveDate = *body.EffectiveDate
	}
	rate, err := audited(h, r, func(ctx context.Context) (*admin.FXRateInfo, auditEntry, error) {
		prev, rate, err := h.svc.SetFXRate(ctx, in)
		if err != nil {
			return nil, auditEntry{}, err
		}
		// FX 是按 (base, quote, 日期) upsert 的，before 记录被覆盖的旧值（新建时为 nil）。
		return rate, auditEntry{"fx_rate.set", "fx_rate", rate.Base + "/" + rate.Quote + "@" + rate.EffectiveDate.Format(time.DateOnly), prev, rate}, nil
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	invalidateTodoCache()
	httpx.WriteJSON(w, http.StatusOK, rate)
}

// listAuditLogs 是审计导出的查询入口（技术方案 Phase 4）：?target_type=&target_id=
// 都可以留空，留空表示不按那个维度过滤；?limit= 留空用默认值 100。
func (h *adminHandlers) listAuditLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	in := admin.ListAuditLogsInput{
		TargetType: q.Get("target_type"), TargetID: q.Get("target_id"),
		ActorName: q.Get("actor_name"), Action: q.Get("action"), Before: q.Get("before"),
	}
	qp := &queryParser{r: r}
	in.Limit = qp.int("limit")
	if !qp.ok(w) {
		return
	}
	if v := q.Get("actor_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid actor_id")
			return
		}
		in.ActorID = id
	}
	var err error
	if in.From, err = parseTimeParam(q.Get("from"), false); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid 'from': "+err.Error())
		return
	}
	if in.To, err = parseTimeParam(q.Get("to"), true); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid 'to': "+err.Error())
		return
	}
	entries, next, err := h.svc.ListAuditLogs(r.Context(), in)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": entries, "next_cursor": next})
}

// parseTimeParam 接受 RFC3339 或 YYYY-MM-DD（按 UTC 解释）。endOfDay=true 时，
// 纯日期表示"包含当天"，返回次日 00:00 作为开区间上界（和 /console/usage 的
// 闭区间语义一致）。空字符串返回零值（不限）。
func parseTimeParam(v string, endOfDay bool) (time.Time, error) {
	return parseTimeParamIn(v, endOfDay, time.UTC)
}

// parseTimeParamIn 同 parseTimeParam，纯日期按 loc 时区的零点解释。
func parseTimeParamIn(v string, endOfDay bool, loc *time.Location) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	d, err := time.ParseInLocation("2006-01-02", v, loc)
	if err != nil {
		return time.Time{}, errors.New("want RFC3339 or YYYY-MM-DD")
	}
	if endOfDay {
		d = d.AddDate(0, 0, 1)
	}
	return d, nil
}
