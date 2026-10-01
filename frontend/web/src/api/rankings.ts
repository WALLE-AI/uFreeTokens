import { request } from './client';
import { ApiError } from './errors';

// 公开排行榜接口（GET /v1/rankings/*，免鉴权，与 /v1/catalog 同级）。数据
// 来自 worker 的日级物化表，按 Asia/Shanghai 切日、只含截至昨天的完整日。
// 绝对 token 数受后端 PUBLIC_RANKINGS_SHOW_ABSOLUTE 控制：关闭时响应里
// 直接不带 tokens / total_tokens 字段，所以这里映射成可选的 number，
// 展示层必须能在"只有份额与排名"的情况下正常渲染。

export type RankingsPeriod = 'day' | 'week' | 'month';

export interface RankingsMethodology {
  minDistinctAccounts: number;
  maxAccountShare: number;
  excludesInternal: boolean;
  successOnly: boolean;
  showsAbsolute: boolean;
}

// 所有榜单共有的元信息：period 回显、统计区间（to 不含）、时区、更新时间。
export interface RankingsMeta {
  period: RankingsPeriod;
  from: string;
  to: string;
  tz: string;
  updatedAt: string | null;
  methodology: RankingsMethodology | null;
}

export interface RankingsOthers {
  tokens?: number;
  share: number;
}

export interface RankingSeriesPoint {
  day: string;
  tokens?: number;
  // share 是该模型占当天全平台总量的比例，不是占本模型区间总量的比例。
  share: number;
}

export interface ModelRankingEntry {
  rank: number;
  model: string;
  displayName: string;
  author: string;
  providerDisplay?: string;
  deprecated: boolean;
  tokens?: number;
  share: number;
  // change 是相对上一周期的变化率（0.12 = +12%）；上一周期为 0 时为 null。
  change: number | null;
  series?: RankingSeriesPoint[];
}

export interface ModelRankings extends RankingsMeta {
  totalTokens?: number;
  models: ModelRankingEntry[];
  others: RankingsOthers | null;
}

export interface AuthorRankingEntry {
  rank: number;
  author: string;
  providerDisplay?: string;
  tokens?: number;
  share: number;
  change: number | null;
  models: number;
}

export interface AuthorRankings extends RankingsMeta {
  totalTokens?: number;
  authors: AuthorRankingEntry[];
  others: RankingsOthers | null;
}

export interface SpeedRankingEntry {
  rank: number;
  model: string;
  displayName: string;
  author: string;
  providerDisplay?: string;
  deprecated: boolean;
  // tokensPerSecond 是单请求输出速度的中位数（P50，排名依据）；meanTokensPerSecond 是按 token 加权的平均值。
  tokensPerSecond: number;
  meanTokensPerSecond?: number;
}

export interface SpeedRankings extends RankingsMeta {
  models: SpeedRankingEntry[];
}

export interface AppRankingEntry {
  rank: number;
  appName: string;
  // appUrl 可能是空字符串（应用只声明了 X-Title 没带 HTTP-Referer）。
  appUrl: string;
  tokens?: number;
  share: number;
  change: number | null;
}

export interface AppRankings extends RankingsMeta {
  totalTokens?: number;
  apps: AppRankingEntry[];
  others: RankingsOthers | null;
}

interface RawMethodology {
  min_distinct_accounts: number;
  max_account_share: number;
  excludes_internal: boolean;
  success_only: boolean;
  shows_absolute: boolean;
}

interface RawMeta {
  period: RankingsPeriod;
  from: string;
  to: string;
  tz: string;
  updated_at?: string | null;
  methodology?: RawMethodology | null;
}

interface RawOthers {
  tokens?: number;
  share: number;
}

interface RawModelEntry {
  rank: number;
  model: string;
  display_name?: string;
  author: string;
  provider_display?: string;
  deprecated?: boolean;
  tokens?: number;
  share: number;
  change?: number | null;
  series?: { day: string; tokens?: number; share: number }[];
}

interface RawModelRankings extends RawMeta {
  total_tokens?: number;
  models?: RawModelEntry[] | null;
  others?: RawOthers | null;
}

interface RawAuthorRankings extends RawMeta {
  total_tokens?: number;
  authors?: {
    rank: number;
    author: string;
    provider_display?: string;
    tokens?: number;
    share: number;
    change?: number | null;
    models?: number;
  }[] | null;
  others?: RawOthers | null;
}

interface RawSpeedRankings extends RawMeta {
  models?: {
    rank: number;
    model: string;
    display_name?: string;
    author: string;
    provider_display?: string;
    deprecated?: boolean;
    tokens_per_second: number;
    mean_tokens_per_second?: number;
  }[] | null;
}

interface RawAppRankings extends RawMeta {
  total_tokens?: number;
  apps?: {
    rank: number;
    app_name: string;
    app_url?: string;
    tokens?: number;
    share: number;
    change?: number | null;
  }[] | null;
  others?: RawOthers | null;
}

function mapMeta(raw: RawMeta): RankingsMeta {
  const m = raw.methodology;
  return {
    period: raw.period,
    from: raw.from,
    to: raw.to,
    tz: raw.tz || 'Asia/Shanghai',
    updatedAt: raw.updated_at ?? null,
    methodology: m
      ? {
          minDistinctAccounts: m.min_distinct_accounts,
          maxAccountShare: m.max_account_share,
          excludesInternal: m.excludes_internal,
          successOnly: m.success_only,
          showsAbsolute: m.shows_absolute,
        }
      : null,
  };
}

// 后端在不展示绝对值时省略字段；这里额外把 null 也归一成 undefined，
// 展示层只需判断 `tokens !== undefined`。
function optNumber(v: number | null | undefined): number | undefined {
  return typeof v === 'number' ? v : undefined;
}

function mapOthers(raw: RawOthers | null | undefined): RankingsOthers | null {
  if (!raw) return null;
  return { tokens: optNumber(raw.tokens), share: raw.share };
}

function mapModelEntry(raw: RawModelEntry): ModelRankingEntry {
  return {
    rank: raw.rank,
    model: raw.model,
    displayName: raw.display_name || raw.model,
    author: raw.author,
    providerDisplay: raw.provider_display || undefined,
    deprecated: raw.deprecated ?? false,
    tokens: optNumber(raw.tokens),
    share: raw.share,
    change: raw.change ?? null,
    series: raw.series?.map((p) => ({ day: p.day, tokens: optNumber(p.tokens), share: p.share })),
  };
}

function mapModelRankings(raw: RawModelRankings): ModelRankings {
  return {
    ...mapMeta(raw),
    totalTokens: optNumber(raw.total_tokens),
    models: (raw.models ?? []).map(mapModelEntry),
    others: mapOthers(raw.others),
  };
}

export interface FetchOptions {
  signal?: AbortSignal;
}

// getModelRankings 调用 GET /v1/rankings/models。series=true 时附带每日
// 序列（热门模型趋势图用），排行榜本身不需要。
export async function getModelRankings(
  period: RankingsPeriod,
  opts: FetchOptions & { limit?: number; series?: boolean } = {}
): Promise<ModelRankings> {
  const params = new URLSearchParams({ period, limit: String(opts.limit ?? 20) });
  if (opts.series) params.set('series', 'day');
  const raw = await request<RawModelRankings>(`/v1/rankings/models?${params}`, { signal: opts.signal });
  return mapModelRankings(raw);
}

// getToolRankings / getMultimodalRankings 与 models 同形（无 series），
// tokens/share 只统计含工具调用 / 含图片输入的请求。
export async function getToolRankings(period: RankingsPeriod, opts: FetchOptions = {}): Promise<ModelRankings> {
  const raw = await request<RawModelRankings>(`/v1/rankings/tools?period=${period}`, { signal: opts.signal });
  return mapModelRankings(raw);
}

export async function getMultimodalRankings(period: RankingsPeriod, opts: FetchOptions = {}): Promise<ModelRankings> {
  const raw = await request<RawModelRankings>(`/v1/rankings/multimodal?period=${period}`, { signal: opts.signal });
  return mapModelRankings(raw);
}

// getAuthorRankings 调用 GET /v1/rankings/authors：厂商（virtual_model 的
// '/' 前缀）份额，Top 9 + "其他"。
export async function getAuthorRankings(period: RankingsPeriod, opts: FetchOptions = {}): Promise<AuthorRankings> {
  const raw = await request<RawAuthorRankings>(`/v1/rankings/authors?period=${period}`, { signal: opts.signal });
  return {
    ...mapMeta(raw),
    totalTokens: optNumber(raw.total_tokens),
    authors: (raw.authors ?? []).map((a) => ({
      rank: a.rank,
      author: a.author,
      providerDisplay: a.provider_display || undefined,
      tokens: optNumber(a.tokens),
      share: a.share,
      change: a.change ?? null,
      models: a.models ?? 0,
    })),
    others: mapOthers(raw.others),
  };
}

// getSpeedRankings 调用 GET /v1/rankings/speed：只统计流式成功请求的输出
// tok/s，按模型降序；不暴露上游渠道。
export async function getSpeedRankings(period: RankingsPeriod, opts: FetchOptions = {}): Promise<SpeedRankings> {
  const raw = await request<RawSpeedRankings>(`/v1/rankings/speed?period=${period}`, { signal: opts.signal });
  return {
    ...mapMeta(raw),
    models: (raw.models ?? []).map((m) => ({
      rank: m.rank,
      model: m.model,
      displayName: m.display_name || m.model,
      author: m.author,
      providerDisplay: m.provider_display || undefined,
      deprecated: m.deprecated ?? false,
      tokensPerSecond: m.tokens_per_second,
      meanTokensPerSecond: m.mean_tokens_per_second,
    })),
  };
}

// getAppRankings 调用 GET /v1/rankings/apps：只含通过 X-Title 主动声明
// 身份的应用。
export async function getAppRankings(period: RankingsPeriod, opts: FetchOptions = {}): Promise<AppRankings> {
  const raw = await request<RawAppRankings>(`/v1/rankings/apps?period=${period}`, { signal: opts.signal });
  return {
    ...mapMeta(raw),
    totalTokens: optNumber(raw.total_tokens),
    apps: (raw.apps ?? []).map((a) => ({
      rank: a.rank,
      appName: a.app_name,
      appUrl: a.app_url ?? '',
      tokens: optNumber(a.tokens),
      share: a.share,
      change: a.change ?? null,
    })),
    others: mapOthers(raw.others),
  };
}

// isRankingsMaintenance 判断是否是后端总开关 PUBLIC_RANKINGS_ENABLED 关闭
// 时返回的 503——页面应展示"数据维护中"，而不是普通的加载失败。
export function isRankingsMaintenance(err: unknown): boolean {
  return err instanceof ApiError && err.status === 503 && err.code === 'service_unavailable';
}
