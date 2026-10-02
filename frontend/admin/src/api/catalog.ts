import { request } from './client';
import { ApiError } from './errors';
import type * as G from './generated';
import type {
  CatalogCounts,
  PricingPreviewResult,
  ImportModelItemInput,
  ImportModelsResult,
  ActiveStatus,
  Channel,
  ChannelDetail,
  ChannelSummary,
  FXRate,
  ListData,
  ModelScores,
  ModelStatus,
  ModelType,
  Paginated,
  PriceBook,
  PriceComponentInput,
  Protocol,
  Provider,
  ProviderAccount,
  ProviderAccountDetail,
  ProviderAccountSummary,
  ProviderDetail,
  ProviderKey,
  ProviderKeyCreated,
  ProviderSummary,
  Tier,
  UpstreamModel,
  VirtualModel,
  VirtualModelDetail,
  VirtualModelSummary,
} from '../types';

// 供应商 / 上游账号 / 密钥 / 虚拟模型 / 渠道 / 价格版本 / 汇率（接口方案 §1、§2）。
// 所有函数都是对 cmd/admin 接口的一对一薄封装，不做缓存。

export interface PageQuery {
  page?: number;
  page_size?: number;
  sort?: string;
}

// ---------- 供应商 ----------

export function listProviders(q: PageQuery & { q?: string; status?: string; protocol?: string } = {}, signal?: AbortSignal) {
  return request<Paginated<ProviderSummary>>('/providers', { query: { ...q }, signal });
}

export function getProvider(id: number, signal?: AbortSignal) {
  return request<ProviderDetail>(`/providers/${id}`, { signal });
}

export function createProvider(body: { code: string; name: string; protocol: Protocol }) {
  return request<Provider>('/providers', { method: 'POST', body });
}

export function updateProvider(id: number, body: { name?: string; status?: ActiveStatus }) {
  return request<ProviderDetail & { affected_active_channels: number }>(`/providers/${id}`, { method: 'PATCH', body });
}

// ---------- 上游账号 / 密钥 ----------

export function listProviderAccounts(q: PageQuery & { provider_id?: number; q?: string; status?: string } = {}, signal?: AbortSignal) {
  return request<Paginated<ProviderAccountSummary>>('/provider-accounts', { query: { ...q }, signal });
}

export function getProviderAccount(id: number, signal?: AbortSignal) {
  return request<ProviderAccountDetail>(`/provider-accounts/${id}`, { signal });
}

export function createProviderAccount(body: { provider_id: number; name: string; base_url: string; cost_multiplier?: string; dialect?: unknown }) {
  return request<ProviderAccount>('/provider-accounts', { method: 'POST', body });
}

export function updateProviderAccount(
  id: number,
  body: { name?: string; base_url?: string; region?: string; cost_multiplier?: string; status?: ActiveStatus },
) {
  return request<ProviderAccountDetail>(`/provider-accounts/${id}`, { method: 'PATCH', body });
}

export function addProviderKey(providerAccountId: number, body: { secret: string; weight?: number }) {
  return request<ProviderKeyCreated>(`/provider-accounts/${providerAccountId}/keys`, { method: 'POST', body });
}

// 限流字段传 0 表示清除限制
export function updateProviderKey(
  id: number,
  body: {
    weight?: number;
    status?: 'active' | 'disabled';
    disabled_reason?: string;
    rpm_limit?: number;
    tpm_limit?: number;
    concurrency_limit?: number;
  },
) {
  return request<ProviderKey>(`/provider-keys/${id}`, { method: 'PATCH', body });
}

export function revokeProviderKey(id: number) {
  return request<ProviderKey>(`/provider-keys/${id}/revoke`, { method: 'POST' });
}

// 实时调用上游 /models（仅 openai 协议），502 = 上游不可用，409 = 没有可用密钥
export function listUpstreamModels(providerAccountId: number, signal?: AbortSignal) {
  return request<ListData<UpstreamModel>>(`/provider-accounts/${providerAccountId}/upstream-models`, { signal, timeoutMs: 20_000 });
}

// ---------- 虚拟模型 ----------

export interface ListVirtualModelsQuery extends PageQuery {
  q?: string;
  status?: string; // 逗号分隔
  type?: string;
  family?: string;
  tier?: string;
  missing?: 'sell_price' | 'metadata' | 'channel' | '';
  margin?: 'negative' | ''; // 只要最低毛利率 < 0 的模型
}

export function listVirtualModels(q: ListVirtualModelsQuery = {}, signal?: AbortSignal) {
  return request<Paginated<VirtualModelSummary>>('/virtual-models', { query: { ...q }, signal });
}

// 按名称精确查找；不存在返回 null（接口 404）
export async function findVirtualModelByName(name: string, signal?: AbortSignal): Promise<VirtualModel | null> {
  try {
    return await request<VirtualModel>('/virtual-models/lookup', { query: { name }, signal });
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) return null;
    throw err;
  }
}

export function getVirtualModel(id: number, signal?: AbortSignal) {
  return request<VirtualModelDetail>(`/virtual-models/${id}`, { signal });
}

export interface CreateVirtualModelBody {
  name: string;
  family: string;
  type: ModelType;
  context_window: number;
  max_output: number;
  capabilities?: string[];
  visible_tiers?: Tier[];
}

export function createVirtualModel(body: CreateVirtualModelBody) {
  return request<VirtualModel>('/virtual-models', { method: 'POST', body });
}

export function updateVirtualModel(
  id: number,
  body: {
    type?: ModelType;
    status?: ModelStatus;
    visible_tiers?: Tier[];
    capabilities?: string[];
    context_window?: number;
    max_output?: number;
    aliases?: string[];
  },
) {
  return request<VirtualModelDetail>(`/virtual-models/${id}`, { method: 'PATCH', body });
}

export function setVirtualModelMetadata(
  id: number,
  body: { display_name: string; description: string; provider_display: string; tags: string[]; scores: ModelScores | null },
) {
  return request<{ status: string }>(`/virtual-models/${id}/metadata`, { method: 'PUT', body });
}

// 售价：运行时每个模型只有一个生效售价，不区分 tier（接口方案 §1.3），所以这里不传 tier。
export function setSellPrice(virtualModelId: number, components: PriceComponentInput[]) {
  return request<{ price_book_id: number }>(`/virtual-models/${virtualModelId}/sell-price`, {
    method: 'POST',
    body: { components },
  });
}

export function listSellPriceBooks(virtualModelId: number, limit = 20, signal?: AbortSignal) {
  return request<ListData<PriceBook>>(`/virtual-models/${virtualModelId}/price-books`, { query: { limit }, signal });
}

// ---------- 渠道 ----------

export interface ListChannelsQuery extends PageQuery {
  virtual_model_id?: number;
  provider_account_id?: number;
  provider_id?: number;
  status?: string;
  q?: string;
  margin?: 'negative' | '';
  missing_cost?: boolean;
  dedicated?: boolean;
}

export function listChannels(q: ListChannelsQuery = {}, signal?: AbortSignal) {
  // 三元组参数会触发后端的"精确查找"兼容分支，列表查询不能同时带上 upstream_model
  return request<Paginated<ChannelSummary>>('/channels', { query: { ...q }, signal });
}

export async function findChannel(
  virtualModelId: number,
  providerAccountId: number,
  upstreamModel: string,
  signal?: AbortSignal,
): Promise<Channel | null> {
  try {
    return await request<Channel>('/channels/lookup', {
      query: { virtual_model_id: virtualModelId, provider_account_id: providerAccountId, upstream_model: upstreamModel },
      signal,
    });
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) return null;
    throw err;
  }
}

export function getChannel(id: number, signal?: AbortSignal) {
  return request<ChannelDetail>(`/channels/${id}`, { signal });
}

export function createChannel(body: {
  virtual_model_id: number;
  provider_account_id: number;
  upstream_model: string;
  priority?: number;
  weight?: number;
  allowed_tiers?: Tier[];
  allowed_account_ids?: number[];
}) {
  return request<Channel>('/channels', { method: 'POST', body });
}

// 空数组 = 不限制；停用某模型的最后一个 active 渠道需要 force=true（否则 409）
export function updateChannel(
  id: number,
  body: {
    priority?: number;
    weight?: number;
    status?: ActiveStatus;
    allowed_tiers?: Tier[];
    allowed_account_ids?: number[];
    param_overrides?: Record<string, unknown>;
    force?: boolean;
  },
) {
  return request<ChannelDetail>(`/channels/${id}`, { method: 'PATCH', body });
}

export function setCostPrice(channelId: number, currency: string, components: PriceComponentInput[]) {
  return request<{ price_book_id: number }>(`/channels/${channelId}/cost-price`, {
    method: 'POST',
    body: { currency, components },
  });
}

export function listCostPriceBooks(channelId: number, limit = 20, signal?: AbortSignal) {
  return request<ListData<PriceBook>>(`/channels/${channelId}/price-books`, { query: { limit }, signal });
}

// ---------- 汇率 ----------

export function listFXRates(q: { base?: string; quote?: string; limit?: number } = {}, signal?: AbortSignal) {
  return request<ListData<FXRate>>('/fx-rates', { query: { ...q }, signal });
}

export function listLatestFXRates(signal?: AbortSignal) {
  return request<ListData<FXRate>>('/fx-rates/latest', { signal });
}

export function setFXRate(body: { base: string; quote?: string; rate: string; source?: string; effective_date?: string }) {
  return request<{ status: string }>('/fx-rates', { method: 'POST', body });
}

// ---------- 计数 / 价格预览 / 批量导入（B7） ----------

// 模型库与渠道列表顶部 KPI，一次请求算完
export function getCatalogCounts(signal?: AbortSignal) {
  return request<CatalogCounts>('/catalog/counts', { signal });
}

// 服务端 decimal 计算售价与毛利（替代前端浮点计算）
export function previewPricing(
  body: {
    currency: string;
    cost_multiplier?: string;
    markup_percent: string;
    items: Array<{ key: string; cost_input?: string; cost_output?: string; sell_input?: string; sell_output?: string; markup_percent?: string }>;
  },
  signal?: AbortSignal,
) {
  return request<PricingPreviewResult>('/pricing/preview', { method: 'POST', body, signal });
}

// 批量导入上游模型：dry_run=true 只返回计划（平台现状、人民币成本、售价、毛利、错误），
// 否则逐个模型各自一个事务导入，逐条返回结果。
export function importModels(
  providerAccountId: number,
  body: { dry_run: boolean; currency: string; markup_percent: string; items: ImportModelItemInput[] },
  signal?: AbortSignal,
) {
  return request<ImportModelsResult>(`/provider-accounts/${providerAccountId}/import-models`, { method: 'POST', body, signal, timeoutMs: 120_000 });
}

// 渠道健康（后端 G9）：最近 window_minutes 分钟的请求量/错误率/P95，加上网关熔断、
// 上游 Key 冷却状态与最近的健康事件；判定阈值由服务端给出。类型直接用后端生成的定义。
export function getChannelHealth(windowMinutes = 15, signal?: AbortSignal) {
  return request<G.ChannelHealthReport>('/channels/health', { query: { window_minutes: windowMinutes }, signal });
}

// ---------- 供应商方言（多供应商接口统一技术实施方案 §3.3） ----------

export interface DialectVersion {
  dialect: unknown;
  saved_at: string;
  saved_by: number | null;
}

export interface AccountDialect {
  provider_account_id: number;
  provider_code: string;
  protocol: string;
  dialect: unknown; // 账号上保存的原始配置，null = 没有方言
  effective: { preset?: string; notes?: string } & Record<string, unknown>;
  endpoints: Record<string, boolean>;
  suggested_preset?: string;
  history: DialectVersion[];
}

export interface DialectPreset {
  name: string;
  notes: string;
  dialect: unknown;
}

export function getAccountDialect(id: number, signal?: AbortSignal) {
  return request<AccountDialect>(`/provider-accounts/${id}/dialect`, { signal });
}

export function setAccountDialect(id: number, dialect: unknown) {
  return request<AccountDialect>(`/provider-accounts/${id}/dialect`, { method: 'PUT', body: { dialect } });
}

export function listDialectPresets(signal?: AbortSignal) {
  return request<{ data: DialectPreset[] }>('/meta/dialect-presets', { signal });
}
