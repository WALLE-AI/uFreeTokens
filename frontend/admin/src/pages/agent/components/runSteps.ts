import type { LiveItem } from '../../../agent/AgentProvider';
import type { AgentMessage, AgentToolCall } from '../../../api/agent';
import type { ApprovalView } from './ApprovalCard';
import type { ToolCallView } from './ToolCallCard';

// 把会话历史（GET /agent/sessions/{id}）与 SSE 实时覆盖层统一成「运行」：一条用户消息 + 其后智能体的
// 步骤序列（思考 / 正文 / 工具 / 审批 / 报告），由 RunBlock 按“过程折叠 + 最终回答”渲染。
// 两种来源走同一结构，运行结束重新拉取详情时界面不跳动。

export type Step =
  | { kind: 'thinking'; key: string; text: string }
  | { kind: 'text'; key: string; text: string }
  | { kind: 'tool'; key: string; call: ToolCallView }
  | { kind: 'approval'; key: string; view: ApprovalView }
  | { kind: 'report'; key: string; text: string }
  | { kind: 'divider'; key: string; text: string };

export interface Run {
  key: string;
  user?: string;
  steps: Step[];
  startedAt?: string;
  endedAt?: string;
  live?: boolean;
}

export function approvalFromCall(c: AgentToolCall): ApprovalView {
  return {
    id: c.id,
    tool: c.tool,
    args: c.args,
    summary: c.summary,
    before: c.before,
    after: c.after,
    permission: c.required_perm,
    rationale: c.rationale,
    confidence: c.confidence,
    evidence: c.evidence,
    target_type: c.target_type,
    target_id: c.target_id,
    status: c.status,
    decided_by_name: c.decided_by_name,
    decision_note: c.decision_note,
    result: c.result,
  };
}

export function buildRuns(messages: AgentMessage[], calls: AgentToolCall[]): Run[] {
  const byId = new Map(calls.map((c) => [c.id, c]));
  const runs: Run[] = [];
  let cur: Run | null = null;
  const ensure = (m: AgentMessage): Run => {
    if (!cur) {
      cur = { key: `m${m.seq}`, steps: [], startedAt: m.created_at };
      runs.push(cur);
    }
    cur.endedAt = m.created_at;
    return cur;
  };
  for (const m of messages) {
    if (m.role === 'user') {
      cur = { key: `m${m.seq}`, user: m.content, steps: [], startedAt: m.created_at, endedAt: m.created_at };
      runs.push(cur);
      continue;
    }
    if (m.role === 'summary') {
      ensure(m).steps.push({ kind: 'divider', key: `d${m.seq}`, text: '早期对话已压缩为摘要' });
      continue;
    }
    if (m.role === 'report') {
      ensure(m).steps.push({ kind: 'report', key: `r${m.seq}`, text: m.content });
      continue;
    }
    if (m.role !== 'assistant') continue;
    const tcs = m.tool_calls ?? [];
    if (!m.content && !m.reasoning && tcs.length === 0) continue;
    const run = ensure(m);
    if (m.reasoning) run.steps.push({ kind: 'thinking', key: `t${m.seq}`, text: m.reasoning });
    if (m.content) run.steps.push({ kind: 'text', key: `x${m.seq}`, text: m.content });
    for (const tc of tcs) {
      const rec = byId.get(tc.id);
      if (rec?.risk === 'write' && rec.status !== 'error') {
        run.steps.push({ kind: 'approval', key: tc.id, view: approvalFromCall(rec) });
        continue;
      }
      run.steps.push({
        kind: 'tool',
        key: tc.id,
        call: {
          id: tc.id,
          tool: tc.name,
          args: rec?.args ?? tc.arguments,
          status: rec?.status ?? 'error',
          summary: rec?.summary,
          http_status: rec?.http_status,
          duration_ms: rec?.duration_ms,
          result: rec?.result,
        },
      });
    }
  }
  return runs;
}

export function liveSteps(items: LiveItem[]): Step[] {
  return items.map((it, i): Step => {
    if (it.kind === 'thinking') return { kind: 'thinking', key: `lt${i}`, text: it.text };
    if (it.kind === 'text') return { kind: 'text', key: `lx${i}`, text: it.text };
    if (it.kind === 'tool') return { kind: 'tool', key: it.id, call: it };
    return { kind: 'approval', key: it.data.id, view: { ...it.data, status: it.status } };
  });
}

// mergeLive 把实时覆盖层接到运行列表上：有 pendingUser 时是新的一次运行；否则（审批后继续）接在最后一次运行之后。
export function mergeLive(runs: Run[], pendingUser: string | null, live: LiveItem[], streaming: boolean): Run[] {
  const steps = liveSteps(live);
  if (pendingUser != null) return [...runs, { key: 'live', user: pendingUser, steps, live: true }];
  if (!streaming && steps.length === 0) return runs;
  const last = runs[runs.length - 1];
  if (!last) return [{ key: 'live', steps, live: true }];
  return [...runs.slice(0, -1), { ...last, steps: [...last.steps, ...steps], live: streaming || steps.length > 0 }];
}

// splitFinal 把末尾连续的正文段当作最终回答，其余为过程。
export function splitFinal(steps: Step[]): { process: Step[]; final: Step[] } {
  let i = steps.length;
  while (i > 0 && (steps[i - 1].kind === 'text' || steps[i - 1].kind === 'report')) i--;
  return { process: steps.slice(0, i), final: steps.slice(i) };
}

// toolTitle 从工具描述里取第一个短句作为可读标题（如“列出调价申请”）。
export function toolTitle(description: string | undefined): string {
  if (!description) return '';
  const head = description.split(/[：:（(。，,；;\n]/)[0].trim();
  return head.length > 24 ? `${head.slice(0, 24)}…` : head;
}

export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return '';
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  return `${Math.floor(s / 60)}m${String(s % 60).padStart(2, '0')}s`;
}
