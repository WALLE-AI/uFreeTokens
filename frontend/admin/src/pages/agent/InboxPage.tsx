import { useCallback, useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router';
import { MessagesSquare, Timer } from 'lucide-react';
import { Button, DataState, PageHeader, Pills, useToast } from '../../components/ui';
import { decideToolCall, listAgentProposals, type AgentProposal } from '../../api/agent';
import { errorMessage } from '../../api/errors';
import { useAuth } from '../../api/auth';
import { useAgentMeta } from '../../agent/agentStore';
import { emitMutated } from '../../agent/agentEvents';
import { refreshTodoCounts } from '../../hooks/useTodoCounts';
import { cn } from '../../lib/cn';
import { formatRelative } from '../../lib/time';
import { ApprovalCard, type ApprovalView } from './components/ApprovalCard';

// /agent/inbox 提案收件箱（设计 §19.4b，实施方案 M2-F05 / M3-F02）：交互会话与后台作业的提案统一处理。
// 键盘：J/K 切换、A 通过、R 拒绝、E 编辑（与调价审批一致）；同类批量通过逐条调用、逐条审计，失败项标红保留。

const TABS = [
  { value: 'pending', label: '待处理' },
  { value: 'executed', label: '已执行' },
  { value: 'rejected', label: '已拒绝' },
  { value: 'stale', label: '失效' },
  { value: 'superseded', label: '已作废' },
];

function toView(p: AgentProposal): ApprovalView {
  return {
    id: p.tool_call_id,
    tool: p.tool,
    args: p.args,
    summary: p.summary,
    before: p.before,
    after: p.after,
    permission: p.required_perm,
    rationale: p.rationale,
    confidence: p.confidence,
    evidence: p.evidence,
    target_type: p.target_type,
    target_id: p.target_id,
    status: p.status === 'pending' ? 'pending_approval' : p.status,
    decided_by_name: p.decided_by_name,
    result: p.result,
  };
}

export default function InboxPage() {
  const toast = useToast();
  const meta = useAgentMeta();
  const { me } = useAuth();
  const [status, setStatus] = useState('pending');
  const [mine, setMine] = useState(true);
  const [list, setList] = useState<AgentProposal[] | null>(null);
  const [err, setErr] = useState<unknown>(null);
  const [selected, setSelected] = useState<number | null>(null);
  const [checked, setChecked] = useState<Set<number>>(new Set());
  const [failed, setFailed] = useState<Map<number, string>>(new Map());
  const [batching, setBatching] = useState(false);

  const load = useCallback(() => {
    listAgentProposals({ status, mine: mine ? 1 : 0, limit: 200 })
      .then((r) => {
        setList(r.data ?? []);
        setErr(null);
      })
      .catch(setErr);
  }, [status, mine]);
  useEffect(() => {
    setList(null);
    setChecked(new Set());
    load();
  }, [load]);

  const groups = useMemo(() => {
    const m = new Map<string, AgentProposal[]>();
    for (const p of list ?? []) {
      const k = p.playbook || p.tool;
      m.set(k, [...(m.get(k) ?? []), p]);
    }
    return [...m.entries()];
  }, [list]);
  const flat = useMemo(() => groups.flatMap(([, ps]) => ps), [groups]);
  const current = flat.find((p) => p.id === selected) ?? flat[0] ?? null;
  const pbTitle = (name: string) => meta?.playbooks?.find((p) => p.name === name)?.title ?? name;
  const canHandle = (p: AgentProposal) => !!me && (me.permissions.includes('*') || me.permissions.includes(p.required_perm as never));

  const decideOne = async (p: AgentProposal, decision: 'approve' | 'reject', opts: { note?: string; args?: unknown } = {}) => {
    const res = await decideToolCall(p.session_id, p.tool_call_id, { decision, note: opts.note ?? '', args: opts.args ?? null });
    if (res.status === 'executed') emitMutated({ target_type: res.target_type, target_id: res.target_id });
    if (res.status === 'stale') throw new Error('对象已变化，提案已失效（412）');
    if (res.status === 'failed') throw new Error(`执行失败（HTTP ${res.http_status}）`);
    return res;
  };

  const advance = () => {
    const i = flat.findIndex((p) => p.id === current?.id);
    setSelected(flat[i + 1]?.id ?? flat[i - 1]?.id ?? null);
  };

  const onDecide = async (decision: 'approve' | 'reject', opts: { note?: string; args?: unknown }) => {
    if (!current) return;
    await decideOne(current, decision, opts);
    toast.success(decision === 'approve' ? '已执行' : '已拒绝');
    advance();
    refreshTodoCounts();
    load();
  };

  // 同类批量通过：逐条串行调用，失败项标红保留（D5）。
  const batchApprove = async () => {
    setBatching(true);
    const errs = new Map<number, string>();
    let ok = 0;
    for (const p of flat.filter((x) => checked.has(x.id))) {
      try {
        await decideOne(p, 'approve');
        ok++;
      } catch (e) {
        errs.set(p.id, errorMessage(e));
      }
    }
    setFailed(errs);
    setChecked(new Set(errs.keys()));
    setBatching(false);
    if (errs.size) toast.error(`${ok} 条已执行，${errs.size} 条失败`, '失败项已标红保留');
    else toast.success(`${ok} 条已执行`);
    refreshTodoCounts();
    load();
  };

  // J/K/A/R/E 键盘操作（输入框聚焦时不生效）。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      const t = e.target as HTMLElement;
      if (t.closest('input,textarea,select,[contenteditable="true"]') || document.querySelector('[data-modal-open="true"]')) return;
      const i = flat.findIndex((p) => p.id === current?.id);
      const k = e.key.toLowerCase();
      if (k === 'j') setSelected(flat[Math.min(i + 1, flat.length - 1)]?.id ?? null);
      else if (k === 'k') setSelected(flat[Math.max(i - 1, 0)]?.id ?? null);
      else if (['a', 'r', 'e'].includes(k)) {
        const label = { a: '批准执行', r: '拒绝', e: '编辑' }[k as 'a' | 'r' | 'e'];
        const btn = [...document.querySelectorAll<HTMLButtonElement>('[data-inbox-card] button')].find((b) => b.textContent?.trim() === label);
        if (btn && !btn.disabled) {
          e.preventDefault();
          btn.click();
        }
      } else return;
      e.preventDefault();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [flat, current?.id]);

  return (
    <div className="space-y-4">
      <PageHeader title="提案收件箱" description="智能体提出的写操作在这里审批：通过后以你的身份执行并记入审计日志。" />
      <div className="flex flex-wrap items-center gap-3">
        <Pills options={TABS} value={status} onChange={setStatus} />
        <label className="flex items-center gap-1.5 text-xs text-gray-600 cursor-pointer">
          <input type="checkbox" checked={mine} onChange={(e) => setMine(e.target.checked)} />
          只看我能处理的
        </label>
        <span className="text-[11px] text-gray-400">J / K 切换 · A 通过 · R 拒绝 · E 编辑</span>
      </div>
      <DataState loading={!list && !err} error={err} onRetry={load} empty={list?.length === 0} emptyTitle="没有提案" skeleton="cards">
        <div className="grid grid-cols-1 lg:grid-cols-[minmax(0,22rem)_minmax(0,1fr)] gap-4">
          <div className="border border-gray-200 rounded-xl bg-white overflow-hidden self-start">
            {groups.map(([name, ps]) => {
              const pendingIds = ps.filter((p) => p.status === 'pending' && canHandle(p)).map((p) => p.id);
              return (
                <div key={name}>
                  <div className="px-3 py-1.5 bg-gray-50 border-b border-gray-100 flex items-center justify-between text-[11px] text-gray-500 font-medium">
                    <span>
                      {pbTitle(name)}（{ps.length}）
                    </span>
                    {pendingIds.length > 1 && (
                      <button type="button" className="text-purple-700 hover:underline cursor-pointer" onClick={() => setChecked(new Set(pendingIds))}>
                        全选同类
                      </button>
                    )}
                  </div>
                  <ul className="divide-y divide-gray-100">
                    {ps.map((p) => (
                      <li key={p.id}>
                        <div
                          onClick={() => setSelected(p.id)}
                          className={cn(
                            'px-3 py-2 flex items-start gap-2 cursor-pointer hover:bg-gray-50',
                            current?.id === p.id && 'bg-purple-50/70',
                            failed.has(p.id) && 'bg-rose-50',
                          )}
                        >
                          {p.status === 'pending' && (
                            <input
                              type="checkbox"
                              className="mt-0.5"
                              disabled={!canHandle(p)}
                              checked={checked.has(p.id)}
                              onClick={(e) => e.stopPropagation()}
                              onChange={(e) => {
                                const next = new Set(checked);
                                if (e.target.checked) next.add(p.id);
                                else next.delete(p.id);
                                setChecked(next);
                              }}
                            />
                          )}
                          <div className="min-w-0 flex-1">
                            <div className={cn('text-xs truncate', canHandle(p) ? 'text-gray-900' : 'text-gray-400')}>{p.summary}</div>
                            <div className="text-[11px] text-gray-400 flex items-center gap-1">
                              {p.job_id ? <Timer className="w-3 h-3" /> : <MessagesSquare className="w-3 h-3" />}
                              {formatRelative(p.created_at)}
                              {typeof p.confidence === 'number' && <span className="font-mono text-purple-600">✦{p.confidence.toFixed(2)}</span>}
                              {!canHandle(p) && <span className="text-amber-600">需 {p.required_perm}</span>}
                            </div>
                            {failed.has(p.id) && <div className="text-[11px] text-rose-600 truncate">{failed.get(p.id)}</div>}
                          </div>
                        </div>
                      </li>
                    ))}
                  </ul>
                </div>
              );
            })}
            {checked.size > 0 && (
              <div className="px-3 py-2 border-t border-gray-200 flex items-center justify-between bg-white sticky bottom-0">
                <span className="text-xs text-gray-600">已选 {checked.size}</span>
                <Button size="sm" variant="primary" loading={batching} onClick={() => void batchApprove()}>
                  批量通过（逐条执行）
                </Button>
              </div>
            )}
          </div>
          {current && (
            <div data-inbox-card className="space-y-2">
              <div className="text-[11px] text-gray-500 flex items-center gap-2">
                <span>提案 #{current.id}</span>
                <span>·</span>
                <span>{pbTitle(current.playbook)}</span>
                <span>·</span>
                <Link to={`/agent/${current.session_id}`} className="text-purple-700 hover:underline">
                  {current.job_id ? '⏱ 来自作业会话' : '💬 查看会话'}：{current.session_title || `#${current.session_id}`}
                </Link>
              </div>
              <ApprovalCard key={current.id} view={toView(current)} onDecide={onDecide} />
            </div>
          )}
        </div>
      </DataState>
    </div>
  );
}
