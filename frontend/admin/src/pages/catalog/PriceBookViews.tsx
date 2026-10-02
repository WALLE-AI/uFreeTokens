import { useState } from 'react';
import { ChevronDown, ChevronRight } from 'lucide-react';
import { PriceDiffTable } from '../../components/pricing/PriceComponentEditor';
import { EmptyState } from '../../components/ui';
import { cn } from '../../lib/cn';
import { formatDateTime } from '../../lib/time';
import type { PriceBook, PriceComponent } from '../../types';
import { currencySymbol, formatPrice } from './shared';

const UNIT_LABEL: Record<string, string> = {
  per_1m_tokens: '每百万 token',
  per_request: '每次请求',
  per_image: '每张图',
  per_second: '每秒',
  per_1m_chars: '每百万字符',
};

// 价格组件只读表（当前生效版本 / 历史版本展开）
export function PriceComponentsTable({ components, currency }: { components: PriceComponent[]; currency: string }) {
  if (components.length === 0) return <div className="text-xs text-gray-400 py-3">该版本没有价格组件</div>;
  const sym = currencySymbol(currency);
  return (
    <div className="overflow-x-auto bg-white border border-gray-200 rounded-xl shadow-xs">
      <table className="w-full text-xs">
        <thead>
          <tr className="bg-gray-50 border-b border-gray-200 text-[10px] text-gray-400 uppercase tracking-wider font-semibold">
            <th className="px-4 py-2.5 text-left">计量项</th>
            <th className="px-4 py-2.5 text-left">单位</th>
            <th className="px-4 py-2.5 text-left">档位</th>
            <th className="px-4 py-2.5 text-left">分档 token</th>
            <th className="px-4 py-2.5 text-left">时段</th>
            <th className="px-4 py-2.5 text-right">单价</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-gray-100">
          {components.map((c, i) => (
            <tr key={i}>
              <td className="px-4 py-2.5 font-mono text-gray-900">{c.meter}</td>
              <td className="px-4 py-2.5 text-gray-600">{UNIT_LABEL[c.unit] ?? c.unit}</td>
              <td className="px-4 py-2.5 text-gray-600">{c.service_tier}</td>
              <td className="px-4 py-2.5 font-mono text-gray-600">
                {c.tier_min_input === 0 && c.tier_max_input === null ? '—' : `${c.tier_min_input.toLocaleString('en-US')} – ${c.tier_max_input === null ? '∞' : c.tier_max_input.toLocaleString('en-US')}`}
              </td>
              <td className="px-4 py-2.5 font-mono text-gray-600">
                {c.window_start_min === null ? '—' : `${fmtMin(c.window_start_min)} – ${fmtMin(c.window_end_min ?? 0)}`}
              </td>
              <td className="px-4 py-2.5 text-right font-mono text-gray-900">
                {sym}
                {formatPrice(c.unit_price)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function fmtMin(m: number) {
  return `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`;
}

// 历史版本列表：每行可展开，展开后显示该版本相对上一版本（更早的一本）的差异
export function PriceBookHistory({ books }: { books: PriceBook[] }) {
  const [open, setOpen] = useState<number | null>(null);
  if (books.length === 0) return <EmptyState title="还没有发布过价格" />;
  return (
    <div className="bg-white border border-gray-200 rounded-xl shadow-xs divide-y divide-gray-100">
      {books.map((b, i) => {
        const prev = books[i + 1];
        const expanded = open === b.id;
        return (
          <div key={b.id}>
            <button
              type="button"
              onClick={() => setOpen(expanded ? null : b.id)}
              className={cn('w-full px-4 py-2.5 flex items-center gap-3 text-xs text-left cursor-pointer hover:bg-gray-50/70', expanded && 'bg-gray-50/70')}
            >
              {expanded ? <ChevronDown className="w-3 h-3 text-gray-400" /> : <ChevronRight className="w-3 h-3 text-gray-400" />}
              <span className="font-mono text-gray-400">#{b.id}</span>
              <span className="text-gray-900">生效于 {formatDateTime(b.effective_from)}</span>
              {b.effective_to && <span className="text-gray-400">至 {formatDateTime(b.effective_to)}</span>}
              <span className="font-mono text-gray-500">{b.currency}</span>
              {b.tier && <span className="text-[11px] text-gray-400" title="tier 在运行时不参与计价，仅作记录">tier: {b.tier}</span>}
              {b.is_current && (
                <span className="px-2 py-0.5 rounded-full text-[11px] border bg-emerald-50 text-emerald-700 border-emerald-200">当前生效</span>
              )}
              {!b.is_current && new Date(b.effective_from).getTime() > Date.now() && (
                <span className="px-2 py-0.5 rounded-full text-[11px] border bg-blue-50 text-blue-700 border-blue-200">预约生效</span>
              )}
              <span className="ml-auto text-[11px] text-gray-400">{b.components.length} 项</span>
            </button>
            {expanded && (
              <div className="px-4 pb-4 pt-1 space-y-2">
                {b.note && <p className="text-[11px] text-gray-500">备注：{b.note}</p>}
                {prev ? (
                  <>
                    <p className="text-[11px] text-gray-400">相对上一版本 #{prev.id} 的变化：</p>
                    <PriceDiffTable current={prev.components} next={b.components} currency={b.currency} />
                  </>
                ) : (
                  <PriceComponentsTable components={b.components} currency={b.currency} />
                )}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}
