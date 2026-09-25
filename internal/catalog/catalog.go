// Package catalog 提供路由/计费所需的配置快照：虚拟模型、渠道、上游账号、
// 已解密的上游 Key、售价表。技术方案 §7.3 设计的是"LISTEN/NOTIFY + 全量热加载"，
// 本阶段先实现一个更简单的版本——带 TTL 的定时重新加载，避免每个请求都查库，
// 同时不引入 LISTEN 连接管理的复杂度；换成热加载时只需要替换 Store.Get 的实现，
// Snapshot 的结构与调用方（router/relay）不需要变。
package catalog

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
	"github.com/WALLE-AI/uFreeTokens/internal/secretbox"
)

type VirtualModel struct {
	ID            int64
	Name          string
	Family        string
	Type          string
	ContextWindow int
	MaxOutput     int
	Capabilities  []string
	VisibleTiers  []string
	Status        string
}

// HasCapability 判断该虚拟模型是否声明了某能力（tools/vision/json_schema/stream 等）。
func (m *VirtualModel) HasCapability(c string) bool {
	for _, x := range m.Capabilities {
		if x == c {
			return true
		}
	}
	return false
}

type ProviderAccount struct {
	ID             int64
	ProviderID     int64
	ProviderCode   string
	Protocol       string // openai / anthropic / gemini
	Name           string
	BaseURL        string
	CostMultiplier decimal.Decimal
	Status         string
}

type ProviderKey struct {
	ID                int64
	ProviderAccountID int64
	Secret            string // 已解密的明文，仅存在于内存快照中
	Weight            int
	Status            string
}

type Channel struct {
	ID                int64
	VirtualModelID    int64
	ProviderAccountID int64
	UpstreamModel     string
	Priority          int
	Weight            int
	Capabilities      []string // nil = 继承虚拟模型
	ContextWindow     *int     // nil = 继承虚拟模型
	ParamOverrides    map[string]any
	AllowedTiers      []string // nil = 不限制
	Status            string
}

// EffectiveCapabilities 返回该渠道实际生效的能力集合（渠道未声明则继承虚拟模型）。
func (c *Channel) EffectiveCapabilities(vm *VirtualModel) []string {
	if c.Capabilities != nil {
		return c.Capabilities
	}
	return vm.Capabilities
}

// EffectiveContextWindow 同上，渠道可能比官方声明的上下文更小（如某些转售渠道限流）。
func (c *Channel) EffectiveContextWindow(vm *VirtualModel) int {
	if c.ContextWindow != nil {
		return *c.ContextWindow
	}
	return vm.ContextWindow
}

// AllowsTier 判断该渠道是否对指定用户分组可见；AllowedTiers 为空表示不限制。
func (c *Channel) AllowsTier(tier string) bool {
	if len(c.AllowedTiers) == 0 {
		return true
	}
	for _, t := range c.AllowedTiers {
		if t == tier {
			return true
		}
	}
	return false
}

// Snapshot 是某一时刻的全量配置视图，路由/计费只读它，不直接查库。
type Snapshot struct {
	LoadedAt         time.Time
	Models           map[string]*VirtualModel // by name
	ChannelsByVM     map[int64][]*Channel
	ProviderAccounts map[int64]*ProviderAccount
	KeysByAccount    map[int64][]*ProviderKey
	// SellPriceBooks 按 virtual_model_id 索引，取当前生效的最新版本。
	// 简化：暂不区分 tier（全部按默认价），tier 差异化售价留待促销引擎阶段补齐。
	SellPriceBooks map[int64]pricing.Book
}

// Store 负责从数据库加载 Snapshot 并做简单的 TTL 缓存。
type Store struct {
	pool *pgxpool.Pool
	box  *secretbox.Box
	ttl  time.Duration

	mu       sync.RWMutex
	cached   *Snapshot
	cachedAt time.Time
}

func NewStore(pool *pgxpool.Pool, box *secretbox.Box, ttl time.Duration) *Store {
	return &Store{pool: pool, box: box, ttl: ttl}
}

// Get 返回缓存中的快照；超过 TTL 会同步重新加载一次
// （并发下可能有多个 goroutine 同时重载，属于可接受的重复工作，
// 优先保证实现简单；高 QPS 场景应换成 §7.3 的后台异步刷新 + atomic.Pointer）。
func (s *Store) Get(ctx context.Context) (*Snapshot, error) {
	s.mu.RLock()
	if s.cached != nil && time.Since(s.cachedAt) < s.ttl {
		snap := s.cached
		s.mu.RUnlock()
		return snap, nil
	}
	s.mu.RUnlock()

	snap, err := s.load(ctx)
	if err != nil {
		s.mu.RLock()
		stale := s.cached
		s.mu.RUnlock()
		if stale != nil {
			// 加载失败时宁可用旧快照兜底，也不要让所有请求跟着报错
			// （数据库短暂抖动不应该导致网关整体不可用）。
			return stale, nil
		}
		return nil, err
	}

	s.mu.Lock()
	s.cached, s.cachedAt = snap, time.Now()
	s.mu.Unlock()
	return snap, nil
}

func (s *Store) load(ctx context.Context) (*Snapshot, error) {
	snap := &Snapshot{
		LoadedAt:         time.Now(),
		Models:           map[string]*VirtualModel{},
		ChannelsByVM:     map[int64][]*Channel{},
		ProviderAccounts: map[int64]*ProviderAccount{},
		KeysByAccount:    map[int64][]*ProviderKey{},
		SellPriceBooks:   map[int64]pricing.Book{},
	}

	if err := s.loadModels(ctx, snap); err != nil {
		return nil, err
	}
	if err := s.loadProviderAccounts(ctx, snap); err != nil {
		return nil, err
	}
	if err := s.loadProviderKeys(ctx, snap); err != nil {
		return nil, err
	}
	if err := s.loadChannels(ctx, snap); err != nil {
		return nil, err
	}
	if err := s.loadSellPrices(ctx, snap); err != nil {
		return nil, err
	}
	return snap, nil
}

func (s *Store) loadModels(ctx context.Context, snap *Snapshot) error {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, family, type, context_window, max_output, capabilities, visible_tiers, status
		 FROM virtual_models WHERE status = 'active'`)
	if err != nil {
		return fmt.Errorf("catalog: load virtual_models: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		m := &VirtualModel{}
		if err := rows.Scan(&m.ID, &m.Name, &m.Family, &m.Type, &m.ContextWindow, &m.MaxOutput,
			&m.Capabilities, &m.VisibleTiers, &m.Status); err != nil {
			return fmt.Errorf("catalog: scan virtual_model: %w", err)
		}
		snap.Models[m.Name] = m
	}
	return rows.Err()
}

func (s *Store) loadProviderAccounts(ctx context.Context, snap *Snapshot) error {
	rows, err := s.pool.Query(ctx,
		`SELECT pa.id, pa.provider_id, p.code, p.protocol, pa.name, pa.base_url, pa.cost_multiplier, pa.status
		 FROM provider_accounts pa JOIN providers p ON p.id = pa.provider_id
		 WHERE pa.status = 'active' AND p.status = 'active'`)
	if err != nil {
		return fmt.Errorf("catalog: load provider_accounts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		a := &ProviderAccount{}
		if err := rows.Scan(&a.ID, &a.ProviderID, &a.ProviderCode, &a.Protocol, &a.Name, &a.BaseURL,
			&a.CostMultiplier, &a.Status); err != nil {
			return fmt.Errorf("catalog: scan provider_account: %w", err)
		}
		snap.ProviderAccounts[a.ID] = a
	}
	return rows.Err()
}

func (s *Store) loadProviderKeys(ctx context.Context, snap *Snapshot) error {
	rows, err := s.pool.Query(ctx,
		`SELECT id, provider_account_id, secret_ciphertext, secret_dek_wrapped, weight, status
		 FROM provider_keys WHERE status = 'active'`)
	if err != nil {
		return fmt.Errorf("catalog: load provider_keys: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id, accountID   int64
			ciphertext, dek []byte
			weight          int
			status          string
		)
		if err := rows.Scan(&id, &accountID, &ciphertext, &dek, &weight, &status); err != nil {
			return fmt.Errorf("catalog: scan provider_key: %w", err)
		}
		secret, err := s.box.Open(&secretbox.Sealed{Ciphertext: ciphertext, WrappedDEK: dek})
		if err != nil {
			// 解密失败通常意味着 KEK 配置错误：跳过这条 Key 并继续（不要让一条坏数据
			// 拖垮整个快照加载），由调用方通过日志/监控发现。
			continue
		}
		snap.KeysByAccount[accountID] = append(snap.KeysByAccount[accountID], &ProviderKey{
			ID: id, ProviderAccountID: accountID, Secret: secret, Weight: weight, Status: status,
		})
	}
	return rows.Err()
}

func (s *Store) loadChannels(ctx context.Context, snap *Snapshot) error {
	rows, err := s.pool.Query(ctx,
		`SELECT id, virtual_model_id, provider_account_id, upstream_model, priority, weight,
		        capabilities, context_window, param_overrides, allowed_tiers, status
		 FROM channels WHERE status = 'active'`)
	if err != nil {
		return fmt.Errorf("catalog: load channels: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		c := &Channel{}
		if err := rows.Scan(&c.ID, &c.VirtualModelID, &c.ProviderAccountID, &c.UpstreamModel,
			&c.Priority, &c.Weight, &c.Capabilities, &c.ContextWindow, &c.ParamOverrides,
			&c.AllowedTiers, &c.Status); err != nil {
			return fmt.Errorf("catalog: scan channel: %w", err)
		}
		snap.ChannelsByVM[c.VirtualModelID] = append(snap.ChannelsByVM[c.VirtualModelID], c)
	}
	return rows.Err()
}

func (s *Store) loadSellPrices(ctx context.Context, snap *Snapshot) error {
	// 每个虚拟模型取当前生效、effective_from 最新的一个 sell price_book。
	rows, err := s.pool.Query(ctx,
		`SELECT DISTINCT ON (pb.virtual_model_id)
		        pb.id, pb.virtual_model_id
		 FROM price_books pb
		 WHERE pb.kind = 'sell' AND pb.effective_from <= now()
		   AND (pb.effective_to IS NULL OR pb.effective_to > now())
		 ORDER BY pb.virtual_model_id, pb.effective_from DESC`)
	if err != nil {
		return fmt.Errorf("catalog: load sell price_books: %w", err)
	}
	bookIDs := map[int64]int64{} // bookID -> virtualModelID
	for rows.Next() {
		var bookID, vmID int64
		if err := rows.Scan(&bookID, &vmID); err != nil {
			rows.Close()
			return fmt.Errorf("catalog: scan price_book: %w", err)
		}
		bookIDs[bookID] = vmID
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(bookIDs) == 0 {
		return nil
	}

	ids := make([]int64, 0, len(bookIDs))
	for id := range bookIDs {
		ids = append(ids, id)
	}

	crows, err := s.pool.Query(ctx,
		`SELECT price_book_id, meter, unit, service_tier, tier_min_input, tier_max_input,
		        window_start_min, window_end_min, unit_price
		 FROM price_components WHERE price_book_id = ANY($1)`, ids)
	if err != nil {
		return fmt.Errorf("catalog: load price_components: %w", err)
	}
	defer crows.Close()

	books := map[int64]pricing.Book{}
	for crows.Next() {
		var (
			bookID                 int64
			meter, unit, tier      string
			tierMinInput           int
			tierMaxInput           *int
			windowStart, windowEnd *int16
			unitPrice              decimal.Decimal
		)
		if err := crows.Scan(&bookID, &meter, &unit, &tier, &tierMinInput, &tierMaxInput,
			&windowStart, &windowEnd, &unitPrice); err != nil {
			return fmt.Errorf("catalog: scan price_component: %w", err)
		}
		vmID := bookIDs[bookID]
		b := books[vmID]
		b.ID = bookID
		b.Components = append(b.Components, pricing.Component{
			Meter: pricing.Meter(meter), Unit: pricing.Unit(unit), ServiceTier: tier,
			TierMinInput: tierMinInput, TierMaxInput: tierMaxInput,
			WindowStartMin: windowStart, WindowEndMin: windowEnd, UnitPrice: unitPrice,
		})
		books[vmID] = b
	}
	if err := crows.Err(); err != nil {
		return err
	}

	snap.SellPriceBooks = books
	return nil
}
