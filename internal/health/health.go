// Package health 是渠道熔断与 Key 冷却的运行时状态（技术方案 §7.6）。
// 这些状态不在请求路径上落库：熔断器是进程内的（每个网关实例各自统计，不需要
// 跨实例一致），Key 冷却用 Redis 共享（429/配额耗尽是上游对某个 Key 的限制，
// 跨实例必须看到同一个状态，否则每个实例都要各自撞一次 429 才知道该 Key 在冷却）。
//
// 可观测性（运营后台 GET /channels/health，方案 G9）：熔断器状态变化时，网关把当前
// 状态写到 Redis（uft:breaker:channel:<id>，非 closed 时带 TTL），并把状态变化与
// Key 冷却作为事件推进 Redis 列表 uft:health:events；worker 定期把事件落到
// channel_health_events 表。发布是异步、尽力而为的，失败不影响请求。
package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
	// instance 标识当前网关实例（主机名），写进熔断状态与事件，便于区分多实例。
	instance string
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
	host, _ := os.Hostname()
	return &Registry{
		breakers: map[int64]*gobreaker.TwoStepCircuitBreaker[struct{}]{},
		settings: settings,
		redis:    rdb,
		instance: host,
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

// ---------- 状态发布（供运营后台观测） ----------

// EventsRedisKey 是健康事件列表；BreakerRedisKey 是某渠道当前的熔断状态。
const EventsRedisKey = "uft:health:events"

// maxQueuedEvents 是事件列表的长度上限：worker 停摆时最多保留这么多条，防止无限增长。
const maxQueuedEvents = 10000

func BreakerRedisKey(channelID int64) string { return fmt.Sprintf("uft:breaker:channel:%d", channelID) }

// Event 是推进 EventsRedisKey 的一条健康事件（JSON）。
type Event struct {
	ChannelID     int64          `json:"channel_id,omitempty"`
	ProviderKeyID int64          `json:"provider_key_id,omitempty"`
	Event         string         `json:"event"` // breaker_open / breaker_half_open / breaker_closed / key_cooldown
	Detail        map[string]any `json:"detail,omitempty"`
	Gateway       string         `json:"gateway"`
	At            time.Time      `json:"at"`
}

// BreakerState 是写在 BreakerRedisKey 里的当前熔断状态。
type BreakerState struct {
	State   string    `json:"state"` // open / half_open
	Since   time.Time `json:"since"`
	Gateway string    `json:"gateway"`
}

func stateName(s gobreaker.State) string {
	switch s {
	case gobreaker.StateOpen:
		return "open"
	case gobreaker.StateHalfOpen:
		return "half_open"
	default:
		return "closed"
	}
}

func (r *Registry) publishBreaker(channelID int64, from, to gobreaker.State) {
	if r.redis == nil {
		return
	}
	now := time.Now()
	ev := Event{ChannelID: channelID, Event: "breaker_" + stateName(to), Gateway: r.instance, At: now,
		Detail: map[string]any{"from": stateName(from)}}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		key := BreakerRedisKey(channelID)
		if to == gobreaker.StateClosed {
			_ = r.redis.Del(ctx, key).Err()
		} else if raw, err := json.Marshal(BreakerState{State: stateName(to), Since: now, Gateway: r.instance}); err == nil {
			// TTL 取熔断冷却的数倍：网关实例宕掉时状态不会永远停在 open
			_ = r.redis.Set(ctx, key, raw, 4*r.settings.OpenTimeout+time.Minute).Err()
		}
		r.pushEvent(ctx, ev)
	}()
}

func (r *Registry) pushEvent(ctx context.Context, ev Event) {
	raw, err := json.Marshal(ev)
	if err != nil {
		return
	}
	pipe := r.redis.Pipeline()
	pipe.LPush(ctx, EventsRedisKey, raw)
	pipe.LTrim(ctx, EventsRedisKey, 0, maxQueuedEvents-1)
	_, _ = pipe.Exec(ctx)
}

// ClearKeyCooldown 清除某个上游 Key 的冷却记录（运营吊销或重新启用 Key 时调用）。
func ClearKeyCooldown(ctx context.Context, rdb *redis.Client, providerKeyID int64) error {
	if rdb == nil {
		return nil
	}
	return rdb.Del(ctx, cooldownRedisKey(providerKeyID)).Err()
}

// CooldownRemaining 返回一批上游 Key 剩余的冷却时长（不在冷却中的不出现在结果里）。
func CooldownRemaining(ctx context.Context, rdb *redis.Client, providerKeyIDs []int64) (map[int64]time.Duration, error) {
	out := map[int64]time.Duration{}
	if rdb == nil || len(providerKeyIDs) == 0 {
		return out, nil
	}
	pipe := rdb.Pipeline()
	cmds := make([]*redis.DurationCmd, len(providerKeyIDs))
	for i, id := range providerKeyIDs {
		cmds[i] = pipe.PTTL(ctx, cooldownRedisKey(id))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	for i, c := range cmds {
		if d, err := c.Result(); err == nil && d > 0 {
			out[providerKeyIDs[i]] = d
		}
	}
	return out, nil
}

// BreakerStates 读取一批渠道当前的熔断状态（closed 的不出现在结果里）。
func BreakerStates(ctx context.Context, rdb *redis.Client, channelIDs []int64) (map[int64]BreakerState, error) {
	out := map[int64]BreakerState{}
	if rdb == nil || len(channelIDs) == 0 {
		return out, nil
	}
	keys := make([]string, len(channelIDs))
	for i, id := range channelIDs {
		keys[i] = BreakerRedisKey(id)
	}
	vals, err := rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	for i, v := range vals {
		s, ok := v.(string)
		if !ok {
			continue
		}
		var st BreakerState
		if json.Unmarshal([]byte(s), &st) == nil {
			out[channelIDs[i]] = st
		}
	}
	return out, nil
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
	// 事件推送不放在请求路径上：异步、尽力而为，失败不影响冷却本身。
	ev := Event{ProviderKeyID: providerKeyID, Event: "key_cooldown", Gateway: r.instance, At: time.Now(),
		Detail: map[string]any{"seconds": int64(d / time.Second)}}
	go func() {
		pctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		r.pushEvent(pctx, ev)
	}()
}

// PersistEvents 把 Redis 事件列表里的事件批量写入 channel_health_events（worker 定期调用），
// 返回写入条数。先写库、再从列表删除：写库失败时事件留在 Redis 里，下次重试。
func PersistEvents(ctx context.Context, pool *pgxpool.Pool, rdb *redis.Client) (int, error) {
	if rdb == nil {
		return 0, nil
	}
	const batchSize = 500
	stored := 0
	for {
		items, err := rdb.LRange(ctx, EventsRedisKey, -batchSize, -1).Result()
		if err != nil {
			return stored, fmt.Errorf("health: read events: %w", err)
		}
		if len(items) == 0 {
			return stored, nil
		}
		batch := &pgx.Batch{}
		// LPUSH 让最新的在头部；从尾部取的是最早的，倒序遍历得到时间正序。
		for i := len(items) - 1; i >= 0; i-- {
			var ev Event
			if json.Unmarshal([]byte(items[i]), &ev) != nil {
				continue
			}
			batch.Queue(`INSERT INTO channel_health_events (channel_id, provider_key_id, event, detail, gateway, occurred_at)
				VALUES (NULLIF($1::bigint, 0), NULLIF($2::bigint, 0), $3, $4, NULLIF($5, ''), $6)`,
				ev.ChannelID, ev.ProviderKeyID, ev.Event, ev.Detail, ev.Gateway, ev.At)
		}
		if err := pool.SendBatch(ctx, batch).Close(); err != nil {
			return stored, fmt.Errorf("health: store events: %w", err)
		}
		if err := rdb.LTrim(ctx, EventsRedisKey, 0, int64(-len(items)-1)).Err(); err != nil {
			return stored, fmt.Errorf("health: trim events: %w", err)
		}
		stored += len(items)
		if len(items) < batchSize {
			return stored, nil
		}
	}
}
