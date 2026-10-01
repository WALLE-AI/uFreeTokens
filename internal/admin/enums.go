package admin

import (
	"maps"
	"slices"
)

// Enums 是运营后台用到的全部枚举字典（GET /meta/enums），前端不再各自硬编码。
// 取值与数据库 CHECK 约束 / 本包的校验表保持一致；新增取值时改这里即可。
type Enums struct {
	Tiers              []string `json:"tiers"`
	Protocols          []string `json:"protocols"`
	ModelTypes         []string `json:"model_types"`
	ModelStatuses      []string `json:"model_statuses"`
	Capabilities       []string `json:"capabilities"`
	Meters             []string `json:"meters"`
	Units              []string `json:"units"`
	Currencies         []string `json:"currencies"`
	AccountTypes       []string `json:"account_types"`
	AccountStatuses    []string `json:"account_statuses"`
	APIKeyStatuses     []string `json:"api_key_statuses"`
	ProviderStatuses   []string `json:"provider_statuses"`
	ProviderKeyStatus  []string `json:"provider_key_statuses"`
	ChannelStatuses    []string `json:"channel_statuses"`
	ChangeStatuses     []string `json:"change_statuses"`
	ChangeDirections   []string `json:"change_directions"`
	LedgerTypes        []string `json:"ledger_types"`
	LedgerBalanceKinds []string `json:"ledger_balance_kinds"`
	GrantSources       []string `json:"grant_sources"`
	SourceLevels       []string `json:"source_levels"`
	SourceKinds        []string `json:"source_kinds"`
	Fetchers           []string `json:"fetchers"`
	ListingStatuses    []string `json:"listing_statuses"`
	MemberRoles        []string `json:"member_roles"`
	// 外部数据采集（docs/外部数据采集模块（价格情报与评测榜单）技术方案.md）
	SourceDomains    []string `json:"source_domains"`
	OfferTypes       []string `json:"offer_types"`
	OfferStatuses    []string `json:"offer_statuses"`
	ModelAliasStatus []string `json:"model_alias_statuses"`
	DataSourceStatus []string `json:"data_source_run_statuses"`
	// 基准测试（docs/基准测试与排行榜数据服务技术方案.md §3.2）
	BenchmarkCategories []string `json:"benchmark_categories"`
	BenchmarkStatuses   []string `json:"benchmark_statuses"`
	BenchmarkOrigins    []string `json:"benchmark_origins"`
	// ScoreKeys / DesignArenaKeys 是 virtual_model_metadata.scores 允许的键（§3.1）。
	ScoreKeys       []string `json:"score_keys"`
	DesignArenaKeys []string `json:"design_arena_keys"`
}

// validTiers 是账户分组 / 可见分组 / 渠道允许分组的合法取值。
var validTiers = []string{"free", "pro", "enterprise"}

func sortedKeys(m map[string]bool) []string {
	return slices.Sorted(maps.Keys(m))
}

func EnumValues() Enums {
	return Enums{
		Tiers:               validTiers,
		Protocols:           sortedKeys(validProtocols),
		ModelTypes:          []string{"chat", "embedding", "image", "audio", "rerank"},
		ModelStatuses:       []string{"active", "hidden", "deprecated"},
		Capabilities:        []string{"stream", "tools", "vision", "json_mode", "reasoning"},
		Meters:              []string{"input", "input_cache_read", "input_cache_write", "output", "output_reasoning", "request"},
		Units:               []string{"per_1m_tokens", "per_request", "per_image", "per_second"},
		Currencies:          []string{"CNY", "USD"},
		AccountTypes:        []string{"personal", "organization"},
		AccountStatuses:     []string{"active", "suspended", "closed"},
		APIKeyStatuses:      []string{"active", "disabled", "revoked"},
		ProviderStatuses:    []string{"active", "disabled"},
		ProviderKeyStatus:   []string{"active", "disabled", "exhausted", "revoked"},
		ChannelStatuses:     []string{"active", "disabled"},
		ChangeStatuses:      []string{"pending", "blocked", "auto_approved", "approved", "rejected", "applied", "superseded"},
		ChangeDirections:    []string{"up", "down", "mixed", "new", "removed"},
		LedgerTypes:         []string{"recharge", "consume", "refund", "grant", "grant_expire", "adjust"},
		LedgerBalanceKinds:  []string{"cash", "bonus"},
		GrantSources:        []string{"signup", "promotion", "compensation", "invite"},
		SourceLevels:        []string{"L1", "L2", "L3", "L4", "L5"},
		SourceKinds:         []string{"api", "html", "dataset", "billing", "manual"},
		Fetchers:            []string{"html_table", "litellm_dataset", "openrouter_models", "modelsdev", "tabular", "offer_page", "manual"},
		SourceDomains:       []string{"price", "offer", "benchmark"},
		OfferTypes:          []string{"free_model", "discount", "off_peak", "free_quota", "new_user_credit", "price_cut"},
		OfferStatuses:       []string{"new", "confirmed", "ignored", "expired", "adopted"},
		ModelAliasStatus:    ModelAliasStatuses,
		DataSourceStatus:    []string{"running", "ok", "unchanged", "failed", "rejected"},
		ListingStatuses:     []string{"pending", "published", "dismissed"},
		MemberRoles:         []string{"owner", "admin", "developer", "billing", "viewer"},
		BenchmarkCategories: benchmarkCategories,
		BenchmarkStatuses:   benchmarkStatuses,
		BenchmarkOrigins:    benchmarkOrigins,
		ScoreKeys:           ScoreKeys,
		DesignArenaKeys:     DesignArenaKeys,
	}
}
