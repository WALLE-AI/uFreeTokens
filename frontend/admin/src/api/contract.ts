// 编译期契约检查：前端手写的类型（types.ts）里期望的每个字段，后端都真的会返回。
// generated.ts 由后端 Go 类型生成（docs/admin-openapi.json 同源）；后端删字段或改名
// 时这里会编译失败（npm run lint / tsc），而不是上线后页面显示空白。
//
// 只检查"字段名是否存在"（手写类型的枚举联合、非空断言比生成类型更严格，这是有意的）。
// 新增一个对接后端结构的手写类型时，在下面补一行。
import type * as G from './generated';
import type * as T from '../types';

// MissingKeys<H, G> 是手写类型 H 里有、生成类型 G 里没有的字段名；必须为 never。
type MissingKeys<H, Gen> = Exclude<keyof H, keyof Gen>;
type Expect<X extends never> = X;

export type ContractChecks = [
  Expect<MissingKeys<T.AuditLogEntry, G.AuditLogEntry>>,
  Expect<MissingKeys<T.TodoCounts, G.TodoCounts>>,
  Expect<MissingKeys<T.PriceComponent, G.PriceComponentInput>>,
  Expect<MissingKeys<T.PriceBrief, G.PriceBrief>>,
  Expect<MissingKeys<T.CostCNY, G.CostCNY>>,
  Expect<MissingKeys<T.PriceBook, G.PriceBookInfo>>,
  Expect<MissingKeys<T.FXRate, G.FXRateInfo>>,
  Expect<MissingKeys<T.ProviderSummary, G.ProviderSummary>>,
  Expect<MissingKeys<T.ProviderDetail, G.ProviderDetail>>,
  Expect<MissingKeys<T.Provider, G.Provider>>,
  Expect<MissingKeys<T.ProviderAccountSummary, G.ProviderAccountSummary>>,
  Expect<MissingKeys<T.ProviderKey, G.ProviderKeyInfo>>,
  Expect<MissingKeys<T.ProviderAccountDetail, G.ProviderAccountDetail>>,
  Expect<MissingKeys<T.ProviderAccount, G.ProviderAccount>>,
  Expect<MissingKeys<T.ProviderKeyCreated, G.ProviderKeySummary>>,
  Expect<MissingKeys<T.UpstreamModel, G.UpstreamModel>>,
  Expect<MissingKeys<T.VirtualModel, G.VirtualModel>>,
  Expect<MissingKeys<T.VirtualModelSummary, G.VirtualModelSummary>>,
  Expect<MissingKeys<T.VirtualModelMetadata, G.VirtualModelMetadata>>,
  Expect<MissingKeys<T.VirtualModelDetail, G.VirtualModelDetail>>,
  Expect<MissingKeys<T.ChannelSummary, G.ChannelSummary>>,
  Expect<MissingKeys<T.PriceObservation, G.PriceObservationInfo>>,
  Expect<MissingKeys<T.ChannelDetail, G.ChannelDetail>>,
  Expect<MissingKeys<T.Channel, G.Channel>>,
  Expect<MissingKeys<T.PriceSource, G.PriceSourceInfo>>,
  Expect<MissingKeys<T.ChangeRequestSummary, G.ChangeRequestSummary>>,
  Expect<MissingKeys<T.ComponentChange, G.ComponentChange>>,
  Expect<MissingKeys<T.ValidationIssue, G.ValidationIssue>>,
  Expect<MissingKeys<T.Evidence, G.EvidenceInfo>>,
  Expect<MissingKeys<T.ChangeImpact, G.ChangeImpact>>,
  Expect<MissingKeys<T.ChangeRequestDetail, G.ChangeRequestDetail>>,
  Expect<MissingKeys<T.BatchApproveResult, G.BatchApproveResult>>,
  Expect<MissingKeys<T.PendingListing, G.PendingListing>>,
  Expect<MissingKeys<T.PublishListingResult, G.PublishListingResultDTO>>,
  Expect<MissingKeys<T.ReferencePrice, G.ReferencePriceLookupResult>>,
  Expect<MissingKeys<T.ReferencePriceLookupResult, G.ReferencePriceLookupResponse>>,
  Expect<MissingKeys<T.Account, G.Account>>,
  Expect<MissingKeys<T.WalletSummary, G.WalletSummary>>,
  Expect<MissingKeys<T.ApiKeyCreated, G.CreatedAPIKey>>,
  Expect<MissingKeys<T.WalletAdjustReceipt, G.WalletAdjustDTO>>,
  Expect<MissingKeys<T.AccountSummary, G.AccountSummary>>,
  Expect<MissingKeys<T.AccountMember, G.AccountMember>>,
  Expect<MissingKeys<T.GrantsSummary, G.GrantsSummary>>,
  Expect<MissingKeys<T.LedgerEntry, G.LedgerEntry>>,
  Expect<MissingKeys<T.CreditGrant, G.CreditGrantInfo>>,
  Expect<MissingKeys<T.ApiKeyListItem, G.APIKeyListItem>>,
  Expect<MissingKeys<T.Metrics, G.Metrics>>,
  Expect<MissingKeys<T.StatsOverview, G.StatsOverview>>,
  Expect<MissingKeys<T.UsageGroup, G.UsageGroup>>,
  Expect<MissingKeys<T.UsagePoint, G.UsagePoint>>,
  Expect<MissingKeys<T.UsageResult, G.UsageResult>>,
  Expect<MissingKeys<T.RequestLogItem, G.RequestLogItem>>,
  Expect<MissingKeys<T.RequestLogDetail, G.RequestLogDetail>>,
  Expect<MissingKeys<T.AdminMe, G.MeResponse>>,
  Expect<MissingKeys<T.AdminUser, G.AdminUser>>,
  Expect<MissingKeys<T.AdminRole, G.Role>>,
  Expect<MissingKeys<T.MetaEnums, G.MetaEnumsResponse>>,
  Expect<MissingKeys<T.CatalogCounts, G.CatalogCounts>>,
  Expect<MissingKeys<T.PricingPreviewItem, G.PricingPreviewResultItem>>,
  Expect<MissingKeys<T.PricingPreviewResult, G.PricingPreviewResult>>,
  Expect<MissingKeys<T.ImportModelItemInput, G.ImportModelItem>>,
  Expect<MissingKeys<T.ImportModelRow, G.ImportModelResultItem>>,
  Expect<MissingKeys<T.ImportModelsResult, G.ImportModelsResponse>>,
  Expect<MissingKeys<T.BatchItemResult, G.BatchItemResult>>,
];

