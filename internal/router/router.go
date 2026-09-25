// Package router 实现渠道/Key 选择算法（技术方案 §7.5）。
//
// 算法：硬过滤 → 按 priority 分层，取最优先且非空的一层 → 层内按 weight 加权随机。
// 这是 V1 方案"打分后选最高"的替代方案——量纲不同的指标直接加权相加会导致全部流量
// 挤到同一个渠道（羊群效应），触发限流后再一起切换，来回振荡。用显式的 priority 表达
// 主备关系（运营可预期），只在同一层内做"智能"分流。
//
// 当前实现范围：硬过滤 + 优先级分层 + 加权随机。技术方案 §7.5.2 里基于实时延迟/成功率
// 的动态权重因子（health/latency/cost）、P2C、提示缓存亲和性，依赖 §7.6 的运行时健康度
// 模块，尚未实现——属于下一阶段（重试与故障转移、§7.6-7.7）要接入的部分，这里先保证
// "从候选集合里选出一个能用的" 是正确、无偏、可测的。
package router

import (
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

// Pick 从 snapshot 中为 vm 选出一个渠道 + Key。tier 是发起请求账户的分组；
// exclude 是本次请求内已经失败过、需要排除的渠道 ID（用于故障转移重试，当前 relay
// 还是单次调用，暂时总是传 nil，接口先留好）。
func Pick(snapshot *catalog.Snapshot, vm *catalog.VirtualModel, f Features, tier string, exclude map[int64]bool) (*Picked, error) {
	candidates := filterChannels(snapshot, vm, f, tier, exclude)
	if len(candidates) == 0 {
		return nil, ErrNoAvailableChannel
	}

	layer := topPriorityLayer(candidates)
	channel := weightedRandomChannel(layer)

	account := snapshot.ProviderAccounts[channel.ProviderAccountID]
	keys := activeKeys(snapshot.KeysByAccount[channel.ProviderAccountID])
	if len(keys) == 0 {
		// 理论上不会发生：filterChannels 已经要求渠道至少有一个可用 Key；
		// 这里是防御性检查（比如加载快照和选择之间数据发生了变化）。
		return nil, ErrNoAvailableChannel
	}
	key := weightedRandomKey(keys)

	return &Picked{Channel: channel, Account: account, Key: key}, nil
}

func filterChannels(snapshot *catalog.Snapshot, vm *catalog.VirtualModel, f Features, tier string, exclude map[int64]bool) []*catalog.Channel {
	var out []*catalog.Channel
	for _, c := range snapshot.ChannelsByVM[vm.ID] {
		if c.Status != "active" {
			continue
		}
		if exclude != nil && exclude[c.ID] {
			continue
		}
		if !c.AllowsTier(tier) {
			continue
		}
		account, ok := snapshot.ProviderAccounts[c.ProviderAccountID]
		if !ok || account.Status != "active" {
			continue
		}
		if len(activeKeys(snapshot.KeysByAccount[c.ProviderAccountID])) == 0 {
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

func activeKeys(keys []*catalog.ProviderKey) []*catalog.ProviderKey {
	var out []*catalog.ProviderKey
	for _, k := range keys {
		if k.Status == "active" {
			out = append(out, k)
		}
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
		total += weightOrDefault(c.Weight)
	}
	if total <= 0 {
		return layer[rand.IntN(len(layer))]
	}
	r := rand.IntN(total)
	for _, c := range layer {
		w := weightOrDefault(c.Weight)
		if r < w {
			return c
		}
		r -= w
	}
	return layer[len(layer)-1] // 理论不可达，防御性兜底
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
