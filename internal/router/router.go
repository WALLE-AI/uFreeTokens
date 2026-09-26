// Package router 实现渠道/Key 选择算法（技术方案 §7.5）。
//
// 算法：硬过滤 → 按 priority 分层，取最优先且非空的一层 → 层内按 weight 加权随机。
// 这是 V1 方案"打分后选最高"的替代方案——量纲不同的指标直接加权相加会导致全部流量
// 挤到同一个渠道（羊群效应），触发限流后再一起切换，来回振荡。用显式的 priority 表达
// 主备关系（运营可预期），只在同一层内做"智能"分流。
//
// 硬过滤额外接入了熔断器状态（ChannelHealth）与 Key 冷却状态（KeyHealth，见
// internal/health），配合 relay 层的重试循环实现"换渠道/换 Key 重试"（§7.6-7.7）。
//
// 层内加权随机还接入了 catalog.Channel.NegativeMargin（技术方案 §7.16.7 毛利
// 守护）：挂牌价结构性亏钱的渠道，有效权重打 10% 折扣（不是硬性排除——它仍然
// 可能是这一层唯一的候选，届时依然会被选中，只是同层有健康渠道时流量会被
// 自动挤过去）。
//
// 仍未实现的：基于实时延迟/成功率的动态权重因子、P2C、提示缓存亲和性（§7.5.2 的
// 加权公式），留作后续——静态 weight 已经能避免羊群效应，动态因子是锦上添花。
package router

import (
	"context"
	"errors"
	"math/rand/v2"

	"github.com/WALLE-AI/uFreeTokens/internal/catalog"
)

var ErrNoAvailableChannel = errors.New("router: no available channel")

// Features 是一次请求的路由相关特征（技术方案 §7.5.1）。
type Features struct {
	Stream          bool
	NeedTools       bool
	NeedVision      bool
	NeedJSONSchema  bool
	EstInputTokens  int
	MaxOutputTokens int
}

// Picked 是一次路由决策的结果。
type Picked struct {
	Channel *catalog.Channel
	Account *catalog.ProviderAccount
	Key     *catalog.ProviderKey
}

// ChannelHealth 由 internal/health.Registry 实现：判断某渠道当前是否因为熔断而
// 应该被跳过。只做只读查询，不消耗熔断器的 Half-Open 试探配额
// （消耗配额的是 relay 层实际尝试前调用的 TryChannel，那一步在 router 之外）。
type ChannelHealth interface {
	ChannelOpen(channelID int64) bool
}

// KeyHealth 由 internal/health.Registry 实现：判断某上游 Key 是否处于冷却期。
type KeyHealth interface {
	KeyOnCooldown(ctx context.Context, providerKeyID int64) bool
}

// SelectOptions 是一次 Pick 调用的过滤/排除条件。零值表示不做任何额外过滤，
// 与最初没有重试机制时的行为完全一致。
type SelectOptions struct {
	ExcludeChannels map[int64]bool // 本次请求内已经尝试失败、需要排除的渠道
	ExcludeKeys     map[int64]bool // 本次请求内已经尝试失败、需要排除的 Key
	ChannelHealth   ChannelHealth  // nil = 不做熔断过滤
	KeyHealth       KeyHealth      // nil = 不做冷却过滤
}

// Pick 从 snapshot 中为 vm 选出一个渠道 + Key。tier 是发起请求账户的分组。
func Pick(ctx context.Context, snapshot *catalog.Snapshot, vm *catalog.VirtualModel, f Features, tier string, opts SelectOptions) (*Picked, error) {
	candidates := filterChannels(ctx, snapshot, vm, f, tier, opts)
	if len(candidates) == 0 {
		return nil, ErrNoAvailableChannel
	}

	layer := topPriorityLayer(candidates)
	channel := weightedRandomChannel(layer)

	account := snapshot.ProviderAccounts[channel.ProviderAccountID]
	keys := selectableKeys(ctx, snapshot.KeysByAccount[channel.ProviderAccountID], opts)
	if len(keys) == 0 {
		// 理论上不会发生：filterChannels 已经要求渠道至少有一个可选 Key；
		// 这里是防御性检查（比如加载快照和选择之间数据发生了变化）。
		return nil, ErrNoAvailableChannel
	}
	key := weightedRandomKey(keys)

	return &Picked{Channel: channel, Account: account, Key: key}, nil
}

func filterChannels(ctx context.Context, snapshot *catalog.Snapshot, vm *catalog.VirtualModel, f Features, tier string, opts SelectOptions) []*catalog.Channel {
	var out []*catalog.Channel
	for _, c := range snapshot.ChannelsByVM[vm.ID] {
		if c.Status != "active" {
			continue
		}
		if opts.ExcludeChannels != nil && opts.ExcludeChannels[c.ID] {
			continue
		}
		if opts.ChannelHealth != nil && opts.ChannelHealth.ChannelOpen(c.ID) {
			continue
		}
		if !c.AllowsTier(tier) {
			continue
		}
		account, ok := snapshot.ProviderAccounts[c.ProviderAccountID]
		if !ok || account.Status != "active" {
			continue
		}
		if len(selectableKeys(ctx, snapshot.KeysByAccount[c.ProviderAccountID], opts)) == 0 {
			continue
		}

		caps := c.EffectiveCapabilities(vm)
		if f.NeedTools && !hasCapability(caps, "tools") {
			continue
		}
		if f.NeedVision && !hasCapability(caps, "vision") {
			continue
		}
		if f.NeedJSONSchema && !hasCapability(caps, "json_schema") {
			continue
		}
		if f.Stream && !hasCapability(caps, "stream") {
			continue
		}

		window := c.EffectiveContextWindow(vm)
		if window > 0 && f.EstInputTokens+f.MaxOutputTokens > window {
			continue
		}

		out = append(out, c)
	}
	return out
}

func hasCapability(caps []string, want string) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}

// selectableKeys 返回一个上游账号下"状态 active 且未被排除且未在冷却"的 Key 列表。
func selectableKeys(ctx context.Context, keys []*catalog.ProviderKey, opts SelectOptions) []*catalog.ProviderKey {
	var out []*catalog.ProviderKey
	for _, k := range keys {
		if k.Status != "active" {
			continue
		}
		if opts.ExcludeKeys != nil && opts.ExcludeKeys[k.ID] {
			continue
		}
		if opts.KeyHealth != nil && opts.KeyHealth.KeyOnCooldown(ctx, k.ID) {
			continue
		}
		out = append(out, k)
	}
	return out
}

// topPriorityLayer 返回 priority 数值最小（最优先）的一组渠道，用它们做加权随机。
func topPriorityLayer(candidates []*catalog.Channel) []*catalog.Channel {
	best := candidates[0].Priority
	for _, c := range candidates {
		if c.Priority < best {
			best = c.Priority
		}
	}
	var layer []*catalog.Channel
	for _, c := range candidates {
		if c.Priority == best {
			layer = append(layer, c)
		}
	}
	return layer
}

func weightedRandomChannel(layer []*catalog.Channel) *catalog.Channel {
	total := 0
	for _, c := range layer {
		total += channelWeight(c)
	}
	if total <= 0 {
		return layer[rand.IntN(len(layer))]
	}
	r := rand.IntN(total)
	for _, c := range layer {
		w := channelWeight(c)
		if r < w {
			return c
		}
		r -= w
	}
	return layer[len(layer)-1] // 理论不可达，防御性兜底
}

// channelWeight 是渠道的有效路由权重：配置的 weight，毛利为负时打 10% 折扣
// （技术方案 §7.16.7），下限 1——不能让一个仍在服务的渠道有效权重归零，那样
// 它会被 rand.IntN(total) 排除出可选范围，等价于硬性下线，超出了"降权"的本意。
func channelWeight(c *catalog.Channel) int {
	w := weightOrDefault(c.Weight)
	if c.NegativeMargin {
		w /= 10
		if w < 1 {
			w = 1
		}
	}
	return w
}

func weightedRandomKey(keys []*catalog.ProviderKey) *catalog.ProviderKey {
	total := 0
	for _, k := range keys {
		total += weightOrDefault(k.Weight)
	}
	if total <= 0 {
		return keys[rand.IntN(len(keys))]
	}
	r := rand.IntN(total)
	for _, k := range keys {
		w := weightOrDefault(k.Weight)
		if r < w {
			return k
		}
		r -= w
	}
	return keys[len(keys)-1]
}

func weightOrDefault(w int) int {
	if w <= 0 {
		return 1
	}
	return w
}
