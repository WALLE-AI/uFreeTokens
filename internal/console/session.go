package console

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// SessionTTL 是会话的滑动过期时间：每次 Get 命中都会把 TTL 重置回这个值
	// （"7 天不活跃才需要重新登录"，不是"注册 7 天后强制过期"）。
	SessionTTL = 7 * 24 * time.Hour
	// SessionCookieName 是控制台会话 Cookie 的名字：HttpOnly、SameSite=Lax、
	// Path=/console；Secure 是否设置由 Service 的配置决定（本地 http 开发环境
	// 需要能关掉）。
	SessionCookieName = "uft_session"
	sessionTokenBytes = 32
)

// SessionData 是会话里保存的全部内容——刻意做到很薄：只有身份，不缓存
// email/tier 这类会变化的数据，那些每次都应该从 users/accounts 表查（技术
// 方案：控制台鉴权与 API Key 鉴权完全分离，会话只负责"你是谁"）。
type SessionData struct {
	UserID    int64 `json:"user_id"`
	AccountID int64 `json:"account_id"`
	CreatedAt int64 `json:"created_at"` // unix 秒
}

var ErrSessionNotFound = errors.New("console: session not found or expired")

// SessionStore 把会话存在 Redis 里：uft:sess:<sha256(token)> -> JSON(SessionData)，
// TTL 由 SessionTTL 控制、每次 Get 命中滑动续期；另建 uft:user_sess:<uid> 这个
// set 记录该用户名下所有会话的 token 哈希，支持"全部下线"
// （DeleteAllForUser，目前没有 HTTP 入口调用它，留给后续"在其它设备退出登录"
// 功能用）。只存 token 的哈希，不存明文——泄露 Redis 快照不足以冒充会话，
// 还需要原始 Cookie 明文，和 internal/auth 对 API Key 只存 HMAC 是同一个思路。
type SessionStore struct {
	rdb *redis.Client
}

func NewSessionStore(rdb *redis.Client) *SessionStore {
	return &SessionStore{rdb: rdb}
}

// NewSessionToken 生成一个新的高熵会话 token（Cookie 的明文值）。
func NewSessionToken() (string, error) {
	buf := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("console: read random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func sessionKey(tokenHash string) string  { return "uft:sess:" + tokenHash }
func userSessionsKey(userID int64) string { return fmt.Sprintf("uft:user_sess:%d", userID) }

// Create 建一个新会话，返回 Cookie 的明文 token。
func (s *SessionStore) Create(ctx context.Context, data SessionData) (token string, err error) {
	token, err = NewSessionToken()
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("console: marshal session: %w", err)
	}
	th := hashToken(token)

	pipe := s.rdb.TxPipeline()
	pipe.Set(ctx, sessionKey(th), raw, SessionTTL)
	pipe.SAdd(ctx, userSessionsKey(data.UserID), th)
	pipe.Expire(ctx, userSessionsKey(data.UserID), SessionTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return "", fmt.Errorf("console: create session: %w", err)
	}
	return token, nil
}

// Get 校验 token 并返回会话数据，命中时做滑动续期（TTL 重置为完整的
// SessionTTL）。续期失败不影响本次鉴权结果——只会导致这个会话在下次 Get 之前
// 提前一点过期，比因为一次 Redis 抖动就让用户意外掉线更能接受。
func (s *SessionStore) Get(ctx context.Context, token string) (*SessionData, error) {
	th := hashToken(token)
	raw, err := s.rdb.Get(ctx, sessionKey(th)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("console: get session: %w", err)
	}
	var data SessionData
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("console: unmarshal session: %w", err)
	}
	_ = s.rdb.Expire(ctx, sessionKey(th), SessionTTL).Err()
	return &data, nil
}

// Delete 使某个 token 立即失效（Logout）。token 本来就不存在时是安全的
// no-op，调用方不需要先判断。
func (s *SessionStore) Delete(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	if err := s.rdb.Del(ctx, sessionKey(hashToken(token))).Err(); err != nil {
		return fmt.Errorf("console: delete session: %w", err)
	}
	return nil
}

// DeleteAllForUser 让某个用户名下的全部会话立即失效（"全部下线"）。
func (s *SessionStore) DeleteAllForUser(ctx context.Context, userID int64) error {
	uk := userSessionsKey(userID)
	hashes, err := s.rdb.SMembers(ctx, uk).Result()
	if err != nil {
		return fmt.Errorf("console: list user sessions: %w", err)
	}
	if len(hashes) == 0 {
		return nil
	}
	keys := make([]string, len(hashes))
	for i, h := range hashes {
		keys[i] = sessionKey(h)
	}
	pipe := s.rdb.TxPipeline()
	pipe.Del(ctx, keys...)
	pipe.Del(ctx, uk)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("console: delete user sessions: %w", err)
	}
	return nil
}
