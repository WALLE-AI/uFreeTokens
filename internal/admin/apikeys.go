package admin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/auth"
)

type APIKey struct {
	ID               int64
	AccountID        int64
	Name             string
	DisplayPrefix    string
	Status           string
	AllowedModels    []string
	RPMLimit         *int
	TPMLimit         *int
	ConcurrencyLimit *int
	CreatedAt        time.Time
}

// CreatedAPIKey 只在创建那一刻存在——RawKey 之后再也拿不到（数据库只存 HMAC），
// 调用方必须在这次响应里把它交给用户，错过就要吊销重新生成。
type CreatedAPIKey struct {
	APIKey
	RawKey string
}

type CreateAPIKeyInput struct {
	AccountID        int64
	Name             string
	AllowedModels    []string // nil = 不限制
	RPMLimit         *int
	TPMLimit         *int
	ConcurrencyLimit *int
}

// CreateAPIKey 生成一个新的 User API Key（技术方案 §7.2）。明文只在返回值里出现
// 这一次，数据库只保存 HMAC 摘要和用于展示的前缀。
func (s *Service) CreateAPIKey(ctx context.Context, in CreateAPIKeyInput) (*CreatedAPIKey, error) {
	if in.Name == "" {
		return nil, errors.New("admin: api key name is required")
	}
	if len(s.pepper) == 0 {
		return nil, errors.New("admin: server misconfigured, no API key pepper available")
	}

	key, err := auth.GenerateAPIKey(s.pepper)
	if err != nil {
		return nil, fmt.Errorf("admin: generate api key: %w", err)
	}

	out := &CreatedAPIKey{
		APIKey: APIKey{
			AccountID: in.AccountID, Name: in.Name, DisplayPrefix: key.DisplayPrefix, Status: "active",
			AllowedModels: in.AllowedModels, RPMLimit: in.RPMLimit, TPMLimit: in.TPMLimit, ConcurrencyLimit: in.ConcurrencyLimit,
		},
		RawKey: key.Raw,
	}

	err = s.pool.QueryRow(ctx,
		`INSERT INTO api_keys (account_id, name, display_prefix, key_hmac, status, allowed_models, rpm_limit, tpm_limit, concurrency_limit)
		 VALUES ($1, $2, $3, $4, 'active', $5, $6, $7, $8)
		 RETURNING id, created_at`,
		in.AccountID, in.Name, key.DisplayPrefix, key.HMAC, in.AllowedModels, in.RPMLimit, in.TPMLimit, in.ConcurrencyLimit,
	).Scan(&out.ID, &out.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("admin: insert api_key: %w", err)
	}
	return out, nil
}

// ListAPIKeys 返回某账户下的全部 Key（不含明文/HMAC，只有展示用的前缀）。
func (s *Service) ListAPIKeys(ctx context.Context, accountID int64) ([]APIKey, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, account_id, name, display_prefix, status, allowed_models, rpm_limit, tpm_limit, concurrency_limit, created_at
		 FROM api_keys WHERE account_id = $1 ORDER BY id`,
		accountID,
	)
	if err != nil {
		return nil, fmt.Errorf("admin: list api_keys: %w", err)
	}
	defer rows.Close()

	var out []APIKey
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.AccountID, &k.Name, &k.DisplayPrefix, &k.Status,
			&k.AllowedModels, &k.RPMLimit, &k.TPMLimit, &k.ConcurrencyLimit, &k.CreatedAt); err != nil {
			return nil, fmt.Errorf("admin: scan api_key: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

var ErrAPIKeyNotFound = errors.New("admin: api key not found")

// RevokeAPIKey 把某个 Key 标记为不可用。之后拿它请求网关会被 401
// （internal/auth.PostgresStore.FindByHMAC 只认 status='active'）。
func (s *Service) RevokeAPIKey(ctx context.Context, apiKeyID int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE api_keys SET status = 'revoked' WHERE id = $1 AND status != 'revoked'`, apiKeyID)
	if err != nil {
		return fmt.Errorf("admin: revoke api_key: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// 可能是不存在，也可能是已经被吊销过——两种情况都返回同一个 not-found 语义
		// 的错误对调用方更简单；如果需要区分，调用方应该先 ListAPIKeys 检查状态。
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM api_keys WHERE id = $1)`, apiKeyID).Scan(&exists); err != nil {
			return fmt.Errorf("admin: check api_key existence: %w", err)
		}
		if !exists {
			return ErrAPIKeyNotFound
		}
		// 已经是 revoked：幂等，视为成功。
	}
	return nil
}
