import { useCallback, useEffect, useState } from 'react';
import { Archive, Timer } from 'lucide-react';
import { DataState, SearchInput } from '../../../components/ui';
import { useAgent } from '../../../agent/AgentProvider';
import { listAgentSessions, updateAgentSession, type AgentSession } from '../../../api/agent';
import { useCan } from '../../../api/auth';
import { cn } from '../../../lib/cn';
import { formatRelative } from '../../../lib/time';

// SessionList：Dock「历史」页签（原 /agent 会话工作台左栏）。后台作业会话以 ⏱ 标识、只读；
// 有 audit:read 时可切换查看全部管理员的会话。

const STATUS_DOT: Record<string, string> = {
  running: 'bg-purple-500 animate-pulse',
  awaiting_approval: 'bg-amber-500',
  failed: 'bg-rose-500',
  stopped: 'bg-gray-400',
  completed: 'bg-emerald-500',
  idle: 'bg-gray-300',
};

export function SessionList({ className }: { className?: string }) {
  const { sessionId, openSession, selectSession, detail } = useAgent();
  const canAudit = useCan('audit:read');
  const [list, setList] = useState<AgentSession[] | null>(null);
  const [q, setQ] = useState('');
  const [scope, setScope] = useState<'mine' | 'all'>('mine');
  const [err, setErr] = useState<unknown>(null);

  const load = useCallback(() => {
    listAgentSessions({ q: q || undefined, scope: scope === 'all' ? 'all' : undefined, limit: 50 })
      .then((p) => {
        setList(p.data ?? []);
        setErr(null);
      })
      .catch(setErr);
  }, [q, scope]);
  useEffect(() => {
    load();
  }, [load, detail?.session.status, sessionId]);

  const archive = (x: AgentSession) =>
    void updateAgentSession(x.id, { archived: true }).then(() => {
      if (x.id === sessionId) selectSession(null);
      load();
    });

  return (
    <div className={cn('flex flex-col min-h-0', className)}>
      <div className="p-3 space-y-2 border-b border-gray-100">
        <SearchInput placeholder="搜索会话" value={q} onChange={(e) => setQ(e.target.value)} />
        {canAudit && (
          <div className="flex gap-1 text-[11px]">
            {(['mine', 'all'] as const).map((k) => (
              <button key={k} type="button" onClick={() => setScope(k)} className={cn('px-2 py-0.5 rounded-full cursor-pointer', scope === k ? 'bg-purple-100 text-purple-700' : 'text-gray-500 hover:bg-gray-50')}>
                {k === 'mine' ? '我的' : '全部管理员'}
              </button>
            ))}
          </div>
        )}
      </div>
      <div className="flex-1 overflow-y-auto">
        <DataState loading={!list && !err} error={err} onRetry={load} empty={list?.length === 0} emptyTitle="还没有会话" skeleton="text">
          <ul className="py-1">
            {(list ?? []).map((x) => (
              <li key={x.id} className={cn('group flex items-start hover:bg-gray-50', x.id === sessionId && 'bg-purple-50/70')}>
                <button type="button" onClick={() => openSession(x.id)} className="flex-1 min-w-0 text-left px-3 py-2 flex items-start gap-2 cursor-pointer">
                  <span className={cn('w-1.5 h-1.5 rounded-full mt-1.5 shrink-0', STATUS_DOT[x.status] ?? 'bg-gray-300')} />
                  <span className="min-w-0 flex-1">
                    <span className="flex items-center gap-1 text-xs text-gray-900 truncate">
                      {x.mode === 'batch' && <Timer className="w-3 h-3 text-gray-400 shrink-0" />}
                      <span className="truncate">{x.title || `会话 #${x.id}`}</span>
                    </span>
                    <span className="block text-[11px] text-gray-400 truncate">
                      {formatRelative(x.updated_at)}
                      {scope === 'all' && ` · ${x.admin_name}`}
                    </span>
                  </span>
                </button>
                {scope === 'mine' && x.mode !== 'batch' && (
                  <button
                    type="button"
                    title="归档"
                    onClick={() => archive(x)}
                    className="p-2 text-gray-300 hover:text-gray-600 cursor-pointer opacity-0 group-hover:opacity-100 focus:opacity-100"
                  >
                    <Archive className="w-3.5 h-3.5" />
                  </button>
                )}
              </li>
            ))}
          </ul>
        </DataState>
      </div>
    </div>
  );
}
