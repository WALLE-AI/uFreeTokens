import { request } from './client';
import type {
  BatchApproveResult,
  ChangeRequestDetail,
  ChangeRequestSummary,
  ListData,
  ModelType,
  Paginated,
  PendingListing,
  PriceSource,
  PublishListingResult,
  ReferencePriceLookupResult,
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

export function rejectChangeRequest(id: number, body: { reason?: string } = {}) {
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

export function dismissListing(id: number, reason?: string) {
  return request<{ status: string }>(`/pending-model-listings/${id}/dismiss`, { method: 'POST', body: { reason: reason ?? '' } });
}

// ---------- 价格源 ----------

export function listPriceSources(q: { provider_id?: number; enabled?: boolean } = {}, signal?: AbortSignal) {
  return request<ListData<PriceSource>>('/price-sources', { query: { ...q }, signal });
}

export function createPriceSource(body: { provider_id?: number; level: SourceLevel; kind: SourceKind; fetcher: string; url?: string }) {
  return request<{ id: number }>('/price-sources', { method: 'POST', body });
}

export function updatePriceSource(
  id: number,
  body: { enabled?: boolean; url?: string; schedule?: string; config?: Record<string, unknown> },
) {
  return request<PriceSource>(`/price-sources/${id}`, { method: 'PATCH', body });
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
