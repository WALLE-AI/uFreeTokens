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
