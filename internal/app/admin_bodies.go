package app

import (
	"encoding/json"
	"time"

	"github.com/shopspring/decimal"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
)

// 管理接口的命名请求体（此前是各 handler 内的匿名结构体）：命名之后 OpenAPI 文档
// （admin_openapi.go）和前端生成的类型可以直接引用同一个定义，代码与文档不会各说各话。

// createAPIKeyRequest 是 createAPIKey 的请求体。
type createAPIKeyRequest struct {
	Name             string     `json:"name"`
	AllowedModels    []string   `json:"allowed_models"`
	RPMLimit         *int       `json:"rpm_limit"`
	TPMLimit         *int       `json:"tpm_limit"`
	ConcurrencyLimit *int       `json:"concurrency_limit"`
	BudgetLimitMicro *int64     `json:"budget_limit_micro"`
	BudgetPeriod     string     `json:"budget_period"`
	ExpiresAt        *time.Time `json:"expires_at"`
}

// createProviderAccountRequest 是 createProviderAccount 的请求体。
type createProviderAccountRequest struct {
	ProviderID     int64            `json:"provider_id"`
	Name           string           `json:"name"`
	BaseURL        string           `json:"base_url"`
	CostMultiplier *decimal.Decimal `json:"cost_multiplier"`
	// Dialect 是可选的供应商方言（如 {"preset":"openrouter"}），见 GET /meta/dialect-presets。
	Dialect json.RawMessage `json:"dialect"`
}

// addProviderKeyRequest 是 addProviderKey 的请求体。
type addProviderKeyRequest struct {
	Secret string `json:"secret"`
	Weight int    `json:"weight"`
}

// setMetadataRequest 是 setVirtualModelMetadata 的请求体。
type setMetadataRequest struct {
	DisplayName     string         `json:"display_name"`
	Description     string         `json:"description"`
	ProviderDisplay string         `json:"provider_display"`
	Tags            []string       `json:"tags"`
	Scores          map[string]any `json:"scores"`
}

// setSellPriceRequest 是 setSellPrice 的请求体。
type setSellPriceRequest struct {
	Tier       string                      `json:"tier"`
	Components []admin.PriceComponentInput `json:"components"`
}

// setCostPriceRequest 是 setCostPrice 的请求体。
type setCostPriceRequest struct {
	Currency   string                      `json:"currency"`
	Components []admin.PriceComponentInput `json:"components"`
}

// setFXRateRequest 是 setFXRate 的请求体。
type setFXRateRequest struct {
	Base          string          `json:"base"`
	Quote         string          `json:"quote"`
	Rate          decimal.Decimal `json:"rate"`
	Source        string          `json:"source"`
	EffectiveDate *time.Time      `json:"effective_date"`
}

// loginRequest 是 login 的请求体。
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	TOTPCode string `json:"totp_code"`
}

// changePasswordRequest 是 changePassword 的请求体。
type changePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// totpCodeRequest 是 totpToggle 的请求体。
type totpCodeRequest struct {
	Code string `json:"code"`
}

// addMemberRequest 是 addAccountMember 的请求体。
type addMemberRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

// updateMemberRequest 是 updateAccountMember 的请求体。
type updateMemberRequest struct {
	Role string `json:"role"`
}

// importModelsRequest 是 importModels 的请求体。
type importModelsRequest struct {
	admin.ImportModelsInput
	DryRun bool `json:"dry_run"`
}

// batchApproveRequest 是 batchApproveChangeRequests 的请求体。
type batchApproveRequest struct {
	IDs               []int64         `json:"ids"`
	Reason            string          `json:"reason"`
	MaxAbsChangeRatio decimal.Decimal `json:"max_abs_change_ratio"`
}

// batchDismissRequest 是 batchDismissListings 的请求体。
type batchDismissRequest struct {
	IDs    []int64 `json:"ids"`
	Reason string  `json:"reason"`
}

// autofillMetadataRequest 是 autofillVirtualModelMetadata 的请求体。
type autofillMetadataRequest struct {
	VirtualModelIDs []int64 `json:"virtual_model_ids"`
	DryRun          bool    `json:"dry_run"` // true = 只预览将要写入的字段
}

// dismissListingRequest 是 dismissPendingModelListing 的请求体。
type dismissListingRequest struct {
	Reason string `json:"reason"`
}

// publishListingRequest 是 publishPendingModelListing 的请求体。虚拟模型名固定为上游原始模型名，
// virtual_model.name 仅为兼容旧调用方保留、被忽略；已有同名虚拟模型时 virtual_model 与 sell_markup 都被忽略。
type publishListingRequest struct {
	VirtualModel struct {
		Name          string   `json:"name,omitempty"`
		Family        string   `json:"family"`
		Type          string   `json:"type"`
		ContextWindow int      `json:"context_window"`
		MaxOutput     int      `json:"max_output"`
		Capabilities  []string `json:"capabilities"`
		VisibleTiers  []string `json:"visible_tiers"`
	} `json:"virtual_model"`
	ProviderAccountID int64           `json:"provider_account_id"`
	SellMarkup        decimal.Decimal `json:"sell_markup"`
}

// priceObservationRequest 是 ingestPriceObservation 的请求体。
type priceObservationRequest struct {
	SourceID      int64                `json:"source_id"`
	Level         string               `json:"level"`
	UpstreamModel string               `json:"upstream_model"`
	Currency      string               `json:"currency"`
	Components    []priceComponentJSON `json:"components"`
	EffectiveFrom *time.Time           `json:"effective_from"`
	ExpiresAt     *time.Time           `json:"expires_at"`
	RawObject     string               `json:"raw_object"`
}

// unmappedObservationRequest 是 ingestUnmappedPriceObservation 的请求体。
type unmappedObservationRequest struct {
	SourceID      int64                `json:"source_id"`
	Level         string               `json:"level"`
	UpstreamModel string               `json:"upstream_model"`
	Currency      string               `json:"currency"`
	Components    []priceComponentJSON `json:"components"`
	EffectiveFrom *time.Time           `json:"effective_from"`
	ExpiresAt     *time.Time           `json:"expires_at"`
	RawObject     string               `json:"raw_object"`
}

// createPriceSourceRequest 是 createPriceSource 的请求体。
type createPriceSourceRequest struct {
	ProviderID    *int64         `json:"provider_id"`
	Level         string         `json:"level"`
	Kind          string         `json:"kind"`
	Fetcher       string         `json:"fetcher"`
	URL           string         `json:"url"`
	Domain        string         `json:"domain"` // price（默认）/ offer / benchmark
	Name          string         `json:"name"`
	Schedule      string         `json:"schedule"`
	Config        map[string]any `json:"config"`
	Enabled       *bool          `json:"enabled"`
	License       string         `json:"license"`
	Attribution   string         `json:"attribution"`
	PublicDisplay bool           `json:"public_display"`
	AutoPublish   bool           `json:"auto_publish"`
}

// ---------- 命名响应体 ----------

// accountDetailResponse 是 GET /accounts/{id} 的响应。
type accountDetailResponse struct {
	Account             *admin.Account        `json:"account"`
	Wallet              *admin.WalletSummary  `json:"wallet"`
	Members             []admin.AccountMember `json:"members"`
	ActiveGrantsSummary admin.GrantsSummary   `json:"active_grants_summary"`
}

// accountWithWallet 是 PATCH /accounts/{id} 的响应。
type accountWithWallet struct {
	Account *admin.Account       `json:"account"`
	Wallet  *admin.WalletSummary `json:"wallet"`
}

// importModelsResponse 是 POST /provider-accounts/{id}/import-models 的响应。
type importModelsResponse struct {
	DryRun    bool                    `json:"dry_run"`
	Currency  string                  `json:"currency"`
	FXRate    *decimal.Decimal        `json:"fx_rate"`
	FXDate    *time.Time              `json:"fx_date"`
	FXMissing bool                    `json:"fx_missing"`
	Items     []importModelResultItem `json:"items"`
}
