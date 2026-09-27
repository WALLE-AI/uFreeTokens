import type { ReactNode } from 'react';
import { cn } from '../../lib/cn';

export interface StackSegment {
  key: string;
  value: number;
  className: string;
}

export interface StackedBucket {
  key: string; // 原始桶值，点击回调用
  label: string; // 轴标签
  segments: StackSegment[];
  tooltip?: ReactNode;
}

export interface LegendItem {
  label: string;
  className: string;
}

// StackedBars：沿用 TrendBars 的 div 柱写法（flex items-end + rounded-t + group-hover 深色浮层），
// 每根柱子由多段叠加（RankingsPage 堆叠柱图的做法）。负值按 0 处理。
export function StackedBars({
  buckets,
  format,
  height = 'h-40',
  onBucketClick,
  legend,
}: {
  buckets: StackedBucket[];
  format: (v: number) => string;
  height?: string;
  onBucketClick?: (b: StackedBucket) => void;
  legend?: LegendItem[];
}) {
  if (buckets.length === 0) {
    return <div className={cn('flex items-center justify-center text-xs text-gray-400', height)}>暂无数据</div>;
  }
  const totals = buckets.map((b) => b.segments.reduce((s, x) => s + Math.max(x.value, 0), 0));
  const max = Math.max(...totals, 0);
  const every = Math.max(1, Math.ceil(buckets.length / 8)); // 轴标签最多约 8 个
  return (
    <div>
      {legend && legend.length > 0 && (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 mb-3 text-[11px] text-gray-500">
          {legend.map((l) => (
            <span key={l.label} className="inline-flex items-center gap-1.5 min-w-0">
              <span className={cn('w-2.5 h-2.5 rounded-sm shrink-0', l.className)} />
              <span className="truncate max-w-48">{l.label}</span>
            </span>
          ))}
        </div>
      )}
      <div className="flex items-start gap-2">
        <div className={cn('flex flex-col justify-between text-[10px] font-mono text-gray-400 text-right w-14 shrink-0', height)}>
          <span>{format(max)}</span>
          <span>0</span>
        </div>
        <div className={cn('relative flex-1 flex items-end gap-1 border-b border-gray-200', height)}>
          <div className="absolute inset-x-0 top-0 border-t border-dashed border-gray-100 pointer-events-none" />
          <div className="absolute inset-x-0 top-1/2 border-t border-dashed border-gray-100 pointer-events-none" />
          {buckets.map((b, i) => {
            const total = totals[i];
            const pct = max > 0 ? Math.max((total / max) * 100, total > 0 ? 2 : 0) : 0;
            return (
              <div
                key={b.key}
                className={cn('relative flex-1 h-full flex items-end group min-w-0', onBucketClick && 'cursor-pointer')}
                onClick={onBucketClick ? () => onBucketClick(b) : undefined}
              >
                <div className="w-full flex flex-col-reverse rounded-t overflow-hidden transition-opacity group-hover:opacity-80" style={{ height: `${pct}%` }}>
                  {total > 0 &&
                    b.segments.map((s) => (
                      <div key={s.key} className={s.className} style={{ height: `${(Math.max(s.value, 0) / total) * 100}%` }} />
                    ))}
                </div>
                <div className="absolute bottom-full left-1/2 -translate-x-1/2 mb-1 opacity-0 group-hover:opacity-100 pointer-events-none transition-opacity z-20 whitespace-nowrap bg-gray-900 text-white text-[11px] rounded-md px-2 py-1 shadow-lg">
                  {b.tooltip ?? (
                    <>
                      <div className="text-gray-300">{b.label}</div>
                      <div className="font-mono">{format(total)}</div>
                    </>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      </div>
      <div className="flex gap-1 text-[10px] text-gray-400 font-mono mt-1 pl-16">
        {buckets.map((b, i) => (
          <span key={b.key} className="flex-1 min-w-0 text-center truncate">
            {i % every === 0 ? b.label : ''}
          </span>
        ))}
      </div>
    </div>
  );
}

// 多序列配色（UI_DESIGN.md §11.2）：purple-600 → purple-400 → purple-200 → gray-300，"其他"用 gray-200。
// Top 8 超过 4 个序列时补充同色系的中间色阶，仍不引入新色相。
export const SERIES_COLORS = [
  'bg-purple-600',
  'bg-purple-400',
  'bg-purple-200',
  'bg-gray-300',
  'bg-purple-700',
  'bg-purple-500',
  'bg-purple-300',
  'bg-gray-400',
];
export const OTHER_COLOR = 'bg-gray-200';
