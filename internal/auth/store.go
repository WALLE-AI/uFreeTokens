package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Principal 是鉴权通过后附着在请求 context 上的身份信息，供后续限流/路由/计费使用。
type Principal struct {
	AccountID        int64
	AccountTier      string
	APIKeyID         int64
	AllowedModels    []string // nil = 不限制
	RPMLimit         *int
	TPMLimit         *int
	ConcurrencyLimit *int
}

var (
	ErrKeyNotFound      = errors.New("auth: api key not found")
	ErrKeyDisabled      = errors.New("auth: api key disabled or revoked")
	ErrKeyExpired       = errors.New("auth: api key expired")
	ErrAccountSuspended = errors.New("auth: account suspended or closed")
)

// Store 抽象出 Key 查找逻辑，便于单测用内存实现替换真实数据库。
type Store interface {
	FindByHMAC(ctx context.Context, keyHMAC []byte) (*Principal, error)
	TouchLastUsed(ctx context.Context, apiKeyID int64, at time.Time)
}

// PostgresStore 是 Store 的 PostgreSQL 实现。
type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

const findByHMACQuery = `
SELECT
    k.id, k.account_id, k.status, k.allowed_models, k.rpm_limit, k.tpm_limit,
    k.concurrency_limit, k.expires_at,
    a.status, a.tier
FROM api_keys k
JOIN accounts a ON a.id = k.account_id
WHERE k.key_hmac = $1
`

func (s *PostgresStore) FindByHMAC(ctx context.Context, keyHMAC []byte) (*Principal, error) {
	row := s.pool.QueryRow(ctx, findByHMACQuery, keyHMAC)

	var (
		p                   Principal
		keyStatus, acctStat string
		expiresAt           *time.Time
	)
	if err := row.Scan(
		&p.APIKeyID, &p.AccountID, &keyStatus, &p.AllowedModels,
		&p.RPMLimit, &p.TPMLimit, &p.ConcurrencyLimit, &expiresAt,
		&acctStat, &p.AccountTier,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrKeyNotFound
		}
		return nil, err
	}

	if keyStatus != "active" {
		return nil, ErrKeyDisabled
	}
	if expiresAt != nil && expiresAt.Before(time.Now()) {
		return nil, ErrKeyExpired
	}
	if acctStat != "active" {
		return nil, ErrAccountSuspended
	}
	return &p, nil
}

// TouchLastUsed 异步、尽力而为地回写最近使用时间；不阻塞请求路径，失败静默丢弃
// （见技术方案 §6.2 关于 last_used_at "由 worker 批量回写，不在热路径更新" 的说明——
// 这里先提供一个简单的 fire-and-forget 版本，Phase2 可换成 worker 批量聚合）。
func (s *PostgresStore) TouchLastUsed(ctx context.Context, apiKeyID int64, at time.Time) {
	go func() {
		c, cancel := detachedContext(ctx)
		defer cancel()
		_, _ = s.pool.Exec(c, `UPDATE api_keys SET last_used_at = $2 WHERE id = $1`, apiKeyID, at)
	}()
}

func detachedContext(_ context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 3*time.Second)
}
