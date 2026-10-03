import { useState, type ReactNode } from 'react';
import { Link } from 'react-router';
import { agentHref } from '../../../agent/brand';
import { Sparkles } from 'lucide-react';
import { Button, Modal, useToast } from '../../../components/ui';
import { useCan } from '../../../api/auth';
import { decideToolCall, type AgentProposal, type ContextRef } from '../../../api/agent';
import { errorMessage } from '../../../api/errors';
import { emitMutated } from '../../../agent/agentEvents';
import { useAgentOptional } from '../../../agent/AgentProvider';
import { useAgentEnabled, useAgentMeta } from '../../../agent/agentStore';
import { verdict } from '../../../agent/useAgentSuggestions';
import { refreshTodoCounts } from '../../../hooks/useTodoCounts';
import { cn } from '../../../lib/cn';
import { ApprovalCard, type ApprovalView } from './ApprovalCard';

// 现有页面的智能体嵌入点（设计 §19.3）：页面动作区按钮、列表行建议标记、详情意见卡。
// 原有的批准/驳回按钮与 A/R 快捷键不变；“采纳”只预填原因或打开审批卡，最终仍由人确认。

// useAgentUsable：智能体已启用且当前管理员有 agent:use。
export function useAgentUsable(): boolean {
  const enabled = useAgentEnabled();
  const allowed = useCan('agent:use');
  return enabled && allowed;
}

// AgentActionButton：「✦ 让智能体处理」——打开 Dock 并新建带剧本与 context_ref 的会话，立即开始运行。
export function AgentActionButton({
  playbook,
  label,
  context,
  message,
  size = 'md',
}: {
  playbook?: string;
  label: string;
  context?: ContextRef[];
  message?: string;
  size?: 'sm' | 'md';
}) {
  const agent = useAgentOptional();
  const meta = useAgentMeta();
  const usable = useAgentUsable();
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  if (!usable || !agent) return null;
  const pb = meta?.playbooks?.find((p) => p.name === playbook);
  const onClick = async () => {
    setBusy(true);
    try {
      let msg = message ?? pb?.starter ?? label;
      if (context?.length && !message) msg += `\n本次只处理：${context.map((c) => c.label ?? `${c.type} #${c.id}`).join('、')}`;
      await agent.start({ playbook, context_ref: context, message: msg, title: pb?.title });
    } catch (e) {
      toast.error('启动智能体失败', errorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Button size={size} loading={busy} onClick={() => void onClick()} icon={<Sparkles className="w-3.5 h-3.5 text-purple-600" />}>
      {label}
    </Button>
  );
}

function proposalView(p: AgentProposal): ApprovalView {
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
  };
}

// ProposalModal：在业务页面里打开提案的审批卡（“采纳”不是直接执行）。
export function ProposalModal({ proposal, onClose, onDone }: { proposal: AgentProposal | null; onClose: () => void; onDone?: () => void }) {
  const toast = useToast();
  if (!proposal) return null;
  return (
    <Modal open onClose={onClose} title="智能体提案" width="xl">
      <ApprovalCard
        view={proposalView(proposal)}
        showSessionLink={
          <Link to={agentHref(proposal.session_id)} onClick={onClose} className="text-[11px] text-purple-700 hover:underline">
            查看完整会话 →
          </Link>
        }
        onDecide={async (decision, opts) => {
          const res = await decideToolCall(proposal.session_id, proposal.tool_call_id, { decision, note: opts.note ?? '', args: opts.args ?? null });
          if (res.status === 'stale') throw new Error('对象已变化，提案已失效');
          if (res.status === 'failed') throw new Error(`执行失败（HTTP ${res.http_status}）`);
          if (res.status === 'executed') emitMutated({ target_type: res.target_type, target_id: res.target_id });
          refreshTodoCounts();
          toast.success(decision === 'approve' ? '已执行' : '已拒绝');
          onDone?.();
          onClose();
        }}
      />
    </Modal>
  );
}

const TONE = {
  purple: 'bg-purple-50 text-purple-700 border-purple-200',
  gray: 'bg-gray-50 text-gray-600 border-gray-200',
  amber: 'bg-amber-50 text-amber-700 border-amber-200',
};

// AgentSuggestionBadge：列表行的「✦ 建议通过 0.86」标记；悬停显示理由，点击打开审批卡。
export function AgentSuggestionBadge({ proposal, onDone }: { proposal: AgentProposal | undefined; onDone?: () => void }) {
  const [open, setOpen] = useState(false);
  if (!proposal) return null;
  const v = verdict(proposal);
  return (
    <>
      <button
        type="button"
        title={proposal.rationale || proposal.summary}
        onClick={(e) => {
          e.stopPropagation();
          setOpen(true);
        }}
        className={cn('inline-flex items-center gap-0.5 px-1.5 py-0.5 rounded border text-[10px] font-medium whitespace-nowrap cursor-pointer', TONE[v.tone])}
      >
        <Sparkles className="w-2.5 h-2.5" />
        {v.label}
        {typeof proposal.confidence === 'number' && <span className="font-mono opacity-80">{proposal.confidence.toFixed(2)}</span>}
      </button>
      {open && <ProposalModal proposal={proposal} onClose={() => setOpen(false)} onDone={onDone} />}
    </>
  );
}

// AgentOpinionCard：收件箱详情中的「智能体意见」卡，位于影响评估之后、操作按钮之前。
// onAdopt 只预填（如驳回原因），不执行任何写操作。
export function AgentOpinionCard({
  proposal,
  onAdopt,
  adoptLabel,
  onDone,
  extra,
}: {
  proposal: AgentProposal | undefined;
  onAdopt?: (p: AgentProposal) => void;
  adoptLabel?: string;
  onDone?: () => void;
  extra?: ReactNode;
}) {
  const agent = useAgentOptional();
  const [open, setOpen] = useState(false);
  if (!proposal) return null;
  const v = verdict(proposal);
  return (
    <div className="border border-purple-200 bg-purple-50/40 rounded-xl p-3 text-xs space-y-1.5">
      <div className="flex items-center gap-1.5">
        <Sparkles className="w-3.5 h-3.5 text-purple-600" />
        <span className="font-semibold text-gray-900">智能体意见</span>
        <span className={cn('px-1.5 py-0.5 rounded border text-[10px] font-medium', TONE[v.tone])}>{v.label}</span>
        {typeof proposal.confidence === 'number' && <span className="ml-auto font-mono text-[11px] text-gray-500">置信度 {proposal.confidence.toFixed(2)}</span>}
      </div>
      {proposal.rationale && <p className="text-gray-700">{proposal.rationale}</p>}
      {extra}
      <div className="flex items-center gap-2 pt-0.5">
        <Link to={agentHref(proposal.session_id)} className="text-[11px] text-purple-700 hover:underline">
          查看完整会话
        </Link>
        {agent && (
          <button
            type="button"
            className="text-[11px] text-purple-700 hover:underline cursor-pointer"
            onClick={() => {
              agent.selectSession(proposal.session_id);
              agent.setDockOpen(true);
            }}
          >
            追问
          </button>
        )}
        <span className="flex-1" />
        <Button size="sm" onClick={() => setOpen(true)}>
          打开提案
        </Button>
        {onAdopt && (
          <Button size="sm" variant="primary" onClick={() => onAdopt(proposal)}>
            {adoptLabel ?? '采纳'}
          </Button>
        )}
      </div>
      {open && <ProposalModal proposal={proposal} onClose={() => setOpen(false)} onDone={onDone} />}
    </div>
  );
}
