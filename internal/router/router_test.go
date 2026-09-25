package router

import (
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

func TestPick_NoChannelsReturnsError(t *testing.T) {
	s := baseSnapshot()
	if _, err := Pick(s, vm(s), Features{}, "free", nil); err != ErrNoAvailableChannel {
		t.Fatalf("err = %v, want ErrNoAvailableChannel", err)
	}
}

func TestPick_FiltersDisabledChannel(t *testing.T) {
	s := baseSnapshot()
	addAccount(s, 1, "active")
	addKey(s, 1, 1, 100, "active")
	addChannel(s, &catalog.Channel{ID: 1, VirtualModelID: 1, ProviderAccountID: 1, Status: "disabled", Priority: 0})

	if _, err := Pick(s, vm(s), Features{}, "free", nil); err != ErrNoAvailableChannel {
		t.Fatalf("err = %v, want ErrNoAvailableChannel", err)
	}
}

func TestPick_FiltersDisabledProviderAccount(t *testing.T) {
	s := baseSnapshot()
	addAccount(s, 1, "disabled")
	addKey(s, 1, 1, 100, "active")
	addChannel(s, &catalog.Channel{ID: 1, VirtualModelID: 1, ProviderAccountID: 1, Status: "active", Priority: 0})

	if _, err := Pick(s, vm(s), Features{}, "free", nil); err != ErrNoAvailableChannel {
		t.Fatalf("err = %v, want ErrNoAvailableChannel", err)
	}
}

func TestPick_FiltersChannelWithNoActiveKeys(t *testing.T) {
	s := baseSnapshot()
	addAccount(s, 1, "active")
	addKey(s, 1, 1, 100, "exhausted") // 唯一的 Key 已失效
	addChannel(s, &catalog.Channel{ID: 1, VirtualModelID: 1, ProviderAccountID: 1, Status: "active", Priority: 0})

	if _, err := Pick(s, vm(s), Features{}, "free", nil); err != ErrNoAvailableChannel {
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

	if _, err := Pick(s, vm(s), Features{}, "free", nil); err != ErrNoAvailableChannel {
		t.Fatalf("free tier should be rejected: err = %v", err)
	}
	picked, err := Pick(s, vm(s), Features{}, "enterprise", nil)
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

	if _, err := Pick(s, vm(s), Features{NeedTools: true}, "free", nil); err != ErrNoAvailableChannel {
		t.Fatalf("channel without tools capability should be filtered: err = %v", err)
	}
	if _, err := Pick(s, vm(s), Features{NeedTools: false}, "free", nil); err != nil {
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

	_, err := Pick(s, vm(s), Features{EstInputTokens: 900, MaxOutputTokens: 500}, "free", nil)
	if err != ErrNoAvailableChannel {
		t.Fatalf("request exceeding channel context window should be filtered: err = %v", err)
	}
	_, err = Pick(s, vm(s), Features{EstInputTokens: 400, MaxOutputTokens: 500}, "free", nil)
	if err != nil {
		t.Fatalf("request within context window should succeed: %v", err)
	}
}

func TestPick_ExcludeSet(t *testing.T) {
	s := baseSnapshot()
	addAccount(s, 1, "active")
	addKey(s, 1, 1, 100, "active")
	addChannel(s, &catalog.Channel{ID: 1, VirtualModelID: 1, ProviderAccountID: 1, Status: "active", Priority: 0})

	_, err := Pick(s, vm(s), Features{}, "free", map[int64]bool{1: true})
	if err != ErrNoAvailableChannel {
		t.Fatalf("excluded channel should not be picked: err = %v", err)
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
		picked, err := Pick(s, vm(s), Features{}, "free", nil)
		if err != nil {
			t.Fatalf("Pick: %v", err)
		}
		if picked.Channel.ID != 1 {
			t.Fatalf("picked channel %d, want always 1 (primary) while it's healthy", picked.Channel.ID)
		}
	}
}

// TestPick_FallsBackToLowerPriorityWhenPrimaryFiltered 验证主渠道不可用时会切换到备用层，
// 而不是直接报错——这是故障转移的基础（完整的重试循环在 relay 层，见 §7.7 后续阶段）。
func TestPick_FallsBackToLowerPriorityWhenPrimaryFiltered(t *testing.T) {
	s := baseSnapshot()
	addAccount(s, 1, "active")
	addAccount(s, 2, "active")
	addKey(s, 1, 1, 100, "active")
	addKey(s, 2, 2, 100, "active")
	addChannel(s, &catalog.Channel{ID: 1, VirtualModelID: 1, ProviderAccountID: 1, Status: "disabled", Priority: 0})
	addChannel(s, &catalog.Channel{ID: 2, VirtualModelID: 1, ProviderAccountID: 2, Status: "active", Priority: 1})

	picked, err := Pick(s, vm(s), Features{}, "free", nil)
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
		picked, err := Pick(s, vm(s), Features{}, "free", nil)
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

func TestPick_WeightedKeySelectionWithinChannel(t *testing.T) {
	s := baseSnapshot()
	addAccount(s, 1, "active")
	addKey(s, 1, 1, 10, "active")
	addKey(s, 1, 2, 90, "active")
	addChannel(s, &catalog.Channel{ID: 1, VirtualModelID: 1, ProviderAccountID: 1, Status: "active", Priority: 0})

	const n = 2000
	counts := map[int64]int{}
	for i := 0; i < n; i++ {
		picked, err := Pick(s, vm(s), Features{}, "free", nil)
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
