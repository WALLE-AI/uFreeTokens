// Package catalog 提供路由/计费所需的配置快照：虚拟模型、渠道、上游账号、
// 已解密的上游 Key、售价表。技术方案 §7.3 设计的是"LISTEN/NOTIFY + 全量热加载"，
// 本阶段先实现一个更简单的版本——带 TTL 的定时重新加载，避免每个请求都查库，
// 同时不引入 LISTEN 连接管理的复杂度；换成热加载时只需要替换 Store.Get 的实现，
// Snapshot 的结构与调用方（router/relay）不需要变。
package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/dialect"
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
	// Metadata 是运营在 virtual_model_metadata 表里维护的展示层信息（技术方案
	// 迭代5：GET /v1/catalog 公开目录）。nil 表示运营还没有为这个模型录入过
	// 元数据；路由/计费逻辑完全不关心这个字段，只有公开目录接口读它。
	Metadata *VirtualModelMetadata
}

// VirtualModelMetadata 对应 virtual_model_metadata 表的一行（运营录入，
// 不是订阅上游拿到的数据）。Scores 的键名由 admin.ValidateScores 在写入时
// 按白名单校验（intelligence_index/coding_index/agentic_index/design_arena.*，
// 见基准测试与排行榜方案 §3.1）；这里读出后原样透传。
type VirtualModelMetadata struct {
	DisplayName     string
	Description     string
	ProviderDisplay string
	Tags            []string
	Scores          map[string]any
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
	// Dialect 是合并后的供应商方言（provider_accounts.extra.dialect，可引用内置预设，
	// 见 internal/dialect）；nil = 没有方言，按协议原样透传。
	Dialect *dialect.Dialect
	// DialectError 非空表示方言配置无法解析：账号被视为不可用（Status 置为
	// dialect_error，路由不会选它的渠道），而不是带着错误的方言继续转发。
	DialectError string
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
	// NegativeMargin 标记这个渠道当前是否至少有一个计量项在挂牌价下毛利为负
	// （sell_unit_price < cost_unit_price × fx_rate × cost_multiplier，技术方案
	// §7.16.7 毛利守护）。每次快照重建时用 SellPriceBooks/CostPriceBooks/FXRates
	// 重算，不需要单独的调度器。router 用它给同优先级内的渠道降权（不是硬性
	// 排除——没有替代渠道时仍然可用，只是被挤到更少的流量），不判断"没配成本价"
	// 或"成本价币种没有汇率"为负毛利：数据不全不等于亏钱，见 loadCostPrices 的
	// 同款原则。
	NegativeMargin bool
	// ExperimentKey/VariantLabel 是 A/B 路由的分组标签（Phase 3）：两者要么都是
	// nil（不参与实验），要么都非 nil（DB 有 CHECK 约束保证成对出现）。分流本身
	// 复用现有的 priority/weight 加权随机（§7.5），这两个字段只用来在
	// request_logs 里记录"这次请求实际走了哪个分组"，供事后按分组聚合对比
	// 成本/延迟/成功率——不引入一套平行的路由机制。
	ExperimentKey *string
	VariantLabel  *string
	// AllowedAccountIDs 是专属渠道的账户白名单（Phase 4 企业专属渠道）：
	// nil/空 = 不限制，非空则只有列在里面的账户能路由到这个渠道——
	// 语义和 AllowedTiers 完全对称，只是维度从"用户分组"换成"具体账户"，用来
	// 给企业客户配独享的路由/配额池，不跟公共流量混在一起。
	AllowedAccountIDs []int64
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

// AllowsAccount 判断该渠道是否对指定账户可见；AllowedAccountIDs 为空表示
// 不限制（公共渠道，谁都能路由到）。
func (c *Channel) AllowsAccount(accountID int64) bool {
	if len(c.AllowedAccountIDs) == 0 {
		return true
	}
	for _, id := range c.AllowedAccountIDs {
		if id == accountID {
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
	// CostPriceBooks 按 channel_id 索引，取当前生效的最新版本，用于计算
	// request_logs.cost_amount（毛利可见性、§7.16 毛利守护的前置数据）。
	// currency='CNY' 的成本价直接使用；非 CNY 的成本价需要 FXRates 里有对应汇率
	// 才能折算成 CNY——没有汇率时 relay 会跳过成本记录（不影响用户计费，只是少
	// 一条毛利可见性数据），而不是假设汇率为 1。
	CostPriceBooks map[int64]pricing.Book
	// FXRates 按原始币种（如 "USD"）索引，值是"1 单位该币种 = 多少 CNY"
	// （技术方案 §7.16.9）。取每个币种 effective_date 最新的一条（不含未来生效的）。
	// 没有汇率数据的币种不在这个 map 里。
	FXRates map[string]decimal.Decimal
	// DeprecatedModels 是 status='deprecated' 的虚拟模型（by name），只供
	// GET /v1/catalog 展示用（配合前端"Show deprecated"筛选项）。故意不放进
	// Models：router/relay 只认 Models，这样已下架模型永远不会被重新路由到，
	// 即使运营在公开目录里把它标成"可见"。'hidden' 状态的模型两个 map 都不进——
	// 那是主动隐藏，不应该出现在任何公开响应里。
	DeprecatedModels map[string]*VirtualModel
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
		CostPriceBooks:   map[int64]pricing.Book{},
		FXRates:          map[string]decimal.Decimal{},
		DeprecatedModels: map[string]*VirtualModel{},
	}

	if err := s.loadModels(ctx, snap); err != nil {
		return nil, err
	}
	if err := s.loadDeprecatedModels(ctx, snap); err != nil {
		return nil, err
	}
	if err := s.loadMetadata(ctx, snap); err != nil {
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
	if err := s.loadCostPrices(ctx, snap); err != nil {
		return nil, err
	}
	if err := s.loadFXRates(ctx, snap); err != nil {
		return nil, err
	}
	computeNegativeMargins(snap)
	return snap, nil
}

// computeNegativeMargins 给每个渠道打上 NegativeMargin 标记（技术方案 §7.16.7
// 毛利守护）：纯内存计算，复用已经加载好的 SellPriceBooks/CostPriceBooks/
// FXRates，不需要额外查库、也不需要单独的调度器——每次快照刷新（默认 TTL
// 10 秒）自动重新评估。这是"挂牌价结构性毛利"，不是按近 7 天实际用量加权的
// "典型毛利"（那个需要解析 request_logs 历史用量，属于更大的工作量，留作后续）。
func computeNegativeMargins(snap *Snapshot) {
	for _, channels := range snap.ChannelsByVM {
		for _, ch := range channels {
			sellBook, ok := snap.SellPriceBooks[ch.VirtualModelID]
			if !ok {
				continue
			}
			costBook, ok := snap.CostPriceBooks[ch.ID]
			if !ok {
				continue
			}
			account, ok := snap.ProviderAccounts[ch.ProviderAccountID]
			if !ok {
				continue
			}
			fxRate := decimal.NewFromInt(1)
			if costBook.Currency != "" && costBook.Currency != "CNY" {
				rate, ok := snap.FXRates[costBook.Currency]
				if !ok {
					continue // 换算不了，不评判（缺数据不等于亏钱）
				}
				fxRate = rate
			}
			ch.NegativeMargin = hasNegativeMargin(sellBook, costBook, fxRate, account.CostMultiplier)
		}
	}
}

func marginMeterKey(meter pricing.Meter, tier string) string {
	if tier == "" {
		tier = "default"
	}
	return string(meter) + "|" + tier
}

// hasNegativeMargin 判断是否存在至少一个计量项：售价 < 成本价 × 汇率 × 合同折扣。
// 只比较售价和成本价都配置了的计量项——一侧没配的计量项没有可比较的对象，
// 不参与判断。
func hasNegativeMargin(sell, cost pricing.Book, fxRate, costMultiplier decimal.Decimal) bool {
	costByMeter := make(map[string]decimal.Decimal, len(cost.Components))
	for _, c := range cost.Components {
		costByMeter[marginMeterKey(c.Meter, c.ServiceTier)] = c.UnitPrice.Mul(fxRate).Mul(costMultiplier)
	}
	for _, s := range sell.Components {
		costPrice, ok := costByMeter[marginMeterKey(s.Meter, s.ServiceTier)]
		if ok && s.UnitPrice.LessThan(costPrice) {
			return true
		}
	}
	return false
}

// loadFXRates 加载每个 base 币种当前生效（effective_date <= 今天）的最新汇率，
// quote 固定为 'CNY'（平台以人民币结算，技术方案 §7.16.9）。
func (s *Store) loadFXRates(ctx context.Context, snap *Snapshot) error {
	rows, err := s.pool.Query(ctx,
		`SELECT DISTINCT ON (base) base, rate
		 FROM fx_rates
		 WHERE quote = 'CNY' AND effective_date <= CURRENT_DATE
		 ORDER BY base, effective_date DESC`)
	if err != nil {
		return fmt.Errorf("catalog: query fx_rates: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var base string
		var rate decimal.Decimal
		if err := rows.Scan(&base, &rate); err != nil {
			return fmt.Errorf("catalog: scan fx_rate: %w", err)
		}
		snap.FXRates[base] = rate
	}
	return rows.Err()
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

// loadDeprecatedModels 单独加载 status='deprecated' 的虚拟模型，只放进
// snap.DeprecatedModels（不进 snap.Models，见该字段注释——router/relay 不会
// 看到它们，这里只是为了 GET /v1/catalog 能把它们标注出来给前端筛选用）。
func (s *Store) loadDeprecatedModels(ctx context.Context, snap *Snapshot) error {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, family, type, context_window, max_output, capabilities, visible_tiers, status
		 FROM virtual_models WHERE status = 'deprecated'`)
	if err != nil {
		return fmt.Errorf("catalog: load deprecated virtual_models: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		m := &VirtualModel{}
		if err := rows.Scan(&m.ID, &m.Name, &m.Family, &m.Type, &m.ContextWindow, &m.MaxOutput,
			&m.Capabilities, &m.VisibleTiers, &m.Status); err != nil {
			return fmt.Errorf("catalog: scan deprecated virtual_model: %w", err)
		}
		snap.DeprecatedModels[m.Name] = m
	}
	return rows.Err()
}

// loadMetadata 用虚拟模型名字关联 virtual_model_metadata（LEFT JOIN 的效果
// 通过"找不到就跳过"实现，而不是真的写 SQL LEFT JOIN——loadModels/
// loadDeprecatedModels 已经把 active + deprecated 虚拟模型都加载进
// snap.Models/snap.DeprecatedModels，这里只需要把有元数据的那些补上
// Metadata 字段）。元数据行对应的模型如果是 'hidden'（两个 map 都没有），
// 直接跳过：元数据是纯展示层数据，没有宿主模型时没有意义。
func (s *Store) loadMetadata(ctx context.Context, snap *Snapshot) error {
	rows, err := s.pool.Query(ctx,
		`SELECT vm.name, m.display_name, m.description, m.provider_display, m.tags, m.scores
		 FROM virtual_model_metadata m JOIN virtual_models vm ON vm.id = m.virtual_model_id`)
	if err != nil {
		return fmt.Errorf("catalog: load virtual_model_metadata: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			name                                      string
			displayName, description, providerDisplay *string
			tags                                      []string
			scoresRaw                                 []byte
		)
		if err := rows.Scan(&name, &displayName, &description, &providerDisplay, &tags, &scoresRaw); err != nil {
			return fmt.Errorf("catalog: scan virtual_model_metadata: %w", err)
		}
		vm, ok := snap.Models[name]
		if !ok {
			vm, ok = snap.DeprecatedModels[name]
		}
		if !ok {
			continue
		}
		meta := &VirtualModelMetadata{Tags: tags}
		if displayName != nil {
			meta.DisplayName = *displayName
		}
		if description != nil {
			meta.Description = *description
		}
		if providerDisplay != nil {
			meta.ProviderDisplay = *providerDisplay
		}
		if len(scoresRaw) > 0 {
			_ = json.Unmarshal(scoresRaw, &meta.Scores)
		}
		vm.Metadata = meta
	}
	return rows.Err()
}

func (s *Store) loadProviderAccounts(ctx context.Context, snap *Snapshot) error {
	rows, err := s.pool.Query(ctx,
		`SELECT pa.id, pa.provider_id, p.code, p.protocol, pa.name, pa.base_url, pa.cost_multiplier, pa.status, pa.extra->'dialect'
		 FROM provider_accounts pa JOIN providers p ON p.id = pa.provider_id
		 WHERE pa.status = 'active' AND p.status = 'active'`)
	if err != nil {
		return fmt.Errorf("catalog: load provider_accounts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		a := &ProviderAccount{}
		var dialectRaw []byte
		if err := rows.Scan(&a.ID, &a.ProviderID, &a.ProviderCode, &a.Protocol, &a.Name, &a.BaseURL,
			&a.CostMultiplier, &a.Status, &dialectRaw); err != nil {
			return fmt.Errorf("catalog: scan provider_account: %w", err)
		}
		d, err := dialect.Load("", dialectRaw)
		if err != nil {
			a.DialectError, a.Status = err.Error(), "dialect_error"
		}
		a.Dialect = d
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
		        capabilities, context_window, param_overrides, allowed_tiers, status,
		        experiment_key, variant_label, allowed_account_ids
		 FROM channels WHERE status = 'active'`)
	if err != nil {
		return fmt.Errorf("catalog: load channels: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		c := &Channel{}
		if err := rows.Scan(&c.ID, &c.VirtualModelID, &c.ProviderAccountID, &c.UpstreamModel,
			&c.Priority, &c.Weight, &c.Capabilities, &c.ContextWindow, &c.ParamOverrides,
			&c.AllowedTiers, &c.Status, &c.ExperimentKey, &c.VariantLabel, &c.AllowedAccountIDs); err != nil {
			return fmt.Errorf("catalog: scan channel: %w", err)
		}
		snap.ChannelsByVM[c.VirtualModelID] = append(snap.ChannelsByVM[c.VirtualModelID], c)
	}
	return rows.Err()
}

func (s *Store) loadSellPrices(ctx context.Context, snap *Snapshot) error {
	books, err := s.loadLatestPriceBooks(ctx, "sell", "virtual_model_id")
	if err != nil {
		return fmt.Errorf("catalog: load sell prices: %w", err)
	}
	snap.SellPriceBooks = books
	return nil
}

// loadCostPrices 加载每个渠道当前生效的成本价。只保留 currency='CNY' 的版本
// （见 Snapshot.CostPriceBooks 的注释）；非 CNY 的成本价会被加载但调用方
// （internal/relay）在使用前还要再检查一次 Currency 字段，这里不静默丢弃，
// 是为了让 catalog 的行为对所有价格版本保持一致、可预测。
func (s *Store) loadCostPrices(ctx context.Context, snap *Snapshot) error {
	books, err := s.loadLatestPriceBooks(ctx, "cost", "channel_id")
	if err != nil {
		return fmt.Errorf("catalog: load cost prices: %w", err)
	}
	snap.CostPriceBooks = books
	return nil
}

// loadLatestPriceBooks 是 loadSellPrices/loadCostPrices 共用的实现：按 keyColumn
// （virtual_model_id 或 channel_id）分组，取每组里 effective_from 最新、当前生效的
// 一个 price_book 及其全部 price_components。
func (s *Store) loadLatestPriceBooks(ctx context.Context, kind, keyColumn string) (map[int64]pricing.Book, error) {
	rows, err := s.pool.Query(ctx, fmt.Sprintf(
		`SELECT DISTINCT ON (pb.%s)
		        pb.id, pb.%s, pb.currency
		 FROM price_books pb
		 WHERE pb.kind = $1 AND pb.effective_from <= now()
		   AND (pb.effective_to IS NULL OR pb.effective_to > now())
		 ORDER BY pb.%s, pb.effective_from DESC`, keyColumn, keyColumn, keyColumn),
		kind)
	if err != nil {
		return nil, fmt.Errorf("query price_books: %w", err)
	}
	type bookMeta struct {
		key      int64
		currency string
	}
	metaByBookID := map[int64]bookMeta{}
	for rows.Next() {
		var bookID, key int64
		var currency string
		if err := rows.Scan(&bookID, &key, &currency); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan price_book: %w", err)
		}
		metaByBookID[bookID] = bookMeta{key: key, currency: currency}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(metaByBookID) == 0 {
		return map[int64]pricing.Book{}, nil
	}

	ids := make([]int64, 0, len(metaByBookID))
	for id := range metaByBookID {
		ids = append(ids, id)
	}

	crows, err := s.pool.Query(ctx,
		`SELECT price_book_id, meter, unit, service_tier, tier_min_input, tier_max_input,
		        window_start_min, window_end_min, unit_price
		 FROM price_components WHERE price_book_id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("query price_components: %w", err)
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
			return nil, fmt.Errorf("scan price_component: %w", err)
		}
		meta := metaByBookID[bookID]
		b := books[meta.key]
		b.ID = bookID
		b.Currency = meta.currency
		b.Components = append(b.Components, pricing.Component{
			Meter: pricing.Meter(meter), Unit: pricing.Unit(unit), ServiceTier: tier,
			TierMinInput: tierMinInput, TierMaxInput: tierMaxInput,
			WindowStartMin: windowStart, WindowEndMin: windowEnd, UnitPrice: unitPrice,
		})
		books[meta.key] = b
	}
	if err := crows.Err(); err != nil {
		return nil, err
	}
	return books, nil
}

// AuthorOf 返回虚拟模型名里 '/' 之前的部分，作为"模型作者/厂商"维度（公开排行榜的
// 市场份额、作者归类，见基准测试与排行榜方案 §4.2）。没有 '/' 时整个名字就是作者。
// 与 frontend/web 的 modelFromCatalog 推导 provider 的规则一致；Go 侧只在这里写一处，
// 物化任务和公开接口共用。
func AuthorOf(virtualModel string) string {
	if i := strings.Index(virtualModel, "/"); i > 0 {
		return virtualModel[:i]
	}
	return virtualModel
}
