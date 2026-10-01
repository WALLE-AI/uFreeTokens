import { request } from './client';
import type {
  Account,
  AccountMember,
  AccountStatus,
  AccountSummary,
  ApiKeyCreated,
  ApiKeyListItem,
  CreditGrant,
  Cursor,
  GrantSource,
  GrantsSummary,
  LedgerEntry,
  ListData,
  Paginated,
  Tier,
  WalletAdjustReceipt,
  WalletSummary,
} from '../types';
import type { PageQuery } from './catalog';

// 账户 / 资金 / API Key（接口方案 §4）。

export interface AccountWithWallet {
  account: Account;
  wallet: WalletSummary;
  members: AccountMember[];
  active_grants_summary: GrantsSummary;
}

// q：纯数字匹配账户 ID；含 @ 匹配 owner 邮箱；否则匹配账户名。
// sort：-created_at（默认）/ cash_balance / last_active_at / name / id，可加 - 降序
export function listAccounts(q: PageQuery & { q?: string; status?: string; tier?: string; type?: string } = {}, signal?: AbortSignal) {
  return request<Paginated<AccountSummary>>('/accounts', { query: { ...q }, signal });
}

// 资金流水：(created_at, id) 倒序游标分页
export function listLedger(
  accountId: number,
  q: { type?: string; balance_kind?: string; from?: string; to?: string; before?: string; limit?: number } = {},
  signal?: AbortSignal,
) {
  return request<Cursor<LedgerEntry>>(`/accounts/${accountId}/ledger`, { query: { ...q }, signal });
}

export function listCreditGrants(accountId: number, activeOnly = false, signal?: AbortSignal) {
  return request<ListData<CreditGrant>>(`/accounts/${accountId}/credit-grants`, { query: { active: activeOnly || undefined }, signal });
}

// 全局检索：q 匹配 Key 名称、展示前缀，或直接粘贴用户发来的完整 Key
export function searchApiKeys(q: PageQuery & { q?: string; account_id?: number; status?: string } = {}, signal?: AbortSignal) {
  return request<Paginated<ApiKeyListItem>>('/api-keys', { query: { ...q }, signal });
}

export function getAccount(id: number, signal?: AbortSignal) {
  return request<AccountWithWallet>(`/accounts/${id}`, { signal });
}

export function createAccount(body: { type: 'personal' | 'organization'; name: string; tier?: Tier; credit_limit_micro?: number }) {
  return request<Account>('/accounts', { method: 'POST', body });
}

// PATCH 只返回 {account, wallet}，成员与赠送摘要需要重新 getAccount
export function updateAccount(id: number, body: { name?: string; status?: AccountStatus; tier?: Tier; credit_limit_micro?: number }) {
  return request<Pick<AccountWithWallet, 'account' | 'wallet'>>(`/accounts/${id}`, { method: 'PATCH', body });
}

// reason 必填。同一账户重复 ref_id → 409 conflict；传 expected_cash_balance_micro 时
// 与实际余额不符 → 409 balance_changed；扣成负数 → 400。
export function adjustWallet(
  accountId: number,
  body: { amount_micro: number; ref_id: string; reason: string; expected_cash_balance_micro?: number },
) {
  return request<WalletAdjustReceipt>(`/accounts/${accountId}/wallet/adjust`, { method: 'POST', body });
}

// ref_id 必填：它是幂等键——同一账户同一来源重复提交只发放一次（409 conflict）。
// 没有工单号时用 newIdempotencyKey() 生成，并在同一次操作的重试中复用。
export function grantCredit(
  accountId: number,
  body: { source: GrantSource; amount_micro: number; reason: string; expires_at?: string; model_scope?: string[]; ref_id: string },
) {
  return request<{ grant_id: number; bonus_after_micro: number }>(`/accounts/${accountId}/credit-grants`, { method: 'POST', body });
}

// 账户下的 Key 分页返回（默认 20 条）。
export function listAccountApiKeys(accountId: number, q: PageQuery & { status?: string } = {}, signal?: AbortSignal) {
  return request<Paginated<ApiKeyListItem>>(`/accounts/${accountId}/api-keys`, { query: { ...q }, signal });
}

export function createApiKey(
  accountId: number,
  body: {
    name: string;
    allowed_models?: string[];
    rpm_limit?: number;
    tpm_limit?: number;
    concurrency_limit?: number;
    budget_limit_micro?: number;
    budget_period?: 'none' | 'daily' | 'monthly';
    expires_at?: string;
  },
) {
  return request<ApiKeyCreated>(`/accounts/${accountId}/api-keys`, { method: 'POST', body });
}

// 编辑 Key：名称、启用/停用、限额、预算、过期时间（吊销走 revokeApiKey，不可逆）。
export function updateApiKey(
  id: number,
  body: {
    name?: string;
    status?: 'active' | 'disabled';
    allowed_models?: string[];
    rpm_limit?: number;
    tpm_limit?: number;
    concurrency_limit?: number;
    budget_limit_micro?: number;
    budget_period?: 'none' | 'daily' | 'monthly';
    expires_at?: string;
    clear_expires_at?: boolean;
  },
) {
  return request<ApiKeyListItem>(`/api-keys/${id}`, { method: 'PATCH', body });
}

// ---------- 成员 ----------

export function addAccountMember(accountId: number, email: string, role: string) {
  return request<AccountMember>(`/accounts/${accountId}/members`, { method: 'POST', body: { email, role } });
}

export function updateAccountMember(accountId: number, userId: number, role: string) {
  return request<AccountMember>(`/accounts/${accountId}/members/${userId}`, { method: 'PATCH', body: { role } });
}

export function removeAccountMember(accountId: number, userId: number) {
  return request<void>(`/accounts/${accountId}/members/${userId}`, { method: 'DELETE' });
}

export function revokeApiKey(id: number) {
  return request<{ status: string }>(`/api-keys/${id}/revoke`, { method: 'POST' });
}
