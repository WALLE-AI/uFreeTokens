// Package ratelimit 实现 RPM / TPM / 并发三种限流（技术方案 §7.12）。
// 全部基于 Redis：RPM 用 GCRA（github.com/go-redis/redis_rate），TPM 和并发用
// Lua 脚本保证"检查 + 扣减/占用"的原子性（这是从 wallet 的教训里学到的——
// 分成两次 Redis 调用做"先查后改"在并发下会漏判，必须一次脚本搞定）。
//
// Redis 不可用时统一 fail-open（放行，只记一条 warning）：限流组件本身故障
// 不应该导致全站请求都被拒绝，这与技术方案 §7.12 的原则一致。当前没有实现
// "本地令牌桶兜底"（§7.12 提到的降级方案），只是简单放行——已知的范围简化。
package ratelimit

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	rrate "github.com/go-redis/redis_rate/v10"
)

// Result 是一次限流判定的结果。
type Result struct {
	Allowed    bool
	RetryAfter time.Duration // Allowed=false 时，建议客户端等待多久再重试
}

func allow() Result               { return Result{Allowed: true} }
func deny(d time.Duration) Result { return Result{Allowed: false, RetryAfter: d} }

type Limiter struct {
	rdb    *redis.Client
	gcra   *rrate.Limiter
	logger *slog.Logger
}

func New(rdb *redis.Client, logger *slog.Logger) *Limiter {
	return &Limiter{rdb: rdb, gcra: rrate.NewLimiter(rdb), logger: logger}
}

func (l *Limiter) failOpen(op, subject string, err error) Result {
	l.logger.Warn("ratelimit: redis error, failing open", "op", op, "subject", subject, "error", err)
	return allow()
}

// AllowRPM 检查并消耗 subject（通常是 api_key_id 或 account_id 的字符串形式）
// 每分钟的请求配额。limit<=0 表示不限制。
func (l *Limiter) AllowRPM(ctx context.Context, subject string, limit int) Result {
	if limit <= 0 {
		return allow()
	}
	res, err := l.gcra.Allow(ctx, "uft:rpm:"+subject, rrate.PerMinute(limit))
	if err != nil {
		return l.failOpen("rpm", subject, err)
	}
	if res.Allowed > 0 {
		return allow()
	}
	return deny(res.RetryAfter)
}

// tpmScript 用固定窗口（每分钟）做 token 配额扣减：
// 当前窗口累计用量 + amount 超过 limit 就拒绝且不扣减；否则原子地累加。
// 用固定窗口而不是 GCRA/滑动窗口，是因为 token 数量是变长的"扣费"而不是
// 定量的"一次请求"，GCRA 的漏桶模型不直接适用；固定窗口在分钟边界上会有
// 一定的突发容忍度误差，可接受（技术方案 §7.12 也只要求"近似"）。
var tpmScript = redis.NewScript(`
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local amount = tonumber(ARGV[2])
local window = tonumber(ARGV[3])
local current = tonumber(redis.call('GET', key) or '0')
if current + amount > limit then
  return {0, current}
end
local newval = redis.call('INCRBY', key, amount)
if newval == amount then
  redis.call('EXPIRE', key, window)
end
return {1, newval}
`)

// ConsumeTPM 尝试在当前分钟窗口内扣减 amount 个 token 配额。limit<=0 表示不限制。
func (l *Limiter) ConsumeTPM(ctx context.Context, subject string, limit int, amount int64) Result {
	if limit <= 0 || amount <= 0 {
		return allow()
	}
	key := "uft:tpm:" + subject
	res, err := tpmScript.Run(ctx, l.rdb, []string{key}, limit, amount, 60).Result()
	if err != nil {
		return l.failOpen("tpm", subject, err)
	}
	vals, ok := res.([]interface{})
	if !ok || len(vals) != 2 {
		return l.failOpen("tpm", subject, fmt.Errorf("unexpected script result: %#v", res))
	}
	allowed, _ := vals[0].(int64)
	if allowed == 1 {
		return allow()
	}
	// 固定窗口在下一分钟边界重置，粗略给个 <=60s 的 Retry-After。
	return deny(time.Minute)
}

// concurrencyScript 用有序集合模拟并发租约：score 是租约到期的 unix 毫秒时间戳
// （用毫秒而不是秒，是因为曾经用秒粒度时，TTL 小于 1 秒的租约会在创建的同一秒
// 就被当成"已过期"清理掉——见本文件对应的单测），每次获取前先清理过期租约
// 再判断是否还有空位——全部在一次脚本里完成，避免"先 ZCARD 判断、再 ZADD 占位"
// 两步操作之间的竞态导致超发。
var concurrencyScript = redis.NewScript(`
local key = KEYS[1]
local now = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
local member = ARGV[3]
local expireAt = tonumber(ARGV[4])
redis.call('ZREMRANGEBYSCORE', key, '-inf', now)
local count = redis.call('ZCARD', key)
if count >= limit then
  return 0
end
redis.call('ZADD', key, expireAt, member)
redis.call('EXPIRE', key, 3600)
return 1
`)

// AcquireConcurrency 尝试为 subject 获取一个并发名额。limit<=0 表示不限制
// （此时 release 是 no-op）。ttl 是租约的兜底过期时间——正常情况下调用方应该
// 在请求结束时主动调用 release；ttl 只是防止进程崩溃导致名额永久占用。
func (l *Limiter) AcquireConcurrency(ctx context.Context, subject string, limit int, leaseID string, ttl time.Duration) (release func(), result Result) {
	noop := func() {}
	if limit <= 0 {
		return noop, allow()
	}

	key := "uft:concur:" + subject
	now := time.Now()
	res, err := concurrencyScript.Run(ctx, l.rdb, []string{key}, now.UnixMilli(), limit, leaseID, now.Add(ttl).UnixMilli()).Result()
	if err != nil {
		return noop, l.failOpen("concurrency", subject, err)
	}

	acquired, _ := res.(int64)
	if acquired != 1 {
		return noop, deny(0) // 具体等待时长未知：并发名额取决于其它请求何时结束，不是固定窗口
	}

	release = func() {
		relCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := l.rdb.ZRem(relCtx, key, leaseID).Err(); err != nil {
			l.logger.Warn("ratelimit: release concurrency lease failed", "subject", subject, "error", err)
		}
	}
	return release, allow()
}
