import { useMemo } from 'react';
import { useNavigate } from 'react-router';
import { getUsage } from '../../api/stats';
import { Card, DataState, DataTable, PageHeader, SectionTitle, SegmentedToggle, type Column } from '../../components/ui';
import { OTHER_COLOR, SERIES_COLORS, StackedBars, type StackedBucket } from '../../components/stats/StackedBars';
import { PointTooltip } from '../../components/stats/UsageTrend';
import { METRICS, bucketLabel, fillBuckets, formatMs, rangeBounds, spanMs, type MetricKey } from '../../components/stats/metrics';
import { useAsync } from '../../hooks/useAsync';
import { useQueryParams } from '../../hooks/useQueryState';
import { cn } from '../../lib/cn';
import { formatCompact, formatMicroCompact, formatRatio } from '../../lib/money';
import type { UsageGroup, UsageGroupBy, UsagePoint } from '../../types';
import { DraftInput } from './filters';

// 用量分析（UI_DESIGN.md §5.6 透视页）：维度 × 指标 × 时间范围，全部选择存 URL，便于分享
// "上周 DeepSeek 渠道的毛利分析"这类视图。上半部分按时间堆叠（Top 8 + 其他），下半部分排名表。

type Dim = Exclude<UsageGroupBy, 'none'>;
type AnaRange = '7d' | '30d' | 'custom';

const DIM_OPTIONS: Array<{ value: Dim; label: string }> = [
  { value: 'virtual_model', label: '按模型' },
  { value: 'channel', label: '按渠道' },
  { value: 'provider', label: '按供应商' },
  { value: 'account', label: '按账户' },
  { value: 'api_key', label: '按 API Key' },
];
const METRIC_OPTIONS: Array<{ value: MetricKey; label: string }> = (Object.keys(METRICS) as MetricKey[]).map((k) => ({ value: k, label: METRICS[k].label }));
const RANGE_OPTIONS: Array<{ value: AnaRange; label: string }> = [
  { value: '7d', label: '7 天' },
  { value: '30d', label: '30 天' },
  { value: 'custom', label: '自定义' },
];
const DAY = 86400_000;
const OTHER = '__other__';

function targetHref(dim: Dim, key: string): string | null {
  if (!key || key === OTHER) return null;
  switch (dim) {
    case 'virtual_model':
      return `/models?q=${encodeURIComponent(key)}`;
    case 'channel':
      return `/channels/${key}`;
    case 'provider':
      return `/providers/${key}`;
    case 'account':
      return `/accounts/${key}`;
    case 'api_key':
      return `/logs?api_key_id=${key}`;
  }
}

export default function AnalyticsPage() {
  const navigate = useNavigate();
  const [params, setParams] = useQueryParams();
  const dim = (DIM_OPTIONS.some((o) => o.value === params.dim) ? params.dim : 'virtual_model') as Dim;
  const metric = (params.metric && params.metric in METRICS ? params.metric : 'revenue') as MetricKey;
  const range = (RANGE_OPTIONS.some((o) => o.value === params.range) ? params.range : '7d') as AnaRange;
  const def = METRICS[metric];

  const bounds = useMemo(() => rangeBounds(range, { from: params.from, to: params.to }), [range, params.from, params.to]);
  const span = spanMs(bounds.from, bounds.to);
  const hourAllowed = span <= 7 * DAY;
  const interval = params.interval === 'hour' && hourAllowed ? 'hour' : 'day';
  const rangeError = !Number.isFinite(span) || span <= 0 ? '时间范围无效：结束日期需不早于开始日期' : span > 90 * DAY ? '时间范围最长 90 天' : null;

  // 可叠加指标按维度堆叠；错误率、P95 不能按分组相加，趋势图改为查询整体（group_by=none）的精确值
  const chart = useAsync(
    (signal) =>
      rangeError
        ? Promise.resolve(null)
        : getUsage(
            def.additive
              ? { ...bounds, interval, group_by: dim, top: 8, order_by: def.orderBy }
              : { ...bounds, interval, group_by: 'none' },
            signal,
          ),
    [bounds.from, bounds.to, interval, dim, metric, rangeError],
  );
  const ranking = useAsync(
    (signal) => (rangeError ? Promise.resolve(null) : getUsage({ ...bounds, interval: 'none', group_by: dim, top: 50, order_by: def.orderBy }, signal)),
    [bounds.from, bounds.to, dim, metric, rangeError],
  );

  // ---------- 趋势图 ----------
  const { buckets, legend } = useMemo(() => {
    const data = chart.data;
    if (!data) return { buckets: [] as StackedBucket[], legend: [] };
    const bucketKeys = fillBuckets(interval, bounds.from, bounds.to);
    if (!def.additive) {
      const byBucket = new Map<string, UsagePoint>(data.series.map((p) => [p.bucket, p]));
      return {
        buckets: bucketKeys.map((b) => {
          const p = byBucket.get(b);
          return {
            key: b,
            label: bucketLabel(b),
            segments: [{ key: 'v', value: p ? (def.value(p) ?? 0) : 0, className: 'bg-purple-500' }],
            tooltip: <PointTooltip bucket={b} m={p} />,
          };
        }),
        legend: [{ label: `${def.label}（整体）`, className: 'bg-purple-500' }],
      };
    }
    const groups = data.groups.map((g) => g.key);
    const color = (key: string, i: number) => (key === OTHER ? OTHER_COLOR : SERIES_COLORS[i % SERIES_COLORS.length]);
    const byBucket = new Map<string, Map<string, UsagePoint>>();
    for (const p of data.series) {
      if (!byBucket.has(p.bucket)) byBucket.set(p.bucket, new Map());
      byBucket.get(p.bucket)!.set(p.group, p);
    }
    return {
      buckets: bucketKeys.map((b) => {
        const row = byBucket.get(b);
        const segs = groups.map((g, i) => ({ key: g, value: row?.get(g) ? (def.value(row.get(g)!) ?? 0) : 0, className: color(g, i) }));
        return {
          key: b,
          label: bucketLabel(b),
          segments: segs,
          tooltip: (
            <div className="space-y-0.5">
              <div className="text-gray-300">{b}</div>
              {segs
                .filter((s) => s.value > 0)
                .sort((x, y) => y.value - x.value)
                .slice(0, 6)
                .map((s) => (
                  <div key={s.key} className="flex items-center gap-1.5 font-mono">
                    <span className={cn('w-2 h-2 rounded-sm', s.className)} />
                    <span className="truncate max-w-40">{data.groups.find((g) => g.key === s.key)?.label ?? s.key}</span>
                    <span className="ml-auto pl-2">{def.format(s.value)}</span>
                  </div>
                ))}
              {segs.every((s) => s.value <= 0) && <div>无数据</div>}
            </div>
          ),
        };
      }),
      legend: data.groups.map((g, i) => ({ label: g.label, className: color(g.key, i) })),
    };
  }, [chart.data, interval, bounds.from, bounds.to, def]);

  // ---------- 排名表 ----------
  const sort = params.sort || `-${metric}`;
  const rows = useMemo(() => {
    const all = ranking.data?.groups ?? [];
    const main = all.filter((g) => g.key !== OTHER);
    const other = all.filter((g) => g.key === OTHER);
    const field = sort.replace(/^-/, '') as MetricKey;
    const sdef = METRICS[field] ?? def;
    const desc = sort.startsWith('-');
    const val = (g: UsageGroup) => sdef.value(g.totals);
    main.sort((a, b) => {
      const va = val(a);
      const vb = val(b);
      if (va === null) return 1;
      if (vb === null) return -1;
      return desc ? vb - va : va - vb;
    });
    return [...main, ...other];
  }, [ranking.data, sort, def]);
  const shareTotal = def.additive ? rows.reduce((s, g) => s + Math.max(def.value(g.totals) ?? 0, 0), 0) : 0;

  const columns: Column<UsageGroup>[] = [
    {
      key: 'name',
      header: DIM_OPTIONS.find((o) => o.value === dim)!.label.replace('按', ''),
      render: (g) => (
        <div className="min-w-0">
          <div className={cn('truncate max-w-72', g.key === OTHER ? 'text-gray-400' : 'text-gray-900')}>{g.label}</div>
          {g.key !== OTHER && g.key !== g.label && <div className="font-mono text-[11px] text-gray-400 truncate max-w-72">{g.key || '（无）'}</div>}
          {g.key === OTHER && <div className="text-[11px] text-gray-400">仅请求数 / 收入 / 成本为汇总值</div>}
        </div>
      ),
    },
    { key: 'requests', header: '请求数', numeric: true, sortable: true, render: (g) => formatCompact(g.totals.requests) },
    { key: 'tokens', header: 'Tokens', numeric: true, sortable: true, render: (g) => formatCompact(g.totals.input_tokens + g.totals.output_tokens) },
    { key: 'revenue', header: '收入', numeric: true, sortable: true, render: (g) => formatMicroCompact(g.totals.revenue_micro) },
    { key: 'cost', header: '成本', numeric: true, sortable: true, render: (g) => formatMicroCompact(g.totals.cost_micro) },
    {
      key: 'gross_profit',
      header: '毛利（率）',
      numeric: true,
      sortable: true,
      render: (g) => (
        <span className={cn(g.totals.gross_profit_micro < 0 && 'text-rose-700')}>
          {formatMicroCompact(g.totals.gross_profit_micro)}
          <span className="text-gray-400"> · {formatRatio(g.totals.gross_margin)}</span>
        </span>
      ),
    },
    {
      key: 'error_rate',
      header: '错误率',
      numeric: true,
      sortable: true,
      render: (g) => (
        <span className={cn(g.totals.error_rate !== null && Number(g.totals.error_rate) > 0.05 && 'text-rose-700')}>{formatRatio(g.totals.error_rate, 2)}</span>
      ),
    },
    { key: 'p95', header: 'P95', numeric: true, sortable: true, render: (g) => formatMs(g.totals.p95_latency_ms) },
    ...(def.additive
      ? [
          {
            key: 'share',
            header: `${def.label}占比`,
            width: 'w-40',
            render: (g: UsageGroup) => {
              const v = Math.max(def.value(g.totals) ?? 0, 0);
              const pct = shareTotal > 0 ? (v / shareTotal) * 100 : 0;
              return (
                <div className="flex items-center gap-2">
                  <div className="flex-1 h-1.5 rounded-full bg-gray-100 overflow-hidden">
                    <div className="h-full bg-purple-500 rounded-full" style={{ width: `${pct}%` }} />
                  </div>
                  <span className="font-mono text-[11px] text-gray-500 w-10 text-right">{pct.toFixed(1)}%</span>
                </div>
              );
            },
          } satisfies Column<UsageGroup>,
        ]
      : []),
  ];

  const openBucket = (b: StackedBucket) => {
    if (interval === 'day') setParams({ range: 'custom', from: b.key, to: b.key, interval: 'hour' });
  };

  return (
    <div>
      <PageHeader title="用量分析" description="按维度拆分请求量、收入、成本与毛利；所有选择都保存在链接里，可直接分享" />

      <div className="flex flex-wrap items-center gap-2 mb-4">
        <SegmentedToggle options={DIM_OPTIONS} value={dim} onChange={(v) => setParams({ dim: v === 'virtual_model' ? null : v, sort: null })} />
        <SegmentedToggle options={METRIC_OPTIONS} value={metric} onChange={(v) => setParams({ metric: v === 'revenue' ? null : v, sort: null })} />
      </div>
      <div className="flex flex-wrap items-center gap-2 mb-6">
        <SegmentedToggle
          options={RANGE_OPTIONS}
          value={range}
          onChange={(v) => setParams({ range: v === '7d' ? null : v, from: null, to: null, interval: v === '30d' ? null : params.interval || null })}
        />
        {range === 'custom' && (
          <>
            <DraftInput type="date" mono={false} width="w-40" value={params.from ?? ''} placeholder="开始日期" onCommit={(v) => setParams({ from: v || null })} />
            <span className="text-gray-400 text-xs">至</span>
            <DraftInput type="date" mono={false} width="w-40" value={params.to ?? ''} placeholder="结束日期（含当天）" onCommit={(v) => setParams({ to: v || null })} />
          </>
        )}
        <SegmentedToggle
          options={[
            { value: 'day', label: '按天' },
            { value: 'hour', label: hourAllowed ? '按小时' : '按小时（≤7 天）' },
          ]}
          value={interval}
          onChange={(v) => hourAllowed && setParams({ interval: v === 'day' ? null : v })}
        />
        <span className="text-[11px] text-gray-400 ml-auto">时间按 UTC 分桶 · 统计结果缓存 60 秒</span>
      </div>

      {rangeError ? (
        <div className="bg-amber-50/80 border border-amber-200 rounded-xl p-4 text-xs text-amber-900">{rangeError}</div>
      ) : (
        <div className="space-y-6">
          <div>
            <SectionTitle>
              {def.label}趋势{def.additive ? `（Top 8 ${DIM_OPTIONS.find((o) => o.value === dim)!.label.replace('按', '')} + 其他）` : ''}
            </SectionTitle>
            <Card>
              <DataState loading={chart.loading} error={chart.error} onRetry={chart.reload} skeleton="text">
                <StackedBars buckets={buckets} legend={legend} format={def.format} height="h-48" onBucketClick={interval === 'day' ? openBucket : undefined} />
                {!def.additive && <p className="mt-2 text-[11px] text-gray-400">{def.label}不能按分组相加，趋势图显示整体值；各分组的值见下方排名表。</p>}
                {interval === 'day' && <p className="mt-2 text-[11px] text-gray-400">点击某一天查看当天按小时的拆分。</p>}
              </DataState>
            </Card>
          </div>

          <div>
            <SectionTitle>排名</SectionTitle>
            <DataState
              loading={ranking.loading}
              error={ranking.error}
              onRetry={ranking.reload}
              empty={rows.length === 0}
              emptyTitle="所选时间范围内没有调用记录"
            >
              <DataTable
                columns={columns}
                rows={rows}
                rowKey={(g) => g.key || '(none)'}
                sort={sort}
                onSortChange={(s) => setParams({ sort: s || null }, { keepPage: true })}
                onRowClick={(g) => {
                  const href = targetHref(dim, g.key);
                  if (href) navigate(href);
                }}
              />
            </DataState>
          </div>
        </div>
      )}
    </div>
  );
}
