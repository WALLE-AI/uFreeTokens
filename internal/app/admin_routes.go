package app

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/WALLE-AI/uFreeTokens/internal/adminauth"
)

// AdminRoute 描述一个受保护的管理接口及其所需权限。所有业务路由都在
// adminRouteTable 里集中声明——新增接口必须在这里写明权限，漏写权限的接口
// 不可能被注册（见 TestAdminRoutes_EveryRouteHasPermission）。
type AdminRoute struct {
	Method     string
	Pattern    string
	Permission adminauth.Permission // 空字符串表示"任何已登录管理员"
	handler    http.HandlerFunc
}

// permAuthenticated 表示只要求登录、不要求具体权限点（/me、登出、改自己密码、待办计数）。
const permAuthenticated adminauth.Permission = ""

func (h *adminHandlers) adminRouteTable() []AdminRoute {
	const (
		get, post, patch, put = http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodPut
	)
	return []AdminRoute{
		// --- 会话 / 身份 ---
		{post, "/auth/logout", permAuthenticated, h.logout},
		{get, "/me", permAuthenticated, h.me},
		{post, "/auth/password", permAuthenticated, h.changePassword},
		{post, "/auth/totp/setup", permAuthenticated, h.totpSetup},
		{post, "/auth/totp/enable", permAuthenticated, h.totpEnable},
		{post, "/auth/totp/disable", permAuthenticated, h.totpDisable},
		{get, "/admin-users", adminauth.PermAdminUserManage, h.listAdminUsers},
		{post, "/admin-users", adminauth.PermAdminUserManage, h.createAdminUser},
		{patch, "/admin-users/{adminUserID}", adminauth.PermAdminUserManage, h.updateAdminUser},
		{get, "/admin-roles", adminauth.PermAdminUserManage, h.listAdminRoles},
		{get, "/meta/enums", permAuthenticated, h.metaEnums},

		// --- 账户 / 钱包 / API Key ---
		{get, "/accounts", adminauth.PermAccountRead, h.listAccounts},
		{post, "/accounts", adminauth.PermAccountWrite, h.createAccount},
		{get, "/accounts/{accountID}", adminauth.PermAccountRead, h.getAccount},
		{patch, "/accounts/{accountID}", adminauth.PermAccountWrite, h.updateAccount},
		{get, "/accounts/{accountID}/ledger", adminauth.PermAccountRead, h.listLedger},
		{get, "/accounts/{accountID}/credit-grants", adminauth.PermAccountRead, h.listCreditGrants},
		{post, "/accounts/{accountID}/credit-grants", adminauth.PermWalletAdjust, h.grantCredit},
		{get, "/accounts/{accountID}/usage", adminauth.PermObserveRead, h.accountUsage},
		{post, "/accounts/{accountID}/wallet/adjust", adminauth.PermWalletAdjust, h.adjustWallet},
		{post, "/accounts/{accountID}/api-keys", adminauth.PermAccountWrite, h.createAPIKey},
		{get, "/accounts/{accountID}/api-keys", adminauth.PermAccountRead, h.listAPIKeys},
		{get, "/api-keys", adminauth.PermAccountRead, h.searchAPIKeys},
		{patch, "/api-keys/{apiKeyID}", adminauth.PermAccountWrite, h.updateAPIKey},
		{post, "/api-keys/{apiKeyID}/revoke", adminauth.PermAccountWrite, h.revokeAPIKey},
		{post, "/accounts/{accountID}/members", adminauth.PermAccountWrite, h.addAccountMember},
		{patch, "/accounts/{accountID}/members/{userID}", adminauth.PermAccountWrite, h.updateAccountMember},
		{http.MethodDelete, "/accounts/{accountID}/members/{userID}", adminauth.PermAccountWrite, h.removeAccountMember},

		// --- 供应商 / 上游账号 / 上游密钥 ---
		{get, "/providers", adminauth.PermCatalogRead, h.listProviders},
		{post, "/providers", adminauth.PermCatalogWrite, h.createProvider},
		{get, "/providers/{providerID}", adminauth.PermCatalogRead, h.getProvider},
		{patch, "/providers/{providerID}", adminauth.PermCatalogWrite, h.updateProvider},
		{get, "/provider-accounts", adminauth.PermCatalogRead, h.listProviderAccounts},
		{post, "/provider-accounts", adminauth.PermProviderKeyWrite, h.createProviderAccount},
		{get, "/provider-accounts/{providerAccountID}", adminauth.PermCatalogRead, h.getProviderAccount},
		{patch, "/provider-accounts/{providerAccountID}", adminauth.PermCatalogWrite, h.updateProviderAccount},
		{post, "/provider-accounts/{providerAccountID}/keys", adminauth.PermProviderKeyWrite, h.addProviderKey},
		// 会用解密后的上游密钥真实调用上游，按写密钥的权限收口。
		{get, "/provider-accounts/{providerAccountID}/upstream-models", adminauth.PermProviderKeyWrite, h.listUpstreamModels},
		// dry_run 只读；正式导入在 handler 内额外要求 catalog:write + pricing:write。
		{post, "/provider-accounts/{providerAccountID}/import-models", adminauth.PermCatalogRead, h.importModels},
		{patch, "/provider-keys/{providerKeyID}", adminauth.PermProviderKeyWrite, h.updateProviderKey},
		{post, "/provider-keys/{providerKeyID}/revoke", adminauth.PermProviderKeyWrite, h.revokeProviderKey},

		// --- 虚拟模型 / 渠道 ---
		{get, "/virtual-models", adminauth.PermCatalogRead, h.virtualModels},
		{get, "/virtual-models/lookup", adminauth.PermCatalogRead, h.getVirtualModelByName},
		{post, "/virtual-models", adminauth.PermCatalogWrite, h.createVirtualModel},
		{get, "/virtual-models/{virtualModelID}", adminauth.PermCatalogRead, h.getVirtualModel},
		{patch, "/virtual-models/{virtualModelID}", adminauth.PermCatalogWrite, h.updateVirtualModel},
		{put, "/virtual-models/{virtualModelID}/metadata", adminauth.PermCatalogWrite, h.setVirtualModelMetadata},
		{get, "/virtual-models/{virtualModelID}/price-books", adminauth.PermPricingRead, h.listSellPriceBooks},
		{post, "/virtual-models/{virtualModelID}/sell-price", adminauth.PermPricingWrite, h.setSellPrice},
		{get, "/channels", adminauth.PermCatalogRead, h.channels},
		{get, "/channels/lookup", adminauth.PermCatalogRead, h.findChannel},
		{post, "/channels", adminauth.PermCatalogWrite, h.createChannel},
		{get, "/channels/{channelID}", adminauth.PermCatalogRead, h.getChannel},
		{patch, "/channels/{channelID}", adminauth.PermCatalogWrite, h.updateChannel},
		{get, "/channels/{channelID}/price-books", adminauth.PermPricingRead, h.listCostPriceBooks},
		{post, "/channels/{channelID}/cost-price", adminauth.PermPricingWrite, h.setCostPrice},
		{get, "/catalog/counts", adminauth.PermCatalogRead, h.catalogCounts},
		{get, "/channels/health", adminauth.PermObserveRead, h.channelHealth},

		// --- 基准测试（公开 /v1/benchmarks 的数据来源） ---
		{get, "/benchmarks", adminauth.PermCatalogRead, h.listBenchmarks},
		{post, "/benchmarks", adminauth.PermCatalogWrite, h.createBenchmark},
		{get, "/benchmarks/{benchmarkID}", adminauth.PermCatalogRead, h.getBenchmark},
		{patch, "/benchmarks/{benchmarkID}", adminauth.PermCatalogWrite, h.updateBenchmark},
		{post, "/benchmarks/{benchmarkID}/runs", adminauth.PermCatalogWrite, h.createBenchmarkRun},
		{get, "/benchmark-runs/{runID}", adminauth.PermCatalogRead, h.getBenchmarkRun},
		{post, "/benchmark-runs/{runID}/publish", adminauth.PermCatalogWrite, h.publishBenchmarkRun},
		{http.MethodDelete, "/benchmark-runs/{runID}", adminauth.PermCatalogWrite, h.deleteBenchmarkRun},
		{get, "/public-apps", adminauth.PermCatalogRead, h.listPublicApps},
		{get, "/public-app-rules", adminauth.PermCatalogRead, h.listPublicAppRules},
		{post, "/public-app-rules", adminauth.PermCatalogWrite, h.createPublicAppRule},
		{http.MethodDelete, "/public-app-rules/{ruleID}", adminauth.PermCatalogWrite, h.deletePublicAppRule},

		// --- 价格 / 汇率 / 价格同步 ---
		{get, "/fx-rates", adminauth.PermPricingRead, h.listFXRates},
		{get, "/fx-rates/latest", adminauth.PermPricingRead, h.listLatestFXRates},
		{post, "/fx-rates", adminauth.PermPricingWrite, h.setFXRate},
		{post, "/pricing/preview", adminauth.PermPricingRead, h.pricingPreview},
		{post, "/pricesync/reference-price-lookup", adminauth.PermPricingRead, h.referencePriceLookup},
		{get, "/price-sources", adminauth.PermPricingRead, h.listPriceSources},
		{get, "/price-sources/{priceSourceID}", adminauth.PermPricingRead, h.getPriceSource},
		{post, "/price-sources", adminauth.PermPricingWrite, h.createPriceSource},
		{patch, "/price-sources/{priceSourceID}", adminauth.PermPricingWrite, h.updatePriceSource},
		{post, "/price-sources/{priceSourceID}/run", adminauth.PermPricingWrite, h.runPriceSourceNow},
		{get, "/price-sources/{priceSourceID}/runs", adminauth.PermPricingRead, h.listDataSourceRuns},
		{post, "/channels/{channelID}/price-observations", adminauth.PermPricingWrite, h.ingestPriceObservation},
		{post, "/providers/{providerID}/price-observations", adminauth.PermPricingWrite, h.ingestUnmappedPriceObservation},
		{get, "/price-change-requests", adminauth.PermPricingRead, h.listChangeRequests},
		{get, "/price-change-requests/{changeRequestID}", adminauth.PermPricingRead, h.getChangeRequest},
		{post, "/price-change-requests/{changeRequestID}/approve", adminauth.PermPriceChangeApprove, h.approveChangeRequest},
		{post, "/price-change-requests/{changeRequestID}/reject", adminauth.PermPriceChangeApprove, h.rejectChangeRequest},
		{post, "/price-change-requests/batch-approve", adminauth.PermPriceChangeApprove, h.batchApproveChangeRequests},
		{get, "/pending-model-listings", adminauth.PermPricingRead, h.listPendingListings},
		{post, "/pending-model-listings/{listingID}/publish", adminauth.PermPricingWrite, h.publishPendingModelListing},
		{post, "/pending-model-listings/{listingID}/dismiss", adminauth.PermPricingWrite, h.dismissPendingModelListing},
		{post, "/pending-model-listings/batch-dismiss", adminauth.PermPricingWrite, h.batchDismissListings},

		// --- 外部数据采集：优惠雷达 / 比价看板 / 榜单模型名映射 ---
		{get, "/upstream-offers", adminauth.PermPricingRead, h.listUpstreamOffers},
		{get, "/upstream-offers/{offerID}", adminauth.PermPricingRead, h.getUpstreamOffer},
		{post, "/upstream-offers/{offerID}/status", adminauth.PermPricingWrite, h.setUpstreamOfferStatus},
		{post, "/upstream-offers/{offerID}/adopt", adminauth.PermPricingWrite, h.adoptUpstreamOffer},
		{get, "/pricesync/price-comparison", adminauth.PermPricingRead, h.priceComparison},
		{get, "/model-aliases", adminauth.PermCatalogRead, h.listModelAliases},
		{get, "/model-aliases/namespaces", adminauth.PermCatalogRead, h.listModelAliasNamespaces},
		{put, "/model-aliases", adminauth.PermCatalogWrite, h.setModelAlias},

		// --- 观测 / 审计 ---
		{get, "/stats/overview", adminauth.PermObserveRead, h.statsOverview},
		{get, "/stats/usage", adminauth.PermObserveRead, h.statsUsage},
		{get, "/request-logs", adminauth.PermObserveRead, h.listRequestLogs},
		{get, "/request-logs/{requestID}", adminauth.PermObserveRead, h.getRequestLog},
		{get, "/audit-logs", adminauth.PermAuditRead, h.listAuditLogs},
		{get, "/todo-counts", permAuthenticated, h.getTodoCounts},
	}
}

func (h *adminHandlers) registerRoutes(r chi.Router) {
	for _, rt := range h.adminRouteTable() {
		r.With(requirePermission(rt.Permission)).Method(rt.Method, rt.Pattern, rt.handler)
	}
}

// AdminRouteTable 返回全部受保护路由及其权限，供测试与文档生成使用。
func AdminRouteTable() []AdminRoute {
	h := &adminHandlers{}
	return h.adminRouteTable()
}
