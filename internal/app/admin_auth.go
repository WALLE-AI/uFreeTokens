package app

import (
	"context"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/WALLE-AI/uFreeTokens/internal/adminauth"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
)

// 管理员登录、会话鉴权与权限检查（B5 安全基线，见
// docs/运营后台接口与数据库设计问题分析及执行方案.md §3 B5）。

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
}

// authenticate 解析 Authorization: Bearer <token>：
//   - 以 "uas_" 开头的是管理员会话令牌，查 admin_sessions；
//   - 否则与应急共享令牌（AdminDeps.AdminToken）常量时间比较，匹配则身份为 system。
//
// 两者都不匹配返回 401。应急令牌为空字符串时不接受任何非会话令牌。
func (h *adminHandlers) authenticate(next http.Handler) http.Handler {
	legacy := []byte(h.legacyToken)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		var p *adminauth.Principal
		switch {
		case token == "":
		case strings.HasPrefix(token, "uas_") && h.auth != nil:
			var err error
			p, err = h.auth.Authenticate(r.Context(), token)
			if err != nil && !errors.Is(err, adminauth.ErrSessionInvalid) {
				h.log.Error("admin session lookup failed", "error", err)
				httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Internal server error.")
				return
			}
		case len(legacy) > 0 && subtle.ConstantTimeCompare([]byte(token), legacy) == 1:
			p = adminauth.SystemPrincipal()
		}
		if p == nil {
			httpx.WriteError(w, r, http.StatusUnauthorized, "unauthorized", "Missing, invalid or expired admin credentials.")
			return
		}
		next.ServeHTTP(w, r.WithContext(adminauth.WithPrincipal(r.Context(), p)))
	})
}

// requirePermission 检查当前管理员是否拥有 perm；perm 为空只要求已登录。
func requirePermission(perm adminauth.Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := adminauth.FromContext(r.Context())
			if p == nil {
				httpx.WriteError(w, r, http.StatusUnauthorized, "unauthorized", "Missing, invalid or expired admin credentials.")
				return
			}
			if perm != permAuthenticated && !p.Can(perm) {
				httpx.WriteError(w, r, http.StatusForbidden, "permission_denied", "This action requires permission '"+string(perm)+"'.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// can 供 handler 内做字段级的附加权限检查（例如修改 base_url 需要 provider_key:write）。
func can(r *http.Request, perm adminauth.Permission) bool {
	return adminauth.FromContext(r.Context()).Can(perm)
}

func forbid(w http.ResponseWriter, r *http.Request, perm adminauth.Permission) {
	httpx.WriteError(w, r, http.StatusForbidden, "permission_denied", "This action requires permission '"+string(perm)+"'.")
}

// clientIP 取调用方地址：RemoteAddr 属于可信代理时，从 X-Forwarded-For 自右向左
// 找第一个不属于可信代理的地址；否则直接用 RemoteAddr（不信任客户端自填的头）。
func (h *adminHandlers) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil || !h.isTrustedProxy(addr) {
		return host
	}
	parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
		if err != nil {
			break
		}
		if !h.isTrustedProxy(a) {
			return a.String()
		}
	}
	return host
}

func (h *adminHandlers) isTrustedProxy(a netip.Addr) bool {
	a = a.Unmap()
	for _, p := range h.trustedProxies {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// ---------- 登录 / 会话 ----------

func (h *adminHandlers) login(w http.ResponseWriter, r *http.Request) {
	if h.auth == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "not_implemented", "Admin login is not configured on this server.")
		return
	}
	var body loginRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	res, err := h.auth.Login(r.Context(), adminauth.LoginInput{
		Email: body.Email, Password: body.Password, TOTPCode: body.TOTPCode, IP: h.clientIP(r), UserAgent: r.UserAgent(),
	})
	switch {
	case errors.Is(err, adminauth.ErrTOTPRequired):
		httpx.WriteError(w, r, http.StatusUnauthorized, "totp_required", "Two-factor code required.")
		return
	case errors.Is(err, adminauth.ErrTOTPInvalid):
		httpx.WriteError(w, r, http.StatusUnauthorized, "invalid_totp", "Invalid two-factor code.")
		return
	case errors.Is(err, adminauth.ErrInvalidCredentials):
		httpx.WriteError(w, r, http.StatusUnauthorized, "invalid_credentials", "Invalid email or password.")
		return
	case errors.Is(err, adminauth.ErrTooManyAttempts):
		w.Header().Set("Retry-After", "900")
		httpx.WriteError(w, r, http.StatusTooManyRequests, "rate_limit_exceeded", "Too many failed login attempts, try again later.")
		return
	case err != nil:
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *adminHandlers) logout(w http.ResponseWriter, r *http.Request) {
	p := adminauth.FromContext(r.Context())
	if p.SessionID != 0 && h.auth != nil {
		if err := h.auth.Logout(r.Context(), p.SessionID); err != nil {
			writeAdminError(w, r, h.log, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

type meResponse struct {
	ID          int64                  `json:"id"`
	Name        string                 `json:"name"`
	Email       string                 `json:"email"`
	Roles       []string               `json:"roles"`
	Permissions []adminauth.Permission `json:"permissions"`
	BreakGlass  bool                   `json:"break_glass"`
	TOTPEnabled bool                   `json:"totp_enabled"`
}

func (h *adminHandlers) me(w http.ResponseWriter, r *http.Request) {
	p := adminauth.FromContext(r.Context())
	perms := p.Permissions
	if p.Can(adminauth.PermAll) {
		perms = append([]adminauth.Permission{adminauth.PermAll}, adminauth.AllPermissions...)
	}
	resp := meResponse{ID: p.AdminID, Name: p.Name, Email: p.Email, Roles: p.Roles, Permissions: perms, BreakGlass: p.BreakGlass}
	if !p.BreakGlass && h.auth != nil {
		if u, err := h.auth.GetAdmin(r.Context(), p.AdminID); err == nil {
			resp.TOTPEnabled = u.TOTPEnabled
		}
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (h *adminHandlers) changePassword(w http.ResponseWriter, r *http.Request) {
	p := adminauth.FromContext(r.Context())
	if p.BreakGlass || h.auth == nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "The emergency token identity has no password.")
		return
	}
	var body changePasswordRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	if _, err := audited(h, r, func(ctx context.Context) (struct{}, auditEntry, error) {
		if err := h.auth.ChangePassword(ctx, p.AdminID, p.SessionID, body.OldPassword, body.NewPassword); err != nil {
			return struct{}{}, auditEntry{}, err
		}
		return struct{}{}, auditEntry{"admin_user.change_password", "admin_user", idStr(p.AdminID), nil, map[string]any{"password_changed": true}}, nil
	}); err != nil {
		if errors.Is(err, adminauth.ErrInvalidCredentials) {
			httpx.WriteError(w, r, http.StatusBadRequest, "invalid_credentials", "Current password is incorrect.")
			return
		}
		writeAdminError(w, r, h.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- 管理员管理 ----------

func (h *adminHandlers) requireAuthService(w http.ResponseWriter, r *http.Request) bool {
	if h.auth == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "not_implemented", "Admin accounts are not configured on this server.")
		return false
	}
	return true
}

func (h *adminHandlers) listAdminUsers(w http.ResponseWriter, r *http.Request) {
	if !h.requireAuthService(w, r) {
		return
	}
	list, err := h.auth.ListAdmins(r.Context())
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": list})
}

func (h *adminHandlers) listAdminRoles(w http.ResponseWriter, r *http.Request) {
	if !h.requireAuthService(w, r) {
		return
	}
	list, err := h.auth.ListRoles(r.Context())
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": list})
}

func (h *adminHandlers) createAdminUser(w http.ResponseWriter, r *http.Request) {
	if !h.requireAuthService(w, r) {
		return
	}
	var in adminauth.CreateAdminInput
	if err := decodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	u, err := audited(h, r, func(ctx context.Context) (*adminauth.AdminUser, auditEntry, error) {
		u, err := h.auth.CreateAdmin(ctx, in)
		if err != nil {
			return nil, auditEntry{}, err
		}
		return u, auditEntry{"admin_user.create", "admin_user", idStr(u.ID), nil, u}, nil
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, u)
}

func (h *adminHandlers) updateAdminUser(w http.ResponseWriter, r *http.Request) {
	if !h.requireAuthService(w, r) {
		return
	}
	id, ok := pathID(w, r, "adminUserID", "admin user")
	if !ok {
		return
	}
	var in adminauth.UpdateAdminInput
	if err := decodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	after, err := audited(h, r, func(ctx context.Context) (*adminauth.AdminUser, auditEntry, error) {
		before, after, err := h.auth.UpdateAdmin(ctx, id, in)
		if err != nil {
			return nil, auditEntry{}, err
		}
		return after, auditEntry{"admin_user.update", "admin_user", idStr(id), before,
			map[string]any{"user": after, "password_reset": in.Password != nil}}, nil
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, after)
}

// ---------- 两步验证（TOTP） ----------

// totpSetup 生成新的 TOTP 密钥（只在这次响应里出现），管理员用验证器扫码后调用
// totpEnable 提交一个验证码确认启用。
func (h *adminHandlers) totpSetup(w http.ResponseWriter, r *http.Request) {
	p := actor(r)
	if p.BreakGlass || !h.requireAuthService(w, r) {
		if p.BreakGlass {
			httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "The emergency token identity cannot enroll two-factor authentication.")
		}
		return
	}
	setup, err := audited(h, r, func(ctx context.Context) (*adminauth.TOTPSetup, auditEntry, error) {
		setup, err := h.auth.SetupTOTP(ctx, p.AdminID, p.Email)
		if err != nil {
			return nil, auditEntry{}, err
		}
		return setup, auditEntry{"admin_user.totp_setup", "admin_user", idStr(p.AdminID), nil, map[string]any{"pending": true}}, nil
	})
	if err != nil {
		writeTOTPError(w, r, h, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, setup)
}

func (h *adminHandlers) totpEnable(w http.ResponseWriter, r *http.Request) {
	h.totpToggle(w, r, true)
}

func (h *adminHandlers) totpDisable(w http.ResponseWriter, r *http.Request) {
	h.totpToggle(w, r, false)
}

func (h *adminHandlers) totpToggle(w http.ResponseWriter, r *http.Request, enable bool) {
	p := actor(r)
	if p.BreakGlass || !h.requireAuthService(w, r) {
		if p.BreakGlass {
			httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "The emergency token identity has no two-factor settings.")
		}
		return
	}
	var body totpCodeRequest
	if err := decodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	action := "admin_user.totp_disable"
	if enable {
		action = "admin_user.totp_enable"
	}
	if _, err := audited(h, r, func(ctx context.Context) (struct{}, auditEntry, error) {
		var err error
		if enable {
			err = h.auth.EnableTOTP(ctx, p.AdminID, body.Code)
		} else {
			err = h.auth.DisableTOTP(ctx, p.AdminID, body.Code)
		}
		if err != nil {
			return struct{}{}, auditEntry{}, err
		}
		return struct{}{}, auditEntry{action, "admin_user", idStr(p.AdminID), nil, map[string]any{"totp_enabled": enable}}, nil
	}); err != nil {
		writeTOTPError(w, r, h, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeTOTPError(w http.ResponseWriter, r *http.Request, h *adminHandlers, err error) {
	switch {
	case errors.Is(err, adminauth.ErrTOTPInvalid):
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_totp", "Invalid two-factor code.")
	case errors.Is(err, adminauth.ErrTOTPUnavailable):
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "kek_not_configured", "Two-factor authentication requires a server KEK.")
	case errors.Is(err, adminauth.ErrTOTPNotSetUp), errors.Is(err, adminauth.ErrTOTPAlreadyOn):
		httpx.WriteError(w, r, http.StatusConflict, "conflict", err.Error())
	default:
		writeAdminError(w, r, h.log, err)
	}
}
