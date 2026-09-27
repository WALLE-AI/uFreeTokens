import type { AccountStatus, ApiKey, GrantSource, LedgerType, Tier } from '../../types';

// 用户与财务页面共用的选项与小组件（仅 src/pages/accounts 内部使用）。

export const ACCOUNT_STATUS_OPTIONS: Array<{ value: AccountStatus; label: string }> = [
  { value: 'active', label: '正常' },
  { value: 'suspended', label: '已暂停' },
  { value: 'closed', label: '已关闭' },
];

export const TIER_OPTIONS: Array<{ value: Tier; label: string }> = [
  { value: 'free', label: 'free' },
  { value: 'pro', label: 'pro' },
  { value: 'enterprise', label: 'enterprise' },
];

export const ACCOUNT_TYPE_LABELS: Record<string, string> = {
  personal: '个人',
  organization: '组织',
};

export const LEDGER_TYPE_LABELS: Record<LedgerType, string> = {
  recharge: '充值',
  consume: '消费',
  refund: '退款',
  grant: '赠送',
  grant_expire: '赠送过期',
  adjust: '调账',
};

export const GRANT_SOURCE_OPTIONS: Array<{ value: GrantSource; label: string; hint: string }> = [
  { value: 'signup', label: '注册', hint: '新用户注册赠送' },
  { value: 'promotion', label: '活动', hint: '营销活动发放' },
  { value: 'compensation', label: '补偿', hint: '故障 / 投诉补偿' },
  { value: 'invite', label: '邀请', hint: '邀请奖励' },
];

export const ROLE_LABELS: Record<string, string> = {
  owner: '所有者',
  admin: '管理员',
  developer: '开发者',
  billing: '财务',
  viewer: '只读',
};

// 大额调账阈值：|金额| ≥ ¥1000 时需要输入账户名确认（UI_DESIGN.md §5.5）
export const LARGE_ADJUST_MICRO = 1_000 * 1_000_000;

// 关联单号：ADJ-20260927-X7K2P9（运营没有工单号时一键生成）
export function genRefId(prefix = 'ADJ'): string {
  const d = new Date();
  const p = (n: number) => String(n).padStart(2, '0');
  const rand = Math.random().toString(36).slice(2, 8).toUpperCase();
  return `${prefix}-${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}-${rand}`;
}

// API Key 限流摘要：rpm / tpm / 并发，未设置的不显示
export function keyLimitsText(k: Pick<ApiKey, 'rpm_limit' | 'tpm_limit' | 'concurrency_limit'>): string {
  const parts: string[] = [];
  if (k.rpm_limit) parts.push(`${k.rpm_limit.toLocaleString('en-US')} rpm`);
  if (k.tpm_limit) parts.push(`${k.tpm_limit.toLocaleString('en-US')} tpm`);
  if (k.concurrency_limit) parts.push(`并发 ${k.concurrency_limit}`);
  return parts.length ? parts.join(' · ') : '不限';
}

