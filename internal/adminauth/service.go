package adminauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/WALLE-AI/uFreeTokens/internal/console"
	"github.com/WALLE-AI/uFreeTokens/internal/secretbox"
	"github.com/WALLE-AI/uFreeTokens/internal/store"
)

var (
	ErrInvalidCredentials = errors.New("adminauth: invalid email or password")
	ErrTooManyAttempts    = errors.New("adminauth: too many failed login attempts, try again later")
	ErrSessionInvalid     = errors.New("adminauth: session is invalid or expired")
	ErrAdminNotFound      = errors.New("adminauth: admin user not found")
	ErrEmailTaken         = errors.New("adminauth: email already in use")
	ErrInvalidInput       = errors.New("adminauth: invalid input")
	ErrLastSuperAdmin     = errors.New("adminauth: cannot remove the last active super_admin")
)

// Config 是会话与登录限流参数；零值字段用默认值。
type Config struct {
	IdleTimeout      time.Duration // 滑动过期，默认 12h
	AbsoluteTimeout  time.Duration // 绝对过期，默认 7 天
	MaxFailures      int           // 窗口内同一邮箱的失败上限，默认 5
	MaxIPFailures    int           // 窗口内同一 IP 的失败上限，默认 MaxFailures*4（同一出口 IP 后面可能有多名运营）
	FailureWindow    time.Duration // 默认 15 分钟
	MinPasswordRunes int           // 默认 10
	// Box 用于加密存储 TOTP 两步验证密钥；nil 时不能启用两步验证。
	Box *secretbox.Box
}

func (c Config) withDefaults() Config {
	if c.IdleTimeout <= 0 {
		c.IdleTimeout = 12 * time.Hour
	}
	if c.AbsoluteTimeout <= 0 {
		c.AbsoluteTimeout = 7 * 24 * time.Hour
	}
	if c.MaxFailures <= 0 {
		c.MaxFailures = 5
	}
	if c.MaxIPFailures <= 0 {
		c.MaxIPFailures = c.MaxFailures * 4
	}
	if c.FailureWindow <= 0 {
		c.FailureWindow = 15 * time.Minute
	}
	if c.MinPasswordRunes <= 0 {
		c.MinPasswordRunes = 10
	}
	return c
}

type Service struct {
	pool *pgxpool.Pool
	cfg  Config
	now  func() time.Time
}

func New(pool *pgxpool.Pool, cfg Config) *Service {
	return &Service{pool: pool, cfg: cfg.withDefaults(), now: time.Now}
}

// AdminUser 是管理员的对外表示（不含密码哈希）。
type AdminUser struct {
	ID          int64        `json:"id"`
	Email       string       `json:"email"`
	Name        string       `json:"name"`
	Status      string       `json:"status"`
	Roles       []string     `json:"roles"`
	Permissions []Permission `json:"permissions"`
	TOTPEnabled bool         `json:"totp_enabled"`
	LastLoginAt *time.Time   `json:"last_login_at"`
	CreatedAt   time.Time    `json:"created_at"`
}

type LoginInput struct {
	Email, Password string
	TOTPCode        string // 启用了两步验证的管理员必填
	IP, UserAgent   string
}

type LoginResult struct {
	Token     string     `json:"token"`
	ExpiresAt time.Time  `json:"expires_at"`
	User      *AdminUser `json:"user"`
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "uas_" + base64.RawURLEncoding.EncodeToString(b), nil
}

func nullIP(ip string) any {
	if ip == "" {
		return nil
	}
	return ip
}

// Login 校验邮箱密码并创建会话。失败次数按邮箱和 IP 分别计数，超过上限在窗口
// 期内直接拒绝（不再校验密码，避免被用来做在线爆破）。邮箱不存在时仍然走一遍
// 完整的密码哈希计算，抹平响应时间差。
func (s *Service) Login(ctx context.Context, in LoginInput) (*LoginResult, error) {
	email := strings.TrimSpace(strings.ToLower(in.Email))
	if email == "" || in.Password == "" {
		return nil, ErrInvalidCredentials
	}
	since := s.now().Add(-s.cfg.FailureWindow)
	var byEmail, byIP int
	if err := s.db(ctx).QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE email = $1),
		        count(*) FILTER (WHERE $2::inet IS NOT NULL AND ip = $2::inet)
		 FROM admin_login_events
		 WHERE NOT success AND created_at >= $3 AND (email = $1 OR ($2::inet IS NOT NULL AND ip = $2::inet))`,
		email, nullIP(in.IP), since).Scan(&byEmail, &byIP); err != nil {
		return nil, fmt.Errorf("adminauth: count login failures: %w", err)
	}
	if byEmail >= s.cfg.MaxFailures || byIP >= s.cfg.MaxIPFailures {
		return nil, ErrTooManyAttempts
	}

	var (
		id           int64
		hash, status string
	)
	err := s.db(ctx).QueryRow(ctx, `SELECT id, password_hash, status FROM admin_users WHERE email = $1 AND id <> 0`, email).Scan(&id, &hash, &status)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		console.VerifyDummyPassword(in.Password)
		s.recordLogin(ctx, email, nil, false, in)
		return nil, ErrInvalidCredentials
	case err != nil:
		return nil, fmt.Errorf("adminauth: load admin user: %w", err)
	}
	ok, verr := console.VerifyPassword(in.Password, hash)
	if verr != nil || !ok || status != "active" {
		s.recordLogin(ctx, email, &id, false, in)
		return nil, ErrInvalidCredentials
	}
	var totpOn bool
	if err := s.db(ctx).QueryRow(ctx, `SELECT totp_enabled FROM admin_users WHERE id = $1`, id).Scan(&totpOn); err != nil {
		return nil, fmt.Errorf("adminauth: load totp flag: %w", err)
	}
	if totpOn {
		if strings.TrimSpace(in.TOTPCode) == "" {
			// 密码正确但缺验证码：不计入失败次数，前端据此显示验证码输入框
			return nil, ErrTOTPRequired
		}
		if err := s.checkTOTP(ctx, id, in.TOTPCode); err != nil {
			s.recordLogin(ctx, email, &id, false, in)
			if errors.Is(err, ErrTOTPInvalid) {
				return nil, ErrTOTPInvalid
			}
			return nil, err
		}
	}

	token, err := newToken()
	if err != nil {
		return nil, fmt.Errorf("adminauth: generate token: %w", err)
	}
	now := s.now()
	expires := now.Add(s.cfg.IdleTimeout)
	absolute := now.Add(s.cfg.AbsoluteTimeout)
	tx, err := store.BeginOrJoin(ctx, s.pool)
	if err != nil {
		return nil, fmt.Errorf("adminauth: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx,
		`INSERT INTO admin_sessions (admin_user_id, token_hash, ip, user_agent, created_at, last_seen_at, expires_at, absolute_expires_at)
		 VALUES ($1, $2, $3::inet, NULLIF($4, ''), $5, $5, $6, $7)`,
		id, hashToken(token), nullIP(in.IP), truncate(in.UserAgent, 256), now, expires, absolute); err != nil {
		return nil, fmt.Errorf("adminauth: insert session: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE admin_users SET last_login_at = $2 WHERE id = $1`, id, now); err != nil {
		return nil, fmt.Errorf("adminauth: update last_login_at: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO admin_login_events (email, admin_user_id, success, ip, user_agent) VALUES ($1, $2, true, $3::inet, NULLIF($4, ''))`,
		email, id, nullIP(in.IP), truncate(in.UserAgent, 256)); err != nil {
		return nil, fmt.Errorf("adminauth: record login: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("adminauth: commit login: %w", err)
	}
	user, err := s.GetAdmin(ctx, id)
	if err != nil {
		return nil, err
	}
	return &LoginResult{Token: token, ExpiresAt: expires, User: user}, nil
}

func (s *Service) recordLogin(ctx context.Context, email string, id *int64, success bool, in LoginInput) {
	_, _ = s.db(ctx).Exec(ctx,
		`INSERT INTO admin_login_events (email, admin_user_id, success, ip, user_agent) VALUES ($1, $2, $3, $4::inet, NULLIF($5, ''))`,
		email, id, success, nullIP(in.IP), truncate(in.UserAgent, 256))
}

// Authenticate 用会话令牌换出管理员身份，并滑动续期（最多每分钟写一次库）。
func (s *Service) Authenticate(ctx context.Context, token string) (*Principal, error) {
	if !strings.HasPrefix(token, "uas_") {
		return nil, ErrSessionInvalid
	}
	now := s.now()
	var (
		sessionID, adminID int64
		lastSeen, absolute time.Time
	)
	err := s.db(ctx).QueryRow(ctx,
		`SELECT s.id, s.admin_user_id, s.last_seen_at, s.absolute_expires_at
		 FROM admin_sessions s JOIN admin_users u ON u.id = s.admin_user_id
		 WHERE s.token_hash = $1 AND s.expires_at > $2 AND s.absolute_expires_at > $2 AND u.status = 'active'`,
		hashToken(token), now).Scan(&sessionID, &adminID, &lastSeen, &absolute)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSessionInvalid
	}
	if err != nil {
		return nil, fmt.Errorf("adminauth: load session: %w", err)
	}
	if now.Sub(lastSeen) > time.Minute {
		expires := now.Add(s.cfg.IdleTimeout)
		if expires.After(absolute) {
			expires = absolute
		}
		if _, err := s.db(ctx).Exec(ctx, `UPDATE admin_sessions SET last_seen_at = $2, expires_at = $3 WHERE id = $1`, sessionID, now, expires); err != nil {
			return nil, fmt.Errorf("adminauth: touch session: %w", err)
		}
	}
	u, err := s.GetAdmin(ctx, adminID)
	if err != nil {
		return nil, err
	}
	return &Principal{AdminID: u.ID, Name: u.Name, Email: u.Email, Roles: u.Roles, Permissions: u.Permissions, SessionID: sessionID}, nil
}

// Logout 删除会话；令牌不存在也视为成功（幂等）。
func (s *Service) Logout(ctx context.Context, sessionID int64) error {
	_, err := s.db(ctx).Exec(ctx, `DELETE FROM admin_sessions WHERE id = $1`, sessionID)
	return err
}

// PurgeExpiredSessions 清理过期会话，返回删除条数。
func (s *Service) PurgeExpiredSessions(ctx context.Context) (int64, error) {
	tag, err := s.db(ctx).Exec(ctx, `DELETE FROM admin_sessions WHERE expires_at <= now() OR absolute_expires_at <= now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ChangePassword 校验旧密码后修改，并注销该管理员除当前会话外的所有会话。
func (s *Service) ChangePassword(ctx context.Context, adminID, currentSessionID int64, oldPassword, newPassword string) error {
	if err := s.validatePassword(newPassword); err != nil {
		return err
	}
	var hash string
	if err := s.db(ctx).QueryRow(ctx, `SELECT password_hash FROM admin_users WHERE id = $1`, adminID).Scan(&hash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAdminNotFound
		}
		return fmt.Errorf("adminauth: load admin: %w", err)
	}
	if ok, _ := console.VerifyPassword(oldPassword, hash); !ok {
		return ErrInvalidCredentials
	}
	newHash, err := console.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("adminauth: hash password: %w", err)
	}
	tx, err := store.BeginOrJoin(ctx, s.pool)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `UPDATE admin_users SET password_hash = $2, updated_at = now() WHERE id = $1`, adminID, newHash); err != nil {
		return fmt.Errorf("adminauth: update password: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM admin_sessions WHERE admin_user_id = $1 AND id <> $2`, adminID, currentSessionID); err != nil {
		return fmt.Errorf("adminauth: revoke sessions: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *Service) validatePassword(pw string) error {
	if utf8.RuneCountInString(pw) < s.cfg.MinPasswordRunes {
		return fmt.Errorf("%w: password must be at least %d characters", ErrInvalidInput, s.cfg.MinPasswordRunes)
	}
	if len(pw) > 256 {
		return fmt.Errorf("%w: password too long", ErrInvalidInput)
	}
	return nil
}

// ---------- 管理员管理 ----------

const adminSelect = `SELECT u.id, u.email, u.name, u.status, u.totp_enabled, u.last_login_at, u.created_at,
	COALESCE(array_agg(DISTINCT ur.role_code) FILTER (WHERE ur.role_code IS NOT NULL), '{}'),
	COALESCE(array_agg(DISTINCT p) FILTER (WHERE p IS NOT NULL), '{}')
	FROM admin_users u
	LEFT JOIN admin_user_roles ur ON ur.admin_user_id = u.id
	LEFT JOIN admin_roles r ON r.code = ur.role_code
	LEFT JOIN LATERAL unnest(r.permissions) p ON true`

func scanAdmin(row pgx.Row) (*AdminUser, error) {
	var (
		u     AdminUser
		perms []string
	)
	if err := row.Scan(&u.ID, &u.Email, &u.Name, &u.Status, &u.TOTPEnabled, &u.LastLoginAt, &u.CreatedAt, &u.Roles, &perms); err != nil {
		return nil, err
	}
	u.Permissions = make([]Permission, len(perms))
	for i, p := range perms {
		u.Permissions[i] = Permission(p)
	}
	return &u, nil
}

func (s *Service) GetAdmin(ctx context.Context, id int64) (*AdminUser, error) {
	u, err := scanAdmin(s.db(ctx).QueryRow(ctx, adminSelect+` WHERE u.id = $1 GROUP BY u.id`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAdminNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("adminauth: load admin: %w", err)
	}
	return u, nil
}

func (s *Service) ListAdmins(ctx context.Context) ([]AdminUser, error) {
	rows, err := s.db(ctx).Query(ctx, adminSelect+` WHERE u.id <> 0 GROUP BY u.id ORDER BY u.id`)
	if err != nil {
		return nil, fmt.Errorf("adminauth: list admins: %w", err)
	}
	defer rows.Close()
	out := []AdminUser{}
	for rows.Next() {
		u, err := scanAdmin(rows)
		if err != nil {
			return nil, fmt.Errorf("adminauth: scan admin: %w", err)
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// Role 是角色定义。
type Role struct {
	Code        string       `json:"code"`
	Name        string       `json:"name"`
	Permissions []Permission `json:"permissions"`
}

func (s *Service) ListRoles(ctx context.Context) ([]Role, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT code, name, permissions FROM admin_roles ORDER BY code`)
	if err != nil {
		return nil, fmt.Errorf("adminauth: list roles: %w", err)
	}
	defer rows.Close()
	out := []Role{}
	for rows.Next() {
		var (
			r     Role
			perms []string
		)
		if err := rows.Scan(&r.Code, &r.Name, &perms); err != nil {
			return nil, err
		}
		for _, p := range perms {
			r.Permissions = append(r.Permissions, Permission(p))
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type CreateAdminInput struct {
	Email    string   `json:"email"`
	Name     string   `json:"name"`
	Password string   `json:"password"`
	Roles    []string `json:"roles"`
}

func (s *Service) CreateAdmin(ctx context.Context, in CreateAdminInput) (*AdminUser, error) {
	email := strings.TrimSpace(strings.ToLower(in.Email))
	name := strings.TrimSpace(in.Name)
	if !strings.Contains(email, "@") || len(email) > 254 {
		return nil, fmt.Errorf("%w: invalid email", ErrInvalidInput)
	}
	if name == "" || utf8.RuneCountInString(name) > 64 {
		return nil, fmt.Errorf("%w: name must be 1-64 characters", ErrInvalidInput)
	}
	if err := s.validatePassword(in.Password); err != nil {
		return nil, err
	}
	if len(in.Roles) == 0 {
		return nil, fmt.Errorf("%w: at least one role is required", ErrInvalidInput)
	}
	hash, err := console.HashPassword(in.Password)
	if err != nil {
		return nil, fmt.Errorf("adminauth: hash password: %w", err)
	}
	tx, err := store.BeginOrJoin(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO admin_users (email, name, password_hash) VALUES ($1, $2, $3) RETURNING id`, email, name, hash).Scan(&id); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrEmailTaken
		}
		return nil, fmt.Errorf("adminauth: insert admin: %w", err)
	}
	if err := setRoles(ctx, tx, id, in.Roles); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetAdmin(ctx, id)
}

func setRoles(ctx context.Context, tx pgx.Tx, id int64, roles []string) error {
	var known int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM admin_roles WHERE code = ANY($1)`, roles).Scan(&known); err != nil {
		return fmt.Errorf("adminauth: check roles: %w", err)
	}
	uniq := map[string]bool{}
	for _, r := range roles {
		uniq[r] = true
	}
	if known != len(uniq) {
		return fmt.Errorf("%w: unknown role in %v", ErrInvalidInput, roles)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM admin_user_roles WHERE admin_user_id = $1`, id); err != nil {
		return fmt.Errorf("adminauth: clear roles: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO admin_user_roles (admin_user_id, role_code) SELECT $1, unnest($2::text[]) ON CONFLICT DO NOTHING`, id, roles); err != nil {
		return fmt.Errorf("adminauth: set roles: %w", err)
	}
	return nil
}

type UpdateAdminInput struct {
	Name     *string   `json:"name"`
	Status   *string   `json:"status"`
	Roles    *[]string `json:"roles"`
	Password *string   `json:"password"` // 管理员重置他人密码
	// ResetTOTP 清除该管理员的两步验证绑定（丢失验证器时由超级管理员操作）。
	ResetTOTP bool `json:"reset_totp"`
}

// UpdateAdmin 修改管理员；停用或重置密码会同时注销其全部会话。不允许把最后
// 一个 active 的 super_admin 停用或去掉角色（否则再也没人能管理管理员）。
func (s *Service) UpdateAdmin(ctx context.Context, id int64, in UpdateAdminInput) (before, after *AdminUser, err error) {
	if id == SystemAdminID {
		return nil, nil, fmt.Errorf("%w: the system identity cannot be modified", ErrInvalidInput)
	}
	before, err = s.GetAdmin(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	tx, err := store.BeginOrJoin(ctx, s.pool)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	// 串行化所有管理员变更，保证"最后一个 super_admin"检查不被并发绕过。
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('admin_users'))`); err != nil {
		return nil, nil, err
	}
	revokeSessions := false
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" || utf8.RuneCountInString(name) > 64 {
			return nil, nil, fmt.Errorf("%w: name must be 1-64 characters", ErrInvalidInput)
		}
		if _, err := tx.Exec(ctx, `UPDATE admin_users SET name = $2, updated_at = now() WHERE id = $1`, id, name); err != nil {
			return nil, nil, err
		}
	}
	if in.Status != nil {
		if *in.Status != "active" && *in.Status != "disabled" {
			return nil, nil, fmt.Errorf("%w: status must be active/disabled", ErrInvalidInput)
		}
		if _, err := tx.Exec(ctx, `UPDATE admin_users SET status = $2, updated_at = now() WHERE id = $1`, id, *in.Status); err != nil {
			return nil, nil, err
		}
		revokeSessions = revokeSessions || *in.Status == "disabled"
	}
	if in.Roles != nil {
		if len(*in.Roles) == 0 {
			return nil, nil, fmt.Errorf("%w: at least one role is required", ErrInvalidInput)
		}
		if err := setRoles(ctx, tx, id, *in.Roles); err != nil {
			return nil, nil, err
		}
	}
	if in.Password != nil {
		if err := s.validatePassword(*in.Password); err != nil {
			return nil, nil, err
		}
		hash, err := console.HashPassword(*in.Password)
		if err != nil {
			return nil, nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE admin_users SET password_hash = $2, updated_at = now() WHERE id = $1`, id, hash); err != nil {
			return nil, nil, err
		}
		revokeSessions = true
	}
	if in.ResetTOTP {
		if err := s.clearTOTP(ctx, id); err != nil {
			return nil, nil, err
		}
		revokeSessions = true
	}
	var supers int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM admin_users u JOIN admin_user_roles ur ON ur.admin_user_id = u.id
		 WHERE ur.role_code = 'super_admin' AND u.status = 'active' AND u.id <> 0`).Scan(&supers); err != nil {
		return nil, nil, err
	}
	if supers == 0 && slices.Contains(before.Roles, "super_admin") && before.Status == "active" {
		return nil, nil, ErrLastSuperAdmin
	}
	if revokeSessions {
		if _, err := tx.Exec(ctx, `DELETE FROM admin_sessions WHERE admin_user_id = $1`, id); err != nil {
			return nil, nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	after, err = s.GetAdmin(ctx, id)
	return before, after, err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// db 返回 ctx 里的环境事务（store.RunInTx），没有时返回连接池——管理员管理操作
// 可以和审计日志写在同一个事务里。
func (s *Service) db(ctx context.Context) store.Querier { return store.Q(ctx, s.pool) }
