import { useMemo, useState, type ComponentType } from 'react';
import { Link, useNavigate } from 'react-router';
import { Activity, AlertTriangle, ArrowRight, BadgeDollarSign, CircleCheck, PackagePlus, PartyPopper, Scale, TicketX } from 'lucide-react';
import { listAuditLogs } from '../api/audit';
import { getStatsOverview, getUsage } from '../api/stats';
import { Card, DataState, KpiStrip, PageHeader, SectionTitle, SegmentedToggle, StatCard } from '../components/ui';
import { StackedBars, type StackedBucket } from '../components/stats/StackedBars';
import { PointTooltip } from '../components/stats/UsageTrend';
import { bucketLabel, fillBuckets, formatMs, pointDelta, rangeBounds, relativeDelta } from '../components/stats/metrics';
import { useAsync } from '../hooks/useAsync';
import { useTodoCounts } from '../hooks/useTodoCounts';
import { actionLabel, actorLabel, targetHref, targetLabel } from '../lib/audit';
import { cn } from '../lib/cn';
import { formatCompact, formatMicroCompact, formatRatio } from '../lib/money';
import { formatDateTime, formatRelative } from '../lib/time';
import type { Metrics, UsagePoint } from '../types';

// 工作台（UI_DESIGN.md §4）：今天有什么要处理？平台运转正常吗？钱赚得怎么样？
// 各数据块独立加载（互不阻塞）；统计接口在服务端缓存 60 秒。

type DashRange = 'today' | '7d' | '30d';
const RANGE_OPTIONS: Array<{ value: DashRange; label: string }> = [
  { value: 'today', label: '今日' },
  { value: '7d', label: '7 天' },
  { value: '30d', label: '30 天' },
];

// 渠道健康阈值：错误率 > 5% 或 P95 > 5 秒视为异常
const HEALTH_ERROR_RATE = 0.05;
const HEALTH_P95_MS = 5000;

export default function DashboardPage() {
  const [range, setRange] = useState<DashRange>('7d');
  const bounds = useMemo(() => rangeBounds(range), [range]);
  const interval = range === 'today' ? 'hour' : 'day';

  return (
    <div className="space-y-6">
      <PageHeader
        title="工作台"
        description="今天有什么要处理？平台运转正常吗？钱赚得怎么样？"
        actions={<SegmentedToggle options={RANGE_OPTIONS} value={range} onChange={setRange} />}
      />
      <TodoStrip />
      <KpiSection from={bounds.from} to={bounds.to} />
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
        <div className="lg:col-span-2">
          <TrendSection from={bounds.from} to={bounds.to} interval={interval} />
        </div>
        <TopModels from={bounds.from} to={bounds.to} />
      </div>
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        <ChannelHealth from={bounds.from} to={bounds.to} />
        <RecentActions />
      </div>
    </div>
  );
}

// ---------- 待办 ----------

interface Todo {
  to: string;
  icon: ComponentType<{ className?: string }>;
  text: string;
  n: number;
  alert: boolean;
}

function TodoStrip() {
  const c = useTodoCounts();
  const todos: Todo[] | null = c
    ? [
        {
          to: '/pricing/changes',
          icon: Scale,
          text: `${c.price_changes_pending + c.price_changes_blocked} 个调价待审批${c.price_changes_blocked > 0 ? `（${c.price_changes_blocked} 个被拦截）` : ''}`,
          n: c.price_changes_pending + c.price_changes_blocked,
          alert: c.price_changes_blocked > 0,
        },
        { to: '/pricing/listings', icon: PackagePlus, text: `${c.listings_pending} 个新模型待上架`, n: c.listings_pending, alert: false },
        { to: '/channels?margin=negative', icon: AlertTriangle, text: `${c.channels_negative_margin} 个渠道负毛利`, n: c.channels_negative_margin, alert: true },
        { to: '/channels?missing_cost=true', icon: TicketX, text: `${c.channels_missing_cost} 个渠道未设成本价`, n: c.channels_missing_cost, alert: false },
        {
          to: '/models?missing=sell_price',
          icon: BadgeDollarSign,
          text: `${c.models_missing_sell_price} 个模型缺售价`,
          n: c.models_missing_sell_price,
          alert: true,
        },
      ].filter((t) => t.n > 0)
    : null;

  return (
    <Card padding="p-4">
      <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold mb-2">待办</div>
      {todos === null ? (
        <div className="text-xs text-gray-400">正在加载待办…</div>
      ) : todos.length === 0 ? (
        // 数量为 0 时明确告诉运营"真的没事"，而不是隐藏整块（§4）
        <div className="flex items-center gap-2 text-xs text-gray-600">
          <PartyPopper className="w-4 h-4 text-purple-600" />
          暂无待办
        </div>
      ) : (
        <div className="flex flex-wrap gap-3">
          {todos.map((t) => (
            <Link
              key={t.to}
              to={t.to}
              className={cn(
                'flex items-center gap-2 px-3 py-2 rounded-lg border text-xs transition-colors',
                t.alert ? 'bg-rose-50 border-rose-200 text-rose-700 hover:border-rose-300' : 'bg-purple-50/60 border-purple-100 text-purple-700 hover:border-purple-200',
              )}
            >
              <t.icon className="w-3.5 h-3.5" />
              <span className="font-medium">{t.text}</span>
              <ArrowRight className="w-3 h-3" />
            </Link>
          ))}
        </div>
      )}
    </Card>
  );
}

// ---------- KPI ----------

function KpiSection({ from, to }: { from: string; to: string }) {
  const overview = useAsync((signal) => getStatsOverview({ from, to }, signal), [from, to]);
  const o = overview.data;
  const hint = o
    ? `对比区间：${formatDateTime(new Date(Date.parse(o.from) - (Date.parse(o.to) - Date.parse(o.from))).toISOString())} ~ ${formatDateTime(o.from)}`
    : undefined;
  const cur = o?.current;
  const prev = o?.previous;
  return (
    <DataState loading={overview.loading} error={overview.error} onRetry={overview.reload} skeleton="cards">
      {cur && prev && (
        <KpiStrip cols={6}>
          <StatCard
            label="请求数"
            value={<span className="font-mono">{formatCompact(cur.requests)}</span>}
            sub={`上期 ${formatCompact(prev.requests)}`}
            delta={relativeDelta(cur.requests, prev.requests)}
            deltaHint={hint}
          />
          <StatCard
            primary
            label="收入（售价）"
            value={<span className="font-mono">{formatMicroCompact(cur.revenue_micro)}</span>}
            sub={`上期 ${formatMicroCompact(prev.revenue_micro)}`}
            delta={relativeDelta(cur.revenue_micro, prev.revenue_micro)}
            deltaHint={hint}
          />
          <StatCard
            label="成本"
            value={<span className="font-mono">{formatMicroCompact(cur.cost_micro)}</span>}
            sub={`上期 ${formatMicroCompact(prev.cost_micro)}`}
            delta={relativeDelta(cur.cost_micro, prev.cost_micro)}
            goodWhenUp={false}
            deltaHint={hint}
          />
          <StatCard
            label="毛利率"
            value={<span className="font-mono">{formatRatio(cur.gross_margin)}</span>}
            sub={`上期 ${formatRatio(prev.gross_margin)}`}
            delta={pointDelta(cur.gross_margin, prev.gross_margin)}
            deltaUnit="pp"
            deltaHint={hint ? `${hint}（差值，百分点）` : undefined}
            warning={cur.gross_margin !== null && Number(cur.gross_margin) < 0}
          />
          <StatCard
            label="错误率"
            value={<span className="font-mono">{formatRatio(cur.error_rate, 2)}</span>}
            sub={`上期 ${formatRatio(prev.error_rate, 2)}`}
            delta={pointDelta(cur.error_rate, prev.error_rate)}
            deltaUnit="pp"
            goodWhenUp={false}
            deltaHint={hint ? `${hint}（差值，百分点）` : undefined}
            warning={cur.error_rate !== null && Number(cur.error_rate) > HEALTH_ERROR_RATE}
          />
          <StatCard
            label="P95 延迟"
            value={<span className="font-mono">{formatMs(cur.p95_latency_ms)}</span>}
            sub={`上期 ${formatMs(prev.p95_latency_ms)}`}
            delta={relativeDelta(cur.p95_latency_ms, prev.p95_latency_ms)}
            goodWhenUp={false}
            deltaHint={hint}
          />
        </KpiStrip>
      )}
    </DataState>
  );
}

// ---------- 收入 / 成本趋势 ----------

function TrendSection({ from, to, interval }: { from: string; to: string; interval: 'hour' | 'day' }) {
  const navigate = useNavigate();
  const usage = useAsync((signal) => getUsage({ from, to, interval, group_by: 'none' }, signal), [from, to, interval]);

  const buckets: StackedBucket[] = useMemo(() => {
    const byBucket = new Map<string, UsagePoint>();
    for (const p of usage.data?.series ?? []) byBucket.set(p.bucket, p);
    return fillBuckets(interval, from, to).map((b) => {
      const p = byBucket.get(b);
      return {
        key: b,
        label: bucketLabel(b),
        segments: [
          { key: 'cost', value: p?.cost_micro ?? 0, className: 'bg-gray-300' },
          { key: 'profit', value: p?.gross_profit_micro ?? 0, className: 'bg-emerald-500' },
        ],
        tooltip: <PointTooltip bucket={b} m={p} />,
      };
    });
  }, [usage.data, from, to, interval]);

  const openDay = (b: StackedBucket) => {
    const day = b.key.slice(0, 10);
    // 当天按小时拆分
    const q = new URLSearchParams({ range: 'custom', from: day, to: day, interval: 'hour' });
    navigate(`/analytics?${q.toString()}`);
  };

  return (
    <div>
      <SectionTitle actions={<Link to="/analytics" className="text-xs text-purple-600 hover:text-purple-700">用量分析 →</Link>}>
        收入 / 成本趋势
      </SectionTitle>
      <Card>
        <DataState loading={usage.loading} error={usage.error} onRetry={usage.reload} skeleton="text">
          <StackedBars
            buckets={buckets}
            format={formatMicroCompact}
            onBucketClick={openDay}
            legend={[
              { label: '毛利', className: 'bg-emerald-500' },
              { label: '成本', className: 'bg-gray-300' },
            ]}
          />
          <p className="mt-2 text-[11px] text-gray-400">柱高 = 收入（成本 + 毛利）；时间按 UTC 分桶，点击柱子查看当天拆分。</p>
        </DataState>
      </Card>
    </div>
  );
}

// ---------- Top 模型 ----------

function TopModels({ from, to }: { from: string; to: string }) {
  const usage = useAsync(
    (signal) => getUsage({ from, to, interval: 'none', group_by: 'virtual_model', top: 5, order_by: 'revenue_micro' }, signal),
    [from, to],
  );
  const groups = (usage.data?.groups ?? []).filter((g) => g.key !== '__other__');
  const max = Math.max(...groups.map((g) => g.totals.revenue_micro), 0);
  return (
    <div>
      <SectionTitle actions={<Link to="/analytics?dim=virtual_model&metric=revenue" className="text-xs text-purple-600 hover:text-purple-700">更多 →</Link>}>
        收入 Top 模型
      </SectionTitle>
      <Card padding="p-0">
        <DataState
          loading={usage.loading}
          error={usage.error}
          onRetry={usage.reload}
          empty={groups.length === 0}
          emptyTitle="所选时间范围内没有收入"
          skeleton="text"
        >
          <ol className="divide-y divide-gray-100">
            {groups.map((g, i) => (
              <li key={g.key}>
                <Link to={`/models?q=${encodeURIComponent(g.key)}`} className="block px-4 py-3 hover:bg-gray-50/70 transition-colors">
                  <div className="flex items-center gap-3 text-xs">
                    <span className={cn('w-5 text-center font-mono font-bold', i === 0 ? 'text-purple-600' : 'text-gray-400')}>{i + 1}</span>
                    <span className="flex-1 min-w-0 truncate text-gray-900" title={g.key}>
                      {g.label}
                    </span>
                    <span className="font-mono text-gray-900">{formatMicroCompact(g.totals.revenue_micro)}</span>
                  </div>
                  <div className="ml-8 mt-1.5 flex items-center gap-2">
                    <div className="flex-1 h-1.5 rounded-full bg-gray-100 overflow-hidden">
                      <div className="h-full bg-purple-500 rounded-full" style={{ width: `${max > 0 ? (g.totals.revenue_micro / max) * 100 : 0}%` }} />
                    </div>
                    <span className="text-[11px] text-gray-400 font-mono w-20 text-right">毛利 {formatRatio(g.totals.gross_margin)}</span>
                  </div>
                </Link>
              </li>
            ))}
          </ol>
        </DataState>
      </Card>
    </div>
  );
}

// ---------- 渠道健康 ----------

function isUnhealthy(m: Metrics): boolean {
  return (m.error_rate !== null && Number(m.error_rate) > HEALTH_ERROR_RATE) || (m.p95_latency_ms !== null && m.p95_latency_ms > HEALTH_P95_MS);
}

function ChannelHealth({ from, to }: { from: string; to: string }) {
  const usage = useAsync(
    (signal) => getUsage({ from, to, interval: 'none', group_by: 'channel', top: 20, order_by: 'errors' }, signal),
    [from, to],
  );
  const bad = (usage.data?.groups ?? []).filter((g) => g.key !== '__other__' && g.key !== '' && isUnhealthy(g.totals));
  return (
    <div>
      <SectionTitle actions={<span className="text-[11px] text-gray-400">错误率 &gt; 5% 或 P95 &gt; 5s</span>}>渠道健康</SectionTitle>
      <Card padding="p-0">
        <DataState loading={usage.loading} error={usage.error} onRetry={usage.reload} skeleton="text">
          {bad.length === 0 ? (
            <div className="px-4 py-6 flex items-center justify-center gap-2 text-xs text-gray-600">
              <CircleCheck className="w-4 h-4 text-emerald-600" />
              全部渠道正常
            </div>
          ) : (
            <ul className="divide-y divide-gray-100">
              {bad.map((g) => {
                const err = g.totals.error_rate !== null && Number(g.totals.error_rate) > HEALTH_ERROR_RATE;
                return (
                  <li key={g.key}>
                    <Link to={`/channels/${g.key}`} className="px-4 py-2.5 flex items-center gap-3 text-xs hover:bg-gray-50/70">
                      <span className={cn('w-2 h-2 rounded-full shrink-0', err ? 'bg-rose-500' : 'bg-amber-500')} />
                      <span className="font-mono text-gray-400 w-10">#{g.key}</span>
                      <span className="flex-1 min-w-0 truncate text-gray-900">{g.label}</span>
                      <span className={cn('font-mono', err ? 'text-rose-700' : 'text-gray-500')}>错误率 {formatRatio(g.totals.error_rate, 1)}</span>
                      <span className={cn('font-mono w-24 text-right', !err ? 'text-amber-700' : 'text-gray-500')}>P95 {formatMs(g.totals.p95_latency_ms)}</span>
                    </Link>
                  </li>
                );
              })}
            </ul>
          )}
        </DataState>
      </Card>
      <p className="mt-1.5 text-[11px] text-gray-400">
        <Activity className="inline w-3 h-3 mr-1 -mt-0.5" />
        基于调用日志统计；熔断器的实时状态暂无接口（后端 G9）。
      </p>
    </div>
  );
}

// ---------- 最近操作 ----------

function RecentActions() {
  const recent = useAsync((signal) => listAuditLogs({ limit: 8 }, signal), []);
  return (
    <div>
      <SectionTitle actions={<Link to="/audit" className="text-xs text-purple-600 hover:text-purple-700">全部审计日志 →</Link>}>最近操作</SectionTitle>
      <DataState
        loading={recent.loading}
        error={recent.error}
        onRetry={recent.reload}
        empty={recent.data?.data.length === 0}
        emptyTitle="暂无操作记录"
        skeleton="text"
      >
        <Card padding="p-0">
          <ul className="divide-y divide-gray-100">
            {recent.data?.data.map((e) => {
              const href = targetHref(e.target_type, e.target_id);
              return (
                <li key={e.id} className="px-4 py-2.5 flex items-center gap-3 text-xs">
                  <span className="w-16 shrink-0 text-gray-400 text-[11px]" title={formatDateTime(e.created_at)}>
                    {formatRelative(e.created_at)}
                  </span>
                  <span className="text-gray-900 font-medium shrink-0">{actorLabel(e)}</span>
                  <span className="text-gray-600 shrink-0">{actionLabel(e.action)}</span>
                  {href ? (
                    <Link to={href} className="text-purple-600 hover:text-purple-700 truncate">
                      {targetLabel(e.target_type, e.target_id)}
                    </Link>
                  ) : (
                    <span className="text-gray-400 truncate">{targetLabel(e.target_type, e.target_id)}</span>
                  )}
                </li>
              );
            })}
          </ul>
        </Card>
      </DataState>
    </div>
  );
}
