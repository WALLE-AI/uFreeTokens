// 集成测试：连真实 Redis（Memurai/redis，无需 Docker），验证三种限流的原子性与边界行为。
// 跳过逻辑与其它包一致：连不上就 Skip，不让 `go test ./...` 在没有 Redis 的机器上失败。
package ratelimit

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/WALLE-AI/uFreeTokens/internal/config"
	"github.com/WALLE-AI/uFreeTokens/internal/observability"
)

const defaultTestRedisAddr = "localhost:6379"

func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("UFT_TEST_REDIS_ADDR")
	if addr == "" {
		addr = defaultTestRedisAddr
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		t.Skipf("skipping: redis not reachable at %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func newLimiter(t *testing.T) *Limiter {
	t.Helper()
	logger := observability.NewLogger(config.LogConfig{Level: "error", Format: "console"})
	return New(testRedis(t), logger)
}

func uniqueSubject(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())
}

func TestAllowRPM_AllowsUpToLimitThenDenies(t *testing.T) {
	l := newLimiter(t)
	ctx := context.Background()
	subject := uniqueSubject(t)

	for i := 0; i < 3; i++ {
		res := l.AllowRPM(ctx, subject, 3)
		if !res.Allowed {
			t.Fatalf("request %d should be allowed within limit", i)
		}
	}

	res := l.AllowRPM(ctx, subject, 3)
	if res.Allowed {
		t.Fatal("4th request should be denied (limit=3/min)")
	}
	if res.RetryAfter <= 0 {
		t.Errorf("RetryAfter = %v, want > 0", res.RetryAfter)
	}
}

func TestAllowRPM_NoLimitAlwaysAllows(t *testing.T) {
	l := newLimiter(t)
	ctx := context.Background()
	subject := uniqueSubject(t)

	for i := 0; i < 50; i++ {
		if res := l.AllowRPM(ctx, subject, 0); !res.Allowed {
			t.Fatalf("request %d should always be allowed when limit<=0", i)
		}
	}
}

func TestAllowRPM_DifferentSubjectsAreIndependent(t *testing.T) {
	l := newLimiter(t)
	ctx := context.Background()
	subjectA, subjectB := uniqueSubject(t)+"-a", uniqueSubject(t)+"-b"

	l.AllowRPM(ctx, subjectA, 1) // 用掉 A 的唯一配额
	if res := l.AllowRPM(ctx, subjectA, 1); res.Allowed {
		t.Fatal("subject A should be exhausted")
	}
	if res := l.AllowRPM(ctx, subjectB, 1); !res.Allowed {
		t.Error("subject B should be unaffected by subject A's usage")
	}
}

func TestConsumeTPM_AllowsWithinLimitThenDenies(t *testing.T) {
	l := newLimiter(t)
	ctx := context.Background()
	subject := uniqueSubject(t)

	if res := l.ConsumeTPM(ctx, subject, 1000, 400); !res.Allowed {
		t.Fatal("first consumption of 400/1000 should be allowed")
	}
	if res := l.ConsumeTPM(ctx, subject, 1000, 400); !res.Allowed {
		t.Fatal("second consumption bringing total to 800/1000 should be allowed")
	}
	if res := l.ConsumeTPM(ctx, subject, 1000, 300); res.Allowed {
		t.Fatal("third consumption would bring total to 1100/1000, should be denied")
	}
	// 被拒绝的那次不应该真的扣减：紧接着消耗 200（800+200=1000）应该仍然被允许。
	if res := l.ConsumeTPM(ctx, subject, 1000, 200); !res.Allowed {
		t.Error("denied consumption must not have been debited; 800+200=1000 should still fit")
	}
}

func TestConsumeTPM_NoLimitAlwaysAllows(t *testing.T) {
	l := newLimiter(t)
	ctx := context.Background()
	subject := uniqueSubject(t)
	if res := l.ConsumeTPM(ctx, subject, 0, 1_000_000_000); !res.Allowed {
		t.Error("should always allow when limit<=0")
	}
}

func TestAcquireConcurrency_LimitsAndReleases(t *testing.T) {
	l := newLimiter(t)
	ctx := context.Background()
	subject := uniqueSubject(t)
	const limit = 2

	rel1, res1 := l.AcquireConcurrency(ctx, subject, limit, "lease-1", time.Minute)
	if !res1.Allowed {
		t.Fatal("first lease should be acquired")
	}
	_, res2 := l.AcquireConcurrency(ctx, subject, limit, "lease-2", time.Minute)
	if !res2.Allowed {
		t.Fatal("second lease should be acquired (limit=2)")
	}
	_, res3 := l.AcquireConcurrency(ctx, subject, limit, "lease-3", time.Minute)
	if res3.Allowed {
		t.Fatal("third lease should be denied (limit=2, both slots taken)")
	}

	rel1() // 释放第一个租约
	_, res4 := l.AcquireConcurrency(ctx, subject, limit, "lease-4", time.Minute)
	if !res4.Allowed {
		t.Error("after releasing a lease, a new acquisition should succeed")
	}
}

func TestAcquireConcurrency_ExpiredLeaseIsReclaimed(t *testing.T) {
	l := newLimiter(t)
	ctx := context.Background()
	subject := uniqueSubject(t)

	_, res1 := l.AcquireConcurrency(ctx, subject, 1, "short-lived", 50*time.Millisecond)
	if !res1.Allowed {
		t.Fatal("first lease should be acquired")
	}
	_, res2 := l.AcquireConcurrency(ctx, subject, 1, "blocked", time.Minute)
	if res2.Allowed {
		t.Fatal("second lease should be denied while the first is still within its ttl")
	}

	time.Sleep(150 * time.Millisecond) // 等第一个租约的 ttl 过期

	_, res3 := l.AcquireConcurrency(ctx, subject, 1, "after-expiry", time.Minute)
	if !res3.Allowed {
		t.Error("after the first lease's ttl expires, it should be reclaimed and a new acquisition should succeed")
	}
}

func TestAcquireConcurrency_NoLimitAlwaysAllows(t *testing.T) {
	l := newLimiter(t)
	ctx := context.Background()
	subject := uniqueSubject(t)
	_, res := l.AcquireConcurrency(ctx, subject, 0, "x", time.Minute)
	if !res.Allowed {
		t.Error("should always allow when limit<=0")
	}
}

// TestAcquireConcurrency_ConcurrentRacesNeverExceedLimit 是并发正确性的回归测试
// （呼应 wallet 包的透支回归测试思路）：limit=5，20 个 goroutine 同时抢，
// 必须精确地只有 5 个成功——Lua 脚本的原子性是这里的保证，如果退化成
// "先 ZCARD 再 ZADD" 两步操作，在这个测试里几乎必然会超发。
func TestAcquireConcurrency_ConcurrentRacesNeverExceedLimit(t *testing.T) {
	l := newLimiter(t)
	ctx := context.Background()
	subject := uniqueSubject(t)
	const limit = 5
	const n = 20

	var wg sync.WaitGroup
	var succeeded int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, res := l.AcquireConcurrency(ctx, subject, limit, fmt.Sprintf("lease-%d", i), time.Minute)
			if res.Allowed {
				atomic.AddInt64(&succeeded, 1)
			}
		}(i)
	}
	wg.Wait()

	if succeeded != limit {
		t.Errorf("succeeded acquisitions = %d, want exactly %d", succeeded, limit)
	}
}
