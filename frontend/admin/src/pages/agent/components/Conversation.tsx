import { useEffect, useMemo, useRef, useState } from 'react';
import { Bot, MapPin, Send, Square, X } from 'lucide-react';
import { Button, Textarea } from '../../../components/ui';
import { useAgent, type Phase } from '../../../agent/AgentProvider';
import { useAgentMeta } from '../../../agent/agentStore';
import { ASSISTANT_NAME } from '../../../agent/brand';
import { cn } from '../../../lib/cn';
import { RunBlock, type RunState } from './RunBlock';
import { buildRuns, mergeLive } from './runSteps';

// Conversation：运行流 + 输入框（Enter 发送 / Shift+Enter 换行 / Esc 停止），渲染在全局 Dock 的「对话」页签。持久化的消息来自 GET /agent/sessions/{id}，运行中的增量来自 SSE 覆盖层，
// 两者都转换为 Run（runSteps.ts）后由 RunBlock 渲染；运行结束后重新拉取详情、清空覆盖层。

function lastRunState(phase: Phase, sessionStatus: string | undefined): RunState {
  if (phase === 'streaming') return 'running';
  if (phase === 'awaiting_approval') return 'awaiting';
  if (phase === 'error') return 'error';
  if (sessionStatus === 'stopped') return 'stopped';
  return 'done';
}

function formatTokens(n: number): string {
  return n >= 1000 ? `${(n / 1000).toFixed(1)}k` : String(n);
}

export function ContextChips() {
  const { chips, dismissChip, detail, page, dismissPage } = useAgent();
  const sessionRefs = (detail?.session.context_ref as Array<{ type: string; id: string; label?: string }> | null) ?? [];
  if (chips.length === 0 && sessionRefs.length === 0 && !page) return null;
  return (
    <div className="flex flex-wrap items-center gap-1 text-[11px]">
      <span className="text-gray-400">上下文：</span>
      {page && (
        <span title={`发送时附带当前页面：${page.path}`} className="pl-1.5 pr-1 py-0.5 rounded-full bg-sky-50 text-sky-700 border border-sky-200 inline-flex items-center gap-0.5">
          <MapPin className="w-3 h-3" />
          {page.title ?? page.path}
          <button type="button" onClick={dismissPage} className="cursor-pointer text-sky-400 hover:text-sky-700" aria-label="不附带当前页面">
            <X className="w-3 h-3" />
          </button>
        </span>
      )}
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
  const { detail, detailError, live, pendingUser, phase, lastError, send, cancel, decide, sessionId, runStartedAt } = useAgent();
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

  const messages = detail?.messages;
  const calls = detail?.tool_calls;
  const history = useMemo(() => buildRuns(messages ?? [], calls ?? []), [messages, calls]);
  const runs = useMemo(() => mergeLive(history, pendingUser, live, running), [history, pendingUser, live, running]);
  const empty = runs.length === 0;
  const sess = detail?.session;
  const footer = sess && sess.tokens_in + sess.tokens_out > 0 ? `共消耗 ${formatTokens(sess.tokens_in + sess.tokens_out)} tokens · ${sess.model || meta?.model || ''}` : undefined;

  return (
    <div className={cn('flex flex-col min-h-0', className)}>
      <div ref={scrollRef} className="flex-1 overflow-y-auto px-3 py-3 space-y-5">
        {detailError != null && <div className="text-xs text-rose-600">会话加载失败：{String(detailError)}</div>}
        {empty && (
          <div className="text-xs text-gray-500 space-y-2 pt-6">
            <div className="flex items-center gap-1.5 text-gray-900 font-medium">
              <Bot className="w-4 h-4 text-purple-500" />
              {sessionId ? '开始对话' : `你好，我是${ASSISTANT_NAME}`}
            </div>
            <p>平台全局助手：用自然语言查询待办、调价、渠道健康、用量与日志；写操作会生成提案，由你审批后才执行。</p>
            <div className="flex flex-wrap gap-1.5 pt-1">
              {['今天有多少待审调价？被拦截的是哪些？', '巡检最近一小时的渠道健康', '有哪些新的上游优惠？'].map((q) => (
                <button key={q} type="button" onClick={() => setText(q)} className="px-2 py-1 rounded-full border border-gray-200 hover:border-purple-300 hover:text-purple-700 cursor-pointer">
                  {q}
                </button>
              ))}
            </div>
          </div>
        )}
        {runs.map((r, i) => {
          const last = i === runs.length - 1;
          return (
            <RunBlock
              key={r.key}
              run={r}
              state={last ? lastRunState(phase, sess?.status) : 'done'}
              startedAtMs={last ? runStartedAt : null}
              onDecide={onDecide}
              footer={last ? footer : undefined}
            />
          );
        })}
        {lastError && <div className="text-[11px] text-rose-600 pl-5">{lastError}</div>}
        {phase === 'awaiting_approval' && !running && <div className="text-[11px] text-amber-600 pl-5">等待审批：处理上面的提案后智能体会继续。</div>}
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
            placeholder={meta?.enabled ? `问${ASSISTANT_NAME}… Enter 发送，Shift+Enter 换行` : '智能体未启用'}
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
