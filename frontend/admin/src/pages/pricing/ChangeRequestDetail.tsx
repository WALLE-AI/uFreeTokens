import { useState, type ReactNode } from 'react';
import { Link } from 'react-router';
import { AlertTriangle, ChevronDown, ChevronRight, ExternalLink, Info } from 'lucide-react';
import { Money, StatusBadge } from '../../components/ui';
import { cn } from '../../lib/cn';
import { formatDateTime, formatRelative } from '../../lib/time';
import type { ChangeRequestDetail, PriceComponent } from '../../types';
import { MarginText, RatioText, meterLabel, unitLabel } from './shared';

// 调价申请详情（UI_DESIGN.md §5.2 收件箱右栏）。只负责展示；批准/驳回的
// 操作区由页面渲染在底部，因为要和列表选中项、键盘快捷键联动。

export function ChangeRequestDetailView({ d }: { d: ChangeRequestDetail }) {
  const decided = d.status !== 'pending' && d.status !== 'blocked';
  return (
    <div className="space-y-5">
      <Header d={d} />

      {d.status === 'blocked' && (
        <div className="bg-rose-50 border border-rose-200 text-rose-700 rounded-xl p-3 text-xs flex items-start gap-2">
          <AlertTriangle className="w-4 h-4 shrink-0 mt-0.5" />
          <div>
            <div className="font-medium">系统已拦截，需要人工确认</div>
            <div className="mt-0.5 text-rose-600/90">
              {d.blocked_reason ?? '变化幅度超过自动审批阈值'}。确认这确实是厂商在调价（而不是解析错误）后才能批准。
            </div>
          </div>
        </div>
      )}

      {decided && <DecisionInfo d={d} />}

      <Section title="成本价变化" hint={`币种 ${d.currency || '—'}`}>
        <ComponentChanges d={d} />
      </Section>

      <Section title="影响评估" hint={d.impact ? `按近 ${d.impact.window_days} 天实际用量估算，仅供审批参考` : undefined}>
        <ImpactCard d={d} />
      </Section>

      {d.issues.length > 0 && (
        <Section title="校验告警">
          <ul className="space-y-1.5">
            {d.issues.map((is, i) => (
              <li key={i} className="flex items-start gap-2 text-xs">
                <SeverityTag severity={is.severity} />
                <span className="text-gray-700">
                  {is.message}
                  <span className="ml-1 font-mono text-[11px] text-gray-400">{is.rule}</span>
                </span>
              </li>
            ))}
          </ul>
        </Section>
      )}

      <Section title="当前价 vs 提案">
        <div className="grid grid-cols-1 xl:grid-cols-2 gap-3">
          <PriceList
            title="当前生效成本价"
            sub={d.current_book ? `价格版本 #${d.current_book.id} · ${formatDateTime(d.current_book.effective_from)} 起` : undefined}
            currency={d.current_book?.currency}
            components={d.current_book?.components ?? []}
            emptyText="该渠道此前没有成本价（新模型）"
          />
          <PriceList
            title="提案价格"
            sub={d.proposed_spec.effective_from ? `预约生效 ${formatDateTime(d.proposed_spec.effective_from)}` : '批准后立即生效'}
            currency={d.proposed_spec.currency}
            components={d.proposed_spec.components}
            emptyText="提案中没有任何计量项（来源认为该模型已无价格）"
            highlight
          />
        </div>
      </Section>

      <Section title="证据" hint={d.evidence.length ? `${d.evidence.length} 条价格观测` : undefined}>
        {d.evidence.length === 0 ? (
          <p className="text-xs text-gray-400">没有关联的价格观测</p>
        ) : (
          <ul className="space-y-2">
            {d.evidence.map((e) => (
              <EvidenceItem key={e.observation_id} e={e} />
            ))}
          </ul>
        )}
      </Section>
    </div>
  );
}

function Header({ d }: { d: ChangeRequestDetail }) {
  return (
    <div>
      <div className="flex items-center gap-2 flex-wrap">
        <span className="font-mono text-[11px] text-gray-400">#{d.id}</span>
        <Link to={`/models/${d.virtual_model_id}`} className="text-sm font-semibold text-gray-900 hover:text-purple-700 truncate">
          {d.virtual_model_name}
        </Link>
        <StatusBadge kind="price_change" value={d.status} />
        <StatusBadge kind="direction" value={d.direction} />
      </div>
      <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-gray-500">
        <Link to={`/channels/${d.channel_id}`} className="inline-flex items-center gap-1 hover:text-purple-700">
          渠道 #{d.channel_id}
          <ExternalLink className="w-3 h-3" />
        </Link>
        <span>
          {d.provider_code} / {d.provider_account_name}
        </span>
        <span className="font-mono">{d.upstream_model}</span>
        <span>最大变化 <RatioText ratio={d.max_change_ratio} /></span>
        <span title={formatDateTime(d.created_at)}>创建于 {formatRelative(d.created_at)}</span>
        <span>生效时间 {formatDateTime(d.effective_from)}</span>
      </div>
    </div>
  );
}

function DecisionInfo({ d }: { d: ChangeRequestDetail }) {
  return (
    <div className="bg-gray-50 border border-gray-200 rounded-xl p-3 text-xs grid grid-cols-2 sm:grid-cols-4 gap-3">
      <KV label="处理人">{d.decided_by_name || (d.decided_by ? `#${d.decided_by}` : d.status === 'auto_approved' ? '系统自动' : '—')}</KV>
      <KV label="处理时间">{formatDateTime(d.decided_at)}</KV>
      <KV label="生效价格版本">{d.applied_book_id ? <span className="font-mono">#{d.applied_book_id}</span> : '—'}</KV>
      <KV label="理由">{d.decision_reason || '—'}</KV>
    </div>
  );
}

function KV({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="min-w-0">
      <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold">{label}</div>
      <div className="mt-0.5 text-gray-900 break-words">{children}</div>
    </div>
  );
}

function Section({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
  return (
    <section>
      <div className="flex items-baseline justify-between mb-2">
        <h3 className="text-xs font-semibold text-gray-900">{title}</h3>
        {hint && <span className="text-[11px] text-gray-400">{hint}</span>}
      </div>
      {children}
    </section>
  );
}

function ComponentChanges({ d }: { d: ChangeRequestDetail }) {
  if (d.components.length === 0) return <p className="text-xs text-gray-400">没有计量项差异</p>;
  return (
    <div className="border border-gray-200 rounded-xl overflow-hidden">
      <table className="w-full text-xs">
        <thead className="bg-gray-50 text-[10px] text-gray-400 uppercase tracking-wider font-semibold">
          <tr>
            <th className="px-3 py-2 text-left">计量</th>
            <th className="px-3 py-2 text-right">当前</th>
            <th className="px-3 py-2 text-right">提案</th>
            <th className="px-3 py-2 text-right">变化</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-gray-100">
          {d.components.map((c, i) => (
            <tr key={i}>
              <td className="px-3 py-2">
                {meterLabel(c.meter)}
                {(c.service_tier !== 'default' || c.tier_min_input > 0) && (
                  <span className="ml-1 text-[11px] text-gray-400">
                    {c.service_tier !== 'default' ? c.service_tier : ''}
                    {c.tier_min_input > 0 ? ` ≥${c.tier_min_input.toLocaleString('en-US')}` : ''}
                  </span>
                )}
              </td>
              <td className="px-3 py-2 text-right font-mono text-gray-500">
                {c.old_price ?? <span className="text-blue-600 font-sans">新增</span>}
              </td>
              <td className="px-3 py-2 text-right font-mono text-gray-900 font-medium">
                {c.new_price ?? <span className="text-gray-400 font-sans line-through">移除</span>}
              </td>
              <td className="px-3 py-2 text-right">
                <RatioText ratio={c.change_ratio} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function ImpactCard({ d }: { d: ChangeRequestDetail }) {
  const im = d.impact;
  if (!im) return <p className="text-xs text-gray-400">无法评估影响</p>;
  const after = im.margin_after === null ? null : Number(im.margin_after);
  const losing = after !== null && after < 0;
  const delta = im.cost_delta_micro;
  return (
    <div className="space-y-2">
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
        <div className="bg-gray-50 border border-gray-200 rounded-xl p-3">
          <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold">{im.window_days} 天成本（调整前 → 后）</div>
          <div className="mt-1 text-xs flex items-center gap-1.5 flex-wrap">
            <Money micro={im.cost_before_micro} className="text-gray-500" />
            <span className="text-gray-300">→</span>
            <Money micro={im.cost_after_micro} className="text-gray-900 font-semibold" />
          </div>
        </div>
        <div className={cn('rounded-xl p-3 border', delta > 0 ? 'bg-rose-50/60 border-rose-100' : 'bg-emerald-50/60 border-emerald-100')}>
          <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold">成本变化（按 {im.window_days} 天用量）</div>
          <div className={cn('mt-1 text-sm font-bold font-mono', delta > 0 ? 'text-rose-700' : delta < 0 ? 'text-emerald-700' : 'text-gray-700')}>
            {delta > 0 ? '+' : ''}
            <Money micro={delta} />
          </div>
        </div>
        <div className={cn('rounded-xl p-3 border', losing ? 'bg-rose-50 border-rose-200' : 'bg-purple-50/60 border-purple-100')}>
          <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold">毛利率（对当前售价）</div>
          <div className="mt-1 text-sm font-bold flex items-center gap-1.5">
            <MarginText ratio={im.margin_before} className="text-gray-500 font-normal text-xs" />
            <span className="text-gray-300 text-xs">→</span>
            <MarginText ratio={im.margin_after} />
          </div>
          {im.sell_price && (
            <div className="mt-0.5 text-[11px] text-gray-400 font-mono">
              售价 ¥{im.sell_price.input ?? '—'} / ¥{im.sell_price.output ?? '—'}
            </div>
          )}
        </div>
      </div>
      {losing && (
        <div className="bg-rose-50 border border-rose-200 text-rose-700 rounded-xl p-3 text-xs flex items-start gap-2">
          <AlertTriangle className="w-4 h-4 shrink-0 mt-0.5" />
          <div>
            调整后该渠道将<b>亏损</b>（售价低于成本）。建议批准后立即
            <Link to={`/models/${d.virtual_model_id}#sell-price`} className="underline mx-0.5 hover:text-rose-800">
              调整该模型售价
            </Link>
            ，或驳回并联系供应商确认。
          </div>
        </div>
      )}
      {!im.sell_price && (
        <p className="text-[11px] text-amber-700 flex items-center gap-1">
          <Info className="w-3 h-3" /> 该模型没有生效售价，无法计算毛利率。
        </p>
      )}
      {im.fx_missing && (
        <p className="text-[11px] text-amber-700 flex items-center gap-1">
          <Info className="w-3 h-3" /> 缺少 {d.currency}→CNY 汇率，成本估算不完整。请先在"价格源 & 汇率"页设置汇率。
        </p>
      )}
    </div>
  );
}

function SeverityTag({ severity }: { severity: string }) {
  const map: Record<string, { label: string; className: string }> = {
    blocking: { label: '拦截', className: 'bg-rose-50 text-rose-700 border-rose-200' },
    force_review: { label: '需人工', className: 'bg-amber-50 text-amber-700 border-amber-200' },
    warning: { label: '告警', className: 'bg-gray-50 text-gray-600 border-gray-200' },
  };
  const s = map[severity] ?? map.warning;
  return <span className={cn('shrink-0 px-1.5 py-0.5 rounded-full border text-[10px] font-medium', s.className)}>{s.label}</span>;
}

function PriceList({
  title,
  sub,
  currency,
  components,
  emptyText,
  highlight,
}: {
  title: string;
  sub?: string;
  currency?: string;
  components: PriceComponent[];
  emptyText: string;
  highlight?: boolean;
}) {
  return (
    <div className={cn('rounded-xl border p-3', highlight ? 'border-purple-200 bg-purple-50/30' : 'border-gray-200 bg-white')}>
      <div className="flex items-baseline justify-between gap-2">
        <div className="text-xs font-medium text-gray-900">{title}</div>
        {currency && <span className="text-[11px] font-mono text-gray-400">{currency}</span>}
      </div>
      {sub && <div className="text-[11px] text-gray-400 mt-0.5">{sub}</div>}
      {components.length === 0 ? (
        <p className="text-[11px] text-gray-400 mt-2">{emptyText}</p>
      ) : (
        <ul className="mt-2 space-y-1">
          {components.map((c, i) => (
            <li key={i} className="flex items-center justify-between gap-2 text-xs">
              <span className="text-gray-600 truncate">
                {meterLabel(c.meter)}
                {c.service_tier !== 'default' && <span className="text-[11px] text-gray-400 ml-1">{c.service_tier}</span>}
                {c.tier_min_input > 0 && <span className="text-[11px] text-gray-400 ml-1">≥{c.tier_min_input.toLocaleString('en-US')}</span>}
                {c.window_start_min !== null && (
                  <span className="text-[11px] text-gray-400 ml-1">
                    时段 {minToClock(c.window_start_min)}–{minToClock(c.window_end_min)}
                  </span>
                )}
              </span>
              <span className="font-mono text-gray-900 whitespace-nowrap">
                {c.unit_price} <span className="text-[11px] text-gray-400 font-sans">{unitLabel(c.unit)}</span>
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function minToClock(m: number | null): string {
  if (m === null) return '—';
  return `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`;
}

function EvidenceItem({ e }: { e: ChangeRequestDetail['evidence'][number] }) {
  const [open, setOpen] = useState(false);
  return (
    <li className="border border-gray-200 rounded-xl p-3 text-xs">
      <div className="flex items-center gap-2 flex-wrap">
        <span className="px-1.5 py-0.5 rounded bg-gray-100 text-gray-700 font-mono text-[10px]">{e.source_level}</span>
        <span className="text-gray-500">{e.source_kind}</span>
        <span className="font-mono text-[11px] text-gray-400">观测 #{e.observation_id}</span>
        <span className="text-[11px] text-gray-400 ml-auto" title={formatDateTime(e.observed_at)}>
          {formatRelative(e.observed_at)}
        </span>
      </div>
      {e.source_url && (
        <a
          href={e.source_url}
          target="_blank"
          rel="noreferrer noopener"
          className="mt-1 inline-flex items-center gap-1 text-[11px] text-purple-600 hover:text-purple-700 break-all"
        >
          {e.source_url}
          <ExternalLink className="w-3 h-3 shrink-0" />
        </a>
      )}
      {e.raw_excerpt && (
        <div className="mt-1.5">
          <button type="button" onClick={() => setOpen((v) => !v)} className="inline-flex items-center gap-1 text-[11px] text-gray-500 hover:text-gray-800 cursor-pointer">
            {open ? <ChevronDown className="w-3 h-3" /> : <ChevronRight className="w-3 h-3" />}
            原始内容（前 2KB）
          </button>
          {open && (
            <pre className="mt-1.5 max-h-48 overflow-auto bg-gray-50 border border-gray-200 rounded-lg p-2 font-mono text-[11px] text-gray-700 whitespace-pre-wrap break-all">
              {e.raw_excerpt}
            </pre>
          )}
        </div>
      )}
    </li>
  );
}
