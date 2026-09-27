import { describeError } from '../../api/errors';
import { Switch } from '../../components/ui/index';
import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router';
import { BarChart3, KeyRound, Wallet } from 'lucide-react';
import { listAccountApiKeys, listCreditGrants, listLedger, revokeApiKey } from '../../api/accounts';
import { getAccountUsage } from '../../api/stats';
import {
  ConfirmDialog,
  DataState,
  DataTable,
  EmptyState,
  Money,
  Pills,
  SegmentedToggle,
  ShareBar,
  StatusBadge,
  TrendBars,
  useToast,
  type Column,
} from '../../components/ui';
import { useAsync } from '../../hooks/useAsync';
import { cn } from '../../lib/cn';
import { formatCompact, formatMicroCompact } from '../../lib/money';
import { formatDateTime, formatRelative } from '../../lib/time';
import type { AccountMember, ApiKey, LedgerEntry, Metrics } from '../../types';
import { LEDGER_TYPE_LABELS, ROLE_LABELS, keyLimitsText } from './shared';

const card = 'bg-white border border-gray-200 rounded-xl shadow-xs';

// ---------- 成员 ----------

export function MembersTable({ members }: { members: AccountMember[] }) {
  if (members.length === 0) {
    return <div className="bg-gray-50 border border-dashed border-gray-200 rounded-xl p-6 text-center text-xs text-gray-400">该账户没有关联的控制台用户（由运营直接创建）</div>;
  }
  return (
    <div className={cn(card, 'divide-y divide-gray-100')}>
      {members.map((m) => (
        <div key={m.user_id} className="px-4 py-2.5 flex items-center gap-3 text-xs">
          <span className="font-mono text-gray-900">{m.email ?? `用户 #${m.user_id}`}</span>
          {m.email_verified ? (
            <span className="text-[11px] text-emerald-700">已验证</span>
          ) : (
            <span className="text-[11px] text-amber-700">邮箱未验证</span>
          )}
          <span className="ml-auto px-2 py-0.5 rounded-full bg-gray-100 text-gray-700 text-[11px]">{ROLE_LABELS[m.role] ?? m.role}</span>
          <span className="text-[11px] text-gray-400 w-24 text-right">{formatRelative(m.created_at)}加入</span>
        </div>
      ))}
    </div>
  );
}

// ---------- 用量趋势 ----------

type UsageMetric = 'revenue' | 'requests' | 'tokens';

const METRIC_VALUE: Record<UsageMetric, (m: Metrics) => number> = {
  revenue: (m) => m.revenue_micro,
  requests: (m) => m.requests,
  tokens: (m) => m.input_tokens + m.output_tokens,
};

const METRIC_FORMAT: Record<UsageMetric, (v: number) => string> = {
  revenue: formatMicroCompact,
  requests: formatCompact,
  tokens: formatCompact,
};

// 最近 30 天的日期桶（后端只返回有数据的桶，这里补齐 0，柱子才能对齐日期）
function last30Days(): string[] {
  const out: string[] = [];
  const now = new Date();
  for (let i = 29; i >= 0; i--) {
    const d = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate() - i));
    out.push(d.toISOString().slice(0, 10));
  }
  return out;
}

export function UsageSection({ accountId }: { accountId: number }) {
  const [metric, setMetric] = useState<UsageMetric>('revenue');
  const days = last30Days();
  const from = days[0];

  const trend = useAsync((signal) => getAccountUsage(accountId, { interval: 'day', from }, signal), [accountId, from]);
  const byModel = useAsync(
    (signal) => getAccountUsage(accountId, { interval: 'none', group_by: 'virtual_model', from, top: 8, order_by: 'revenue_micro' }, signal),
    [accountId, from],
  );

  const byDay = new Map((trend.data?.series ?? []).map((p) => [p.bucket, p]));
  const points = days.map((d) => {
    const p = byDay.get(d);
    const v = p ? METRIC_VALUE[metric](p) : 0;
    return {
      label: d,
      value: v,
      tooltip: (
        <>
          <div className="text-gray-300">{d}</div>
          <div className="font-mono">消费 {formatMicroCompact(p?.revenue_micro ?? 0)}</div>
          <div className="font-mono">请求 {formatCompact(p?.requests ?? 0)}</div>
          <div className="font-mono">tokens {formatCompact((p?.input_tokens ?? 0) + (p?.output_tokens ?? 0))}</div>
        </>
      ),
    };
  });
  const totals = trend.data?.totals;
  const groups = byModel.data?.groups ?? [];
  const groupTotal = groups.reduce((s, g) => s + g.totals.revenue_micro, 0);

  return (
    <div className="space-y-4">
      <div className={cn(card, 'p-5')}>
        <div className="flex items-center justify-between mb-4">
          <div className="text-xs text-gray-500">
            最近 30 天
            {totals && (
              <span className="ml-3 text-gray-900">
                消费 <Money micro={totals.revenue_micro} /> · 请求 <span className="font-mono">{formatCompact(totals.requests)}</span>
                {totals.error_rate !== null && Number(totals.error_rate) > 0 && (
                  <span className="text-rose-600"> · 错误率 {(Number(totals.error_rate) * 100).toFixed(1)}%</span>
                )}
              </span>
            )}
          </div>
          <SegmentedToggle<UsageMetric>
            options={[
              { value: 'revenue', label: '消费' },
              { value: 'requests', label: '请求数' },
              { value: 'tokens', label: 'tokens' },
            ]}
            value={metric}
            onChange={setMetric}
          />
        </div>
        <DataState
          loading={trend.loading}
          error={trend.error}
          onRetry={trend.reload}
          empty={!!totals && totals.requests === 0}
          emptyIcon={<BarChart3 className="w-8 h-8" />}
          emptyTitle="最近 30 天没有调用"
          skeleton="text"
        >
          <TrendBars points={points} format={METRIC_FORMAT[metric]} />
        </DataState>
      </div>

      {groups.length > 0 && (
        <div className={cn(card, 'p-5')}>
          <div className="text-xs font-medium text-gray-900 mb-3">模型消费排行（30 天）</div>
          <ShareBar segments={groups.map((g) => ({ label: g.label, value: g.totals.revenue_micro }))} />
          <div className="mt-3 divide-y divide-gray-100">
            {groups.map((g, i) => (
              <div key={g.key} className="py-1.5 flex items-center gap-3 text-xs">
                <span className="w-4 text-gray-400 font-mono">{i + 1}</span>
                <span className={cn('truncate', g.key === '__other__' ? 'text-gray-400' : 'text-gray-900 font-mono')}>{g.label}</span>
                <span className="ml-auto font-mono text-gray-500">{formatCompact(g.totals.requests)} 次</span>
                <span className="w-24 text-right">
                  <Money micro={g.totals.revenue_micro} className="text-gray-900" />
                </span>
                <span className="w-12 text-right font-mono text-gray-400">{groupTotal > 0 ? `${((g.totals.revenue_micro / groupTotal) * 100).toFixed(0)}%` : '—'}</span>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

// ---------- API 密钥 ----------

export function ApiKeysSection({ accountId, reloadKey, onChanged }: { accountId: number; reloadKey: unknown; onChanged: () => void }) {
  const toast = useToast();
  const keys = useAsync((signal) => listAccountApiKeys(accountId, signal), [accountId, reloadKey]);
  const [revoking, setRevoking] = useState<ApiKey | null>(null);
  const [busy, setBusy] = useState(false);

  const doRevoke = async () => {
    if (!revoking) return;
    setBusy(true);
    try {
      await revokeApiKey(revoking.id);
      toast.success(`已吊销 ${revoking.name}`);
      setRevoking(null);
      keys.reload();
      onChanged();
    } catch (err) {
      toast.error('吊销失败', describeError(err));
    } finally {
      setBusy(false);
    }
  };

  const columns: Column<ApiKey>[] = [
    { key: 'name', header: '名称', render: (k) => <span className="text-gray-900">{k.name}</span> },
    { key: 'prefix', header: '前缀', render: (k) => <span className="font-mono text-gray-600">{k.display_prefix}…</span> },
    { key: 'status', header: '状态', render: (k) => <StatusBadge kind="api_key" value={k.status} /> },
    {
      key: 'models',
      header: '允许的模型',
      render: (k) =>
        k.allowed_models?.length ? (
          <span className="text-gray-600" title={k.allowed_models.join('\n')}>
            {k.allowed_models.length} 个
          </span>
        ) : (
          <span className="text-gray-400">不限</span>
        ),
    },
    { key: 'limits', header: '限制', render: (k) => <span className="text-gray-600 font-mono text-[11px]">{keyLimitsText(k)}</span> },
    { key: 'created', header: '创建时间', render: (k) => <span className="text-gray-500 text-[11px]">{formatDateTime(k.created_at)}</span> },
  ];

  return (
    <>
      <DataState loading={keys.loading} error={keys.error} onRetry={keys.reload} skeleton="table">
        {keys.data && (
          <DataTable
            columns={columns}
            rows={keys.data.data ?? []}
            rowKey={(k) => k.id}
            rowActions={[{ label: '吊销', danger: true, hidden: (k) => k.status === 'revoked', onClick: setRevoking }]}
            empty={
              <EmptyState icon={<KeyRound className="w-8 h-8" />} title="还没有 API Key" description="用户可在控制台自助创建，或由运营在这里代开" />
            }
          />
        )}
      </DataState>
      <ConfirmDialog
        open={!!revoking}
        onClose={() => setRevoking(null)}
        onConfirm={doRevoke}
        loading={busy}
        level="typed"
        confirmText={revoking?.name}
        confirmLabel="吊销"
        title="吊销 API Key"
      >
        <p className="text-xs">
          吊销后使用 <span className="font-mono">{revoking?.display_prefix}…</span> 的请求会立即被网关拒绝（401），<span className="font-medium text-rose-600">此操作不可逆</span>。
        </p>
      </ConfirmDialog>
    </>
  );
}

// ---------- 资金流水 ----------

const LEDGER_FILTERS = [
  { value: '', label: '全部' },
  { value: 'recharge', label: '充值' },
  { value: 'consume', label: '消费' },
  { value: 'refund', label: '退款' },
  { value: 'grant', label: '赠送' },
  { value: 'grant_expire', label: '赠送过期' },
  { value: 'adjust', label: '调账' },
];

export function LedgerSection({ accountId, reloadKey, highlightRef }: { accountId: number; reloadKey: unknown; highlightRef: string | null }) {
  const [type, setType] = useState('');
  const [extra, setExtra] = useState<LedgerEntry[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  const [moreError, setMoreError] = useState<unknown>(null);
  const sentinel = useRef<HTMLDivElement>(null);

  const first = useAsync(
    async (signal) => {
      const res = await listLedger(accountId, { type: type || undefined, limit: 30 }, signal);
      setExtra([]);
      setCursor(res.next_cursor || null);
      setMoreError(null);
      return res.data;
    },
    [accountId, type, reloadKey],
  );

  const loadMore = async () => {
    if (!cursor || loadingMore) return;
    setLoadingMore(true);
    setMoreError(null);
    try {
      const res = await listLedger(accountId, { type: type || undefined, limit: 30, before: cursor });
      setExtra((prev) => [...prev, ...res.data]);
      setCursor(res.next_cursor || null);
    } catch (err) {
      setMoreError(err);
    } finally {
      setLoadingMore(false);
    }
  };

  // 无限滚动：哨兵进入视口时加载下一页（web 调用日志同款）
  const loadMoreRef = useRef(loadMore);
  loadMoreRef.current = loadMore;
  useEffect(() => {
    const el = sentinel.current;
    if (!el) return;
    const io = new IntersectionObserver((entries) => {
      if (entries[0]?.isIntersecting) void loadMoreRef.current();
    });
    io.observe(el);
    return () => io.disconnect();
  }, [cursor]);

  const rows = [...(first.data ?? []), ...extra];

  return (
    <div className="space-y-3">
      <Pills options={LEDGER_FILTERS} value={type} onChange={setType} />
      <DataState
        loading={first.loading}
        error={first.error}
        onRetry={first.reload}
        empty={rows.length === 0}
        emptyIcon={<Wallet className="w-8 h-8" />}
        emptyTitle={type ? '没有该类型的流水' : '还没有资金流水'}
        skeleton="table"
      >
        <div className={cn(card, 'overflow-hidden')}>
          <div className="max-h-[32rem] overflow-y-auto">
            <table className="w-full text-xs">
              <thead className="bg-gray-50 text-[10px] text-gray-400 uppercase tracking-wider font-semibold sticky top-0">
                <tr>
                  <th className="px-4 py-2 text-left font-semibold">时间</th>
                  <th className="px-4 py-2 text-left font-semibold">类型</th>
                  <th className="px-4 py-2 text-right font-semibold">金额</th>
                  <th className="px-4 py-2 text-right font-semibold">现金余额</th>
                  <th className="px-4 py-2 text-right font-semibold">赠送余额</th>
                  <th className="px-4 py-2 text-left font-semibold">关联</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100">
                {rows.map((e) => {
                  const hl = highlightRef !== null && e.ref_type === 'admin' && e.ref_id === highlightRef;
                  return (
                    <tr key={e.id} className={cn('hover:bg-gray-50/70', hl && 'bg-purple-50/60')}>
                      <td className="px-4 py-2 text-gray-500 whitespace-nowrap" title={formatDateTime(e.created_at)}>
                        {formatDateTime(e.created_at)}
                      </td>
                      <td className="px-4 py-2">
                        <span className="text-gray-900">{LEDGER_TYPE_LABELS[e.type] ?? e.type}</span>
                        <span className="text-[11px] text-gray-400 ml-1.5">{e.balance_kind === 'cash' ? '现金' : '赠送'}</span>
                      </td>
                      <td className="px-4 py-2 text-right">
                        <Money micro={e.amount_micro} signed />
                      </td>
                      <td className="px-4 py-2 text-right">
                        <Money micro={e.cash_after_micro} className="text-gray-600" />
                      </td>
                      <td className="px-4 py-2 text-right">
                        <Money micro={e.bonus_after_micro} className="text-gray-600" />
                      </td>
                      <td className="px-4 py-2 font-mono text-[11px] truncate max-w-56">
                        {e.ref_type === 'request' ? (
                          <Link to={`/logs?request_id=${encodeURIComponent(e.ref_id)}`} className="text-purple-600 hover:text-purple-700">
                            {e.ref_id}
                          </Link>
                        ) : (
                          <span className="text-gray-600" title={`${e.ref_type}: ${e.ref_id}`}>
                            {e.ref_type === 'admin' ? '' : `${e.ref_type}: `}
                            {e.ref_id}
                            {e.grant_id !== null && <span className="text-gray-400"> · 赠款 #{e.grant_id}</span>}
                          </span>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
            {cursor && (
              <div ref={sentinel} className="py-3 text-center text-[11px] text-gray-400">
                {moreError ? (
                  <button type="button" onClick={() => void loadMore()} className="text-rose-600 hover:text-rose-700 cursor-pointer">
                    加载失败，点击重试
                  </button>
                ) : loadingMore ? (
                  '正在加载...'
                ) : (
                  <button type="button" onClick={() => void loadMore()} className="text-purple-600 hover:text-purple-700 cursor-pointer">
                    加载更多
                  </button>
                )}
              </div>
            )}
            {!cursor && rows.length > 0 && <div className="py-3 text-center text-[11px] text-gray-300">已经到底了</div>}
          </div>
        </div>
      </DataState>
    </div>
  );
}

// ---------- 赠送余额 ----------

export function GrantsSection({ accountId, reloadKey }: { accountId: number; reloadKey: unknown }) {
  const [activeOnly, setActiveOnly] = useState(true);
  const grants = useAsync((signal) => listCreditGrants(accountId, activeOnly, signal), [accountId, activeOnly, reloadKey]);
  const now = Date.now();
  const sourceLabel: Record<string, string> = { signup: '注册', promotion: '活动', compensation: '补偿', invite: '邀请' };

  return (
    <div className="space-y-3">
      <Switch checked={activeOnly} onChange={setActiveOnly} label="只看有效（未用完且未过期）" />
      <DataState
        loading={grants.loading}
        error={grants.error}
        onRetry={grants.reload}
        empty={(grants.data?.data.length ?? 0) === 0}
        emptyTitle={activeOnly ? '没有有效的赠送余额' : '还没有发放过赠送余额'}
        skeleton="table"
      >
        <div className={cn(card, 'overflow-x-auto')}>
          <table className="w-full text-xs">
            <thead className="bg-gray-50 text-[10px] text-gray-400 uppercase tracking-wider font-semibold">
              <tr>
                <th className="px-4 py-2 text-left font-semibold">来源</th>
                <th className="px-4 py-2 text-right font-semibold">金额</th>
                <th className="px-4 py-2 text-right font-semibold">剩余</th>
                <th className="px-4 py-2 text-left font-semibold">模型范围</th>
                <th className="px-4 py-2 text-left font-semibold">到期</th>
                <th className="px-4 py-2 text-left font-semibold">发放时间</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {grants.data?.data.map((g) => {
                const expired = g.expires_at !== null && new Date(g.expires_at).getTime() <= now;
                const used = g.remaining_micro === 0;
                return (
                  <tr key={g.id} className={cn(expired || used ? 'text-gray-400' : 'hover:bg-gray-50/70')}>
                    <td className="px-4 py-2">
                      {sourceLabel[g.source] ?? g.source}
                      <span className="text-[11px] text-gray-400 ml-1.5 font-mono">#{g.id}</span>
                    </td>
                    <td className="px-4 py-2 text-right">
                      <Money micro={g.amount_micro} className={expired || used ? 'text-gray-400' : 'text-gray-900'} />
                    </td>
                    <td className="px-4 py-2 text-right">
                      <Money micro={g.remaining_micro} className={expired || used ? 'text-gray-400' : 'text-emerald-700'} />
                    </td>
                    <td className="px-4 py-2">
                      {g.model_scope?.length ? (
                        <span title={g.model_scope.join('\n')} className="font-mono text-[11px]">
                          {g.model_scope.length === 1 ? g.model_scope[0] : `${g.model_scope.length} 个模型`}
                        </span>
                      ) : (
                        '全部模型'
                      )}
                    </td>
                    <td className="px-4 py-2 whitespace-nowrap">
                      {g.expires_at ? (
                        <span className={expired ? 'line-through' : ''}>{formatDateTime(g.expires_at).slice(0, 16)}</span>
                      ) : (
                        '永久'
                      )}
                      {expired && <span className="ml-1.5 text-[11px]">已过期</span>}
                      {!expired && used && <span className="ml-1.5 text-[11px]">已用完</span>}
                    </td>
                    <td className="px-4 py-2 text-[11px] text-gray-500">{formatDateTime(g.created_at)}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </DataState>
    </div>
  );
}

