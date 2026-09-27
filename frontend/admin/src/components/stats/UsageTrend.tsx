import { useMemo, useState } from 'react';
import { Link } from 'react-router';
import { getUsage, type StatsFilterQuery } from '../../api/stats';
import { useAsync } from '../../hooks/useAsync';
import type { Metrics, UsagePoint } from '../../types';
import { Card, DataState, SegmentedToggle, TrendBars } from '../ui';
import { METRICS, bucketLabel, fillBuckets, formatMs, rangeBounds, type MetricKey } from './metrics';
import { formatCompact, formatMicroCompact, formatRatio } from '../../lib/money';

type TrendRange = '7d' | '30d';
type TrendMetric = Extract<MetricKey, 'requests' | 'revenue' | 'gross_profit' | 'error_rate'>;

const RANGE_OPTIONS: Array<{ value: TrendRange; label: string }> = [
  { value: '7d', label: '7 天' },
  { value: '30d', label: '30 天' },
];
const METRIC_OPTIONS: Array<{ value: TrendMetric; label: string }> = [
  { value: 'requests', label: '请求数' },
  { value: 'revenue', label: '收入' },
  { value: 'gross_profit', label: '毛利' },
  { value: 'error_rate', label: '错误率' },
];

// UsageTrend 是详情页里的"用量趋势"段落（UI_DESIGN.md §5.4 第 2 段）：按天柱状图 +
// 区间汇总。filter 决定统计范围，例如 {virtual_model: name}、{channel_id}、{account_id}。
export function UsageTrend({ filter, defaultMetric = 'requests' }: { filter: StatsFilterQuery; defaultMetric?: TrendMetric }) {
  const [range, setRange] = useState<TrendRange>('7d');
  const [metric, setMetric] = useState<TrendMetric>(defaultMetric);
  const bounds = useMemo(() => rangeBounds(range), [range]);
  const filterKey = JSON.stringify(filter);

  const usage = useAsync((signal) => getUsage({ ...filter, ...bounds, interval: 'day', group_by: 'none' }, signal), [filterKey, bounds.from, bounds.to]);

  const def = METRICS[metric];
  const points = useMemo(() => {
    const byBucket = new Map<string, UsagePoint>();
    for (const p of usage.data?.series ?? []) byBucket.set(p.bucket, p);
    return fillBuckets('day', bounds.from, bounds.to).map((b) => {
      const p = byBucket.get(b);
      return {
        label: bucketLabel(b),
        value: p ? (def.value(p) ?? 0) : 0,
        tooltip: <PointTooltip bucket={b} m={p} />,
      };
    });
  }, [usage.data, bounds.from, bounds.to, def]);

  const totals = usage.data?.totals;
  const params = new URLSearchParams(
    Object.entries(filter).flatMap(([k, v]) => (v === undefined || v === '' ? [] : [[k, String(v)]])),
  );

  return (
    <Card>
      <div className="flex flex-wrap items-center justify-between gap-2 mb-4">
        <SegmentedToggle options={METRIC_OPTIONS} value={metric} onChange={setMetric} />
        <div className="flex items-center gap-3">
          <SegmentedToggle options={RANGE_OPTIONS} value={range} onChange={setRange} />
          <Link to={`/logs?${params.toString()}`} className="text-xs text-purple-600 hover:text-purple-700">
            调用日志 →
          </Link>
        </div>
      </div>
      <DataState loading={usage.loading} error={usage.error} onRetry={usage.reload} skeleton="text">
        {totals && totals.requests === 0 ? (
          <div className="py-8 text-center text-xs text-gray-400">所选时间范围内没有调用记录</div>
        ) : (
          <>
            <TrendBars points={points} format={def.format} height="h-28" />
            {totals && <TotalsRow m={totals} />}
          </>
        )}
      </DataState>
    </Card>
  );
}

function TotalsRow({ m }: { m: Metrics }) {
  const items = [
    ['请求', formatCompact(m.requests)],
    ['错误率', formatRatio(m.error_rate, 2)],
    ['收入', formatMicroCompact(m.revenue_micro)],
    ['毛利率', formatRatio(m.gross_margin)],
    ['P95', formatMs(m.p95_latency_ms)],
  ];
  return (
    <div className="mt-4 pt-3 border-t border-gray-100 grid grid-cols-5 gap-2">
      {items.map(([label, value]) => (
        <div key={label}>
          <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold">{label}</div>
          <div className="font-mono text-gray-900 mt-0.5">{value}</div>
        </div>
      ))}
    </div>
  );
}

export function PointTooltip({ bucket, m }: { bucket: string; m: Metrics | undefined }) {
  return (
    <div className="space-y-0.5">
      <div className="text-gray-300">{bucket}</div>
      {m ? (
        <>
          <div className="font-mono">请求 {formatCompact(m.requests)} · 错误率 {formatRatio(m.error_rate, 2)}</div>
          <div className="font-mono">
            收入 {formatMicroCompact(m.revenue_micro)} · 成本 {formatMicroCompact(m.cost_micro)}
          </div>
          <div className="font-mono">毛利 {formatMicroCompact(m.gross_profit_micro)}</div>
        </>
      ) : (
        <div>无调用</div>
      )}
    </div>
  );
}
