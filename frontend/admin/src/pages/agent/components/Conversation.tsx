import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { Bot, FileText, Loader2, Send, Square, UserRound, X } from 'lucide-react';
import { Button, Textarea } from '../../../components/ui';
import { useAgent, type LiveItem } from '../../../agent/AgentProvider';
import { useAgentMeta } from '../../../agent/agentStore';
import type { AgentMessage, AgentToolCall } from '../../../api/agent';
import { cn } from '../../../lib/cn';
import { Markdown } from './Markdown';
import { ApprovalCard, type ApprovalView } from './ApprovalCard';
import { ToolCallCard } from './ToolCallCard';

// Conversation：消息流 + 输入框（Enter 发送 / Shift+Enter 换行 / Esc 停止）。Dock 与 /agent 页面
// 共用同一个组件（设计 §19.1）。持久化的消息来自 GET /agent/sessions/{id}，运行中的增量来自 SSE 覆盖层，
// 运行结束后重新拉取详情、清空覆盖层。

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

function Bubble({ role, children }: { role: 'user' | 'assistant' | 'report'; children: ReactNode }) {
  if (role === 'user') {
    return (
      <div className="flex gap-2 justify-end">
        <div className="max-w-[85%] bg-purple-600 text-white rounded-2xl rounded-tr-sm px-3 py-2 text-xs whitespace-pre-wrap break-words">{children}</div>
        <UserRound className="w-5 h-5 text-gray-300 shrink-0 mt-0.5" />
      </div>
    );
  }
  return (
    <div className="flex gap-2">
      {role === 'report' ? <FileText className="w-5 h-5 text-gray-400 shrink-0 mt-0.5" /> : <Bot className="w-5 h-5 text-purple-500 shrink-0 mt-0.5" />}
      <div className={cn('min-w-0 flex-1', role === 'report' && 'bg-gray-50 border border-gray-200 rounded-xl px-3 py-2')}>{children}</div>
    </div>
  );
}

function Timeline({ messages, calls, onDecide }: { messages: AgentMessage[]; calls: AgentToolCall[]; onDecide: (id: string, d: 'approve' | 'reject', o: { note?: string; args?: unknown }) => Promise<void> }) {
  const byId = useMemo(() => new Map(calls.map((c) => [c.id, c])), [calls]);
  return (
    <>
      {messages.map((m) => {
        if (m.role === 'user') return <Bubble key={m.seq} role="user">{m.content}</Bubble>;
        if (m.role === 'summary')
          return (
            <div key={m.seq} className="text-[11px] text-gray-400 text-center">
              — 早期对话已压缩为摘要 —
            </div>
          );
        if (m.role === 'report')
          return (
            <Bubble key={m.seq} role="report">
              <Markdown text={m.content} />
            </Bubble>
          );
        if (m.role !== 'assistant') return null;
        const tcs = m.tool_calls ?? [];
        if (!m.content && tcs.length === 0) return null;
        return (
          <Bubble key={m.seq} role="assistant">
            <div className="space-y-1.5">
              {m.content && <Markdown text={m.content} />}
              {tcs.map((tc) => {
                const rec = byId.get(tc.id);
                if (rec?.risk === 'write' && rec.status !== 'error') {
                  return <ApprovalCard key={tc.id} view={approvalFromCall(rec)} onDecide={(d, o) => onDecide(tc.id, d, o)} />;
                }
                return (
                  <ToolCallCard
                    key={tc.id}
                    call={{
                      id: tc.id,
                      tool: tc.name,
                      args: rec?.args ?? tc.arguments,
                      status: rec?.status ?? 'error',
                      summary: rec?.summary,
                      http_status: rec?.http_status,
                      duration_ms: rec?.duration_ms,
                      result: rec?.result,
                    }}
                  />
                );
              })}
            </div>
          </Bubble>
        );
      })}
    </>
  );
}

function LiveItems({ items, onDecide }: { items: LiveItem[]; onDecide: (id: string, d: 'approve' | 'reject', o: { note?: string; args?: unknown }) => Promise<void> }) {
  if (items.length === 0) return null;
  return (
    <Bubble role="assistant">
      <div className="space-y-1.5">
        {items.map((it, i) => {
          if (it.kind === 'text') return <Markdown key={i} text={it.text} />;
          if (it.kind === 'tool') return <ToolCallCard key={it.id} call={it} />;
          return (
            <ApprovalCard
              key={it.data.id}
              view={{ ...it.data, permission: it.data.permission, status: it.status }}
              onDecide={(d, o) => onDecide(it.data.id, d, o)}
            />
          );
        })}
      </div>
    </Bubble>
  );
}

export function ContextChips() {
  const { chips, dismissChip, detail } = useAgent();
  const sessionRefs = (detail?.session.context_ref as Array<{ type: string; id: string; label?: string }> | null) ?? [];
  if (chips.length === 0 && sessionRefs.length === 0) return null;
  return (
    <div className="flex flex-wrap items-center gap-1 text-[11px]">
      <span className="text-gray-400">上下文：</span>
      {sessionRefs.slice(0, 6).map((c) => (
        <span key={`s${c.type}${c.id}`} className="px-1.5 py-0.5 rounded-full bg-gray-100 text-gray-600">
          {c.label ?? `${c.type} #${c.id}`}
        </span>
      ))}
      {sessionRefs.length > 6 && <span className="text-gray-400">等 {sessionRefs.length} 个</span>}
      {!detail &&
        chips.map((c) => (
          <span key={`${c.type}${c.id}`} className="pl-1.5 pr-1 py-0.5 rounded-full bg-purple-50 text-purple-700 border border-purple-200 inline-flex items-center gap-0.5">
            {c.label ?? `${c.type} #${c.id}`}
            <button type="button" onClick={() => dismissChip(c)} className="cursor-pointer text-purple-400 hover:text-purple-700" aria-label="移除">
              <X className="w-3 h-3" />
            </button>
          </span>
        ))}
    </div>
  );
}

export function Conversation({ className }: { className?: string }) {
  const { detail, detailError, live, pendingUser, phase, lastError, send, cancel, decide, sessionId } = useAgent();
  const meta = useAgentMeta();
  const [text, setText] = useState('');
  const scrollRef = useRef<HTMLDivElement>(null);
  const running = phase === 'streaming';
  const readOnly = !!detail?.read_only;

  useEffect(() => {
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [detail, live, pendingUser]);

  const submit = async () => {
    const t = text.trim();
    if (!t || running) return;
    setText('');
    await send(t);
  };
  const onDecide = async (id: string, d: 'approve' | 'reject', o: { note?: string; args?: unknown }) => {
    await decide(id, d, o);
  };

  const messages = detail?.messages ?? [];
  const calls = detail?.tool_calls ?? [];
  const empty = messages.length === 0 && !pendingUser && live.length === 0;

  return (
    <div className={cn('flex flex-col min-h-0', className)}>
      <div ref={scrollRef} className="flex-1 overflow-y-auto px-3 py-3 space-y-3">
        {detailError != null && <div className="text-xs text-rose-600">会话加载失败：{String(detailError)}</div>}
        {empty && (
          <div className="text-xs text-gray-500 space-y-2 pt-6">
            <div className="flex items-center gap-1.5 text-gray-900 font-medium">
              <Bot className="w-4 h-4 text-purple-500" />
              {sessionId ? '开始对话' : '运营智能体'}
            </div>
            <p>用自然语言查询待办、调价、渠道健康与日志；写操作会生成提案，由你审批后才执行。</p>
            <div className="flex flex-wrap gap-1.5 pt-1">
              {['今天有多少待审调价？被拦截的是哪些？', '巡检最近一小时的渠道健康', '有哪些新的上游优惠？'].map((q) => (
                <button key={q} type="button" onClick={() => setText(q)} className="px-2 py-1 rounded-full border border-gray-200 hover:border-purple-300 hover:text-purple-700 cursor-pointer">
                  {q}
                </button>
              ))}
            </div>
          </div>
        )}
        <Timeline messages={messages} calls={calls} onDecide={onDecide} />
        {pendingUser && <Bubble role="user">{pendingUser}</Bubble>}
        <LiveItems items={live} onDecide={onDecide} />
        {running && live.length === 0 && (
          <div className="flex items-center gap-1.5 text-[11px] text-gray-400 pl-7">
            <Loader2 className="w-3 h-3 animate-spin" />
            思考中…
          </div>
        )}
        {lastError && <div className="text-[11px] text-rose-600 pl-7">{lastError}</div>}
        {phase === 'awaiting_approval' && !running && <div className="text-[11px] text-amber-600 pl-7">等待审批：处理上面的提案后智能体会继续。</div>}
      </div>
      <div className="border-t border-gray-100 px-3 py-2 space-y-1.5">
        <ContextChips />
        {readOnly ? (
          <div className="text-[11px] text-gray-500">该会话为只读（他人会话或后台作业），发送消息将新建会话。</div>
        ) : null}
        <div className="flex items-end gap-1.5">
          <Textarea
            rows={2}
            value={text}
            placeholder={meta?.enabled ? '输入指令… Enter 发送，Shift+Enter 换行' : '智能体未启用'}
            disabled={!meta?.enabled}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
                e.preventDefault();
                void submit();
              } else if (e.key === 'Escape' && running) {
                void cancel();
              }
            }}
            className="flex-1 resize-none"
          />
          {running ? (
            <Button size="sm" onClick={() => void cancel()} icon={<Square className="w-3 h-3" />}>
              停止
            </Button>
          ) : (
            <Button size="sm" variant="primary" disabled={!text.trim() || !meta?.enabled} onClick={() => void submit()} icon={<Send className="w-3 h-3" />}>
              发送
            </Button>
          )}
        </div>
      </div>
    </div>
  );
}
