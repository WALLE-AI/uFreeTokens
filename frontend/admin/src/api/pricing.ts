import { request } from './client';
import type {
  BatchItemResult,
  BatchApproveResult,
  ChangeRequestDetail,
  ChangeRequestSummary,
  DataSourceRun,
  ListData,
  ModelType,
  Offer,
  OfferStatus,
  Paginated,
  PendingListing,
  PriceComparisonRow,
  PriceSource,
  PublishListingResult,
  ReferencePriceLookupResult,
  SourceDomain,
  SourceKind,
  SourceLevel,
  Tier,
} from '../types';
import type { PageQuery } from './catalog';

// 价格同步：调价审批、待上架、价格源、参考价（接口方案 §5）。
// 后端 PriceSync 未装配时这些接口统一返回 503 not_implemented。

// ---------- 调价审批 ----------

export interface ListChangeRequestsQuery extends PageQuery {
  status?: string; // 逗号分隔；默认 pending,blocked；"all" = 全部
  direction?: string;
  channel_id?: number;
  provider_id?: number;
}

export function listChangeRequests(q: ListChangeRequestsQuery = {}, signal?: AbortSignal) {
  return request<Paginated<ChangeRequestSummary>>('/price-change-requests', { query: { ...q }, signal });
}

export function getChangeRequest(id: number, signal?: AbortSignal) {
  return request<ChangeRequestDetail>(`/price-change-requests/${id}`, { signal });
}

// 批准 blocked 项必须 confirm_blocked=true（否则 409）
export function approveChangeRequest(id: number, body: { reason?: string; confirm_blocked?: boolean } = {}) {
  return request<{ applied_book_id: number }>(`/price-change-requests/${id}/approve`, { method: 'POST', body });
}

// 驳回必须填写理由（后端 400）
export function rejectChangeRequest(id: number, body: { reason: string }) {
  return request<{ status: string }>(`/price-change-requests/${id}/reject`, { method: 'POST', body });
}

// 只允许 pending 且 |变化率| <= max_abs_change_ratio（上限 0.2）；逐条返回结果
export function batchApproveChangeRequests(body: { ids: number[]; reason?: string; max_abs_change_ratio: string }) {
  return request<{ results: BatchApproveResult[] }>('/price-change-requests/batch-approve', { method: 'POST', body });
}

// ---------- 待上架 ----------

export function listPendingListings(q: PageQuery & { status?: string; provider_id?: number } = {}, signal?: AbortSignal) {
  return request<Paginated<PendingListing>>('/pending-model-listings', { query: { ...q }, signal });
}

export interface PublishListingBody {
  virtual_model: {
    name: string;
    family: string;
    type: ModelType;
    context_window: number;
    max_output: number;
    capabilities?: string[];
    visible_tiers?: Tier[];
  };
  provider_account_id: number;
  sell_markup: string; // 售价 = 成本 × (1 + sell_markup)
}

export function publishListing(id: number, body: PublishListingBody) {
  return request<PublishListingResult>(`/pending-model-listings/${id}/publish`, { method: 'POST', body });
}

// 批量忽略：逐条独立处理，逐条返回结果
export function batchDismissListings(ids: number[], reason?: string) {
  return request<{ results: BatchItemResult[] }>('/pending-model-listings/batch-dismiss', { method: 'POST', body: { ids, reason: reason ?? '' } });
}

export function dismissListing(id: number, reason?: string) {
  return request<{ status: string }>(`/pending-model-listings/${id}/dismiss`, { method: 'POST', body: { reason: reason ?? '' } });
}

// ---------- 数据源（价格 / 优惠 / 评测榜单，表名仍是 price_sources） ----------

export function listPriceSources(q: { provider_id?: number; enabled?: boolean; domain?: SourceDomain } = {}, signal?: AbortSignal) {
  return request<ListData<PriceSource>>('/price-sources', { query: { ...q }, signal });
}

export interface CreatePriceSourceBody {
  provider_id?: number;
  level: SourceLevel;
  kind: SourceKind;
  fetcher: string;
  url?: string;
  domain?: SourceDomain; // 默认 price
  name?: string;
  schedule?: string;
  config?: Record<string, unknown>;
  enabled?: boolean;
  license?: string;
  attribution?: string;
  public_display?: boolean;
  auto_publish?: boolean;
}

export function createPriceSource(body: CreatePriceSourceBody) {
  return request<{ id: number }>('/price-sources', { method: 'POST', body });
}

// PATCH：只传要改的字段；url / license / attribution 传空字符串表示清除，provider_id=0 表示解绑供应商
export interface UpdatePriceSourceBody {
  enabled?: boolean;
  url?: string;
  schedule?: string;
  config?: Record<string, unknown>;
  name?: string;
  license?: string;
  attribution?: string;
  public_display?: boolean;
  auto_publish?: boolean;
  provider_id?: number;
}

export function updatePriceSource(id: number, body: UpdatePriceSourceBody) {
  return request<PriceSource>(`/price-sources/${id}`, { method: 'PATCH', body });
}

// 立即运行：把 next_run_at 置为现在，worker 约 1 分钟内拾取（202）；已停用的来源返回 400
export function runPriceSourceNow(id: number) {
  return request<PriceSource>(`/price-sources/${id}/run`, { method: 'POST' });
}

export function listDataSourceRuns(id: number, limit = 50, signal?: AbortSignal) {
  return request<ListData<DataSourceRun>>(`/price-sources/${id}/runs`, { query: { limit }, signal });
}

// ---------- 优惠雷达 ----------

export interface ListOffersQuery extends PageQuery {
  status?: string;
  offer_type?: string;
  provider_code?: string;
  q?: string;
}

export function listUpstreamOffers(q: ListOffersQuery = {}, signal?: AbortSignal) {
  return request<Paginated<Offer>>('/upstream-offers', { query: { ...q }, signal });
}

export function getUpstreamOffer(id: number, signal?: AbortSignal) {
  return request<Offer>(`/upstream-offers/${id}`, { signal });
}

// 已采用的情报不能再改状态（409）
export function setUpstreamOfferStatus(id: number, status: Extract<OfferStatus, 'confirmed' | 'ignored' | 'new'>) {
  return request<Offer>(`/upstream-offers/${id}/status`, { method: 'POST', body: { status } });
}

export interface AdoptOfferBody {
  side: 'cost' | 'sell'; // cost：记录我方成本优惠（仅供毛利参考）；sell：对用户让利，立即参与计费
  channel_id?: number; // side=cost 必填
  virtual_model?: string; // side=sell 必填（虚拟模型名）
  name?: string;
  discount_ratio?: string; // 价格乘数，覆盖情报里的值；0 = 免费
  starts_at?: string; // RFC3339
  ends_at?: string;
  priority?: number;
  budget_total?: number; // 让利预算上限（微元）；不传 = 不限
}

// 带 Idempotency-Key：超时后重试不会重复生成促销
export function adoptUpstreamOffer(id: number, body: AdoptOfferBody, idempotencyKey?: string) {
  return request<{ promotion_id: number }>(`/upstream-offers/${id}/adopt`, { method: 'POST', body, idempotencyKey });
}

// ---------- 比价看板 ----------

export function getPriceComparison(q: PageQuery & { q?: string } = {}, signal?: AbortSignal) {
  return request<Paginated<PriceComparisonRow>>('/pricesync/price-comparison', { query: { ...q }, signal, timeoutMs: 60_000 });
}

// ---------- 参考价 ----------

// 查 OpenRouter / LiteLLM 的公开价格（USD / 百万 token），只读、不落库
export function lookupReferencePrices(upstreamModels: string[], signal?: AbortSignal) {
  return request<ReferencePriceLookupResult>('/pricesync/reference-price-lookup', {
    method: 'POST',
    body: { upstream_models: upstreamModels },
    signal,
    timeoutMs: 60_000,
  });
}

export type { ListData };
