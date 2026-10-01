package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/store"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/pricing"
)

var validProtocols = map[string]bool{"openai": true, "anthropic": true, "gemini": true}

type Provider struct {
	ID       int64  `json:"id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
}

type CreateProviderInput struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"` // openai / anthropic / gemini（三者都已有适配器实现，见 internal/adapter）
	// AllowedHosts 是上游 base_url 的域名白名单（见 urlpolicy.go），可空。
	AllowedHosts []string `json:"allowed_hosts"`
	Currency     string   `json:"currency"` // 成本价默认币种，空 = USD
}

func (s *Service) CreateProvider(ctx context.Context, in CreateProviderInput) (*Provider, error) {
	if in.Code == "" || in.Name == "" {
		return nil, errors.New("admin: provider code and name are required")
	}
	if !validProtocols[in.Protocol] {
		return nil, fmt.Errorf("admin: invalid protocol %q, want openai/anthropic/gemini", in.Protocol)
	}
	hosts, err := normalizeHosts(in.AllowedHosts)
	if err != nil {
		return nil, err
	}
	currency := strings.ToUpper(strings.TrimSpace(in.Currency))
	if currency == "" {
		currency = "USD"
	}
	if !validCurrency(currency) {
		return nil, invalid("currency must be a currency code such as USD or CNY")
	}
	p := &Provider{Code: in.Code, Name: in.Name, Protocol: in.Protocol}
	if err := s.db(ctx).QueryRow(ctx,
		`INSERT INTO providers (code, name, protocol, status, allowed_hosts, currency) VALUES ($1, $2, $3, 'active', $4, $5) RETURNING id`,
		in.Code, in.Name, in.Protocol, hosts, currency,
	).Scan(&p.ID); err != nil {
		return nil, fmt.Errorf("admin: insert provider: %w", err)
	}
	return p, nil
}

type ProviderAccount struct {
	ID             int64           `json:"id"`
	ProviderID     int64           `json:"provider_id"`
	Name           string          `json:"name"`
	BaseURL        string          `json:"base_url"`
	CostMultiplier decimal.Decimal `json:"cost_multiplier"`
}

type CreateProviderAccountInput struct {
	ProviderID     int64
	Name           string
	BaseURL        string
	CostMultiplier *decimal.Decimal // nil = 1（不打折）
}

// CreateProviderAccount 建一个上游账号。BaseURL 按 urlpolicy.go 校验（https、
// 供应商域名白名单、禁止内网地址），防止持有写权限的人把网关流量或上游密钥
// 引到任意地址（技术方案 §7.15）。
func (s *Service) CreateProviderAccount(ctx context.Context, in CreateProviderAccountInput) (*ProviderAccount, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || in.BaseURL == "" {
		return nil, errors.New("admin: provider account name and base_url are required")
	}
	mult := decimal.NewFromInt(1)
	if in.CostMultiplier != nil {
		if !in.CostMultiplier.IsPositive() {
			return nil, invalid("cost_multiplier must be > 0")
		}
		mult = *in.CostMultiplier
	}
	baseURL, err := s.validateUpstreamURL(ctx, s.db(ctx), in.ProviderID, in.BaseURL)
	if err != nil {
		return nil, err
	}
	in.BaseURL = baseURL
	pa := &ProviderAccount{ProviderID: in.ProviderID, Name: in.Name, BaseURL: in.BaseURL, CostMultiplier: mult}
	if err := s.db(ctx).QueryRow(ctx,
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
	ID                int64  `json:"id"`
	ProviderAccountID int64  `json:"provider_account_id"`
	Last4             string `json:"last4"`
	Weight            int    `json:"weight"`
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
		// 不能因为漏配置就把明文 Key 存到数据库里。
		return nil, ErrKEKNotConfigured
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
	if err := s.db(ctx).QueryRow(ctx,
		`INSERT INTO provider_keys (provider_account_id, secret_ciphertext, secret_dek_wrapped, secret_last4, weight, status)
		 VALUES ($1, $2, $3, $4, $5, 'active') RETURNING id`,
		in.ProviderAccountID, sealed.Ciphertext, sealed.WrappedDEK, last4, weight,
	).Scan(&out.ID); err != nil {
		return nil, fmt.Errorf("admin: insert provider_key: %w", err)
	}
	return out, nil
}

type VirtualModel struct {
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	Family        string   `json:"family"`
	Type          string   `json:"type"`
	ContextWindow int      `json:"context_window"`
	MaxOutput     int      `json:"max_output"`
	Capabilities  []string `json:"capabilities"`
	VisibleTiers  []string `json:"visible_tiers"`
}

type CreateVirtualModelInput struct {
	Name          string   `json:"name"`
	Family        string   `json:"family"`
	Type          string   `json:"type"` // chat / embedding / image / audio / rerank
	ContextWindow int      `json:"context_window"`
	MaxOutput     int      `json:"max_output"`
	Capabilities  []string `json:"capabilities"`
	VisibleTiers  []string `json:"visible_tiers"` // 空则默认对 free/pro/enterprise 都可见
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
	if err := s.db(ctx).QueryRow(ctx,
		`INSERT INTO virtual_models (name, family, type, context_window, max_output, capabilities, visible_tiers, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, 'active') RETURNING id`,
		in.Name, in.Family, in.Type, in.ContextWindow, in.MaxOutput, caps, tiers,
	).Scan(&vm.ID); err != nil {
		return nil, fmt.Errorf("admin: insert virtual_model: %w", err)
	}
	return vm, nil
}

var ErrVirtualModelNotFound = errors.New("admin: virtual model not found")

// GetVirtualModelByName 按 virtual_models.name（唯一约束）查一条记录，供调用方
// 在创建前先判断"这个名字是不是已经存在了"——比如手工联调工具反复点"导入"
// 时，直接照着上游模型 ID 建虚拟模型名字，重复导入会撞 name 的唯一约束，
// 调用方应该先查一遍、存在就复用，而不是每次都硬 INSERT 再处理冲突错误。
func (s *Service) GetVirtualModelByName(ctx context.Context, name string) (*VirtualModel, error) {
	vm := &VirtualModel{}
	if err := s.db(ctx).QueryRow(ctx,
		`SELECT id, name, family, type, context_window, max_output, capabilities, visible_tiers
		 FROM virtual_models WHERE name = $1`,
		name,
	).Scan(&vm.ID, &vm.Name, &vm.Family, &vm.Type, &vm.ContextWindow, &vm.MaxOutput, &vm.Capabilities, &vm.VisibleTiers); err != nil {
		if isNoRows(err) {
			return nil, ErrVirtualModelNotFound
		}
		return nil, fmt.Errorf("admin: get virtual_model by name: %w", err)
	}
	return vm, nil
}

// SetVirtualModelMetadataInput 对应一条 virtual_model_metadata（技术方案
// 迭代5：GET /v1/catalog 公开目录的展示层信息，由运营录入，不是自动生成的）。
type SetVirtualModelMetadataInput struct {
	VirtualModelID  int64          `json:"virtual_model_id"`
	DisplayName     string         `json:"display_name"`
	Description     string         `json:"description"`
	ProviderDisplay string         `json:"provider_display"`
	Tags            []string       `json:"tags"`
	Scores          map[string]any `json:"scores"` // nil = 不设置/清空评分
}

// SetVirtualModelMetadata upsert 一条虚拟模型的展示层元数据（技术方案 §6：
// 挂牌类文案/评分改动，不像价格那样要求版本化保留历史，改错了直接覆盖）。
// virtual_model_id 不存在时返回 ErrVirtualModelNotFound（外键约束会拒绝插入，
// 这里把 Postgres 的 23503 错误码翻译成包内统一的哨兵错误）。
func (s *Service) SetVirtualModelMetadata(ctx context.Context, in SetVirtualModelMetadataInput) error {
	if in.VirtualModelID <= 0 {
		return errors.New("admin: virtual_model_id is required")
	}
	tags := in.Tags
	if tags == nil {
		tags = []string{}
	}
	if err := ValidateScores(in.Scores); err != nil {
		return err
	}
	var scoresJSON []byte
	if in.Scores != nil {
		var err error
		scoresJSON, err = json.Marshal(in.Scores)
		if err != nil {
			return fmt.Errorf("admin: marshal scores: %w", err)
		}
	}

	_, err := s.db(ctx).Exec(ctx,
		`INSERT INTO virtual_model_metadata (virtual_model_id, display_name, description, provider_display, tags, scores, updated_at)
		 VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), NULLIF($4, ''), $5, $6, now())
		 ON CONFLICT (virtual_model_id) DO UPDATE SET
		   display_name = EXCLUDED.display_name,
		   description = EXCLUDED.description,
		   provider_display = EXCLUDED.provider_display,
		   tags = EXCLUDED.tags,
		   scores = EXCLUDED.scores,
		   updated_at = now()`,
		in.VirtualModelID, in.DisplayName, in.Description, in.ProviderDisplay, tags, scoresJSON,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return ErrVirtualModelNotFound
		}
		return fmt.Errorf("admin: upsert virtual_model_metadata: %w", err)
	}
	return nil
}

type Channel struct {
	ID                int64   `json:"id"`
	VirtualModelID    int64   `json:"virtual_model_id"`
	ProviderAccountID int64   `json:"provider_account_id"`
	UpstreamModel     string  `json:"upstream_model"`
	Priority          int     `json:"priority"`
	Weight            int     `json:"weight"`
	ExperimentKey     *string `json:"experiment_key"`
	VariantLabel      *string `json:"variant_label"`
	AllowedAccountIDs []int64 `json:"allowed_account_ids"`
}

type CreateChannelInput struct {
	VirtualModelID    int64    `json:"virtual_model_id"`
	ProviderAccountID int64    `json:"provider_account_id"`
	UpstreamModel     string   `json:"upstream_model"`
	Priority          int      `json:"priority"` // 数字越小越优先，默认 0（主）
	Weight            int      `json:"weight"`   // <=0 时用默认值 100
	AllowedTiers      []string `json:"allowed_tiers"`
	// ExperimentKey/VariantLabel 给这个渠道打 A/B 实验分组标签（Phase 3）：
	// 要么都留空，要么都填——分流仍然用 Priority/Weight（同一个 ExperimentKey
	// 下的几个渠道通常配相同 Priority、按 Weight 分比例），这两个字段只是让
	// request_logs 记得下来"这次请求走了哪个分组"，供事后按组聚合对比。
	ExperimentKey string `json:"experiment_key"`
	VariantLabel  string `json:"variant_label"`
	// AllowedAccountIDs 给这个渠道配专属账户白名单（Phase 4）：空 = 公共渠道，
	// 非空则只有列在里面的账户能路由到它，语义和 AllowedTiers 完全对称。
	AllowedAccountIDs []int64 `json:"allowed_account_ids"`
}

// CreateChannel 把一个虚拟模型接到某个上游账号上（技术方案 §6.3，路由的最小单位）。
func (s *Service) CreateChannel(ctx context.Context, in CreateChannelInput) (*Channel, error) {
	if in.UpstreamModel == "" {
		return nil, errors.New("admin: upstream_model is required")
	}
	if (in.ExperimentKey == "") != (in.VariantLabel == "") {
		return nil, errors.New("admin: experiment_key and variant_label must be set together")
	}
	weight := in.Weight
	if weight <= 0 {
		weight = 100
	}

	ch := &Channel{
		VirtualModelID: in.VirtualModelID, ProviderAccountID: in.ProviderAccountID,
		UpstreamModel: in.UpstreamModel, Priority: in.Priority, Weight: weight,
	}
	if err := s.db(ctx).QueryRow(ctx,
		`INSERT INTO channels (virtual_model_id, provider_account_id, upstream_model, priority, weight, allowed_tiers, experiment_key, variant_label, allowed_account_ids, status)
		 VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''), $9, 'active') RETURNING id, experiment_key, variant_label, allowed_account_ids`,
		in.VirtualModelID, in.ProviderAccountID, in.UpstreamModel, in.Priority, weight, in.AllowedTiers, in.ExperimentKey, in.VariantLabel, in.AllowedAccountIDs,
	).Scan(&ch.ID, &ch.ExperimentKey, &ch.VariantLabel, &ch.AllowedAccountIDs); err != nil {
		return nil, fmt.Errorf("admin: insert channel: %w", err)
	}
	return ch, nil
}

var ErrChannelNotFound = errors.New("admin: channel not found")

// FindChannel 按 (virtual_model_id, provider_account_id, upstream_model) 这个
// 唯一约束的自然键查一条渠道——和 GetVirtualModelByName 同样的道理：调用方
// 想要"这三元组已经存在就复用，不存在才创建"的幂等语义时，先查一遍比硬
// INSERT 再解析冲突错误更直接。
func (s *Service) FindChannel(ctx context.Context, virtualModelID, providerAccountID int64, upstreamModel string) (*Channel, error) {
	ch := &Channel{}
	if err := s.db(ctx).QueryRow(ctx,
		`SELECT id, virtual_model_id, provider_account_id, upstream_model, priority, weight, experiment_key, variant_label, allowed_account_ids
		 FROM channels WHERE virtual_model_id = $1 AND provider_account_id = $2 AND upstream_model = $3`,
		virtualModelID, providerAccountID, upstreamModel,
	).Scan(&ch.ID, &ch.VirtualModelID, &ch.ProviderAccountID, &ch.UpstreamModel, &ch.Priority, &ch.Weight, &ch.ExperimentKey, &ch.VariantLabel, &ch.AllowedAccountIDs); err != nil {
		if isNoRows(err) {
			return nil, ErrChannelNotFound
		}
		return nil, fmt.Errorf("admin: find channel: %w", err)
	}
	return ch, nil
}

// PriceComponentInput 对应一条 price_components（技术方案 §6.4）。
type PriceComponentInput struct {
	Meter          string          `json:"meter"`        // input / input_cache_read / input_cache_write / output / output_reasoning / request
	Unit           string          `json:"unit"`         // per_1m_tokens / per_request / per_image / per_second
	ServiceTier    string          `json:"service_tier"` // 空则默认 "default"
	TierMinInput   int             `json:"tier_min_input"`
	TierMaxInput   *int            `json:"tier_max_input"`
	WindowStartMin *int16          `json:"window_start_min"`
	WindowEndMin   *int16          `json:"window_end_min"`
	UnitPrice      decimal.Decimal `json:"unit_price"`
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
	Tier           string     // 空 = 默认价格档（不区分用户分组）
	EffectiveFrom  *time.Time // nil = now()；非 nil 时是"预约生效"（技术方案 §7.16.7）
	Components     []PriceComponentInput
}

// SetSellPrice 给虚拟模型发布一个新的售价版本（技术方案 §6.4：价格是版本化的，
// "修改价格" = 插入一条新记录；旧版本永久保留、不删除、不能被覆盖——这是故意的，
// 账单纠纷时需要能查到"当时到底生效的是哪个价格"）。EffectiveFrom 为 nil 时立即
// 生效；给未来时间则是预约生效，internal/catalog 的快照加载器只挑
// effective_from <= now() 的最新版本，到点之前请求仍然按旧版本计价，到点后
// 下一次快照刷新（默认 TTL 10 秒）自动切换，不需要重启、也不需要精确对时。
func (s *Service) SetSellPrice(ctx context.Context, in SetSellPriceInput) (int64, error) {
	return s.setPrice(ctx, priceTarget{kind: "sell", virtualModelID: &in.VirtualModelID, tier: in.Tier, currency: "CNY", effectiveFrom: in.EffectiveFrom}, in.Components)
}

type SetCostPriceInput struct {
	ChannelID     int64
	Currency      string     // 空则默认 "CNY"；非 CNY 的成本价需要 internal/catalog.Snapshot.FXRates 里有对应汇率才会参与计算
	EffectiveFrom *time.Time // nil = now()；语义同 SetSellPriceInput.EffectiveFrom
	Components    []PriceComponentInput
}

// SetCostPrice 给渠道发布一个新的成本价版本，用于计算 request_logs.cost_amount
// （毛利可见性）。版本化规则、生效方式与 SetSellPrice 完全一致。
func (s *Service) SetCostPrice(ctx context.Context, in SetCostPriceInput) (int64, error) {
	currency := in.Currency
	if currency == "" {
		currency = "CNY"
	}
	return s.setPrice(ctx, priceTarget{kind: "cost", channelID: &in.ChannelID, currency: currency, effectiveFrom: in.EffectiveFrom}, in.Components)
}

// priceTarget 描述一次价格发布的落点：sell 挂虚拟模型（可选 tier），cost 挂渠道
// （技术方案 §6.4：售价按模型统一，成本按渠道各自结算）。
type priceTarget struct {
	kind           string
	virtualModelID *int64
	channelID      *int64
	tier           string
	currency       string
	effectiveFrom  *time.Time
}

func (s *Service) setPrice(ctx context.Context, target priceTarget, components []PriceComponentInput) (int64, error) {
	if len(components) == 0 {
		return 0, errors.New("admin: at least one price component is required")
	}
	if err := validateComponents(components); err != nil {
		return 0, err
	}
	if target.tier != "" && !slices.Contains(validTiers, target.tier) {
		return 0, invalid("tier must be one of %s, got %q", strings.Join(validTiers, "/"), target.tier)
	}

	tx, err := store.BeginOrJoin(ctx, s.pool)
	if err != nil {
		return 0, fmt.Errorf("admin: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// 同一条价格链（sell: 虚拟模型+tier；cost: 渠道）上的发布串行化，再把新版本
	// 插进版本链：上一本的 effective_to 截到新版本的 effective_from，新版本的
	// effective_to 是下一本（预约生效的）的 effective_from。数据库的排他约束
	// （迁移 00019）保证同一条链上生效区间不重叠。
	chainKey := fmt.Sprintf("price_book:%s:%d:%d:%s", target.kind, deref(target.virtualModelID), deref(target.channelID), target.tier)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, chainKey); err != nil {
		return 0, fmt.Errorf("admin: lock price chain: %w", err)
	}
	var effectiveFrom time.Time
	if err := tx.QueryRow(ctx, `SELECT COALESCE($1::timestamptz, now())`, target.effectiveFrom).Scan(&effectiveFrom); err != nil {
		return 0, fmt.Errorf("admin: resolve effective_from: %w", err)
	}
	const chain = `kind = $1 AND virtual_model_id IS NOT DISTINCT FROM $2 AND channel_id IS NOT DISTINCT FROM $3
		AND COALESCE(tier, '') = $4`
	if _, err := tx.Exec(ctx,
		`UPDATE price_books SET effective_to = $5
		 WHERE `+chain+` AND effective_from <= $5 AND (effective_to IS NULL OR effective_to > $5)`,
		target.kind, target.virtualModelID, target.channelID, target.tier, effectiveFrom,
	); err != nil {
		return 0, fmt.Errorf("admin: close previous price_book: %w", err)
	}
	var bookID int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO price_books (kind, virtual_model_id, channel_id, tier, currency, effective_from, effective_to, created_by)
		 VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6,
		   (SELECT min(effective_from) FROM price_books WHERE `+chain+` AND effective_from > $6), $7)
		 RETURNING id`,
		target.kind, target.virtualModelID, target.channelID, target.tier, target.currency, effectiveFrom, actorFrom(ctx),
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

// validCurrency 接受 ISO 4217 三位代码，也允许最长 16 位的大写字母数字代码
// （内部结算单位/测试用的虚拟币种）。
func validCurrency(c string) bool {
	if len(c) < 3 || len(c) > 16 || c[0] < 'A' || c[0] > 'Z' {
		return false
	}
	for _, r := range c {
		if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func deref(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// validateComponents 在写库前校验价格分量，给出比数据库约束更清楚的错误信息。
func validateComponents(components []PriceComponentInput) error {
	type slot struct {
		meter, serviceTier string
		tierMin            int
		windowStart        int16
		hasWindow          bool
	}
	seen := map[slot]bool{}
	for _, c := range components {
		if !validMeters[c.Meter] {
			return fmt.Errorf("admin: invalid meter %q", c.Meter)
		}
		if !validUnits[c.Unit] {
			return fmt.Errorf("admin: invalid unit %q", c.Unit)
		}
		if c.UnitPrice.IsNegative() {
			return fmt.Errorf("admin: unit_price must not be negative (meter=%s)", c.Meter)
		}
		if c.TierMinInput < 0 || (c.TierMaxInput != nil && *c.TierMaxInput <= c.TierMinInput) {
			return invalid("tier_max_input must be greater than tier_min_input (meter=%s)", c.Meter)
		}
		if (c.WindowStartMin == nil) != (c.WindowEndMin == nil) {
			return invalid("window_start_min and window_end_min must be set together (meter=%s)", c.Meter)
		}
		if c.WindowStartMin != nil && (*c.WindowStartMin < 0 || *c.WindowStartMin > 1439 || *c.WindowEndMin < 1 || *c.WindowEndMin > 1440) {
			return invalid("time window must be within 0-1440 minutes (meter=%s)", c.Meter)
		}
		tier := c.ServiceTier
		if tier == "" {
			tier = "default"
		}
		k := slot{meter: c.Meter, serviceTier: tier, tierMin: c.TierMinInput, hasWindow: c.WindowStartMin != nil}
		if c.WindowStartMin != nil {
			k.windowStart = *c.WindowStartMin
		}
		if seen[k] {
			return invalid("duplicate price component for meter=%s service_tier=%s tier_min_input=%d", c.Meter, tier, c.TierMinInput)
		}
		seen[k] = true
	}
	return nil
}

// SetFXRateInput 对应一条 fx_rates（技术方案 §7.16.9）。
type SetFXRateInput struct {
	Base          string          `json:"base"`           // 原币种，如 "USD"
	Quote         string          `json:"quote"`          // 空则默认 "CNY"（平台结算币种）
	Rate          decimal.Decimal `json:"rate"`           // 1 单位 Base = 多少 Quote
	Source        string          `json:"source"`         // 空则默认 "manual"
	EffectiveDate time.Time       `json:"effective_date"` // 零值则默认今天
}

// SetFXRate 写入/更新某一天生效的汇率。和价格表不同，这里用 upsert 而不是
// 只追加新版本——同一天的汇率写错了应该能直接改，不需要背上一条"错误历史版本"
// 永久留痕（fx_rates 不像 price_books 那样承担"账单纠纷时查历史价格"的审计职责）。
// 被覆盖的旧值作为第一个返回值交给调用方写审计（新建时为 nil）。
//
// EffectiveDate 为零值时取数据库的 CURRENT_DATE——与价格查询里"effective_date <=
// CURRENT_DATE"的口径一致，不受应用服务器本地时区影响。
func (s *Service) SetFXRate(ctx context.Context, in SetFXRateInput) (prev *FXRateInfo, cur *FXRateInfo, err error) {
	base := strings.ToUpper(strings.TrimSpace(in.Base))
	if base == "" {
		return nil, nil, errors.New("admin: fx_rate base currency is required")
	}
	if !in.Rate.IsPositive() {
		return nil, nil, errors.New("admin: fx_rate rate must be positive")
	}
	quote := strings.ToUpper(strings.TrimSpace(in.Quote))
	if quote == "" {
		quote = "CNY"
	}
	if !validCurrency(base) || !validCurrency(quote) || base == quote {
		return nil, nil, invalid("fx_rate base/quote must be two different currency codes (ISO 4217, e.g. USD/CNY)")
	}
	source := in.Source
	if source == "" {
		source = "manual"
	}
	var effDate *time.Time
	if !in.EffectiveDate.IsZero() {
		d := in.EffectiveDate
		effDate = &d
	}
	tx, err := store.BeginOrJoin(ctx, s.pool)
	if err != nil {
		return nil, nil, fmt.Errorf("admin: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var old FXRateInfo
	err = tx.QueryRow(ctx,
		`SELECT base, quote, rate, source, effective_date FROM fx_rates
		 WHERE base = $1 AND quote = $2 AND effective_date = COALESCE($3::date, CURRENT_DATE) FOR UPDATE`,
		base, quote, effDate).Scan(&old.Base, &old.Quote, &old.Rate, &old.Source, &old.EffectiveDate)
	switch {
	case err == nil:
		prev = &old
	case !isNoRows(err):
		return nil, nil, fmt.Errorf("admin: load fx_rate: %w", err)
	}
	cur = &FXRateInfo{}
	if err := tx.QueryRow(ctx,
		`INSERT INTO fx_rates (base, quote, rate, source, effective_date) VALUES ($1, $2, $3, $4, COALESCE($5::date, CURRENT_DATE))
		 ON CONFLICT (base, quote, effective_date) DO UPDATE SET rate = EXCLUDED.rate, source = EXCLUDED.source
		 RETURNING base, quote, rate, source, effective_date`,
		base, quote, in.Rate, source, effDate,
	).Scan(&cur.Base, &cur.Quote, &cur.Rate, &cur.Source, &cur.EffectiveDate); err != nil {
		return nil, nil, fmt.Errorf("admin: upsert fx_rate: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("admin: commit: %w", err)
	}
	return prev, cur, nil
}
