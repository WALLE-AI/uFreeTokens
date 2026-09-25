// Package health 是渠道熔断与 Key 冷却的运行时状态（技术方案 §7.6）。
// 这些状态不落库：熔断器是进程内的（每个网关实例各自统计，不需要跨实例一致），
// Key 冷却用 Redis 共享（429/配额耗尽是上游对某个 Key 的限制，跨实例必须看到
// 同一个状态，否则每个实例都要各自撞一次 429 才知道该 Key 在冷却）。
package health

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sony/gobreaker/v2"
)

// errUpstreamFailure 是喂给 gobreaker 的失败哨兵错误；gobreaker 的 TwoStep done
// 回调按 "err == nil 即成功" 判定，我们只需要一个非 nil 值，具体类型不重要。
var errUpstreamFailure = errors.New("health: upstream attempt failed")

// Registry 同时满足 internal/router 的 ChannelHealth 和 KeyHealth 接口
// （结构化接口匹配，router 不需要依赖本包的具体类型）。
type Registry struct {
	mu       sync.Mutex
	breakers map[int64]*gobreaker.TwoStepCircuitBreaker[struct{}]
	settings BreakerSettings

	redis *redis.Client // 允许为 nil：此时 Key 冷却检查始终放行（fail-open，不阻塞请求)
}

// BreakerSettings 控制单个渠道熔断器的触发/恢复条件。
type BreakerSettings struct {
	// ConsecutiveFailures 或失败率达到阈值才会触发熔断，避免偶发的 1-2 次失败就整渠道拉黑。
	MinRequests    uint32        // 窗口内至少这么多次请求才评估失败率
	FailureRatio   float64       // 失败率达到该比例触发熔断（0~1）
	OpenTimeout    time.Duration // Open -> Half-Open 的冷却时长
	HalfOpenProbes uint32        // Half-Open 状态允许放行的试探请求数
	StatWindow     time.Duration // Closed 状态下滑动统计窗口，超过该时长重置计数
}

func DefaultBreakerSettings() BreakerSettings {
	return BreakerSettings{
		MinRequests:    5,
		FailureRatio:   0.5,
		OpenTimeout:    30 * time.Second,
		HalfOpenProbes: 1,
		StatWindow:     60 * time.Second,
	}
}

func NewRegistry(rdb *redis.Client, settings BreakerSettings) *Registry {
	return &Registry{
		breakers: map[int64]*gobreaker.TwoStepCircuitBreaker[struct{}]{},
		settings: settings,
		redis:    rdb,
	}
}

func (r *Registry) breakerFor(channelID int64) *gobreaker.TwoStepCircuitBreaker[struct{}] {
	r.mu.Lock()
	defer r.mu.Unlock()

	if b, ok := r.breakers[channelID]; ok {
		return b
	}
	s := r.settings
	b := gobreaker.NewTwoStepCircuitBreaker[struct{}](gobreaker.Settings{
		Name:        fmt.Sprintf("channel-%d", channelID),
		MaxRequests: s.HalfOpenProbes,
		Interval:    s.StatWindow,
		Timeout:     s.OpenTimeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.Requests >= s.MinRequests &&
				float64(counts.TotalFailures)/float64(counts.Requests) >= s.FailureRatio
		},
	})
	r.breakers[channelID] = b
	return b
}

// ChannelOpen 供路由做便宜的预过滤：true 表示该渠道当前处于熔断 Open 状态，应被跳过。
// 只读查询，不消耗 Half-Open 的试探配额（消耗配额的是 TryChannel）。
func (r *Registry) ChannelOpen(channelID int64) bool {
	return r.breakerFor(channelID).State() == gobreaker.StateOpen
}

// TryChannel 是真正发起一次上游调用前必须调用的步骤：确认熔断器当前允许通过
// （Closed，或 Half-Open 且试探配额未用完），并预占一个名额。
// ok=false 表示不要发起这次调用，应立即换下一个候选渠道。
// ok=true 时调用方必须在拿到结果后调用 done(success)，否则熔断器的统计会失真。
func (r *Registry) TryChannel(channelID int64) (done func(success bool), ok bool) {
	d, err := r.breakerFor(channelID).Allow()
	if err != nil {
		return nil, false
	}
	return func(success bool) {
		if success {
			d(nil)
		} else {
			d(errUpstreamFailure)
		}
	}, true
}

func cooldownRedisKey(providerKeyID int64) string {
	return fmt.Sprintf("uft:cooldown:key:%d", providerKeyID)
}

// KeyOnCooldown 判断某个上游 Key 当前是否处于冷却期（true = 应跳过）。
// Redis 不可用时 fail-open（返回 false，不阻塞请求）——限流组件故障不应该
// 导致全站请求都被当成"Key 不可用"而失败（技术方案 §7.12 同样的 fail-open 原则）。
func (r *Registry) KeyOnCooldown(ctx context.Context, providerKeyID int64) bool {
	if r.redis == nil {
		return false
	}
	n, err := r.redis.Exists(ctx, cooldownRedisKey(providerKeyID)).Result()
	if err != nil {
		return false
	}
	return n > 0
}

// CooldownKey 让某个上游 Key 在 d 时长内不再被路由选中（429/配额耗尽/Key 失效场景）。
func (r *Registry) CooldownKey(ctx context.Context, providerKeyID int64, d time.Duration) {
	if r.redis == nil || d <= 0 {
		return
	}
	_ = r.redis.Set(ctx, cooldownRedisKey(providerKeyID), 1, d).Err()
}
