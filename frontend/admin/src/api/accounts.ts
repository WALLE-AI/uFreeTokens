import { request } from './client';
import type {
  Account,
  AccountMember,
  AccountStatus,
  AccountSummary,
  ApiKey,
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
  body: { amount: number; ref_id: string; reason: string; expected_cash_balance_micro?: number },
) {
  return request<WalletAdjustReceipt>(`/accounts/${accountId}/wallet/adjust`, { method: 'POST', body });
}

export function grantCredit(
  accountId: number,
  body: { source: GrantSource; amount: number; reason: string; expires_at?: string; model_scope?: string[]; ref_id?: string },
) {
  return request<{ grant_id: number; bonus_after_micro: number }>(`/accounts/${accountId}/credit-grants`, { method: 'POST', body });
}

export function listAccountApiKeys(accountId: number, signal?: AbortSignal) {
  return request<ListData<ApiKey>>(`/accounts/${accountId}/api-keys`, { signal });
}

export function createApiKey(
  accountId: number,
  body: { name: string; allowed_models?: string[]; rpm_limit?: number; tpm_limit?: number; concurrency_limit?: number },
) {
  return request<ApiKeyCreated>(`/accounts/${accountId}/api-keys`, { method: 'POST', body });
}

export function revokeApiKey(id: number) {
  return request<{ status: string }>(`/api-keys/${id}/revoke`, { method: 'POST' });
}
