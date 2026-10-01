package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/health"
)

// 渠道健康（方案 G9，GET /channels/health）：把三类信息合在一起给运营看——
//   - 最近 window 内每个 active 渠道的请求量、错误率、P95 延迟（request_logs）；
//   - 网关熔断器当前状态、上游 Key 冷却（Redis，见 internal/health；没有 Redis 时为 unknown）；
//   - 最近的熔断/冷却事件（channel_health_events，worker 从 Redis 落库）。
// 判定阈值由服务端给出，前端不再硬编码。

// HealthThresholds 是判定"降级"的阈值。
type HealthThresholds struct {
	ErrorRate decimal.Decimal `json:"error_rate"`
	P95Ms     int64           `json:"p95_latency_ms"`
}

var DefaultHealthThresholds = HealthThresholds{ErrorRate: decimal.RequireFromString("0.05"), P95Ms: 5000}

type ChannelHealth struct {
	ChannelID           int64            `json:"channel_id"`
	VirtualModel        string           `json:"virtual_model"`
	ProviderAccount     string           `json:"provider_account"`
	UpstreamModel       string           `json:"upstream_model"`
	Requests            int64            `json:"requests"`
	Errors              int64            `json:"errors"`
	ErrorRate           *decimal.Decimal `json:"error_rate"`
	P95LatencyMs        *int64           `json:"p95_latency_ms"`
	BreakerState        string           `json:"breaker_state"` // closed / open / half_open / unknown
	BreakerSince        *time.Time       `json:"breaker_since"`
	KeysTotal           int              `json:"keys_total"`
	KeysOnCooldown      int              `json:"keys_on_cooldown"`
	CooldownMaxSeconds  int64            `json:"cooldown_max_seconds"`
	Status              string           `json:"status"` // down / degraded / healthy / idle
	StatusReasons       []string         `json:"status_reasons"`
	providerAccountID   int64
	activeProviderKeyID []int64
}

type HealthEvent struct {
	ID            int64           `json:"id"`
	ChannelID     *int64          `json:"channel_id"`
	ProviderKeyID *int64          `json:"provider_key_id"`
	Event         string          `json:"event"`
	Detail        json.RawMessage `json:"detail"`
	Gateway       *string         `json:"gateway"`
	OccurredAt    time.Time       `json:"occurred_at"`
}

type ChannelHealthReport struct {
	WindowMinutes     int              `json:"window_minutes"`
	Thresholds        HealthThresholds `json:"thresholds"`
	RuntimeStateKnown bool             `json:"runtime_state_known"` // false = 没有 Redis，熔断/冷却状态未知
	Channels          []ChannelHealth  `json:"channels"`
	RecentEvents      []HealthEvent    `json:"recent_events"`
}

var statusRank = map[string]int{"down": 0, "degraded": 1, "healthy": 2, "idle": 3}

// ChannelHealth 计算全部 active 渠道的健康状况。rdb 为 nil 时熔断/冷却状态为 unknown。
func (s *Service) ChannelHealth(ctx context.Context, rdb *redis.Client, window time.Duration, th HealthThresholds) (*ChannelHealthReport, error) {
	rows, err := s.db(ctx).Query(ctx,
		`SELECT c.id, vm.name, pa.name, c.upstream_model, c.provider_account_id,
		   count(rl.request_id), count(rl.request_id) FILTER (WHERE rl.status <> 'success'),
		   percentile_cont(0.95) WITHIN GROUP (ORDER BY rl.latency_ms) FILTER (WHERE rl.status = 'success'),
		   COALESCE((SELECT array_agg(k.id) FROM provider_keys k WHERE k.provider_account_id = c.provider_account_id AND k.status = 'active'), '{}')
		 FROM channels c
		 JOIN virtual_models vm ON vm.id = c.virtual_model_id
		 JOIN provider_accounts pa ON pa.id = c.provider_account_id
		 LEFT JOIN request_logs rl ON rl.channel_id = c.id AND rl.created_at >= $1
		 WHERE c.status = 'active'
		 GROUP BY c.id, vm.name, pa.name, c.upstream_model, c.provider_account_id`, time.Now().Add(-window))
	if err != nil {
		return nil, fmt.Errorf("admin: query channel health: %w", err)
	}
	var list []ChannelHealth
	var channelIDs, keyIDs []int64
	for rows.Next() {
		var c ChannelHealth
		var p95 *float64
		if err := rows.Scan(&c.ChannelID, &c.VirtualModel, &c.ProviderAccount, &c.UpstreamModel, &c.providerAccountID,
			&c.Requests, &c.Errors, &p95, &c.activeProviderKeyID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("admin: scan channel health: %w", err)
		}
		if c.Requests > 0 {
			er := decimal.NewFromInt(c.Errors).Div(decimal.NewFromInt(c.Requests)).Round(4)
			c.ErrorRate = &er
		}
		if p95 != nil {
			v := int64(*p95 + 0.5)
			c.P95LatencyMs = &v
		}
		c.KeysTotal = len(c.activeProviderKeyID)
		channelIDs = append(channelIDs, c.ChannelID)
		keyIDs = append(keyIDs, c.activeProviderKeyID...)
		list = append(list, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	report := &ChannelHealthReport{WindowMinutes: int(window / time.Minute), Thresholds: th, RuntimeStateKnown: rdb != nil,
		Channels: []ChannelHealth{}, RecentEvents: []HealthEvent{}}
	breakers, cooldowns := map[int64]health.BreakerState{}, map[int64]time.Duration{}
	if rdb != nil {
		if breakers, err = health.BreakerStates(ctx, rdb, channelIDs); err != nil {
			report.RuntimeStateKnown = false
		} else if cooldowns, err = health.CooldownRemaining(ctx, rdb, dedup(keyIDs)); err != nil {
			report.RuntimeStateKnown = false
		}
	}
	for i := range list {
		c := &list[i]
		c.StatusReasons = []string{}
		c.BreakerState = "unknown"
		if report.RuntimeStateKnown {
			c.BreakerState = "closed"
			if b, ok := breakers[c.ChannelID]; ok {
				c.BreakerState = b.State
				since := b.Since
				c.BreakerSince = &since
			}
			for _, k := range c.activeProviderKeyID {
				if d, ok := cooldowns[k]; ok {
					c.KeysOnCooldown++
					if sec := int64(d / time.Second); sec > c.CooldownMaxSeconds {
						c.CooldownMaxSeconds = sec
					}
				}
			}
		}
		switch {
		case c.KeysTotal == 0:
			c.StatusReasons = append(c.StatusReasons, "上游账号没有可用密钥")
		case report.RuntimeStateKnown && c.KeysOnCooldown == c.KeysTotal:
			c.StatusReasons = append(c.StatusReasons, "全部密钥都在冷却中")
		}
		if c.BreakerState == "open" {
			c.StatusReasons = append(c.StatusReasons, "熔断中")
		}
		down := len(c.StatusReasons) > 0
		if c.BreakerState == "half_open" {
			c.StatusReasons = append(c.StatusReasons, "熔断半开（试探中）")
		}
		if c.ErrorRate != nil && c.ErrorRate.GreaterThan(th.ErrorRate) {
			c.StatusReasons = append(c.StatusReasons, "错误率超过阈值")
		}
		if c.P95LatencyMs != nil && *c.P95LatencyMs > th.P95Ms {
			c.StatusReasons = append(c.StatusReasons, "P95 延迟超过阈值")
		}
		switch {
		case down:
			c.Status = "down"
		case len(c.StatusReasons) > 0:
			c.Status = "degraded"
		case c.Requests == 0:
			c.Status = "idle"
		default:
			c.Status = "healthy"
		}
	}
	sort.SliceStable(list, func(a, b int) bool {
		if statusRank[list[a].Status] != statusRank[list[b].Status] {
			return statusRank[list[a].Status] < statusRank[list[b].Status]
		}
		return list[a].Requests > list[b].Requests
	})
	if list != nil {
		report.Channels = list
	}

	evRows, err := s.db(ctx).Query(ctx,
		`SELECT id, channel_id, provider_key_id, event, detail, gateway, occurred_at
		 FROM channel_health_events ORDER BY occurred_at DESC LIMIT 50`)
	if err != nil {
		return nil, fmt.Errorf("admin: query health events: %w", err)
	}
	defer evRows.Close()
	for evRows.Next() {
		var e HealthEvent
		if err := evRows.Scan(&e.ID, &e.ChannelID, &e.ProviderKeyID, &e.Event, &e.Detail, &e.Gateway, &e.OccurredAt); err != nil {
			return nil, fmt.Errorf("admin: scan health event: %w", err)
		}
		report.RecentEvents = append(report.RecentEvents, e)
	}
	return report, evRows.Err()
}

func dedup(ids []int64) []int64 {
	seen := make(map[int64]bool, len(ids))
	out := ids[:0:0]
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
