import type { ReactNode } from 'react';
import { PowerOff } from 'lucide-react';
import { ApiError, errorMessage } from '../../api/errors';
import { cn } from '../../lib/cn';
import type { ChangeDirection, Meter, PriceUnit } from '../../types';

// 价格审阅页面（调价审批、待上架）共用的小组件与文案。

export const METER_LABELS: Record<Meter, string> = {
  input: '输入',
  output: '输出',
  input_cache_read: '缓存读取',
  input_cache_write: '缓存写入',
  output_reasoning: '推理输出',
  request: '按次',
};

export const UNIT_LABELS: Record<PriceUnit, string> = {
  per_1m_tokens: '/ 百万 tokens',
  per_request: '/ 次',
  per_image: '/ 张',
  per_second: '/ 秒',
};

export function meterLabel(m: string): string {
  return METER_LABELS[m as Meter] ?? m;
}

export function unitLabel(u: string): string {
  return UNIT_LABELS[u as PriceUnit] ?? u;
}

const DIRECTION_STYLE: Record<ChangeDirection, { icon: string; className: string; label: string }> = {
  up: { icon: '▲', className: 'text-rose-600', label: '涨价' },
  down: { icon: '▼', className: 'text-emerald-600', label: '降价' },
  mixed: { icon: '⇅', className: 'text-amber-600', label: '涨跌互现' },
  new: { icon: '✦', className: 'text-blue-600', label: '新增' },
  removed: { icon: '✕', className: 'text-gray-400', label: '移除' },
};

export function DirectionIcon({ direction, className }: { direction: string; className?: string }) {
  const s = DIRECTION_STYLE[direction as ChangeDirection] ?? DIRECTION_STYLE.mixed;
  return (
    <span className={cn('font-mono text-xs', s.className, className)} title={s.label}>
      {s.icon}
    </span>
  );
}

// 变化率：带符号百分比；涨 rose、跌 emerald（成本涨是坏事）
export function RatioText({ ratio, className }: { ratio: string | null | undefined; className?: string }) {
  if (ratio === null || ratio === undefined || ratio === '') return <span className="text-gray-400 font-mono">—</span>;
  const n = Number(ratio);
  if (!Number.isFinite(n)) return <span className="text-gray-400 font-mono">—</span>;
  return (
    <span className={cn('font-mono', n > 0 ? 'text-rose-600' : n < 0 ? 'text-emerald-600' : 'text-gray-500', className)}>
      {n > 0 ? '+' : ''}
      {(n * 100).toFixed(1)}%
    </span>
  );
}

// 毛利率：负数 rose
export function MarginText({ ratio, className }: { ratio: string | number | null | undefined; className?: string }) {
  if (ratio === null || ratio === undefined || ratio === '') return <span className="text-gray-400 font-mono">—</span>;
  const n = typeof ratio === 'number' ? ratio : Number(ratio);
  if (!Number.isFinite(n)) return <span className="text-gray-400 font-mono">—</span>;
  return <span className={cn('font-mono', n < 0 ? 'text-rose-600 font-semibold' : 'text-gray-900', className)}>{(n * 100).toFixed(1)}%</span>;
}

// 价格同步未装配时后端统一返回 503 not_implemented（接口方案 §0.5）：显示说明卡而不是报错
export function isNotConfigured(err: unknown): boolean {
  return err instanceof ApiError && (err.status === 503 || err.code === 'not_implemented');
}

export function PriceSyncDisabledCard() {
  return (
    <div className="bg-gray-50 border border-dashed border-gray-200 rounded-xl p-6 text-center">
      <div className="flex justify-center text-gray-300 mb-2">
        <PowerOff className="w-8 h-8" />
      </div>
      <div className="text-xs font-medium text-gray-700">价格同步服务未启用</div>
      <div className="text-[11px] text-gray-400 mt-1 max-w-md mx-auto">
        当前 cmd/admin 没有装配价格同步引擎（PriceSync），调价审批与待上架队列不可用。请联系后端在部署配置中启用后再使用。
      </div>
    </div>
  );
}

// toastError 的第二行显示后端原文，方便运营把问题转述给研发
export function errorDetail(err: unknown): ReactNode {
  if (err instanceof ApiError) {
    const parts = [err.detail && err.detail !== err.message ? err.detail : '', err.requestId ? `request_id: ${err.requestId}` : ''].filter(Boolean);
    return parts.length ? parts.join(' · ') : undefined;
  }
  return undefined;
}

export function friendlyError(err: unknown, fallback = '操作失败'): string {
  if (err instanceof ApiError && err.status === 409) {
    return `${errorMessage(err, fallback)}（可能已被其他人处理，请刷新后重试）`;
  }
  return errorMessage(err, fallback);
}

