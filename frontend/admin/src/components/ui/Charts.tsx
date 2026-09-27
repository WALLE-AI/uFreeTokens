import type { ReactNode } from 'react';
import { cn } from '../../lib/cn';

export interface TrendPoint {
  label: string; // 时间桶，如 "2026-09-26"
  value: number;
  tooltip?: ReactNode; // hover 浮层内容；缺省显示 label + formatted value
}

export interface TrendBarsProps {
  points: TrendPoint[];
  format?: (v: number) => string;
  height?: string; // Tailwind 高度类
  onBarClick?: (p: TrendPoint) => void;
  barClassName?: string;
}

// TrendBars：沿用 web 个人中心消费趋势的 div 柱（flex items-end + bg-purple-500 rounded-t），
// 补上 hover 浮层（RankingsPage 的 group-hover 深色浮层）、首尾日期和最大值刻度。
export function TrendBars({ points, format = (v) => v.toLocaleString('en-US'), height = 'h-32', onBarClick, barClassName = 'bg-purple-500' }: TrendBarsProps) {
  if (points.length === 0) {
    return <div className={cn('flex items-center justify-center text-xs text-gray-400', height)}>暂无数据</div>;
  }
  const max = Math.max(...points.map((p) => p.value), 0);
  return (
    <div>
      <div className="flex items-start gap-2">
        <div className={cn('flex flex-col justify-between text-[10px] font-mono text-gray-400 text-right w-12 shrink-0', height)}>
          <span>{format(max)}</span>
          <span>0</span>
        </div>
        <div className={cn('relative flex-1 flex items-end gap-1 border-b border-gray-200', height)}>
          <div className="absolute inset-x-0 top-0 border-t border-dashed border-gray-100 pointer-events-none" />
          <div className="absolute inset-x-0 top-1/2 border-t border-dashed border-gray-100 pointer-events-none" />
          {points.map((p) => {
            const pct = max > 0 ? Math.max((p.value / max) * 100, p.value > 0 ? 2 : 0) : 0;
            return (
              <div
                key={p.label}
                className={cn('relative flex-1 h-full flex items-end group', onBarClick && 'cursor-pointer')}
                onClick={onBarClick ? () => onBarClick(p) : undefined}
              >
                <div className={cn('w-full rounded-t transition-opacity group-hover:opacity-80', barClassName)} style={{ height: `${pct}%` }} />
                <div className="absolute bottom-full left-1/2 -translate-x-1/2 mb-1 opacity-0 group-hover:opacity-100 pointer-events-none transition-opacity z-20 whitespace-nowrap bg-gray-900 text-white text-[11px] rounded-md px-2 py-1 shadow-lg">
                  {p.tooltip ?? (
                    <>
                      <div className="text-gray-300">{p.label}</div>
                      <div className="font-mono">{format(p.value)}</div>
                    </>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      </div>
      <div className="flex justify-between text-[10px] text-gray-400 font-mono mt-1 pl-14">
        <span>{points[0].label}</span>
        {points.length > 1 && <span>{points[points.length - 1].label}</span>}
      </div>
    </div>
  );
}

// ShareBar：100% 横向占比条（RankingsPage 的占比条），用于渠道权重、用量占比
export function ShareBar({ segments }: { segments: Array<{ label: string; value: number; className?: string }> }) {
  const total = segments.reduce((s, x) => s + x.value, 0);
  const palette = ['bg-purple-600', 'bg-purple-400', 'bg-purple-200', 'bg-gray-300', 'bg-gray-200'];
  return (
    <div className="flex h-2 w-full rounded-full overflow-hidden bg-gray-100">
      {total > 0 &&
        segments.map((s, i) => (
          <div
            key={s.label}
            title={`${s.label}: ${((s.value / total) * 100).toFixed(1)}%`}
            className={s.className ?? palette[Math.min(i, palette.length - 1)]}
            style={{ width: `${(s.value / total) * 100}%` }}
          />
        ))}
    </div>
  );
}
