import { cn } from '../../lib/cn';

// §2.1 状态徽标字典。颜色严格限定在 §11.2：emerald / rose / amber / purple / blue / gray。
type Tone = 'green' | 'red' | 'amber' | 'purple' | 'blue' | 'gray';

const TONE_CLASS: Record<Tone, { badge: string; dot: string }> = {
  green: { badge: 'bg-emerald-50 text-emerald-700 border-emerald-200', dot: 'bg-emerald-500' },
  red: { badge: 'bg-rose-50 text-rose-700 border-rose-200', dot: 'bg-rose-500' },
  amber: { badge: 'bg-amber-50 text-amber-700 border-amber-200', dot: 'bg-amber-500' },
  purple: { badge: 'bg-purple-50 text-purple-700 border-purple-200', dot: 'bg-purple-500' },
  blue: { badge: 'bg-blue-50 text-blue-700 border-blue-200', dot: 'bg-blue-500' },
  gray: { badge: 'bg-gray-50 text-gray-600 border-gray-200', dot: 'bg-gray-400' },
};

interface StatusDef {
  label: string;
  tone: Tone;
  strike?: boolean; // revoked：灰色删除线
  italic?: boolean; // superseded：灰色斜体
}

export type StatusKind =
  | 'account'
  | 'api_key'
  | 'provider'
  | 'provider_account'
  | 'channel'
  | 'provider_key'
  | 'virtual_model'
  | 'price_change'
  | 'direction'
  | 'listing'
  | 'request'
  | 'usage_source'
  | 'price_source'
  | 'benchmark'
  | 'benchmark_run'
  | 'data_source_run'
  | 'offer'
  | 'model_alias';

const ACTIVE_DISABLED: Record<string, StatusDef> = {
  active: { label: '启用', tone: 'green' },
  disabled: { label: '停用', tone: 'gray' },
};

export const STATUS_DICT: Record<StatusKind, Record<string, StatusDef>> = {
  account: {
    active: { label: '正常', tone: 'green' },
    suspended: { label: '已暂停', tone: 'amber' },
    closed: { label: '已关闭', tone: 'gray' },
  },
  api_key: {
    active: { label: '有效', tone: 'green' },
    disabled: { label: '已停用', tone: 'amber' },
    revoked: { label: '已吊销', tone: 'gray', strike: true },
  },
  provider: ACTIVE_DISABLED,
  provider_account: ACTIVE_DISABLED,
  channel: ACTIVE_DISABLED,
  provider_key: {
    active: { label: '启用', tone: 'green' },
    disabled: { label: '停用', tone: 'gray' },
    exhausted: { label: '额度耗尽', tone: 'amber' },
    revoked: { label: '已吊销', tone: 'gray', strike: true },
  },
  virtual_model: {
    active: { label: '已上架', tone: 'green' },
    hidden: { label: '未公开', tone: 'blue' },
    deprecated: { label: '已废弃', tone: 'amber' },
  },
  price_change: {
    pending: { label: '待审批', tone: 'purple' },
    blocked: { label: '超阈值被拦截', tone: 'red' },
    approved: { label: '已批准', tone: 'green' },
    auto_approved: { label: '自动批准', tone: 'green' },
    applied: { label: '已生效', tone: 'green' },
    rejected: { label: '已驳回', tone: 'gray' },
    superseded: { label: '已被新申请取代', tone: 'gray', italic: true },
  },
  direction: {
    up: { label: '▲ 涨价', tone: 'red' },
    down: { label: '▼ 降价', tone: 'green' },
    mixed: { label: '⇅ 涨跌互现', tone: 'amber' },
    new: { label: '✦ 新增', tone: 'blue' },
    removed: { label: '✕ 移除', tone: 'gray' },
  },
  listing: {
    pending: { label: '待处理', tone: 'purple' },
    published: { label: '已上架', tone: 'green' },
    dismissed: { label: '已忽略', tone: 'gray' },
  },
  request: {
    success: { label: '成功', tone: 'green' },
    upstream_error: { label: '上游错误', tone: 'red' },
  },
  usage_source: {
    upstream: { label: '上游', tone: 'gray' },
    estimated: { label: '估算', tone: 'amber' },
    mixed: { label: '混合', tone: 'amber' },
  },
  price_source: {
    enabled: { label: '启用', tone: 'green' },
    disabled: { label: '停用', tone: 'gray' },
  },
  benchmark: {
    draft: { label: '草稿', tone: 'purple' },
    published: { label: '已发布', tone: 'green' },
    archived: { label: '已归档', tone: 'gray' },
  },
  // 由 published / published_at 推导（见 pages/catalog/benchmarkShared.tsx runState）
  benchmark_run: {
    draft: { label: '草稿', tone: 'purple' },
    published: { label: '当前发布', tone: 'green' },
    history: { label: '历史发布', tone: 'gray', italic: true },
  },
  data_source_run: {
    running: { label: '运行中', tone: 'blue' },
    ok: { label: '成功', tone: 'green' },
    unchanged: { label: '无变化', tone: 'gray' },
    failed: { label: '失败', tone: 'red' },
    rejected: { label: '异常熔断', tone: 'amber' },
  },
  offer: {
    new: { label: '待确认', tone: 'purple' },
    confirmed: { label: '已确认', tone: 'blue' },
    adopted: { label: '已采用', tone: 'green' },
    ignored: { label: '已忽略', tone: 'gray' },
    expired: { label: '已过期', tone: 'gray', italic: true },
  },
  model_alias: {
    suggested: { label: '待确认建议', tone: 'purple' },
    unmatched: { label: '未匹配', tone: 'amber' },
    auto: { label: '自动匹配', tone: 'blue' },
    confirmed: { label: '人工确认', tone: 'green' },
    ignored: { label: '平台无此模型', tone: 'gray' },
  },
};

export interface StatusBadgeProps {
  kind: StatusKind;
  value: string | null | undefined;
  dot?: boolean;
  className?: string;
}

export function StatusBadge({ kind, value, dot = true, className }: StatusBadgeProps) {
  if (!value) return <span className="text-gray-400">—</span>;
  // request.status 是自由文本：未知的非 success 值一律按失败显示
  const def: StatusDef =
    STATUS_DICT[kind][value] ?? (kind === 'request' ? { label: value, tone: 'red' } : { label: value, tone: 'gray' });
  const tone = TONE_CLASS[def.tone];
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] font-medium border whitespace-nowrap',
        tone.badge,
        def.strike && 'line-through',
        def.italic && 'italic',
        className,
      )}
    >
      {dot && kind !== 'direction' && <span className={cn('w-1.5 h-1.5 rounded-full', tone.dot)} />}
      {def.label}
    </span>
  );
}

// 计数徽标（侧栏待办）：存在 blocked 项时变 rose（§11.5）
export function CountBadge({ count, alert }: { count: number | null | undefined; alert?: boolean }) {
  if (!count) return null;
  return (
    <span
      className={cn(
        'min-w-4 h-4 px-1 rounded-full text-white text-[10px] font-semibold inline-flex items-center justify-center font-mono',
        alert ? 'bg-rose-600' : 'bg-purple-600',
      )}
    >
      {count > 99 ? '99+' : count}
    </span>
  );
}
