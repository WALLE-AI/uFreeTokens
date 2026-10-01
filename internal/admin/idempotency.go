package admin

import (
	"context"
	"fmt"
	"time"
)

// Idempotency-Key 的存取（表见迁移 00020）。这些方法直接用连接池、不加入环境
// 事务：占位与结果必须独立于业务事务提交，业务回滚时占位也要能被释放。

const idempotencyTTL = 24 * time.Hour

// IdempotencyRecord 是之前同一 Key 的请求记录。StatusCode 为 0 表示仍在处理中。
type IdempotencyRecord struct {
	RequestHash []byte
	StatusCode  int
	Body        []byte
}

// ClaimIdempotencyKey 尝试为 (adminID, key) 占位。claimed=true 表示本次请求应该
// 正常执行；否则返回之前的记录（重放或报冲突由调用方决定）。超过 24 小时的旧
// 记录视为不存在。
func (s *Service) ClaimIdempotencyKey(ctx context.Context, adminID int64, key string, hash []byte) (claimed bool, prev *IdempotencyRecord, err error) {
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM admin_idempotency_keys WHERE admin_user_id = $1 AND key = $2 AND created_at < $3`,
		adminID, key, time.Now().Add(-idempotencyTTL)); err != nil {
		return false, nil, fmt.Errorf("admin: expire idempotency key: %w", err)
	}
	tag, err := s.pool.Exec(ctx,
		`INSERT INTO admin_idempotency_keys (admin_user_id, key, request_hash) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		adminID, key, hash)
	if err != nil {
		return false, nil, fmt.Errorf("admin: claim idempotency key: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return true, nil, nil
	}
	var rec IdempotencyRecord
	var status *int
	if err := s.pool.QueryRow(ctx,
		`SELECT request_hash, status_code, response_body FROM admin_idempotency_keys WHERE admin_user_id = $1 AND key = $2`,
		adminID, key).Scan(&rec.RequestHash, &status, &rec.Body); err != nil {
		if isNoRows(err) { // 刚好被并发的释放删掉：让调用方重试
			return false, &IdempotencyRecord{}, nil
		}
		return false, nil, fmt.Errorf("admin: load idempotency key: %w", err)
	}
	if status != nil {
		rec.StatusCode = *status
	}
	return false, &rec, nil
}

// CompleteIdempotencyKey 保存本次请求的响应，供之后的重复请求重放。
func (s *Service) CompleteIdempotencyKey(ctx context.Context, adminID int64, key string, status int, body []byte) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE admin_idempotency_keys SET status_code = $3, response_body = $4, completed_at = now() WHERE admin_user_id = $1 AND key = $2`,
		adminID, key, status, body)
	return err
}

// ReleaseIdempotencyKey 删除占位（服务端错误时调用，让客户端可以用同一个 Key 重试）。
func (s *Service) ReleaseIdempotencyKey(ctx context.Context, adminID int64, key string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM admin_idempotency_keys WHERE admin_user_id = $1 AND key = $2`, adminID, key)
	return err
}

// PurgeIdempotencyKeys 删除 24 小时前的记录，返回删除条数。
func (s *Service) PurgeIdempotencyKeys(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM admin_idempotency_keys WHERE created_at < $1`, time.Now().Add(-idempotencyTTL))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
