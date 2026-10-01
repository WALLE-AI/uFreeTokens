import { Fragment, useState } from 'react';
import { Link } from 'react-router';
import { ChevronDown, ChevronRight, RotateCw } from 'lucide-react';
import { getPriceComparison } from '../../api/pricing';
import { Button, DataState, FilterBar, PageHeader, Pagination, type ActiveFilter } from '../../components/ui';
import { useAsync } from '../../hooks/useAsync';
import { useQueryParams } from '../../hooks/useQueryState';
import { cn } from '../../lib/cn';
import { formatDateTime, formatRelative } from '../../lib/time';
import type { ChannelCost, MarketPrice, PriceComparisonRow } from '../../types';
import { MarginText, PriceSyncDisabledCard, isNotConfigured } from './shared';

// 比价看板（外部数据采集技术方案 §3.3）：每个虚拟模型的售价、各渠道成本价、各价格源的市场价。
// 价格均为每百万 tokens；"最低"按输入:输出 = 3:1 的混合单价（人民币）比较，与后端毛利口径一致。
// URL：?q=&page=&page_size=

const BLEND_IN = 3;
const BLEND_OUT = 1;

function n(v: string | null | undefined): number | null {
  if (v === null || v === undefined || v === '') return null;
  const x = Number(v);
  return Number.isFinite(x) ? x : null;
}

function blended(input: string | null, output: string | null): number | null {
  const i = n(input);
  const o = n(output);
  if (i === null && o === null) return null;
  return ((i ?? 0) * BLEND_IN + (o ?? 0) * BLEND_OUT) / (BLEND_IN + BLEND_OUT);
}

function cheapest<T>(items: T[] | null, key: (t: T) => number | null): T | null {
  let best: T | null = null;
  let bestV = Infinity;
  for (const it of items ?? []) {
    const v = key(it);
    if (v !== null && v < bestV) {
      best = it;
      bestV = v;
    }
  }
  return best;
}

function fmt(v: string | null | undefined): string {
  const x = n(v);
  if (x === null) return '—';
  return String(Number(x.toFixed(4)));
}

function sign(currency: string | null | undefined): string {
  return currency === 'CNY' ? '¥' : currency === 'USD' ? '$' : currency ? `${currency} ` : '';
}

// 输入 / 输出 两个价格并排
function PricePair({ input, output, currency, className }: { input: string | null; output: string | null; currency?: string | null; className?: string }) {
  if (n(input) === null && n(output) === null) return <span className="text-gray-400 font-mono">—</span>;
  const s = sign(currency);
  return (
    <span className={cn('font-mono whitespace-nowrap', className)}>
      {s}
      {fmt(input)} <span className="text-gray-300">/</span> {s}
      {fmt(output)}
    </span>
  );
}

// 售价 / 市场最低价：> 1 表示我们更贵（rose），< 1 表示更便宜（emerald）
function VsMarket({ ratio }: { ratio: string | null }) {
  const r = n(ratio);
  if (r === null) return <span className="text-gray-400 font-mono">—</span>;
  return (
    <span className={cn('font-mono', r > 1.0001 ? 'text-rose-600 font-semibold' : r < 0.9999 ? 'text-emerald-600' : 'text-gray-600')} title="售价 ÷ 市场最低价（3:1 混合单价）">
      ×{r.toFixed(2)}
    </span>
  );
}

const PAGE_SIZES = [20, 50, 100];

export default function PriceComparisonPage() {
  const [params, setParams] = useQueryParams();
  const page = Math.max(1, Number(params.page) || 1);
  const pageSize = PAGE_SIZES.includes(Number(params.page_size)) ? Number(params.page_size) : 20;
  const list = useAsync((signal) => getPriceComparison({ q: params.q || undefined, page, page_size: pageSize }, signal), [params.q, page, pageSize]);
  const rows = list.data?.data ?? [];
  const [expanded, setExpanded] = useState<Set<number>>(new Set());

  if (isNotConfigured(list.error)) {
    return (
      <>
        <PageHeader title="比价看板" description="售价、渠道成本与市场价对比" />
        <PriceSyncDisabledCard />
      </>
    );
  }

  const toggle = (id: number) =>
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  const active: ActiveFilter[] = params.q ? [{ key: 'q', label: `搜索: ${params.q}`, onRemove: () => setParams({ q: null }) }] : [];

  return (
    <>
      <PageHeader
        title="比价看板"
        description="每个虚拟模型的售价、我方各渠道成本价与各价格源最新市场价（每百万 tokens）。最低价按输入:输出 = 3:1 混合单价折人民币比较；负毛利标红。"
        actions={
          <Button icon={<RotateCw className="w-3.5 h-3.5" />} loading={list.refreshing} onClick={list.reload}>
            刷新
          </Button>
        }
      />

      <FilterBar search={params.q ?? ''} onSearch={(q) => setParams({ q })} searchPlaceholder="搜索虚拟模型名…" active={active} onClearAll={() => setParams({ q: null })} />

      <DataState loading={list.loading} error={list.error} onRetry={list.reload} empty={rows.length === 0} emptyTitle={params.q ? '没有符合条件的模型' : '还没有虚拟模型'}>
        <div className="overflow-x-auto bg-white border border-gray-200 rounded-xl shadow-xs">
          <table className="w-full text-xs">
            <thead>
              <tr className="bg-gray-50 border-b border-gray-200 text-[10px] text-gray-400 uppercase tracking-wider font-semibold">
                <th className="w-8 px-2 py-2.5" />
                <th className="px-4 py-2.5 text-left">虚拟模型</th>
                <th className="px-4 py-2.5 text-right">售价 输入 / 输出</th>
                <th className="px-4 py-2.5 text-right">最低渠道成本（¥）</th>
                <th className="px-4 py-2.5 text-right">毛利率</th>
                <th className="px-4 py-2.5 text-right">市场最低价（¥）</th>
                <th className="px-4 py-2.5 text-right">售价 / 市场最低</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {rows.map((r) => (
                <ComparisonRow key={r.virtual_model_id} r={r} open={expanded.has(r.virtual_model_id)} onToggle={() => toggle(r.virtual_model_id)} />
              ))}
            </tbody>
          </table>
        </div>
        {list.data && (
          <Pagination
            page={page}
            pageSize={pageSize}
            total={list.data.total}
            pageSizes={PAGE_SIZES}
            onPageChange={(p) => setParams({ page: String(p) }, { keepPage: true })}
            onPageSizeChange={(s) => setParams({ page_size: String(s) })}
          />
        )}
      </DataState>
    </>
  );
}

function ComparisonRow({ r, open, onToggle }: { r: PriceComparisonRow; open: boolean; onToggle: () => void }) {
  const ch = cheapest(r.channels, (c) => blended(c.input_cny, c.output_cny));
  const mk = cheapest(r.market, (m) => blended(m.input_cny, m.output_cny));
  const margin = n(r.margin_ratio);
  const channelCount = r.channels?.length ?? 0;
  const marketCount = r.market?.length ?? 0;
  return (
    <Fragment>
      <tr className={cn('group cursor-pointer transition-colors', open ? 'bg-purple-50/30' : 'hover:bg-gray-50/70', margin !== null && margin < 0 && 'bg-rose-50/40')} onClick={onToggle}>
        <td className="px-2 py-2.5 text-gray-400">{open ? <ChevronDown className="w-3.5 h-3.5" /> : <ChevronRight className="w-3.5 h-3.5" />}</td>
        <td className="px-4 py-2.5">
          <Link to={`/models/${r.virtual_model_id}`} onClick={(e) => e.stopPropagation()} className="font-mono text-gray-900 hover:text-purple-700">
            {r.virtual_model}
          </Link>
          <div className="text-[10px] text-gray-400">
            {channelCount} 个渠道 · {marketCount} 条市场报价
          </div>
        </td>
        <td className="px-4 py-2.5 text-right">
          {r.sell_input === null && r.sell_output === null ? <span className="text-amber-700 text-[11px]">未设售价</span> : <PricePair input={r.sell_input} output={r.sell_output} currency={r.sell_currency} />}
        </td>
        <td className="px-4 py-2.5 text-right">
          {ch ? (
            <>
              <PricePair input={ch.input_cny} output={ch.output_cny} currency="CNY" />
              <div className="text-[10px] text-gray-400 font-mono">{ch.provider_code}</div>
            </>
          ) : (
            <span className="text-gray-400 text-[11px]">{channelCount ? '缺成本价' : '无启用渠道'}</span>
          )}
        </td>
        <td className="px-4 py-2.5 text-right">
          <MarginText ratio={r.margin_ratio} />
        </td>
        <td className="px-4 py-2.5 text-right">
          {mk ? (
            <>
              <PricePair input={mk.input_cny} output={mk.output_cny} currency="CNY" />
              <div className="text-[10px] text-gray-400 truncate max-w-48 ml-auto" title={`${mk.source_name} · ${mk.upstream_model}`}>
                {mk.source_name} <span className="font-mono">{mk.level}</span>
              </div>
            </>
          ) : (
            <span className="text-gray-400 text-[11px]">无市场报价</span>
          )}
        </td>
        <td className="px-4 py-2.5 text-right">
          <VsMarket ratio={r.vs_market_lowest} />
        </td>
      </tr>
      {open && (
        <tr className="bg-gray-50/60">
          <td />
          <td colSpan={6} className="px-4 py-3">
            <div className="grid grid-cols-1 2xl:grid-cols-2 gap-4">
              <ChannelsTable channels={r.channels ?? []} cheapestId={ch?.channel_id} />
              <MarketTable market={r.market ?? []} cheapest={mk} />
            </div>
          </td>
        </tr>
      )}
    </Fragment>
  );
}

const SUB_TH = 'px-3 py-1.5 font-semibold whitespace-nowrap';

function ChannelsTable({ channels, cheapestId }: { channels: ChannelCost[]; cheapestId: number | undefined }) {
  return (
    <div>
      <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold mb-1.5">渠道成本价（启用中）</div>
      {channels.length === 0 ? (
        <div className="text-[11px] text-gray-400">没有启用的渠道</div>
      ) : (
        <table className="w-full text-[11px] bg-white border border-gray-200 rounded-lg overflow-hidden">
          <thead>
            <tr className="text-[10px] text-gray-400 border-b border-gray-100 text-left">
              <th className={SUB_TH}>渠道</th>
              <th className={SUB_TH}>上游模型</th>
              <th className={cn(SUB_TH, 'text-right')}>原币 输入 / 输出</th>
              <th className={cn(SUB_TH, 'text-right')}>折人民币</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-50">
            {channels.map((c) => (
              <tr key={c.channel_id} className={c.channel_id === cheapestId ? 'bg-emerald-50/50' : undefined}>
                <td className="px-3 py-1.5">
                  <Link to={`/channels/${c.channel_id}`} className="font-mono text-purple-600 hover:underline">
                    #{c.channel_id}
                  </Link>{' '}
                  <span className="font-mono text-gray-500">{c.provider_code}</span>
                </td>
                <td className="px-3 py-1.5 font-mono text-gray-600 break-all">{c.upstream_model}</td>
                <td className="px-3 py-1.5 text-right">
                  {c.currency ? <PricePair input={c.input} output={c.output} currency={c.currency} /> : <span className="text-amber-700">未设成本价</span>}
                </td>
                <td className="px-3 py-1.5 text-right">
                  <PricePair input={c.input_cny} output={c.output_cny} currency="CNY" className={c.channel_id === cheapestId ? 'text-emerald-700 font-semibold' : undefined} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

function MarketTable({ market, cheapest: best }: { market: MarketPrice[]; cheapest: MarketPrice | null }) {
  return (
    <div>
      <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold mb-1.5">市场报价（各价格源最新观测）</div>
      {market.length === 0 ? (
        <div className="text-[11px] text-gray-400">价格源还没有观测到这个模型</div>
      ) : (
        <table className="w-full text-[11px] bg-white border border-gray-200 rounded-lg overflow-hidden">
          <thead>
            <tr className="text-[10px] text-gray-400 border-b border-gray-100 text-left">
              <th className={SUB_TH}>来源</th>
              <th className={SUB_TH}>上游模型</th>
              <th className={cn(SUB_TH, 'text-right')}>原币 输入 / 输出</th>
              <th className={cn(SUB_TH, 'text-right')}>折人民币</th>
              <th className={SUB_TH}>观测时间</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-50">
            {market.map((m) => {
              const isBest = m === best;
              return (
                <tr key={`${m.source_id}-${m.upstream_model}`} className={isBest ? 'bg-emerald-50/50' : undefined}>
                  <td className="px-3 py-1.5">
                    <span className="text-gray-700">{m.source_name}</span> <span className="font-mono text-[10px] text-gray-400">{m.level}</span>
                  </td>
                  <td className="px-3 py-1.5 font-mono text-gray-600 break-all">{m.upstream_model}</td>
                  <td className="px-3 py-1.5 text-right">
                    <PricePair input={m.input} output={m.output} currency={m.currency} />
                  </td>
                  <td className="px-3 py-1.5 text-right">
                    <PricePair input={m.input_cny} output={m.output_cny} currency="CNY" className={isBest ? 'text-emerald-700 font-semibold' : undefined} />
                  </td>
                  <td className="px-3 py-1.5 text-gray-500 whitespace-nowrap" title={formatDateTime(m.observed_at)}>
                    {formatRelative(m.observed_at)}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </div>
  );
}
