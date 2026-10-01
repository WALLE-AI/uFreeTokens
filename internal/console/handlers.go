package console

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
	"github.com/WALLE-AI/uFreeTokens/internal/ratelimit"
)

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// HandleRegister 是 POST /console/register 的入口：不需要会话。按 IP 限流
// 3次/分钟（技术方案）防止批量注册。
func (s *Service) HandleRegister(w http.ResponseWriter, r *http.Request) {
	if res := s.AllowRegisterAttempt(r.Context(), clientIP(r)); !res.Allowed {
		writeRateLimited(w, r, res)
		return
	}

	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "Request body is not valid JSON.")
		return
	}

	userID, accountID, err := s.Register(r.Context(), req.Email, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidEmail):
			httpx.WriteError(w, r, http.StatusBadRequest, "invalid_email", "Invalid email address.")
		case errors.Is(err, ErrWeakPassword):
			httpx.WriteError(w, r, http.StatusBadRequest, "weak_password",
				fmt.Sprintf("Password must be at least %d characters.", minPasswordLen))
		case errors.Is(err, ErrEmailTaken):
			httpx.WriteError(w, r, http.StatusConflict, "email_taken", "This email is already registered.")
		default:
			s.logger.Error("console: register failed", "error", err)
			httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Failed to register.")
		}
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"user_id": userID, "account_id": accountID})
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// HandleLogin 是 POST /console/login 的入口：不需要会话。按 IP（10次/分钟）
// 和邮箱（5次/分钟）两个维度限流，成功后签发会话 Cookie。
func (s *Service) HandleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "Request body is not valid JSON.")
		return
	}

	if res := s.AllowLoginAttempt(r.Context(), clientIP(r), req.Email); !res.Allowed {
		writeRateLimited(w, r, res)
		return
	}

	userID, accountID, err := s.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			httpx.WriteError(w, r, http.StatusUnauthorized, "invalid_credentials", "Invalid email or password.")
			return
		}
		s.logger.Error("console: login failed", "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Failed to login.")
		return
	}

	token, err := s.sessions.Create(r.Context(), SessionData{UserID: userID, AccountID: accountID, CreatedAt: time.Now().Unix()})
	if err != nil {
		s.logger.Error("console: create session failed", "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Failed to create session.")
		return
	}
	s.setSessionCookie(w, token)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"user_id": userID, "account_id": accountID})
}

// HandleLogout 是 POST /console/logout 的入口：不需要会话（没带 Cookie/Cookie
// 已经无效都视为成功，客户端此后总归是"未登录"状态）。总是清掉 Cookie。
func (s *Service) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(SessionCookieName); err == nil && cookie.Value != "" {
		_ = s.sessions.Delete(r.Context(), cookie.Value)
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// HandleMe 是 GET /console/me 的入口：需要会话（RequireSession 中间件）。
func (s *Service) HandleMe(w http.ResponseWriter, r *http.Request) {
	sess, ok := SessionFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, r, http.StatusUnauthorized, "unauthorized", "Not logged in.")
		return
	}
	info, err := s.Me(r.Context(), sess)
	if err != nil {
		s.logger.Error("console: me failed", "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Failed to load profile.")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"user_id": info.UserID, "email": info.Email, "email_verified": info.EmailVerified,
		"account_id": info.AccountID, "account_tier": info.AccountTier,
		"exclude_from_public_stats": info.ExcludeFromPublicStats,
	})
}

type publicStatsRequest struct {
	ExcludeFromPublicStats *bool `json:"exclude_from_public_stats"`
}

// HandleSetPublicStats 是 PUT /console/settings/public-stats 的入口：需要会话 + CSRF，
// 只有账户 owner / admin 能修改。
func (s *Service) HandleSetPublicStats(w http.ResponseWriter, r *http.Request) {
	sess, ok := SessionFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, r, http.StatusUnauthorized, "unauthorized", "Not logged in.")
		return
	}
	var req publicStatsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ExcludeFromPublicStats == nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "\"exclude_from_public_stats\" (boolean) is required.")
		return
	}
	exclude := *req.ExcludeFromPublicStats
	if err := s.SetPublicStatsOptOut(r.Context(), sess.AccountID, sess.UserID, exclude); err != nil {
		if errors.Is(err, ErrNotAccountAdmin) {
			httpx.WriteError(w, r, http.StatusForbidden, "permission_denied", "Only account owners and admins can change this setting.")
			return
		}
		s.logger.Error("console: set public stats opt-out failed", "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Failed to update setting.")
		return
	}
	s.RecordAudit(r.Context(), admin.AuditLogInput{
		ActorID: sess.UserID, Action: "account.public_stats_opt_out", TargetType: "account", TargetID: strconv.FormatInt(sess.AccountID, 10),
		IP: clientIP(r), After: map[string]any{"exclude_from_public_stats": exclude},
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"exclude_from_public_stats": exclude})
}

// HandleListKeys 是 GET /console/api-keys 的入口：需要会话。
func (s *Service) HandleListKeys(w http.ResponseWriter, r *http.Request) {
	sess, ok := SessionFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, r, http.StatusUnauthorized, "unauthorized", "Not logged in.")
		return
	}
	keys, err := s.ListKeys(r.Context(), sess.AccountID)
	if err != nil {
		s.logger.Error("console: list keys failed", "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Failed to list API keys.")
		return
	}
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		out = append(out, apiKeyToJSON(k))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": out})
}

type createKeyRequest struct {
	Name string `json:"name"`
}

// HandleCreateKey 是 POST /console/api-keys 的入口：需要会话 + CSRF 头。明文
// Key 只在这次响应里出现一次。
func (s *Service) HandleCreateKey(w http.ResponseWriter, r *http.Request) {
	sess, ok := SessionFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, r, http.StatusUnauthorized, "unauthorized", "Not logged in.")
		return
	}
	var req createKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "Request body is not valid JSON.")
		return
	}
	if req.Name == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "\"name\" is required.")
		return
	}

	created, err := s.CreateKey(r.Context(), sess.AccountID, sess.UserID, req.Name)
	if err != nil {
		s.logger.Error("console: create key failed", "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Failed to create API key.")
		return
	}

	s.RecordAudit(r.Context(), admin.AuditLogInput{
		ActorID: sess.UserID, Action: "api_key.create", TargetType: "api_key",
		TargetID: strconv.FormatInt(created.ID, 10), After: apiKeyToJSON(created.APIKey), IP: clientIP(r),
	})

	resp := apiKeyToJSON(created.APIKey)
	resp["key"] = created.RawKey
	httpx.WriteJSON(w, http.StatusCreated, resp)
}

// HandleRevokeKey 是 POST /console/api-keys/{id}/revoke 的入口：需要会话 +
// CSRF 头。按 account_id 限定范围，A 账户吊销 B 账户的 Key 返回 404。
func (s *Service) HandleRevokeKey(w http.ResponseWriter, r *http.Request) {
	sess, ok := SessionFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, r, http.StatusUnauthorized, "unauthorized", "Not logged in.")
		return
	}
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "Invalid API key id.")
		return
	}

	if err := s.RevokeKey(r.Context(), sess.AccountID, id); err != nil {
		if errors.Is(err, admin.ErrAPIKeyNotFound) {
			httpx.WriteError(w, r, http.StatusNotFound, "api_key_not_found", "API key not found.")
			return
		}
		s.logger.Error("console: revoke key failed", "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Failed to revoke API key.")
		return
	}

	s.RecordAudit(r.Context(), admin.AuditLogInput{
		ActorID: sess.UserID, Action: "api_key.revoke", TargetType: "api_key", TargetID: idStr, IP: clientIP(r),
	})
	w.WriteHeader(http.StatusNoContent)
}

// HandleWallet 是 GET /console/wallet 的入口：需要会话，不需要 API Key
// （技术方案：控制台鉴权与 API Key 鉴权完全分离）。
func (s *Service) HandleWallet(w http.ResponseWriter, r *http.Request) {
	sess, ok := SessionFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, r, http.StatusUnauthorized, "unauthorized", "Not logged in.")
		return
	}
	wal, err := s.Wallet(r.Context(), sess.AccountID)
	if err != nil {
		s.logger.Error("console: wallet failed", "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Failed to load wallet.")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"cash_balance_micro": wal.CashBalance, "bonus_balance_micro": wal.BonusBalance, "frozen_micro": wal.Frozen,
	})
}

// HandleUsageInterval 是 GET /console/usage?from=&to=&group_by=day|model 的
// 入口：需要会话。from/to 是 YYYY-MM-DD，留空默认本月至今；区间最长 90 天
// （技术方案迭代5）。
func (s *Service) HandleUsageInterval(w http.ResponseWriter, r *http.Request) {
	sess, ok := SessionFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, r, http.StatusUnauthorized, "unauthorized", "Not logged in.")
		return
	}
	q := r.URL.Query()
	from, err := parseOptionalDate(q.Get("from"))
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "'from' must be YYYY-MM-DD.")
		return
	}
	to, err := parseOptionalDate(q.Get("to"))
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "'to' must be YYYY-MM-DD.")
		return
	}

	rows, err := s.UsageInterval(r.Context(), sess.AccountID, from, to, q.Get("group_by"))
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidGroupBy):
			httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "'group_by' must be 'day' or 'model'.")
		case errors.Is(err, ErrInvalidDateRange):
			httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid date range: max 90 days, and 'from' must not be after 'to'.")
		default:
			s.logger.Error("console: usage interval failed", "error", err)
			httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Failed to load usage.")
		}
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": rows})
}

func parseOptionalDate(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse("2006-01-02", s)
}

// HandleLogs 是 GET /console/logs?before=&limit=&api_key_id= 的入口：需要
// 会话。keyset 分页，倒序，时间窗最长 30 天，每页最多 100 条（技术方案迭代5）。
func (s *Service) HandleLogs(w http.ResponseWriter, r *http.Request) {
	sess, ok := SessionFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, r, http.StatusUnauthorized, "unauthorized", "Not logged in.")
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))

	var apiKeyID int64
	if v := q.Get("api_key_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid 'api_key_id'.")
			return
		}
		apiKeyID = id
	}

	entries, next, err := s.ListLogs(r.Context(), sess.AccountID, q.Get("before"), limit, apiKeyID)
	if err != nil {
		if errors.Is(err, ErrInvalidCursor) {
			httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "invalid 'before' cursor.")
			return
		}
		s.logger.Error("console: list logs failed", "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Failed to load logs.")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": entries, "next_cursor": next})
}

func apiKeyToJSON(k admin.APIKey) map[string]any {
	return map[string]any{
		"id": k.ID, "name": k.Name, "display_prefix": k.DisplayPrefix, "status": k.Status,
		"allowed_models": k.AllowedModels, "rpm_limit": k.RPMLimit, "tpm_limit": k.TPMLimit,
		"concurrency_limit": k.ConcurrencyLimit, "created_at": k.CreatedAt,
	}
}

func (s *Service) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookieName, Value: token, Path: "/console",
		HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
		MaxAge: int(SessionTTL.Seconds()),
	})
}

func (s *Service) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookieName, Value: "", Path: "/console",
		HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
		MaxAge: -1,
	})
}

func writeRateLimited(w http.ResponseWriter, r *http.Request, res ratelimit.Result) {
	if res.RetryAfter > 0 {
		secs := int(res.RetryAfter.Seconds())
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(secs))
	}
	httpx.WriteError(w, r, http.StatusTooManyRequests, "rate_limit_exceeded", "Too many attempts, please try again later.")
}

// clientIP 尽量拿到客户端地址（去掉端口），和 relay 包同名函数逻辑一致
// （两个包不应该互相依赖，各自维护一份这几行足够简单，不值得为此抽出共享包）。
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
