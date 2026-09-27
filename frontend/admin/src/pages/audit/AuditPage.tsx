import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router';
import { ChevronDown, ChevronRight, FileClock, Loader2 } from 'lucide-react';
import { listAuditLogs, type ListAuditLogsParams } from '../../api/audit';
import { errorMessage } from '../../api/errors';
import { Button, DataState, FilterBar, Input, JsonDiff, PageHeader, Select, type ActiveFilter } from '../../components/ui';
import { useAsync } from '../../hooks/useAsync';
import { useQueryParams } from '../../hooks/useQueryState';
import { ACTION_GROUPS, TARGET_LABELS, actionLabel, actorLabel, targetHref, targetLabel } from '../../lib/audit';
import { cn } from '../../lib/cn';
import { formatDateTime } from '../../lib/time';
import type { AuditLogEntry } from '../../types';

// 审计日志（UI_DESIGN.md §5.7）：只读时间线，游标分页 + 无限滚动。
// 筛选全部存 URL：?target_type= &target_id= &actor_name= &action=（前缀）&from= &to=

const PAGE_SIZE = 50;
const FILTER_KEYS = ['target_type', 'target_id', 'actor_name', 'action', 'from', 'to'] as const;

export default function AuditPage() {
  const [params, setParams] = useQueryParams();
  const filters: ListAuditLogsParams = {
    target_type: params.target_type || undefined,
    target_id: params.target_id || undefined,
    actor_name: params.actor_name || undefined,
    action: params.action || undefined,
    from: params.from || undefined,
    to: params.to || undefined,
  };
  const filterKey = FILTER_KEYS.map((k) => params[k] ?? '').join('|');

  const [extra, setExtra] = useState<AuditLogEntry[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  const [moreError, setMoreError] = useState<string | null>(null);

  const first = useAsync(
    async (signal) => {
      const res = await listAuditLogs({ ...filters, limit: PAGE_SIZE }, signal);
      setExtra([]);
      setCursor(res.next_cursor || null);
      setMoreError(null);
      return res.data;
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [filterKey],
  );

  const loadMore = async () => {
    if (!cursor || loadingMore) return;
    setLoadingMore(true);
    setMoreError(null);
    try {
      const res = await listAuditLogs({ ...filters, limit: PAGE_SIZE, before: cursor });
      setExtra((prev) => [...prev, ...res.data]);
      setCursor(res.next_cursor || null);
    } catch (err) {
      setMoreError(errorMessage(err, '加载失败'));
    } finally {
      setLoadingMore(false);
    }
  };

  // 无限滚动：哨兵进入视口时加载下一页（与 web 调用日志同一做法）
  const sentinel = useRef<HTMLDivElement>(null);
  const loadMoreRef = useRef(loadMore);
  loadMoreRef.current = loadMore;
  useEffect(() => {
    const el = sentinel.current;
    if (!el) return;
    const io = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) void loadMoreRef.current();
    });
    io.observe(el);
    return () => io.disconnect();
  }, [cursor]);

  // 文本类筛选在回车/失焦时才提交，避免每敲一个字就请求一次
  const [targetIdDraft, setTargetIdDraft] = useState(params.target_id ?? '');
  const [actorDraft, setActorDraft] = useState(params.actor_name ?? '');
  useEffect(() => setTargetIdDraft(params.target_id ?? ''), [params.target_id]);
  useEffect(() => setActorDraft(params.actor_name ?? ''), [params.actor_name]);

  const entries = [...(first.data ?? []), ...extra];

  const active: ActiveFilter[] = [];
  if (params.target_type) active.push({ key: 'target_type', label: `对象类型: ${TARGET_LABELS[params.target_type] ?? params.target_type}`, onRemove: () => setParams({ target_type: null }) });
  if (params.target_id) active.push({ key: 'target_id', label: `对象 ID: ${params.target_id}`, onRemove: () => setParams({ target_id: null }) });
  if (params.actor_name) active.push({ key: 'actor_name', label: `操作人: ${params.actor_name}`, onRemove: () => setParams({ actor_name: null }) });
  if (params.action) active.push({ key: 'action', label: `动作: ${ACTION_GROUPS.find((g) => g.value === params.action)?.label ?? params.action}`, onRemove: () => setParams({ action: null }) });
  if (params.from) active.push({ key: 'from', label: `起: ${params.from}`, onRemove: () => setParams({ from: null }) });
  if (params.to) active.push({ key: 'to', label: `止: ${params.to}`, onRemove: () => setParams({ to: null }) });

  // 按日期分组显示
  const groups: Array<{ day: string; items: AuditLogEntry[] }> = [];
  for (const e of entries) {
    const day = formatDateTime(e.created_at).slice(0, 10);
    const last = groups[groups.length - 1];
    if (last && last.day === day) last.items.push(e);
    else groups.push({ day, items: [e] });
  }

  return (
    <>
      <PageHeader title="审计日志" description="所有管理操作的只读记录：谁在什么时候对什么对象做了什么，以及变更前后的差异" />

      <div className="mb-4">
        <FilterBar
          controls={
            <>
              <Select
                value={params.target_type ?? ''}
                placeholder="全部对象类型"
                options={Object.entries(TARGET_LABELS).map(([value, label]) => ({ value, label }))}
                onChange={(e) => setParams({ target_type: e.target.value || null })}
              />
              <div className="w-28">
                <Input
                  mono
                  placeholder="对象 ID"
                  value={targetIdDraft}
                  onChange={(e) => setTargetIdDraft(e.target.value)}
                  onBlur={() => setParams({ target_id: targetIdDraft.trim() || null })}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') setParams({ target_id: targetIdDraft.trim() || null });
                  }}
                />
              </div>
              <div className="w-28">
                <Input
                  placeholder="操作人"
                  value={actorDraft}
                  onChange={(e) => setActorDraft(e.target.value)}
                  onBlur={() => setParams({ actor_name: actorDraft.trim() || null })}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') setParams({ actor_name: actorDraft.trim() || null });
                  }}
                />
              </div>
              <Select
                value={params.action ?? ''}
                placeholder="全部动作"
                options={ACTION_GROUPS}
                onChange={(e) => setParams({ action: e.target.value || null })}
              />
              <label className="flex items-center gap-1 text-[11px] text-gray-400">
                从
                <span className="w-36 inline-block"><Input type="date" value={params.from ?? ''} max={params.to || undefined} onChange={(e) => setParams({ from: e.target.value || null })} /></span>
              </label>
              <label className="flex items-center gap-1 text-[11px] text-gray-400">
                至
                <span className="w-36 inline-block"><Input type="date" value={params.to ?? ''} min={params.from || undefined} onChange={(e) => setParams({ to: e.target.value || null })} /></span>
              </label>
            </>
          }
          active={active}
          onClearAll={() => setParams(Object.fromEntries(FILTER_KEYS.map((k) => [k, null])))}
        />
      </div>

      <DataState
        loading={first.loading}
        error={first.error}
        onRetry={first.reload}
        empty={entries.length === 0}
        emptyIcon={<FileClock className="w-8 h-8" />}
        emptyTitle={active.length ? '没有符合条件的操作记录' : '暂无操作记录'}
        skeleton="text"
      >
        <div className="bg-white border border-gray-200 rounded-xl shadow-xs overflow-hidden">
          {groups.map((g) => (
            <div key={g.day}>
              <div className="px-4 py-1.5 bg-gray-50 border-b border-gray-100 text-[10px] text-gray-400 uppercase tracking-wider font-semibold sticky top-0">
                {g.day}
              </div>
              <ul className="divide-y divide-gray-100">
                {g.items.map((e) => (
                  <AuditRow key={e.id} e={e} />
                ))}
              </ul>
            </div>
          ))}
        </div>
        <div ref={sentinel} className="h-1" />
        <div className="py-4 text-center text-[11px] text-gray-400">
          {loadingMore ? (
            <span className="inline-flex items-center gap-1">
              <Loader2 className="w-3.5 h-3.5 animate-spin" /> 加载中…
            </span>
          ) : moreError ? (
            <span className="text-rose-600">
              {moreError}
              <Button size="sm" variant="ghost" className="ml-2" onClick={() => void loadMore()}>
                重试
              </Button>
            </span>
          ) : cursor ? (
            <Button size="sm" variant="ghost" onClick={() => void loadMore()}>
              加载更早的记录
            </Button>
          ) : (
            `已显示全部 ${entries.length} 条记录`
          )}
        </div>
      </DataState>
    </>
  );
}

function AuditRow({ e }: { e: AuditLogEntry }) {
  const [open, setOpen] = useState(false);
  const hasDiff = e.before != null || e.after != null;
  const href = targetHref(e.target_type, e.target_id);
  return (
    <li className={cn(open && 'bg-gray-50/60')}>
      <div
        role={hasDiff ? 'button' : undefined}
        tabIndex={hasDiff ? 0 : undefined}
        onClick={() => hasDiff && setOpen((v) => !v)}
        onKeyDown={(ev) => {
          if (hasDiff && (ev.key === 'Enter' || ev.key === ' ')) {
            ev.preventDefault();
            setOpen((v) => !v);
          }
        }}
        className={cn('px-4 py-2.5 flex items-center gap-3 text-xs', hasDiff && 'cursor-pointer hover:bg-gray-50/70')}
      >
        <span className="w-3 shrink-0 text-gray-400">
          {hasDiff && (open ? <ChevronDown className="w-3 h-3" /> : <ChevronRight className="w-3 h-3" />)}
        </span>
        <span className="w-16 shrink-0 font-mono text-[11px] text-gray-400" title={formatDateTime(e.created_at)}>
          {formatDateTime(e.created_at).slice(11)}
        </span>
        <span className="w-24 shrink-0 truncate text-gray-700" title={e.actor_id ? `actor_id ${e.actor_id}` : undefined}>
          {actorLabel(e)}
        </span>
        <span className="font-medium text-gray-900 shrink-0">{actionLabel(e.action)}</span>
        {href ? (
          <Link to={href} onClick={(ev) => ev.stopPropagation()} className="text-purple-600 hover:text-purple-700 truncate">
            {targetLabel(e.target_type, e.target_id)}
          </Link>
        ) : (
          <span className="text-gray-600 truncate">{targetLabel(e.target_type, e.target_id)}</span>
        )}
        <span className="ml-auto shrink-0 font-mono text-[10px] text-gray-300">{e.action}</span>
        {e.ip && <span className="shrink-0 font-mono text-[10px] text-gray-300 hidden md:inline">{e.ip}</span>}
      </div>
      {open && hasDiff && (
        <div className="px-4 pb-3 pl-[4.5rem]">
          <JsonDiff before={e.before} after={e.after} />
        </div>
      )}
    </li>
  );
}
