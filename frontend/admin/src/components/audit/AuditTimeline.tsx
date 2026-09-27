import { useState } from 'react';
import { ChevronDown, ChevronRight } from 'lucide-react';
import { listAuditLogs } from '../../api/audit';
import { useAsync } from '../../hooks/useAsync';
import { actionLabel, actorLabel } from '../../lib/audit';
import { formatDateTime, formatRelative } from '../../lib/time';
import type { AuditLogEntry } from '../../types';
import { Button, DataState, JsonDiff } from '../ui';

// AuditTimeline 是每个详情页最后的"操作记录"段落（UI_DESIGN.md §3.2）：
// 把该对象的审计日志嵌在对象上下文里。reloadKey 变化时重新拉取（页面上
// 做完一次写操作后传入新值，让刚发生的操作立刻出现）。
export function AuditTimeline({
  targetType,
  targetId,
  limit = 20,
  reloadKey,
}: {
  targetType: string;
  targetId: string | number;
  limit?: number;
  reloadKey?: unknown;
}) {
  const [extra, setExtra] = useState<AuditLogEntry[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);

  const first = useAsync(
    async (signal) => {
      const res = await listAuditLogs({ target_type: targetType, target_id: String(targetId), limit }, signal);
      setExtra([]);
      setCursor(res.next_cursor || null);
      return res.data;
    },
    [targetType, targetId, limit, reloadKey],
  );

  const loadMore = async () => {
    if (!cursor) return;
    setLoadingMore(true);
    try {
      const res = await listAuditLogs({ target_type: targetType, target_id: String(targetId), limit, before: cursor });
      setExtra((prev) => [...prev, ...res.data]);
      setCursor(res.next_cursor || null);
    } finally {
      setLoadingMore(false);
    }
  };

  const entries = [...(first.data ?? []), ...extra];

  return (
    <DataState
      loading={first.loading}
      error={first.error}
      onRetry={first.reload}
      empty={entries.length === 0}
      emptyTitle="暂无操作记录"
      emptyDescription="该对象上的写操作（创建、修改、调价、审批等）会记录在这里"
      skeleton="text"
    >
      <ol className="relative border-l border-gray-200 ml-2 space-y-3">
        {entries.map((e) => (
          <TimelineItem key={e.id} entry={e} />
        ))}
      </ol>
      {cursor && (
        <div className="mt-3 pl-6">
          <Button variant="ghost" size="sm" loading={loadingMore} onClick={loadMore}>
            加载更早的记录
          </Button>
        </div>
      )}
    </DataState>
  );
}

function TimelineItem({ entry }: { entry: AuditLogEntry }) {
  const [open, setOpen] = useState(false);
  const hasDiff = entry.before != null || entry.after != null;
  return (
    <li className="pl-5 relative">
      <span className="absolute -left-[5px] top-1.5 w-2.5 h-2.5 rounded-full bg-white border-2 border-purple-400" />
      <button
        type="button"
        className="w-full text-left flex items-center gap-2 text-xs disabled:cursor-default cursor-pointer"
        onClick={() => setOpen((v) => !v)}
        disabled={!hasDiff}
      >
        {hasDiff ? (
          open ? <ChevronDown className="w-3 h-3 text-gray-400" /> : <ChevronRight className="w-3 h-3 text-gray-400" />
        ) : (
          <span className="w-3" />
        )}
        <span className="font-medium text-gray-900">{actionLabel(entry.action)}</span>
        <span className="text-gray-500">{actorLabel(entry)}</span>
        <span className="ml-auto text-[11px] text-gray-400" title={formatDateTime(entry.created_at)}>
          {formatRelative(entry.created_at)}
        </span>
      </button>
      {open && hasDiff && (
        <div className="mt-2 ml-5">
          <JsonDiff before={entry.before} after={entry.after} hideUnchanged />
        </div>
      )}
    </li>
  );
}
