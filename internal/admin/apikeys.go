package admin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/auth"
)

type APIKey struct {
	ID               int64     `json:"id"`
	AccountID        int64     `json:"account_id"`
	Name             string    `json:"name"`
	DisplayPrefix    string    `json:"display_prefix"`
	Status           string    `json:"status"`
	AllowedModels    []string  `json:"allowed_models"`
	RPMLimit         *int      `json:"rpm_limit"`
	TPMLimit         *int      `json:"tpm_limit"`
	ConcurrencyLimit *int      `json:"concurrency_limit"`
	CreatedAt        time.Time `json:"created_at"`
}

// CreatedAPIKey 只在创建那一刻存在——RawKey 之后再也拿不到（数据库只存 HMAC），
// 调用方必须在这次响应里把它交给用户，错过就要吊销重新生成。
type CreatedAPIKey struct {
	APIKey
	RawKey string `json:"raw_key"`
}

type CreateAPIKeyInput struct {
	AccountID        int64
	CreatedBy        *int64 // 发起创建的用户（console 自助建 Key 时非空）；nil = 内网管理员操作，无用户身份
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
		`INSERT INTO api_keys (account_id, created_by, name, display_prefix, key_hmac, status, allowed_models, rpm_limit, tpm_limit, concurrency_limit)
		 VALUES ($1, $2, $3, $4, $5, 'active', $6, $7, $8, $9)
		 RETURNING id, created_at`,
		in.AccountID, in.CreatedBy, in.Name, key.DisplayPrefix, key.HMAC, in.AllowedModels, in.RPMLimit, in.TPMLimit, in.ConcurrencyLimit,
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

	out := []APIKey{}
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

// RevokeAPIKeyForAccount 和 RevokeAPIKey 一样，但按 account_id 限定范围——
// console 的自助吊销接口必须防止 A 账户吊销 B 账户的 Key；RevokeAPIKey 本身
// 不做这个限定（是给内网管理员用的，见包文档的已知范围限制），这里单独提供
// 一个按账户限定范围的版本，而不是给 RevokeAPIKey 加一个可选参数——调用方
// 传错/漏传空账户 ID 的后果差异太大（全局吊销 vs 越权拒绝），值得用两个
// 不同名字的方法在类型层面强制调用方想清楚自己要哪种语义。
func (s *Service) RevokeAPIKeyForAccount(ctx context.Context, accountID, apiKeyID int64) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE api_keys SET status = 'revoked' WHERE id = $1 AND account_id = $2 AND status != 'revoked'`,
		apiKeyID, accountID,
	)
	if err != nil {
		return fmt.Errorf("admin: revoke api_key: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := s.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM api_keys WHERE id = $1 AND account_id = $2)`, apiKeyID, accountID,
		).Scan(&exists); err != nil {
			return fmt.Errorf("admin: check api_key existence: %w", err)
		}
		if !exists {
			// 不区分"这把 Key 根本不存在"和"这把 Key 存在但属于别的账户"，
			// 两种情况对调用方来说都应该是"你没有这把 Key"，不能通过错误类型
			// 差异泄露"这个 Key ID 属于别人"这件事。
			return ErrAPIKeyNotFound
		}
		// 已经是 revoked：幂等，视为成功。
	}
	return nil
}
