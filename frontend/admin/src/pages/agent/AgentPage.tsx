import { useCallback, useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router';
import { Archive, Bot, Plus, Timer } from 'lucide-react';
import { Button, DataState, SearchInput, Select } from '../../components/ui';
import { useAgent } from '../../agent/AgentProvider';
import { useAgentMeta } from '../../agent/agentStore';
import { listAgentSessions, updateAgentSession, type AgentSession } from '../../api/agent';
import { useCan } from '../../api/auth';
import { cn } from '../../lib/cn';
import { formatDateTime, formatRelative } from '../../lib/time';
import { Conversation } from './components/Conversation';

// /agent、/agent/:sessionId 会话工作台（设计 §19.4a）：左栏会话列表、中栏对话、右栏上下文与运行指标。
// 后台作业会话以 ⏱ 标识、只读；有 audit:read 时可切换查看全部管理员的会话。

const STATUS_DOT: Record<string, string> = {
  running: 'bg-purple-500 animate-pulse',
  awaiting_approval: 'bg-amber-500',
  failed: 'bg-rose-500',
  stopped: 'bg-gray-400',
  completed: 'bg-emerald-500',
  idle: 'bg-gray-300',
};

export default function AgentPage() {
  const { sessionId: param } = useParams();
  const navigate = useNavigate();
  const meta = useAgentMeta();
  const canAudit = useCan('audit:read');
  const { sessionId, selectSession, detail, start, usage } = useAgent();
  const [list, setList] = useState<AgentSession[] | null>(null);
  const [q, setQ] = useState('');
  const [scope, setScope] = useState<'mine' | 'all'>('mine');
  const [err, setErr] = useState<unknown>(null);
  const [playbook, setPlaybook] = useState('');

  useEffect(() => {
    const id = param ? Number(param) : null;
    if (id && id !== sessionId) selectSession(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [param]);

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

  const open = (id: number) => {
    selectSession(id);
    navigate(`/agent/${id}`);
  };
  const pb = meta?.playbooks?.find((p) => p.name === playbook);
  const s = detail?.session;

  return (
    <div className="-mx-4 md:-mx-8 -my-6 h-[calc(100vh-3rem)] flex border-t border-gray-100">
      <div className="w-64 shrink-0 border-r border-gray-200 flex flex-col bg-white">
        <div className="p-3 space-y-2 border-b border-gray-100">
          <div className="flex items-center gap-1.5">
            <Select
              className="flex-1"
              value={playbook}
              onChange={(e) => setPlaybook(e.target.value)}
              options={[{ value: '', label: '自由对话' }, ...(meta?.playbooks ?? []).map((p) => ({ value: p.name, label: p.title }))]}
            />
            <Button
              size="sm"
              variant="primary"
              icon={<Plus className="w-3 h-3" />}
              onClick={() =>
                void start({ playbook: playbook || undefined, message: pb?.starter }).then(() => {
                  navigate('/agent');
                  load();
                })
              }
            >
              新会话
            </Button>
          </div>
          {pb && <p className="text-[11px] text-gray-500">{pb.description}</p>}
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
                <li key={x.id}>
                  <button
                    type="button"
                    onClick={() => open(x.id)}
                    className={cn('w-full text-left px-3 py-2 flex items-start gap-2 hover:bg-gray-50 cursor-pointer', x.id === sessionId && 'bg-purple-50/70')}
                  >
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
                </li>
              ))}
            </ul>
          </DataState>
        </div>
      </div>

      <div className="flex-1 min-w-0 flex flex-col bg-white">
        {!meta?.enabled ? (
          <div className="p-8 text-xs text-gray-500">
            <Bot className="w-5 h-5 text-gray-400 mb-2" />
            智能体未启用{meta?.missing?.length ? `（缺少：${meta.missing.join('、')}）` : ''}。
          </div>
        ) : (
          <Conversation className="flex-1" />
        )}
      </div>

      <div className="hidden xl:block w-64 shrink-0 border-l border-gray-200 bg-white p-4 space-y-4 text-xs overflow-y-auto">
        {s ? (
          <>
            <div>
              <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold mb-1">会话</div>
              <div className="text-gray-900 font-medium break-words">{s.title || `会话 #${s.id}`}</div>
              <div className="text-gray-500 mt-0.5">
                {s.admin_name} · {formatDateTime(s.created_at)}
              </div>
              {s.playbook && <div className="text-gray-500 mt-0.5">剧本：{meta?.playbooks?.find((p) => p.name === s.playbook)?.title ?? s.playbook}</div>}
            </div>
            <div>
              <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold mb-1">运行指标</div>
              <div className="space-y-0.5 text-gray-600">
                <div>状态：{s.status}{s.status_reason ? `（${s.status_reason}）` : ''}</div>
                <div>
                  轮数：{usage?.turns ? `${usage.turns} / ` : ''}
                  {s.turns}（上限 {meta?.max_turns}）
                </div>
                <div>
                  Token：{((s.tokens_in + s.tokens_out) / 1000).toFixed(1)}k / {((meta?.max_tokens ?? 0) / 1000).toFixed(0)}k
                </div>
                <div>模型：{s.model || meta?.model}</div>
              </div>
            </div>
            {!detail?.read_only && (
              <Button
                size="sm"
                icon={<Archive className="w-3 h-3" />}
                onClick={() =>
                  void updateAgentSession(s.id, { archived: true }).then(() => {
                    selectSession(null);
                    navigate('/agent');
                    load();
                  })
                }
              >
                归档会话
              </Button>
            )}
          </>
        ) : (
          <div className="text-gray-400">选择或新建一个会话。</div>
        )}
      </div>
    </div>
  );
}
