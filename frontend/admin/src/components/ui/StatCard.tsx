import type { ReactNode } from 'react';
import { cn } from '../../lib/cn';

export interface StatCardProps {
  label: ReactNode;
  value: ReactNode;
  sub?: ReactNode;
  primary?: boolean;
  // 环比变化：比率（0.123 = +12.3%）；goodWhenUp 决定涨是绿还是红（错误率、成本涨是坏事）
  delta?: number | null;
  goodWhenUp?: boolean;
  deltaHint?: string; // hover 显示对比区间
  deltaUnit?: 'percent' | 'pp'; // pp：比率类指标（错误率、毛利率）的差值，显示为"百分点"
  warning?: boolean; // "⚠ 异常计数"类卡片，数值用 amber
  onClick?: () => void;
}

// 对齐 web PersonalDashboardPage 的 KPI 卡（主卡 bg-purple-50/60，次卡 bg-gray-50），
// 增加环比与可点击（点击 = 一键加上对应筛选，UI_DESIGN.md §3.1）。
export function StatCard({ label, value, sub, primary, delta, goodWhenUp = true, deltaHint, deltaUnit, warning, onClick }: StatCardProps) {
  const clickable = !!onClick;
  return (
    <div
      role={clickable ? 'button' : undefined}
      tabIndex={clickable ? 0 : undefined}
      onClick={onClick}
      onKeyDown={clickable ? (e) => e.key === 'Enter' && onClick?.() : undefined}
      className={cn(
        'rounded-xl p-4 border transition-colors',
        primary ? 'bg-purple-50/60 border-purple-100' : 'bg-gray-50 border-gray-200',
        clickable && 'cursor-pointer hover:border-purple-200',
      )}
    >
      <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold">{label}</div>
      <div className="flex items-baseline gap-2 mt-1">
        <span className={cn('text-2xl font-bold font-mono', warning ? 'text-amber-700' : 'text-gray-900')}>{value}</span>
        {delta !== undefined && delta !== null && <DeltaTag delta={delta} goodWhenUp={goodWhenUp} hint={deltaHint} unit={deltaUnit} />}
      </div>
      {sub && <div className="text-[11px] text-gray-500 mt-1">{sub}</div>}
    </div>
  );
}

// DeltaTag：delta 为 0.123 表示 +12.3%（unit=percent，相对变化）或 +12.3 个百分点（unit=pp，比率差值）
export function DeltaTag({ delta, goodWhenUp = true, hint, unit = 'percent' }: { delta: number; goodWhenUp?: boolean; hint?: string; unit?: 'percent' | 'pp' }) {
  if (!Number.isFinite(delta)) return null;
  const up = delta > 0;
  const flat = delta === 0;
  const good = flat ? null : up === goodWhenUp;
  return (
    <span
      title={hint}
      className={cn(
        'text-[11px] font-medium font-mono',
        good === null ? 'text-gray-400' : good ? 'text-emerald-600' : 'text-rose-600',
      )}
    >
      {flat ? '—' : up ? '▲' : '▼'} {Math.abs(delta * 100).toFixed(1)}
      {unit === 'pp' ? 'pp' : '%'}
    </span>
  );
}

export function KpiStrip({ children, cols = 4 }: { children: ReactNode; cols?: 3 | 4 | 6 }) {
  const colClass = cols === 3 ? 'sm:grid-cols-3' : cols === 6 ? 'sm:grid-cols-3 lg:grid-cols-6' : 'sm:grid-cols-2 lg:grid-cols-4';
  return <div className={cn('grid grid-cols-1 gap-4 mb-6', colClass)}>{children}</div>;
}
