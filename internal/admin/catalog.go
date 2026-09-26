package admin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
)

var validProtocols = map[string]bool{"openai": true, "anthropic": true, "gemini": true}

type Provider struct {
	ID       int64
	Code     string
	Name     string
	Protocol string
}

type CreateProviderInput struct {
	Code     string
	Name     string
	Protocol string // openai / anthropic / gemini（三者都已有适配器实现，见 internal/adapter）
}

func (s *Service) CreateProvider(ctx context.Context, in CreateProviderInput) (*Provider, error) {
	if in.Code == "" || in.Name == "" {
		return nil, errors.New("admin: provider code and name are required")
	}
	if !validProtocols[in.Protocol] {
		return nil, fmt.Errorf("admin: invalid protocol %q, want openai/anthropic/gemini", in.Protocol)
	}
	p := &Provider{Code: in.Code, Name: in.Name, Protocol: in.Protocol}
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO providers (code, name, protocol, status) VALUES ($1, $2, $3, 'active') RETURNING id`,
		in.Code, in.Name, in.Protocol,
	).Scan(&p.ID); err != nil {
		return nil, fmt.Errorf("admin: insert provider: %w", err)
	}
	return p, nil
}

type ProviderAccount struct {
	ID             int64
	ProviderID     int64
	Name           string
	BaseURL        string
	CostMultiplier decimal.Decimal
}

type CreateProviderAccountInput struct {
	ProviderID     int64
	Name           string
	BaseURL        string
	CostMultiplier *decimal.Decimal // nil = 1（不打折）
}

// CreateProviderAccount 建一个上游账号。BaseURL 只应该由管理员配置——技术方案
// §7.15 要求校验域名白名单、禁止内网地址防 SSRF，这里暂时没做，属于已知缺口
// （部署前必须补上，否则一个能调用这个接口的人可以让网关向任意内网地址发起
// 带着真实上游 Key 的请求）。
func (s *Service) CreateProviderAccount(ctx context.Context, in CreateProviderAccountInput) (*ProviderAccount, error) {
	if in.Name == "" || in.BaseURL == "" {
		return nil, errors.New("admin: provider account name and base_url are required")
	}
	mult := decimal.NewFromInt(1)
	if in.CostMultiplier != nil {
		mult = *in.CostMultiplier
	}
	pa := &ProviderAccount{ProviderID: in.ProviderID, Name: in.Name, BaseURL: in.BaseURL, CostMultiplier: mult}
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO provider_accounts (provider_id, name, base_url, cost_multiplier, status)
		 VALUES ($1, $2, $3, $4, 'active') RETURNING id`,
		in.ProviderID, in.Name, in.BaseURL, mult,
	).Scan(&pa.ID); err != nil {
		return nil, fmt.Errorf("admin: insert provider_account: %w", err)
	}
	return pa, nil
}

// ProviderKeySummary 是 AddProviderKey 的返回值：绝不包含明文或密文，只有
// 足以在控制台辨认这是哪把 Key 的末 4 位（技术方案 §7.15）。
type ProviderKeySummary struct {
	ID                int64
	ProviderAccountID int64
	Last4             string
	Weight            int
}

type AddProviderKeyInput struct {
	ProviderAccountID int64
	Secret            string // 明文上游 Key，只在这一次调用里出现，落库前立刻加密
	Weight            int    // <=0 时用默认值 100
}

// AddProviderKey 给某个上游账号添加一条 Key，用信封加密落库（技术方案 §7.15）：
// 每条 Key 一个随机 DEK 加密明文，DEK 再用配置的 KEK 加密。s.box 为 nil 时
// （没有配置 KEK）直接拒绝——不能因为漏配置就把明文 Key 存到数据库里。
func (s *Service) AddProviderKey(ctx context.Context, in AddProviderKeyInput) (*ProviderKeySummary, error) {
	if in.Secret == "" {
		return nil, errors.New("admin: provider key secret is required")
	}
	if s.box == nil {
		return nil, errors.New("admin: server has no KEK configured, refusing to store an upstream key in plaintext")
	}
	weight := in.Weight
	if weight <= 0 {
		weight = 100
	}

	sealed, err := s.box.Seal(in.Secret)
	if err != nil {
		return nil, fmt.Errorf("admin: encrypt provider key: %w", err)
	}
	last4 := in.Secret
	if len(last4) > 4 {
		last4 = last4[len(last4)-4:]
	}

	out := &ProviderKeySummary{ProviderAccountID: in.ProviderAccountID, Last4: last4, Weight: weight}
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO provider_keys (provider_account_id, secret_ciphertext, secret_dek_wrapped, secret_last4, weight, status)
		 VALUES ($1, $2, $3, $4, $5, 'active') RETURNING id`,
		in.ProviderAccountID, sealed.Ciphertext, sealed.WrappedDEK, last4, weight,
	).Scan(&out.ID); err != nil {
		return nil, fmt.Errorf("admin: insert provider_key: %w", err)
	}
	return out, nil
}

type VirtualModel struct {
	ID            int64
	Name          string
	Family        string
	Type          string
	ContextWindow int
	MaxOutput     int
	Capabilities  []string
	VisibleTiers  []string
}

type CreateVirtualModelInput struct {
	Name          string
	Family        string
	Type          string // chat / embedding / image / audio / rerank
	ContextWindow int
	MaxOutput     int
	Capabilities  []string
	VisibleTiers  []string // 空则默认对 free/pro/enterprise 都可见
}

var validModelTypes = map[string]bool{"chat": true, "embedding": true, "image": true, "audio": true, "rerank": true}

func (s *Service) CreateVirtualModel(ctx context.Context, in CreateVirtualModelInput) (*VirtualModel, error) {
	if in.Name == "" {
		return nil, errors.New("admin: virtual model name is required")
	}
	if !validModelTypes[in.Type] {
		return nil, fmt.Errorf("admin: invalid model type %q", in.Type)
	}
	if in.ContextWindow <= 0 || in.MaxOutput <= 0 {
		return nil, errors.New("admin: context_window and max_output must be positive")
	}
	tiers := in.VisibleTiers
	if len(tiers) == 0 {
		tiers = []string{"free", "pro", "enterprise"}
	}
	// virtual_models.capabilities 是 NOT NULL（默认 '{}'）；一个 nil 的 Go slice
	// 会被 pgx 编码成 SQL NULL 而不是空数组，直接违反约束——必须显式给一个非 nil
	// 的空切片。
	caps := in.Capabilities
	if caps == nil {
		caps = []string{}
	}

	vm := &VirtualModel{
		Name: in.Name, Family: in.Family, Type: in.Type,
		ContextWindow: in.ContextWindow, MaxOutput: in.MaxOutput,
		Capabilities: caps, VisibleTiers: tiers,
	}
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO virtual_models (name, family, type, context_window, max_output, capabilities, visible_tiers, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, 'active') RETURNING id`,
		in.Name, in.Family, in.Type, in.ContextWindow, in.MaxOutput, caps, tiers,
	).Scan(&vm.ID); err != nil {
		return nil, fmt.Errorf("admin: insert virtual_model: %w", err)
	}
	return vm, nil
}

type Channel struct {
	ID                int64
	VirtualModelID    int64
	ProviderAccountID int64
	UpstreamModel     string
	Priority          int
	Weight            int
}

type CreateChannelInput struct {
	VirtualModelID    int64
	ProviderAccountID int64
	UpstreamModel     string
	Priority          int // 数字越小越优先，默认 0（主）
	Weight            int // <=0 时用默认值 100
	AllowedTiers      []string
}

// CreateChannel 把一个虚拟模型接到某个上游账号上（技术方案 §6.3，路由的最小单位）。
func (s *Service) CreateChannel(ctx context.Context, in CreateChannelInput) (*Channel, error) {
	if in.UpstreamModel == "" {
		return nil, errors.New("admin: upstream_model is required")
	}
	weight := in.Weight
	if weight <= 0 {
		weight = 100
	}

	ch := &Channel{
		VirtualModelID: in.VirtualModelID, ProviderAccountID: in.ProviderAccountID,
		UpstreamModel: in.UpstreamModel, Priority: in.Priority, Weight: weight,
	}
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO channels (virtual_model_id, provider_account_id, upstream_model, priority, weight, allowed_tiers, status)
		 VALUES ($1, $2, $3, $4, $5, $6, 'active') RETURNING id`,
		in.VirtualModelID, in.ProviderAccountID, in.UpstreamModel, in.Priority, weight, in.AllowedTiers,
	).Scan(&ch.ID); err != nil {
		return nil, fmt.Errorf("admin: insert channel: %w", err)
	}
	return ch, nil
}

// PriceComponentInput 对应一条 price_components（技术方案 §6.4）。
type PriceComponentInput struct {
	Meter          string // input / input_cache_read / input_cache_write / output / output_reasoning / request
	Unit           string // per_1m_tokens / per_request / per_image / per_second
	ServiceTier    string // 空则默认 "default"
	TierMinInput   int
	TierMaxInput   *int
	WindowStartMin *int16
	WindowEndMin   *int16
	UnitPrice      decimal.Decimal
}

var validMeters = map[string]bool{
	string(pricing.MeterInput): true, string(pricing.MeterInputCacheRead): true, string(pricing.MeterInputCacheWrite): true,
	string(pricing.MeterOutput): true, string(pricing.MeterOutputReasoning): true, string(pricing.MeterRequest): true,
}
var validUnits = map[string]bool{
	string(pricing.UnitPer1MTokens): true, string(pricing.UnitPerRequest): true,
	string(pricing.UnitPerImage): true, string(pricing.UnitPerSecond): true,
}

type SetSellPriceInput struct {
	VirtualModelID int64
	Tier           string // 空 = 默认价格档（不区分用户分组）
	Components     []PriceComponentInput
}

// SetSellPrice 给虚拟模型发布一个新的售价版本（技术方案 §6.4：价格是版本化的，
// "修改价格" = 插入一条新记录，effective_from=now()；旧版本永久保留、不删除、
// 不能被覆盖——这是故意的，账单纠纷时需要能查到"当时到底生效的是哪个价格"）。
// internal/catalog 的快照加载器按 effective_from 取最新一条，所以这里发布之后，
// 网关会在下一次快照刷新时（默认 TTL 10 秒）自动用上新价格，不需要重启。
func (s *Service) SetSellPrice(ctx context.Context, in SetSellPriceInput) (int64, error) {
	return s.setPrice(ctx, priceTarget{kind: "sell", virtualModelID: &in.VirtualModelID, tier: in.Tier, currency: "CNY"}, in.Components)
}

type SetCostPriceInput struct {
	ChannelID  int64
	Currency   string // 空则默认 "CNY"；非 CNY 的成本价目前不参与计算（见 catalog 包注释）
	Components []PriceComponentInput
}

// SetCostPrice 给渠道发布一个新的成本价版本，用于计算 request_logs.cost_amount
// （毛利可见性）。版本化规则、生效方式与 SetSellPrice 完全一致。
func (s *Service) SetCostPrice(ctx context.Context, in SetCostPriceInput) (int64, error) {
	currency := in.Currency
	if currency == "" {
		currency = "CNY"
	}
	return s.setPrice(ctx, priceTarget{kind: "cost", channelID: &in.ChannelID, currency: currency}, in.Components)
}

// priceTarget 描述一次价格发布的落点：sell 挂虚拟模型（可选 tier），cost 挂渠道
// （技术方案 §6.4：售价按模型统一，成本按渠道各自结算）。
type priceTarget struct {
	kind           string
	virtualModelID *int64
	channelID      *int64
	tier           string
	currency       string
}

func (s *Service) setPrice(ctx context.Context, target priceTarget, components []PriceComponentInput) (int64, error) {
	if len(components) == 0 {
		return 0, errors.New("admin: at least one price component is required")
	}
	for _, c := range components {
		if !validMeters[c.Meter] {
			return 0, fmt.Errorf("admin: invalid meter %q", c.Meter)
		}
		if !validUnits[c.Unit] {
			return 0, fmt.Errorf("admin: invalid unit %q", c.Unit)
		}
		if c.UnitPrice.IsNegative() {
			return 0, fmt.Errorf("admin: unit_price must not be negative (meter=%s)", c.Meter)
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("admin: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var bookID int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO price_books (kind, virtual_model_id, channel_id, tier, currency, effective_from)
		 VALUES ($1, $2, $3, NULLIF($4, ''), $5, now()) RETURNING id`,
		target.kind, target.virtualModelID, target.channelID, target.tier, target.currency,
	).Scan(&bookID); err != nil {
		return 0, fmt.Errorf("admin: insert price_book: %w", err)
	}

	for _, c := range components {
		tier := c.ServiceTier
		if tier == "" {
			tier = "default"
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO price_components (price_book_id, meter, unit, service_tier, tier_min_input, tier_max_input, window_start_min, window_end_min, unit_price)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			bookID, c.Meter, c.Unit, tier, c.TierMinInput, c.TierMaxInput, c.WindowStartMin, c.WindowEndMin, c.UnitPrice,
		); err != nil {
			return 0, fmt.Errorf("admin: insert price_component (meter=%s): %w", c.Meter, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("admin: commit: %w", err)
	}
	return bookID, nil
}

// SetFXRateInput 对应一条 fx_rates（技术方案 §7.16.9）。
type SetFXRateInput struct {
	Base          string          // 原币种，如 "USD"
	Quote         string          // 空则默认 "CNY"（平台结算币种）
	Rate          decimal.Decimal // 1 单位 Base = 多少 Quote
	Source        string          // 空则默认 "manual"
	EffectiveDate time.Time       // 零值则默认今天
}

// SetFXRate 写入/更新某一天生效的汇率。和价格表不同，这里用 upsert 而不是
// 只追加新版本——同一天的汇率写错了应该能直接改，不需要背上一条"错误历史版本"
// 永久留痕（fx_rates 不像 price_books 那样承担"账单纠纷时查历史价格"的审计职责）。
func (s *Service) SetFXRate(ctx context.Context, in SetFXRateInput) error {
	if in.Base == "" {
		return errors.New("admin: fx_rate base currency is required")
	}
	if !in.Rate.IsPositive() {
		return errors.New("admin: fx_rate rate must be positive")
	}
	quote := in.Quote
	if quote == "" {
		quote = "CNY"
	}
	source := in.Source
	if source == "" {
		source = "manual"
	}
	effDate := in.EffectiveDate
	if effDate.IsZero() {
		effDate = time.Now()
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO fx_rates (base, quote, rate, source, effective_date) VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (base, quote, effective_date) DO UPDATE SET rate = EXCLUDED.rate, source = EXCLUDED.source`,
		in.Base, quote, in.Rate, source, effDate,
	); err != nil {
		return fmt.Errorf("admin: upsert fx_rate: %w", err)
	}
	return nil
}
