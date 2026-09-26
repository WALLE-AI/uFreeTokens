package router

import (
	"context"
	"testing"

	"github.com/WALLE-AI/uFreeTokens/internal/catalog"
)

func baseSnapshot() *catalog.Snapshot {
	vm := &catalog.VirtualModel{
		ID: 1, Name: "deepseek-v4-flash", ContextWindow: 128000, MaxOutput: 8192,
		Capabilities: []string{"stream", "tools"},
	}
	return &catalog.Snapshot{
		Models:           map[string]*catalog.VirtualModel{vm.Name: vm},
		ChannelsByVM:     map[int64][]*catalog.Channel{},
		ProviderAccounts: map[int64]*catalog.ProviderAccount{},
		KeysByAccount:    map[int64][]*catalog.ProviderKey{},
	}
}

func addAccount(s *catalog.Snapshot, id int64, status string) *catalog.ProviderAccount {
	a := &catalog.ProviderAccount{ID: id, Status: status}
	s.ProviderAccounts[id] = a
	return a
}

func addKey(s *catalog.Snapshot, accountID, keyID int64, weight int, status string) {
	s.KeysByAccount[accountID] = append(s.KeysByAccount[accountID], &catalog.ProviderKey{
		ID: keyID, ProviderAccountID: accountID, Weight: weight, Status: status,
	})
}

func addChannel(s *catalog.Snapshot, c *catalog.Channel) {
	s.ChannelsByVM[c.VirtualModelID] = append(s.ChannelsByVM[c.VirtualModelID], c)
}

func vm(s *catalog.Snapshot) *catalog.VirtualModel { return s.Models["deepseek-v4-flash"] }

func pick(t *testing.T, s *catalog.Snapshot, f Features, tier string, opts SelectOptions) (*Picked, error) {
	t.Helper()
	return Pick(context.Background(), s, vm(s), f, tier, opts)
}

func TestPick_NoChannelsReturnsError(t *testing.T) {
	s := baseSnapshot()
	if _, err := pick(t, s, Features{}, "free", SelectOptions{}); err != ErrNoAvailableChannel {
		t.Fatalf("err = %v, want ErrNoAvailableChannel", err)
	}
}

func TestPick_FiltersDisabledChannel(t *testing.T) {
	s := baseSnapshot()
	addAccount(s, 1, "active")
	addKey(s, 1, 1, 100, "active")
	addChannel(s, &catalog.Channel{ID: 1, VirtualModelID: 1, ProviderAccountID: 1, Status: "disabled", Priority: 0})

	if _, err := pick(t, s, Features{}, "free", SelectOptions{}); err != ErrNoAvailableChannel {
		t.Fatalf("err = %v, want ErrNoAvailableChannel", err)
	}
}

func TestPick_FiltersDisabledProviderAccount(t *testing.T) {
	s := baseSnapshot()
	addAccount(s, 1, "disabled")
	addKey(s, 1, 1, 100, "active")
	addChannel(s, &catalog.Channel{ID: 1, VirtualModelID: 1, ProviderAccountID: 1, Status: "active", Priority: 0})

	if _, err := pick(t, s, Features{}, "free", SelectOptions{}); err != ErrNoAvailableChannel {
		t.Fatalf("err = %v, want ErrNoAvailableChannel", err)
	}
}

func TestPick_FiltersChannelWithNoActiveKeys(t *testing.T) {
	s := baseSnapshot()
	addAccount(s, 1, "active")
	addKey(s, 1, 1, 100, "exhausted") // 唯一的 Key 已失效
	addChannel(s, &catalog.Channel{ID: 1, VirtualModelID: 1, ProviderAccountID: 1, Status: "active", Priority: 0})

	if _, err := pick(t, s, Features{}, "free", SelectOptions{}); err != ErrNoAvailableChannel {
		t.Fatalf("err = %v, want ErrNoAvailableChannel", err)
	}
}

func TestPick_FiltersByTier(t *testing.T) {
	s := baseSnapshot()
	addAccount(s, 1, "active")
	addKey(s, 1, 1, 100, "active")
	addChannel(s, &catalog.Channel{
		ID: 1, VirtualModelID: 1, ProviderAccountID: 1, Status: "active", Priority: 0,
		AllowedTiers: []string{"enterprise"}, // 只对企业用户开放
	})

	if _, err := pick(t, s, Features{}, "free", SelectOptions{}); err != ErrNoAvailableChannel {
		t.Fatalf("free tier should be rejected: err = %v", err)
	}
	picked, err := pick(t, s, Features{}, "enterprise", SelectOptions{})
	if err != nil {
		t.Fatalf("enterprise tier should be allowed: %v", err)
	}
	if picked.Channel.ID != 1 {
		t.Errorf("picked channel = %d, want 1", picked.Channel.ID)
	}
}

func TestPick_FiltersByCapability(t *testing.T) {
	s := baseSnapshot()
	addAccount(s, 1, "active")
	addKey(s, 1, 1, 100, "active")
	// 渠道自己声明的能力集合不含 tools（覆盖虚拟模型上的 tools 能力）
	addChannel(s, &catalog.Channel{
		ID: 1, VirtualModelID: 1, ProviderAccountID: 1, Status: "active", Priority: 0,
		Capabilities: []string{"stream"},
	})

	if _, err := pick(t, s, Features{NeedTools: true}, "free", SelectOptions{}); err != ErrNoAvailableChannel {
		t.Fatalf("channel without tools capability should be filtered: err = %v", err)
	}
	if _, err := pick(t, s, Features{NeedTools: false}, "free", SelectOptions{}); err != nil {
		t.Fatalf("channel should be pickable when tools not required: %v", err)
	}
}

func TestPick_FiltersByContextWindow(t *testing.T) {
	s := baseSnapshot()
	addAccount(s, 1, "active")
	addKey(s, 1, 1, 100, "active")
	small := 1000
	addChannel(s, &catalog.Channel{
		ID: 1, VirtualModelID: 1, ProviderAccountID: 1, Status: "active", Priority: 0,
		ContextWindow: &small,
	})

	_, err := pick(t, s, Features{EstInputTokens: 900, MaxOutputTokens: 500}, "free", SelectOptions{})
	if err != ErrNoAvailableChannel {
		t.Fatalf("request exceeding channel context window should be filtered: err = %v", err)
	}
	_, err = pick(t, s, Features{EstInputTokens: 400, MaxOutputTokens: 500}, "free", SelectOptions{})
	if err != nil {
		t.Fatalf("request within context window should succeed: %v", err)
	}
}

func TestPick_ExcludeChannels(t *testing.T) {
	s := baseSnapshot()
	addAccount(s, 1, "active")
	addKey(s, 1, 1, 100, "active")
	addChannel(s, &catalog.Channel{ID: 1, VirtualModelID: 1, ProviderAccountID: 1, Status: "active", Priority: 0})

	_, err := pick(t, s, Features{}, "free", SelectOptions{ExcludeChannels: map[int64]bool{1: true}})
	if err != ErrNoAvailableChannel {
		t.Fatalf("excluded channel should not be picked: err = %v", err)
	}
}

func TestPick_ExcludeKeys_FallsBackToOtherKeyOnSameChannel(t *testing.T) {
	s := baseSnapshot()
	addAccount(s, 1, "active")
	addKey(s, 1, 1, 100, "active")
	addKey(s, 1, 2, 100, "active")
	addChannel(s, &catalog.Channel{ID: 1, VirtualModelID: 1, ProviderAccountID: 1, Status: "active", Priority: 0})

	picked, err := pick(t, s, Features{}, "free", SelectOptions{ExcludeKeys: map[int64]bool{1: true}})
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if picked.Key.ID != 2 {
		t.Fatalf("picked key = %d, want 2 (the only non-excluded key)", picked.Key.ID)
	}
}

func TestPick_ExcludeKeys_ChannelUnavailableWhenAllKeysExcluded(t *testing.T) {
	s := baseSnapshot()
	addAccount(s, 1, "active")
	addKey(s, 1, 1, 100, "active")
	addChannel(s, &catalog.Channel{ID: 1, VirtualModelID: 1, ProviderAccountID: 1, Status: "active", Priority: 0})

	_, err := pick(t, s, Features{}, "free", SelectOptions{ExcludeKeys: map[int64]bool{1: true}})
	if err != ErrNoAvailableChannel {
		t.Fatalf("err = %v, want ErrNoAvailableChannel (only key excluded)", err)
	}
}

type fakeChannelHealth struct{ open map[int64]bool }

func (f fakeChannelHealth) ChannelOpen(id int64) bool { return f.open[id] }

func TestPick_ChannelHealth_SkipsOpenBreaker(t *testing.T) {
	s := baseSnapshot()
	addAccount(s, 1, "active")
	addAccount(s, 2, "active")
	addKey(s, 1, 1, 100, "active")
	addKey(s, 2, 2, 100, "active")
	addChannel(s, &catalog.Channel{ID: 1, VirtualModelID: 1, ProviderAccountID: 1, Status: "active", Priority: 0})
	addChannel(s, &catalog.Channel{ID: 2, VirtualModelID: 1, ProviderAccountID: 2, Status: "active", Priority: 1})

	health := fakeChannelHealth{open: map[int64]bool{1: true}} // 渠道 1 熔断中
	picked, err := pick(t, s, Features{}, "free", SelectOptions{ChannelHealth: health})
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if picked.Channel.ID != 2 {
		t.Fatalf("picked channel = %d, want 2 (channel 1's breaker is open)", picked.Channel.ID)
	}
}

type fakeKeyHealth struct{ cooling map[int64]bool }

func (f fakeKeyHealth) KeyOnCooldown(_ context.Context, id int64) bool { return f.cooling[id] }

func TestPick_KeyHealth_SkipsCoolingDownKey(t *testing.T) {
	s := baseSnapshot()
	addAccount(s, 1, "active")
	addKey(s, 1, 1, 100, "active")
	addKey(s, 1, 2, 100, "active")
	addChannel(s, &catalog.Channel{ID: 1, VirtualModelID: 1, ProviderAccountID: 1, Status: "active", Priority: 0})

	health := fakeKeyHealth{cooling: map[int64]bool{1: true}}
	picked, err := pick(t, s, Features{}, "free", SelectOptions{KeyHealth: health})
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if picked.Key.ID != 2 {
		t.Fatalf("picked key = %d, want 2 (key 1 is on cooldown)", picked.Key.ID)
	}
}

// TestPick_PrefersTopPriorityLayer 验证"确定性主备"：只要最高优先级层里有可用渠道，
// 就绝不会选到较低优先级的渠道——这是相对 V1"打分取最高"最重要的行为差异。
func TestPick_PrefersTopPriorityLayer(t *testing.T) {
	s := baseSnapshot()
	addAccount(s, 1, "active")
	addAccount(s, 2, "active")
	addKey(s, 1, 1, 100, "active")
	addKey(s, 2, 2, 100, "active")
	addChannel(s, &catalog.Channel{ID: 1, VirtualModelID: 1, ProviderAccountID: 1, Status: "active", Priority: 0}) // 主
	addChannel(s, &catalog.Channel{ID: 2, VirtualModelID: 1, ProviderAccountID: 2, Status: "active", Priority: 1}) // 备

	for i := 0; i < 50; i++ {
		picked, err := pick(t, s, Features{}, "free", SelectOptions{})
		if err != nil {
			t.Fatalf("Pick: %v", err)
		}
		if picked.Channel.ID != 1 {
			t.Fatalf("picked channel %d, want always 1 (primary) while it's healthy", picked.Channel.ID)
		}
	}
}

// TestPick_FallsBackToLowerPriorityWhenPrimaryFiltered 验证主渠道不可用时会切换到备用层，
// 而不是直接报错——这是故障转移的基础，relay 层的重试循环会在每次尝试前把失败的渠道加入
// ExcludeChannels 重新调用 Pick，效果等价于这里的直接过滤。
func TestPick_FallsBackToLowerPriorityWhenPrimaryFiltered(t *testing.T) {
	s := baseSnapshot()
	addAccount(s, 1, "active")
	addAccount(s, 2, "active")
	addKey(s, 1, 1, 100, "active")
	addKey(s, 2, 2, 100, "active")
	addChannel(s, &catalog.Channel{ID: 1, VirtualModelID: 1, ProviderAccountID: 1, Status: "disabled", Priority: 0})
	addChannel(s, &catalog.Channel{ID: 2, VirtualModelID: 1, ProviderAccountID: 2, Status: "active", Priority: 1})

	picked, err := pick(t, s, Features{}, "free", SelectOptions{})
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if picked.Channel.ID != 2 {
		t.Fatalf("picked channel = %d, want 2 (fallback)", picked.Channel.ID)
	}
}

// TestPick_WeightedDistributionWithinLayer 统计检验：同层两个渠道权重 1:3，
// 大量采样后比例应接近 1:3，且绝不能出现"只选一个渠道"的羊群效应（V1 方案的问题）。
func TestPick_WeightedDistributionWithinLayer(t *testing.T) {
	s := baseSnapshot()
	addAccount(s, 1, "active")
	addAccount(s, 2, "active")
	addKey(s, 1, 1, 100, "active")
	addKey(s, 2, 2, 100, "active")
	addChannel(s, &catalog.Channel{ID: 1, VirtualModelID: 1, ProviderAccountID: 1, Status: "active", Priority: 0, Weight: 25})
	addChannel(s, &catalog.Channel{ID: 2, VirtualModelID: 1, ProviderAccountID: 2, Status: "active", Priority: 0, Weight: 75})

	const n = 4000
	counts := map[int64]int{}
	for i := 0; i < n; i++ {
		picked, err := pick(t, s, Features{}, "free", SelectOptions{})
		if err != nil {
			t.Fatalf("Pick: %v", err)
		}
		counts[picked.Channel.ID]++
	}

	if counts[1] == 0 || counts[2] == 0 {
		t.Fatalf("expected both channels to be picked at least once, got counts=%v (herd effect regression)", counts)
	}
	ratio := float64(counts[2]) / float64(counts[1])
	// 期望比例 3:1，允许较宽的统计误差（±30%）避免测试偶发失败。
	if ratio < 2.1 || ratio > 3.9 {
		t.Errorf("weight ratio 2:1 -> observed ratio %.2f (counts=%v), want close to 3.0", ratio, counts)
	}
}

// TestPick_NegativeMarginChannelIsDownweightedWithinLayer 验证毛利守护
// （技术方案 §7.16.7）真的会把流量从结构性亏钱的渠道挤走：两个渠道 weight
// 相同，其中一个标了 NegativeMargin，期望流量比例接近 10:1（降到 1/10）。
func TestPick_NegativeMarginChannelIsDownweightedWithinLayer(t *testing.T) {
	s := baseSnapshot()
	addAccount(s, 1, "active")
	addAccount(s, 2, "active")
	addKey(s, 1, 1, 100, "active")
	addKey(s, 2, 2, 100, "active")
	addChannel(s, &catalog.Channel{ID: 1, VirtualModelID: 1, ProviderAccountID: 1, Status: "active", Priority: 0, Weight: 100})
	addChannel(s, &catalog.Channel{ID: 2, VirtualModelID: 1, ProviderAccountID: 2, Status: "active", Priority: 0, Weight: 100, NegativeMargin: true})

	const n = 4000
	counts := map[int64]int{}
	for i := 0; i < n; i++ {
		picked, err := pick(t, s, Features{}, "free", SelectOptions{})
		if err != nil {
			t.Fatalf("Pick: %v", err)
		}
		counts[picked.Channel.ID]++
	}

	if counts[2] == 0 {
		t.Fatal("the negative-margin channel should still be selectable sometimes, not fully excluded")
	}
	ratio := float64(counts[1]) / float64(counts[2])
	// 期望比例 10:1，给统计误差留宽松空间。
	if ratio < 6 || ratio > 15 {
		t.Errorf("healthy:negative-margin ratio = %.2f (counts=%v), want close to 10.0", ratio, counts)
	}
}

// TestPick_NegativeMarginChannelStillSelectableWhenOnlyOption 验证降权不是硬性
// 排除：即便是这一层唯一的候选，毛利为负的渠道依然会被选中（有效权重下限是
// 1，不会因为除法向下取整变成 0 从而被 rand.IntN 排除出可选范围）。
func TestPick_NegativeMarginChannelStillSelectableWhenOnlyOption(t *testing.T) {
	s := baseSnapshot()
	addAccount(s, 1, "active")
	addKey(s, 1, 1, 100, "active")
	addChannel(s, &catalog.Channel{ID: 1, VirtualModelID: 1, ProviderAccountID: 1, Status: "active", Priority: 0, Weight: 5, NegativeMargin: true})

	picked, err := pick(t, s, Features{}, "free", SelectOptions{})
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if picked.Channel.ID != 1 {
		t.Errorf("Channel.ID = %d, want 1", picked.Channel.ID)
	}
}

func TestPick_WeightedKeySelectionWithinChannel(t *testing.T) {
	s := baseSnapshot()
	addAccount(s, 1, "active")
	addKey(s, 1, 1, 10, "active")
	addKey(s, 1, 2, 90, "active")
	addChannel(s, &catalog.Channel{ID: 1, VirtualModelID: 1, ProviderAccountID: 1, Status: "active", Priority: 0})

	const n = 2000
	counts := map[int64]int{}
	for i := 0; i < n; i++ {
		picked, err := pick(t, s, Features{}, "free", SelectOptions{})
		if err != nil {
			t.Fatalf("Pick: %v", err)
		}
		counts[picked.Key.ID]++
	}
	if counts[1] == 0 || counts[2] == 0 {
		t.Fatalf("expected both keys to be used, got counts=%v", counts)
	}
	if counts[2] <= counts[1] {
		t.Errorf("key 2 has 9x the weight of key 1 but got fewer or equal picks: %v", counts)
	}
}
