package health

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func fastBreakerSettings() BreakerSettings {
	return BreakerSettings{
		MinRequests:    4,
		FailureRatio:   0.5,
		OpenTimeout:    80 * time.Millisecond,
		HalfOpenProbes: 1,
		StatWindow:     time.Minute,
	}
}

func TestChannelOpen_InitiallyClosed(t *testing.T) {
	r := NewRegistry(nil, fastBreakerSettings())
	if r.ChannelOpen(1) {
		t.Error("a fresh channel should not be reported as open")
	}
}

func TestTryChannel_TripsAfterFailureRatioExceeded(t *testing.T) {
	r := NewRegistry(nil, fastBreakerSettings())
	const channelID = 1

	// 4 次请求，3 次失败（75% > 50% 阈值，且达到 MinRequests=4）应该触发熔断。
	outcomes := []bool{true, false, false, false}
	for _, success := range outcomes {
		done, ok := r.TryChannel(channelID)
		if !ok {
			t.Fatalf("TryChannel should still allow requests before the breaker trips")
		}
		done(success)
	}

	if !r.ChannelOpen(channelID) {
		t.Fatal("breaker should be open after exceeding the failure ratio")
	}
	if _, ok := r.TryChannel(channelID); ok {
		t.Error("TryChannel should reject while breaker is open")
	}
}

func TestTryChannel_StaysClosedWhenFailuresAreRare(t *testing.T) {
	r := NewRegistry(nil, fastBreakerSettings())
	const channelID = 2

	// 4 次请求只有 1 次失败（25% < 50% 阈值）不应该触发熔断。
	outcomes := []bool{true, true, true, false}
	for _, success := range outcomes {
		done, ok := r.TryChannel(channelID)
		if !ok {
			t.Fatalf("TryChannel should allow requests while breaker is closed")
		}
		done(success)
	}

	if r.ChannelOpen(channelID) {
		t.Error("breaker should remain closed when failures are below the ratio threshold")
	}
}

func TestTryChannel_RecoversThroughHalfOpenOnSuccess(t *testing.T) {
	r := NewRegistry(nil, fastBreakerSettings())
	const channelID = 3

	for _, success := range []bool{false, false, false, false} {
		done, _ := r.TryChannel(channelID)
		done(success)
	}
	if !r.ChannelOpen(channelID) {
		t.Fatal("breaker should be open after all failures")
	}

	time.Sleep(120 * time.Millisecond) // > OpenTimeout

	done, ok := r.TryChannel(channelID)
	if !ok {
		t.Fatal("breaker should allow a half-open probe after the timeout elapses")
	}
	done(true) // 探测成功 -> 应该恢复 Closed

	if r.ChannelOpen(channelID) {
		t.Error("breaker should be closed again after a successful half-open probe")
	}
	if _, ok := r.TryChannel(channelID); !ok {
		t.Error("breaker should allow normal requests again after recovering")
	}
}

func TestTryChannel_ReturnsToOpenOnFailedProbe(t *testing.T) {
	r := NewRegistry(nil, fastBreakerSettings())
	const channelID = 4

	for _, success := range []bool{false, false, false, false} {
		done, _ := r.TryChannel(channelID)
		done(success)
	}
	time.Sleep(120 * time.Millisecond)

	done, ok := r.TryChannel(channelID)
	if !ok {
		t.Fatal("expected a half-open probe to be allowed")
	}
	done(false) // 探测失败 -> 应该回到 Open

	if !r.ChannelOpen(channelID) {
		t.Error("breaker should go back to open after a failed half-open probe")
	}
}

func TestChannels_AreIndependent(t *testing.T) {
	r := NewRegistry(nil, fastBreakerSettings())
	for _, success := range []bool{false, false, false, false} {
		done, _ := r.TryChannel(100)
		done(success)
	}
	if !r.ChannelOpen(100) {
		t.Fatal("channel 100 should be open")
	}
	if r.ChannelOpen(200) {
		t.Error("channel 200 should be unaffected by channel 100's failures")
	}
}

// --- Key 冷却：真实 Redis 集成测试（skip 逻辑同其它包） ---

const defaultTestDSN = "localhost:6379"

func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("UFT_TEST_REDIS_ADDR")
	if addr == "" {
		addr = defaultTestDSN
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

func TestKeyCooldown_RealRedis(t *testing.T) {
	rdb := testRedis(t)
	r := NewRegistry(rdb, fastBreakerSettings())
	ctx := context.Background()
	keyID := time.Now().UnixNano() // 用唯一 ID 避免测试间串数据

	if r.KeyOnCooldown(ctx, keyID) {
		t.Fatal("key should not be on cooldown before CooldownKey is called")
	}

	r.CooldownKey(ctx, keyID, 150*time.Millisecond)
	if !r.KeyOnCooldown(ctx, keyID) {
		t.Fatal("key should be on cooldown immediately after CooldownKey")
	}

	time.Sleep(250 * time.Millisecond)
	if r.KeyOnCooldown(ctx, keyID) {
		t.Error("key cooldown should have expired")
	}
}

func TestKeyCooldown_NilRedisFailsOpen(t *testing.T) {
	r := NewRegistry(nil, fastBreakerSettings())
	ctx := context.Background()
	if r.KeyOnCooldown(ctx, 1) {
		t.Error("nil redis client should fail open (never report cooldown)")
	}
	r.CooldownKey(ctx, 1, time.Second) // 不应该 panic
}
