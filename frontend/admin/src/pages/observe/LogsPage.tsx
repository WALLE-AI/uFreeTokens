import { useCallback, useEffect, useMemo, useRef, useState, type MouseEvent } from 'react';
import { Link } from 'react-router';
import { Check, Copy, Loader2, ScrollText, SlidersHorizontal } from 'lucide-react';
import { listRequestLogs, type RequestLogsQuery } from '../../api/stats';
import { errorMessage } from '../../api/errors';
import { isAbortError } from '../../api/client';
import {
  Button,
  DataTable,
  DetailDrawer,
  EmptyState,
  ErrorCard,
  FilterBar,
  Money,
  PageHeader,
  SegmentedToggle,
  Select,
  StatusBadge,
  type ActiveFilter,
  type Column,
} from '../../components/ui';
import { formatMs, rangeBounds, spanMs, type RangeKey } from '../../components/stats/metrics';
import { useQueryParams } from '../../hooks/useQueryState';
import { cn } from '../../lib/cn';
import { formatCompact } from '../../lib/money';
import { formatDateTime } from '../../lib/time';
import type { RequestLogItem } from '../../types';
import { DraftInput } from './filters';
import { LogDetail } from './LogDetail';

// 全局调用日志（UI_DESIGN.md §5.6）：筛选全部存 URL，keyset 无限滚动，行点击打开详情抽屉。
// /logs?request_id=xxx 直接定位并打开详情。时间窗：不带 account_id 最长 7 天，带时 30 天。

const PAGE_SIZE = 50;
const DAY = 86400_000;
type LogRange = Extract<RangeKey, '1h' | '24h' | '7d' | 'custom'>;
const RANGE_OPTIONS: Array<{ value: LogRange; label: string }> = [
  { value: '1h', label: '1 小时' },
  { value: '24h', label: '24 小时' },
  { value: '7d', label: '7 天' },
  { value: 'custom', label: '自定义' },
];
const TEXT_FILTERS: Array<{ key: keyof RequestLogsQuery; label: string; placeholder: string; width?: string }> = [
  { key: 'account_id', label: '账户', placeholder: '账户 ID' },
  { key: 'api_key_id', label: 'API Key', placeholder: 'API Key ID' },
  { key: 'virtual_model', label: '模型', placeholder: '虚拟模型名', width: 'w-48' },
  { key: 'channel_id', label: '渠道', placeholder: '渠道 ID' },
  { key: 'provider_id', label: '供应商', placeholder: '供应商 ID' },
  { key: 'error_code', label: '错误码', placeholder: 'error_code' },
  { key: 'min_latency_ms', label: '延迟 ≥', placeholder: '最小延迟 ms' },
];
const FILTER_KEYS = ['range', 'from', 'to', 'status', 'usage_source', 'request_id', ...TEXT_FILTERS.map((f) => f.key)] as const;

export default function LogsPage() {
  const [params, setParams] = useQueryParams();
  const range = (params.range as LogRange) || '24h';
  const [moreOpen, setMoreOpen] = useState(() => TEXT_FILTERS.some((f) => params[f.key]));

  // 查询条件（数字参数非法时忽略，避免把 "abc" 发给后端得到 400）
  const filterKey = FILTER_KEYS.map((k) => params[k] ?? '').join('|');
  const query = useMemo(() => {
    const bounds = rangeBounds(range, { from: params.from, to: params.to });
    const num = (k: string) => (params[k] && /^\d+$/.test(params[k]) ? Number(params[k]) : undefined);
    const q: RequestLogsQuery = {
      from: bounds.from,
      to: bounds.to,
      account_id: num('account_id'),
      api_key_id: num('api_key_id'),
      channel_id: num('channel_id'),
      provider_id: num('provider_id'),
      min_latency_ms: num('min_latency_ms'),
      virtual_model: params.virtual_model || undefined,
      error_code: params.error_code || undefined,
      status: params.status || undefined,
      usage_source: params.usage_source || undefined,
      request_id: params.request_id || undefined,
    };
    return q;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filterKey]);

  // 时间窗在前端先校验，避免后端 400
  const windowError = useMemo(() => {
    const span = spanMs(query.from!, query.to!);
    if (!Number.isFinite(span) || span <= 0) return '时间范围无效：结束时间需晚于开始时间';
    if (query.account_id ? span > 30 * DAY : span > 7 * DAY)
      return query.account_id ? '按账户查询时，时间范围最长 30 天' : '不指定账户时，时间范围最长 7 天（指定账户 ID 可放宽到 30 天）';
    return null;
  }, [query]);

  // ---------- 列表：keyset 分页 + 无限滚动 ----------
  const [items, setItems] = useState<RequestLogItem[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [exhausted, setExhausted] = useState(false);
  const reqSeq = useRef(0);

  const load = useCallback(
    async (before: string | null, signal?: AbortSignal) => {
      const seq = ++reqSeq.current;
      setLoading(true);
      setError(null);
      try {
        const res = await listRequestLogs({ ...query, before: before ?? undefined, limit: PAGE_SIZE }, signal);
        if (seq !== reqSeq.current) return;
        setItems((prev) => (before ? [...prev, ...res.data] : res.data));
        setCursor(res.next_cursor || null);
        setExhausted(!res.next_cursor);
      } catch (err) {
        if (seq === reqSeq.current && !isAbortError(err)) setError(err);
      } finally {
        if (seq === reqSeq.current) setLoading(false);
      }
    },
    [query],
  );

  useEffect(() => {
    setItems([]);
    setCursor(null);
    setExhausted(false);
    if (windowError) return;
    const controller = new AbortController();
    void load(null, controller.signal);
    return () => controller.abort();
  }, [load, windowError]);

  const sentinel = useRef<HTMLDivElement>(null);
  const more = useRef<() => void>(() => {});
  more.current = () => {
    if (cursor && !loading && !error) void load(cursor);
  };
  useEffect(() => {
    const el = sentinel.current;
    if (!el) return;
    const io = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) more.current();
    });
    io.observe(el);
    return () => io.disconnect();
  }, [items.length]);

  // ---------- 详情抽屉 ----------
  const [selected, setSelected] = useState<{ id: string; createdAt?: string } | null>(null);
  const [dismissed, setDismissed] = useState<string | null>(null);
  // /logs?request_id=xxx：直接打开详情（后端在最近 30 天内查找，不受列表时间窗限制）
  useEffect(() => {
    if (params.request_id && !selected && dismissed !== params.request_id) setSelected({ id: params.request_id });
  }, [params.request_id, selected, dismissed]);
  const closeDrawer = () => {
    if (selected) setDismissed(selected.id);
    setSelected(null);
  };

  // ---------- 筛选 UI ----------
  const active: ActiveFilter[] = [];
  if (params.request_id) active.push({ key: 'request_id', label: `request_id: ${params.request_id}`, onRemove: () => setParams({ request_id: null }) });
  if (params.status) active.push({ key: 'status', label: `状态: ${params.status === 'success' ? '成功' : '失败'}`, onRemove: () => setParams({ status: null }) });
  if (params.usage_source) active.push({ key: 'usage_source', label: `用量来源: ${params.usage_source}`, onRemove: () => setParams({ usage_source: null }) });
  for (const f of TEXT_FILTERS) {
    const v = params[f.key];
    if (v) active.push({ key: f.key, label: `${f.label}: ${v}`, onRemove: () => setParams({ [f.key]: null }) });
  }
  const clearAll = () => setParams(Object.fromEntries(FILTER_KEYS.filter((k) => k !== 'range').map((k) => [k, null])));

  const columns: Column<RequestLogItem>[] = [
    {
      key: 'time',
      header: '时间',
      width: 'w-36',
      render: (l) => <span className="text-gray-500 whitespace-nowrap">{formatDateTime(l.created_at).slice(5)}</span>,
    },
    { key: 'id', header: 'request_id', render: (l) => <RequestIdCell id={l.request_id} /> },
    {
      key: 'account',
      header: '账户',
      render: (l) => (
        <Link to={`/accounts/${l.account_id}`} onClick={(e) => e.stopPropagation()} className="font-mono text-purple-600 hover:text-purple-700">
          #{l.account_id}
        </Link>
      ),
    },
    {
      key: 'model',
      header: '模型 / 渠道',
      render: (l) => (
        <div className="min-w-0">
          <div className="font-mono text-gray-900 truncate max-w-56" title={l.virtual_model}>
            {l.virtual_model}
          </div>
          {l.channel_id !== null && (
            <Link to={`/channels/${l.channel_id}`} onClick={(e) => e.stopPropagation()} className="text-[11px] font-mono text-gray-400 hover:text-purple-600">
              渠道 #{l.channel_id}
            </Link>
          )}
        </div>
      ),
    },
    {
      key: 'status',
      header: '状态',
      render: (l) => (
        <div className="flex items-center gap-1.5 whitespace-nowrap">
          <StatusBadge kind="request" value={l.status} />
          {l.http_status !== null && l.http_status !== 200 && <span className="font-mono text-[11px] text-gray-500">{l.http_status}</span>}
          {l.usage_source !== 'upstream' && <StatusBadge kind="usage_source" value={l.usage_source} dot={false} />}
        </div>
      ),
    },
    {
      key: 'attempts',
      header: '尝试',
      numeric: true,
      render: (l) => <span className={cn(l.attempts > 1 && 'text-amber-700 font-medium')}>{l.attempts}</span>,
    },
    {
      key: 'tokens',
      header: '用量',
      numeric: true,
      // 图像按张、语音合成按字符、语音识别按秒，其余按 token 入/出。
      render: (l) =>
        l.image_count ? (
          <span className="whitespace-nowrap">{l.image_count} 张</span>
        ) : l.input_chars ? (
          <span className="whitespace-nowrap">{formatCompact(l.input_chars)} 字</span>
        ) : l.audio_ms ? (
          <span className="whitespace-nowrap">{(l.audio_ms / 1000).toFixed(1)} 秒</span>
        ) : (
          <span className="whitespace-nowrap">
            {l.input_tokens === null ? '—' : formatCompact(l.input_tokens)}
            <span className="text-gray-300"> / </span>
            {l.output_tokens === null ? '—' : formatCompact(l.output_tokens)}
          </span>
        ),
    },
    { key: 'charged', header: '收入', numeric: true, render: (l) => <Money micro={l.charged_amount_micro} /> },
    { key: 'cost', header: '成本', numeric: true, render: (l) => <Money micro={l.cost_micro} className="text-gray-500" /> },
    {
      key: 'latency',
      header: '延迟',
      numeric: true,
      render: (l) => (
        <span className={cn('whitespace-nowrap', l.latency_ms !== null && l.latency_ms > 10_000 && 'text-amber-700')} title={`TTFT ${formatMs(l.ttft_ms)}`}>
          {formatMs(l.latency_ms)}
        </span>
      ),
    },
  ];

  return (
    <div>
      <PageHeader title="调用日志" description="所有账户的请求记录（只有元数据，不含请求/响应正文）；点击行查看计费明细与重试轨迹" />

      <FilterBar
        search={params.request_id ?? ''}
        onSearch={(v) => setParams({ request_id: v || null })}
        searchPlaceholder="粘贴 request_id 精确定位"
        controls={
          <>
            <SegmentedToggle options={RANGE_OPTIONS} value={range} onChange={(v) => setParams({ range: v === '24h' ? null : v, from: null, to: null })} />
            <Select
              value={params.status ?? ''}
              onChange={(e) => setParams({ status: e.target.value || null })}
              placeholder="全部状态"
              options={[
                { value: 'success', label: '成功' },
                { value: 'upstream_error', label: '上游错误' },
              ]}
            />
            <Select
              value={params.usage_source ?? ''}
              onChange={(e) => setParams({ usage_source: e.target.value || null })}
              placeholder="全部用量来源"
              options={[
                { value: 'upstream', label: '上游返回' },
                { value: 'estimated', label: '估算' },
                { value: 'mixed', label: '混合' },
              ]}
            />
            <Button variant="secondary" size="sm" icon={<SlidersHorizontal className="w-3.5 h-3.5" />} onClick={() => setMoreOpen((v) => !v)}>
              更多筛选
            </Button>
          </>
        }
        active={active}
        onClearAll={clearAll}
      />

      {(moreOpen || range === 'custom') && (
        <div className="-mt-2 mb-4 flex flex-wrap items-center gap-2 p-3 bg-gray-50 border border-gray-200 rounded-xl">
          {range === 'custom' && (
            <>
              <DraftInput type="date" mono={false} width="w-40" value={params.from ?? ''} placeholder="开始日期" onCommit={(v) => setParams({ from: v || null })} />
              <span className="text-gray-400 text-xs">至</span>
              <DraftInput type="date" mono={false} width="w-40" value={params.to ?? ''} placeholder="结束日期（含当天）" onCommit={(v) => setParams({ to: v || null })} />
              <span className="w-px h-5 bg-gray-200 mx-1" />
            </>
          )}
          {moreOpen &&
            TEXT_FILTERS.map((f) => (
              <DraftInput key={f.key} width={f.width} value={params[f.key] ?? ''} placeholder={f.placeholder} onCommit={(v) => setParams({ [f.key]: v || null })} />
            ))}
        </div>
      )}

      {windowError ? (
        <div className="bg-amber-50/80 border border-amber-200 rounded-xl p-4 text-xs text-amber-900">{windowError}</div>
      ) : error && items.length === 0 ? (
        <ErrorCard error={error} onRetry={() => void load(null)} />
      ) : !loading && items.length === 0 ? (
        <EmptyState icon={<ScrollText className="w-8 h-8" />} title="没有符合条件的调用" description="试试放宽时间范围或清空筛选条件" />
      ) : (
        <DataTable
          columns={columns}
          rows={items}
          rowKey={(l) => l.request_id}
          onRowClick={(l) => setSelected({ id: l.request_id, createdAt: l.created_at })}
          highlightKey={selected?.id ?? null}
          footer={
            <div ref={sentinel} className="py-3 text-center text-[11px] text-gray-400">
              {loading ? (
                <span className="inline-flex items-center gap-1.5">
                  <Loader2 className="w-3.5 h-3.5 animate-spin" />
                  正在加载…
                </span>
              ) : error ? (
                <button type="button" onClick={() => void load(cursor)} className="text-rose-600 hover:text-rose-700 cursor-pointer">
                  加载失败：{errorMessage(error, '请重试')}（点击重试）
                </button>
              ) : exhausted ? (
                `已显示全部 ${items.length} 条`
              ) : (
                <button type="button" onClick={() => more.current()} className="text-purple-600 hover:text-purple-700 cursor-pointer">
                  加载更多
                </button>
              )}
            </div>
          }
        />
      )}

      <DetailDrawer
        open={!!selected}
        onClose={closeDrawer}
        title="调用详情"
        subtitle={selected?.id}
        fullPageHref={selected ? `/logs?request_id=${encodeURIComponent(selected.id)}` : undefined}
      >
        {selected && <LogDetail requestId={selected.id} createdAt={selected.createdAt} />}
      </DetailDrawer>
    </div>
  );
}

function RequestIdCell({ id }: { id: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async (e: MouseEvent) => {
    e.stopPropagation();
    try {
      await navigator.clipboard.writeText(id);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // 剪贴板不可用（非安全上下文）时静默失败
    }
  };
  return (
    <span className="inline-flex items-center gap-1 group/id">
      <span className="font-mono text-gray-600 truncate max-w-28" title={id}>
        {id}
      </span>
      <button type="button" onClick={copy} className="p-0.5 text-gray-300 hover:text-gray-600 cursor-pointer" aria-label="复制 request_id">
        {copied ? <Check className="w-3 h-3 text-emerald-600" /> : <Copy className="w-3 h-3" />}
      </button>
    </span>
  );
}
