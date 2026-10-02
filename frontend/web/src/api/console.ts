import { request } from './client';

// CSRF_HEADERS 是 /console 写请求都要带的头（CSRFGuard 的要求，见
// internal/console/middleware.go 的包注释：自定义头 + 同源双重校验）。GET
// 请求不需要，CSRFGuard 只拦截非 GET/HEAD/OPTIONS。
const CSRF_HEADERS = { 'X-UFT-CSRF': '1' };

export interface ConsoleUser {
  userId: number;
  email: string;
  emailVerified: boolean;
  accountId: number;
  accountTier: string;
  // 账户是否选择了不计入公开排行榜（数据隐私设置，只有 owner/admin 能改）。
  excludeFromPublicStats: boolean;
}

export interface ConsoleApiKey {
  id: number;
  name: string;
  displayPrefix: string;
  status: string;
  allowedModels: string[] | null;
  rpmLimit: number | null;
  tpmLimit: number | null;
  concurrencyLimit: number | null;
  createdAt: string;
}

export interface CreatedConsoleApiKey extends ConsoleApiKey {
  key: string; // 明文，只在这次创建响应里出现一次
}

export interface ConsoleWallet {
  cashBalanceMicro: number;
  bonusBalanceMicro: number;
  frozenMicro: number;
}

interface RawMe {
  user_id: number;
  email: string;
  email_verified: boolean;
  account_id: number;
  account_tier: string;
  exclude_from_public_stats?: boolean;
}

interface RawApiKey {
  id: number;
  name: string;
  display_prefix: string;
  status: string;
  allowed_models: string[] | null;
  rpm_limit: number | null;
  tpm_limit: number | null;
  concurrency_limit: number | null;
  created_at: string;
}

interface RawCreatedApiKey extends RawApiKey {
  key: string;
}

interface RawWallet {
  cash_balance_micro: number;
  bonus_balance_micro: number;
  frozen_micro: number;
}

function mapMe(raw: RawMe): ConsoleUser {
  return {
    userId: raw.user_id,
    email: raw.email,
    emailVerified: raw.email_verified,
    accountId: raw.account_id,
    accountTier: raw.account_tier,
    excludeFromPublicStats: raw.exclude_from_public_stats ?? false,
  };
}

function mapApiKey(raw: RawApiKey): ConsoleApiKey {
  return {
    id: raw.id,
    name: raw.name,
    displayPrefix: raw.display_prefix,
    status: raw.status,
    allowedModels: raw.allowed_models,
    rpmLimit: raw.rpm_limit,
    tpmLimit: raw.tpm_limit,
    concurrencyLimit: raw.concurrency_limit,
    createdAt: raw.created_at,
  };
}

function mapWallet(raw: RawWallet): ConsoleWallet {
  return {
    cashBalanceMicro: raw.cash_balance_micro,
    bonusBalanceMicro: raw.bonus_balance_micro,
    frozenMicro: raw.frozen_micro,
  };
}

export async function register(email: string, password: string): Promise<{ userId: number; accountId: number }> {
  const raw = await request<{ user_id: number; account_id: number }>('/console/register', {
    method: 'POST',
    body: { email, password },
  });
  return { userId: raw.user_id, accountId: raw.account_id };
}

export async function login(email: string, password: string): Promise<{ userId: number; accountId: number }> {
  const raw = await request<{ user_id: number; account_id: number }>('/console/login', {
    method: 'POST',
    body: { email, password },
  });
  return { userId: raw.user_id, accountId: raw.account_id };
}

export async function logout(): Promise<void> {
  await request<void>('/console/logout', { method: 'POST', headers: CSRF_HEADERS });
}

export async function getMe(): Promise<ConsoleUser> {
  const raw = await request<RawMe>('/console/me');
  return mapMe(raw);
}

// setPublicStatsOptOut 是"不计入公开排行榜"开关（PUT /console/settings/public-stats）：
// 公开榜单只以匿名、聚合形式展示模型用量，账户可以选择退出；非 owner/admin 成员返回 403。
export async function setPublicStatsOptOut(exclude: boolean): Promise<boolean> {
  const raw = await request<{ exclude_from_public_stats: boolean }>('/console/settings/public-stats', {
    method: 'PUT',
    body: { exclude_from_public_stats: exclude },
    headers: CSRF_HEADERS,
  });
  return raw.exclude_from_public_stats;
}

export async function listKeys(): Promise<ConsoleApiKey[]> {
  const raw = await request<{ data: RawApiKey[] }>('/console/api-keys');
  return raw.data.map(mapApiKey);
}

export async function createKey(name: string): Promise<CreatedConsoleApiKey> {
  const raw = await request<RawCreatedApiKey>('/console/api-keys', {
    method: 'POST',
    body: { name },
    headers: CSRF_HEADERS,
  });
  return { ...mapApiKey(raw), key: raw.key };
}

export async function revokeKey(id: number): Promise<void> {
  await request<void>(`/console/api-keys/${id}/revoke`, { method: 'POST', headers: CSRF_HEADERS });
}

export async function getWallet(): Promise<ConsoleWallet> {
  const raw = await request<RawWallet>('/console/wallet');
  return mapWallet(raw);
}

export interface UsageIntervalRow {
  group: string; // group_by='day' 时是 "YYYY-MM-DD"，'model' 时是虚拟模型名
  requests: number;
  inputTokens: number;
  outputTokens: number;
  chargedAmountMicro: number;
}

interface RawUsageIntervalRow {
  group: string;
  requests: number;
  input_tokens: number;
  output_tokens: number;
  charged_amount_micro: number;
}

export interface GetUsageIntervalParams {
  from?: string; // YYYY-MM-DD，留空默认本月 1 号
  to?: string; // YYYY-MM-DD，留空默认今天
  groupBy?: 'day' | 'model';
}

// getUsageInterval 调用 GET /console/usage（技术方案迭代5/6：按天或按模型聚合
// 的区间用量，需要控制台登录态，不需要 API Key）。
export async function getUsageInterval(params: GetUsageIntervalParams = {}): Promise<UsageIntervalRow[]> {
  const q = new URLSearchParams();
  if (params.from) q.set('from', params.from);
  if (params.to) q.set('to', params.to);
  if (params.groupBy) q.set('group_by', params.groupBy);
  const qs = q.toString();
  const raw = await request<{ data: RawUsageIntervalRow[] }>(`/console/usage${qs ? `?${qs}` : ''}`);
  return raw.data.map((r) => ({
    group: r.group,
    requests: r.requests,
    inputTokens: r.input_tokens,
    outputTokens: r.output_tokens,
    chargedAmountMicro: r.charged_amount_micro,
  }));
}

// ConsoleLogEntry 是 GET /console/logs 单条记录——只有元数据，不含任何请求/
// 响应正文（技术方案迭代5/6）。
export interface ConsoleLogEntry {
  requestId: string;
  createdAt: string;
  apiKeyId: number;
  virtualModel: string;
  // endpoint 是 request_logs.endpoint（chat.completions / embeddings / rerank /
  // images.generations / audio.speech / audio.transcriptions）。
  endpoint: string;
  status: string;
  httpStatus: number;
  inputTokens: number;
  outputTokens: number;
  // 多模态用量：生成图片张数、语音合成字符数、语音识别时长（毫秒）。
  images: number;
  inputChars: number;
  audioMillis: number;
  chargedAmountMicro: number;
  latencyMillis: number;
  usageSource: string;
}

interface RawLogEntry {
  request_id: string;
  created_at: string;
  api_key_id: number;
  virtual_model: string;
  endpoint?: string;
  status: string;
  http_status: number;
  input_tokens: number;
  output_tokens: number;
  images?: number;
  input_chars?: number;
  audio_ms?: number;
  charged_amount_micro: number;
  latency_ms: number;
  usage_source: string;
}

function mapLogEntry(raw: RawLogEntry): ConsoleLogEntry {
  return {
    requestId: raw.request_id,
    createdAt: raw.created_at,
    apiKeyId: raw.api_key_id,
    virtualModel: raw.virtual_model,
    endpoint: raw.endpoint ?? '',
    status: raw.status,
    httpStatus: raw.http_status,
    inputTokens: raw.input_tokens,
    outputTokens: raw.output_tokens,
    images: raw.images ?? 0,
    inputChars: raw.input_chars ?? 0,
    audioMillis: raw.audio_ms ?? 0,
    chargedAmountMicro: raw.charged_amount_micro,
    latencyMillis: raw.latency_ms,
    usageSource: raw.usage_source,
  };
}

export interface GetLogsParams {
  before?: string; // 上一页返回的 nextCursor，留空取第一页
  limit?: number; // 默认/最大 100
  apiKeyId?: number;
}

export interface GetLogsResult {
  data: ConsoleLogEntry[];
  nextCursor: string;
}

// getLogs 调用 GET /console/logs（keyset 分页，倒序，技术方案迭代5/6）。
// nextCursor 为空字符串表示已经翻到最后一页。
export async function getLogs(params: GetLogsParams = {}): Promise<GetLogsResult> {
  const q = new URLSearchParams();
  if (params.before) q.set('before', params.before);
  if (params.limit) q.set('limit', String(params.limit));
  if (params.apiKeyId) q.set('api_key_id', String(params.apiKeyId));
  const qs = q.toString();
  const raw = await request<{ data: RawLogEntry[]; next_cursor: string }>(`/console/logs${qs ? `?${qs}` : ''}`);
  return { data: raw.data.map(mapLogEntry), nextCursor: raw.next_cursor };
}
