// Package console 实现面向终端用户的自助控制台后端（技术方案 Phase 1）：
// 注册、登录、自助 API Key 管理、钱包查询。挂在 gateway 进程的 /console/*
// （见 internal/app/gateway.go），和 /v1/* 共享同一个进程但鉴权机制完全
// 独立——/v1/* 用 Authorization: Bearer <api-key>（internal/auth），
// /console/* 用 httpOnly Cookie + Redis Session（本包的 SessionStore）。两者
// 不应该互相依赖：一个用户可以有一个有效的控制台会话、同时手上没有任何
// API Key（刚注册还没建 Key），也可以有一把有效的 API Key、但控制台会话已经
// 过期（换了台设备直接拿 Key 调 /v1）。
//
// 已知的范围限制（尚未实现，非遗漏）：
//   - 没有邮箱验证：Register 直接把账户置为可用，users.email_verified 始终
//     是 false（数据库默认值）。V2 A7 要求的"注册赠送需要验证通过后才发放"
//     因此本迭代也没有实现——没有邮件基础设施，宁可不发这笔赠送余额，也不want
//     在没有验证的前提下发钱造成刷量风险。
//   - 一个 user 目前只会属于自己注册时创建的那一个 personal 账户
//     （account_members 表本身支持多对多，是为组织账户的多成员场景预留的，
//     但 Register 只建 personal + owner 这一种关系）；Login 找账户时直接取
//     第一条，没有"选择要登录到哪个账户"的多账户切换流程。
package console

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/ratelimit"
)

const minPasswordLen = 8

var (
	ErrInvalidEmail       = errors.New("console: invalid email address")
	ErrWeakPassword       = errors.New("console: password too short")
	ErrEmailTaken         = errors.New("console: email already registered")
	ErrInvalidCredentials = errors.New("console: invalid email or password")
)

var emailRegex = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func validEmail(email string) bool { return emailRegex.MatchString(email) }

// Config 是 Service 的可调参数。
type Config struct {
	// CookieSecure 控制会话 Cookie 是否带 Secure 属性（只在 HTTPS 下发送）。
	// 本地 http 开发环境必须关掉，否则浏览器会直接丢弃这个 Cookie；生产
	// 环境（HTTPS 反代）应该打开。对应配置项 console.cookie_secure /
	// 环境变量 UFT_CONSOLE_COOKIE_SECURE。
	CookieSecure bool
}

// Service 组装控制台业务逻辑所需的全部依赖。account/api_keys 相关的实际读写
// 复用 internal/admin.Service（技术方案要求"复用和小幅重构 internal/admin"），
// 这里不重新实现一遍账户/Key 的 CRUD。
type Service struct {
	pool      *pgxpool.Pool
	admin     *admin.Service
	sessions  *SessionStore
	ratelimit *ratelimit.Limiter
	logger    *slog.Logger
	cfg       Config
}

func New(pool *pgxpool.Pool, adminSvc *admin.Service, sessions *SessionStore, rl *ratelimit.Limiter, logger *slog.Logger, cfg Config) *Service {
	return &Service{pool: pool, admin: adminSvc, sessions: sessions, ratelimit: rl, logger: logger, cfg: cfg}
}

// Sessions 暴露内部的 SessionStore，供 internal/app 装配 RequireSession
// 中间件使用——和 admin.Service.Wallet() 是同样的"递出已经装配好的依赖，而不
// 是让调用方自己再建一个"的模式。
func (s *Service) Sessions() *SessionStore { return s.sessions }

// Register 创建一个新用户 + 一个 personal/free 账户 + 空钱包 + owner 身份的
// account_member，四步在同一个数据库事务里完成（技术方案要求的原子性：不
// 允许出现"有 user 没 account"或"有 account 没 wallet"的中间状态）。不做
// 登录，调用方需要接着调用 Login 换取会话（技术方案迭代 3 的验证流程明确是
// "注册 → 登录 → 建 Key"三个独立步骤）。
func (s *Service) Register(ctx context.Context, email, password string) (userID, accountID int64, err error) {
	email = normalizeEmail(email)
	if !validEmail(email) {
		return 0, 0, ErrInvalidEmail
	}
	if len(password) < minPasswordLen {
		return 0, 0, ErrWeakPassword
	}

	hash, err := HashPassword(password)
	if err != nil {
		return 0, 0, fmt.Errorf("console: hash password: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("console: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	acct, err := admin.CreateAccountTx(ctx, tx, admin.CreateAccountInput{Type: "personal", Name: email, Tier: "free"})
	if err != nil {
		return 0, 0, fmt.Errorf("console: create account: %w", err)
	}

	if err := tx.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, status, email_verified) VALUES ($1, $2, 'active', false) RETURNING id`,
		email, hash,
	).Scan(&userID); err != nil {
		if isUniqueViolation(err) {
			return 0, 0, ErrEmailTaken
		}
		return 0, 0, fmt.Errorf("console: insert user: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO account_members (account_id, user_id, role) VALUES ($1, $2, 'owner')`,
		acct.ID, userID,
	); err != nil {
		return 0, 0, fmt.Errorf("console: insert account_member: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("console: commit: %w", err)
	}
	return userID, acct.ID, nil
}

// Login 校验邮箱/密码，返回该用户的身份信息供调用方创建会话。密码错误和
// 邮箱不存在返回完全相同的 ErrInvalidCredentials，且耗时大致相当
// （VerifyDummyPassword），避免被用来做用户名枚举。
func (s *Service) Login(ctx context.Context, email, password string) (userID, accountID int64, err error) {
	email = normalizeEmail(email)

	var (
		hash   string
		status string
	)
	err = s.pool.QueryRow(ctx,
		`SELECT id, password_hash, status FROM users WHERE email = $1`, email,
	).Scan(&userID, &hash, &status)
	if err != nil {
		if isNoRows(err) {
			VerifyDummyPassword(password)
			return 0, 0, ErrInvalidCredentials
		}
		return 0, 0, fmt.Errorf("console: query user: %w", err)
	}

	ok, verr := VerifyPassword(password, hash)
	if verr != nil {
		return 0, 0, fmt.Errorf("console: verify password: %w", verr)
	}
	if !ok || status != "active" {
		return 0, 0, ErrInvalidCredentials
	}

	if err := s.pool.QueryRow(ctx,
		`SELECT account_id FROM account_members WHERE user_id = $1 ORDER BY account_id LIMIT 1`, userID,
	).Scan(&accountID); err != nil {
		return 0, 0, fmt.Errorf("console: find account for user: %w", err)
	}
	return userID, accountID, nil
}

// MeInfo 是 GET /console/me 的响应内容。
type MeInfo struct {
	UserID        int64
	Email         string
	EmailVerified bool
	AccountID     int64
	AccountTier   string
}

func (s *Service) Me(ctx context.Context, sess *SessionData) (*MeInfo, error) {
	info := &MeInfo{UserID: sess.UserID, AccountID: sess.AccountID}
	if err := s.pool.QueryRow(ctx,
		`SELECT email, email_verified FROM users WHERE id = $1`, sess.UserID,
	).Scan(&info.Email, &info.EmailVerified); err != nil {
		return nil, fmt.Errorf("console: query user: %w", err)
	}
	if err := s.pool.QueryRow(ctx,
		`SELECT tier FROM accounts WHERE id = $1`, sess.AccountID,
	).Scan(&info.AccountTier); err != nil {
		return nil, fmt.Errorf("console: query account: %w", err)
	}
	return info, nil
}

// ListKeys 返回该账户名下的全部 API Key（不含明文/HMAC）。
func (s *Service) ListKeys(ctx context.Context, accountID int64) ([]admin.APIKey, error) {
	return s.admin.ListAPIKeys(ctx, accountID)
}

// CreateKey 给该账户建一把新 Key，created_by 记为发起操作的用户。明文只在
// 这次返回值里出现，此后不可恢复（admin.CreatedAPIKey.RawKey 的注释）。
func (s *Service) CreateKey(ctx context.Context, accountID, userID int64, name string) (*admin.CreatedAPIKey, error) {
	createdBy := userID
	return s.admin.CreateAPIKey(ctx, admin.CreateAPIKeyInput{AccountID: accountID, CreatedBy: &createdBy, Name: name})
}

// RevokeKey 吊销该账户名下的一把 Key；按 account_id 限定范围，防止越权吊销
// 别的账户的 Key（admin.Service.RevokeAPIKeyForAccount 的注释）。
func (s *Service) RevokeKey(ctx context.Context, accountID, apiKeyID int64) error {
	return s.admin.RevokeAPIKeyForAccount(ctx, accountID, apiKeyID)
}

// Wallet 返回该账户的钱包余额快照。
func (s *Service) Wallet(ctx context.Context, accountID int64) (*admin.WalletSummary, error) {
	_, w, err := s.admin.GetAccount(ctx, accountID)
	return w, err
}

// RecordAudit 转发给 admin.Service.RecordAudit，供 handlers.go 在 Key 创建/
// 吊销后记审计（技术方案要求：actor 记为 user_id）。
func (s *Service) RecordAudit(ctx context.Context, in admin.AuditLogInput) {
	if _, err := s.admin.RecordAudit(ctx, in); err != nil {
		s.logger.Warn("console: record audit failed", "action", in.Action, "error", err)
	}
}

// AllowLoginAttempt 按 IP 和邮箱两个维度做登录限流（技术方案：IP 10次/分钟，
// 邮箱 5次/分钟），用 fail-closed 的 AllowRPMStrict——防暴力破解场景下，
// Redis 故障时"错误放行"比"错误拒绝"危害大得多（见该方法注释）。两个维度
// 任一触发限流就拒绝，返回触发限流的那个 Result（用于设置 Retry-After）。
func (s *Service) AllowLoginAttempt(ctx context.Context, ip, email string) ratelimit.Result {
	if res := s.ratelimit.AllowRPMStrict(ctx, "console:login:ip:"+ip, 10); !res.Allowed {
		return res
	}
	return s.ratelimit.AllowRPMStrict(ctx, "console:login:email:"+normalizeEmail(email), 5)
}

// AllowRegisterAttempt 按 IP 限流注册（技术方案：3次/分钟）。
func (s *Service) AllowRegisterAttempt(ctx context.Context, ip string) ratelimit.Result {
	return s.ratelimit.AllowRPMStrict(ctx, "console:register:ip:"+ip, 3)
}

func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
