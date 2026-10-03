import { useEffect, useRef, useState } from 'react';
import { Brain, Check, ChevronDown, Copy, FileText, Loader2, Sparkles } from 'lucide-react';
import { IconButton } from '../../../components/ui';
import { useAgentMeta } from '../../../agent/agentStore';
import { ASSISTANT_NAME } from '../../../agent/brand';
import { cn } from '../../../lib/cn';
import { ApprovalCard } from './ApprovalCard';
import { ArtifactCard, isArtifact } from './ArtifactCards';
import { Markdown } from './Markdown';
import { ToolCallCard } from './ToolCallCard';
import { formatDuration, splitFinal, toolTitle, type Run, type Step } from './runSteps';

// RunBlock：一次运行（用户消息 + 智能体过程 + 最终回答），参考 IDE 智能体的交互形态：
//   - 头部「已完成 · 3 次调用 · 41s ˅」，点击折叠 / 展开过程；
//   - 过程是时间线：深度思考（左边框灰字，可滚动）、工具步骤、中间说明、审批卡交替出现；
//   - 运行中过程全部展开、当前思考段自动展开并跟随滚动；结束后过程默认折叠，只留最终回答；
//   - 图表与报表（render_chart / create_report 的结果）显示在回答区，不随过程折叠；
//   - 底部是复制、Token 用量与模型。

export type RunState = 'running' | 'awaiting' | 'error' | 'stopped' | 'done';

export type DecideFn = (id: string, d: 'approve' | 'reject', o: { note?: string; args?: unknown }) => Promise<void>;

const STATE_LABEL: Record<RunState, string> = { running: '运行中', awaiting: '待审批', error: '出错', stopped: '已停止', done: '已完成' };

function useNow(active: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [active]);
  return now;
}

export function ThinkingBlock({ text, active }: { text: string; active: boolean }) {
  const [userOpen, setUserOpen] = useState<boolean | null>(null);
  const open = userOpen ?? active;
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = ref.current;
    if (active && el) el.scrollTop = el.scrollHeight;
  }, [text, active]);
  const preview = text.trim().split('\n')[0];
  return (
    <div className="text-[11px]">
      <button type="button" onClick={() => setUserOpen(!open)} className="group w-full flex items-center gap-1.5 text-left text-gray-500 cursor-pointer">
        {active ? <Loader2 className="w-3.5 h-3.5 text-purple-500 animate-spin shrink-0" /> : <Brain className="w-3.5 h-3.5 text-gray-400 shrink-0" />}
        <span className="font-medium group-hover:text-gray-800 shrink-0">{active ? '思考中' : '深度思考'}</span>
        {!open && <span className="text-gray-400 truncate flex-1 min-w-0">{preview}</span>}
        <ChevronDown className={cn('w-3 h-3 text-gray-400 shrink-0 transition-transform', open && 'rotate-180')} />
      </button>
      {open && (
        <div ref={ref} className="mt-1 ml-1.5 pl-3 border-l-2 border-gray-200 text-gray-500 leading-relaxed whitespace-pre-wrap break-words max-h-44 overflow-y-auto">
          {text}
        </div>
      )}
    </div>
  );
}

function StepView({ step, active, onDecide, titles }: { step: Step; active: boolean; onDecide: DecideFn; titles: Map<string, string> }) {
  switch (step.kind) {
    case 'thinking':
      return <ThinkingBlock text={step.text} active={active} />;
    case 'text':
      return <Markdown text={step.text} />;
    case 'tool':
      return <ToolCallCard call={step.call} title={titles.get(step.call.tool)} />;
    case 'approval':
      return <ApprovalCard view={step.view} onDecide={(d, o) => onDecide(step.view.id, d, o)} />;
    case 'report':
      return (
        <div className="bg-gray-50 border border-gray-200 rounded-xl px-3 py-2">
          <div className="flex items-center gap-1 text-[11px] text-gray-400 mb-1">
            <FileText className="w-3 h-3" />
            运行报告
          </div>
          <Markdown text={step.text} />
        </div>
      );
    case 'divider':
      return <div className="text-[11px] text-gray-400 text-center">— {step.text} —</div>;
  }
}

function CopyButton({ text }: { text: string }) {
  const [done, setDone] = useState(false);
  return (
    <IconButton
      label="复制回答"
      onClick={() => {
        void navigator.clipboard?.writeText(text).then(() => {
          setDone(true);
          setTimeout(() => setDone(false), 1500);
        });
      }}
    >
      {done ? <Check className="w-3.5 h-3.5 text-emerald-500" /> : <Copy className="w-3.5 h-3.5" />}
    </IconButton>
  );
}

export function UserMessage({ text }: { text: string }) {
  return (
    <div className="flex justify-end">
      <div className="max-w-[85%] bg-gray-100 text-gray-800 rounded-2xl rounded-tr-sm px-3 py-1.5 text-xs whitespace-pre-wrap break-words">{text}</div>
    </div>
  );
}

export function RunBlock({
  run,
  state,
  startedAtMs,
  onDecide,
  footer,
}: {
  run: Run;
  state: RunState;
  // startedAtMs 是运行中的本地起始时间（流式请求开始）；没有时用消息时间。
  startedAtMs?: number | null;
  onDecide: DecideFn;
  // footer 是附加在底部的信息（最后一次运行显示 Token 用量与模型）。
  footer?: string;
}) {
  const meta = useAgentMeta();
  const titles = new Map((meta?.tools ?? []).map((t) => [t.name, toolTitle(t.description)]));
  const running = state === 'running';
  const now = useNow(running);
  const { process, final } = running ? { process: run.steps, final: [] as Step[] } : splitFinal(run.steps);
  const hasPending = process.some((s) => s.kind === 'approval' && s.view.status === 'pending_approval');
  const [userOpen, setUserOpen] = useState<boolean | null>(null);
  const open = userOpen ?? (running || hasPending || state !== 'done' || final.length === 0);

  const calls = run.steps.filter((s) => s.kind === 'tool' || s.kind === 'approval').length;
  const start = running && startedAtMs ? startedAtMs : run.startedAt ? Date.parse(run.startedAt) : NaN;
  const end = running ? now : run.endedAt ? Date.parse(run.endedAt) : NaN;
  const elapsed = formatDuration(end - start);
  const lastIdx = run.steps.length - 1;
  const finalText = final.map((s) => (s.kind === 'text' || s.kind === 'report' ? s.text : '')).join('\n\n');
  const hasAgentPart = run.steps.length > 0 || running;
  // 成果物（图表、报表）显示在回答区，不随过程折叠。
  const artifacts = run.steps.flatMap((s) => (s.kind === 'tool' && isArtifact(s.call) ? [s.call] : []));

  return (
    <div className="space-y-2.5">
      {run.user != null && <UserMessage text={run.user} />}
      {hasAgentPart && (
        <div className="space-y-2">
          <div className="flex items-center gap-1.5 text-[11px]">
            <Sparkles className="w-4 h-4 text-purple-500 shrink-0" />
            <span className="text-xs font-medium text-gray-900">{ASSISTANT_NAME}</span>
            {process.length > 0 || running ? (
              <button
                type="button"
                onClick={() => setUserOpen(!open)}
                className={cn(
                  'inline-flex items-center gap-1 cursor-pointer hover:text-gray-800',
                  state === 'running' ? 'text-purple-600' : state === 'awaiting' ? 'text-amber-600' : state === 'error' ? 'text-rose-600' : 'text-gray-400',
                )}
              >
                {running && <Loader2 className="w-3 h-3 animate-spin" />}
                {STATE_LABEL[state]}
                {calls > 0 && <span>· {calls} 次调用</span>}
                {elapsed && <span className="font-mono">· {elapsed}</span>}
                <ChevronDown className={cn('w-3 h-3 transition-transform', open && 'rotate-180')} />
              </button>
            ) : null}
          </div>

          {open && process.length > 0 && (
            <div className="ml-2 pl-3 border-l border-gray-200 space-y-2">
              {process.map((s, i) => (
                <StepView key={s.key} step={s} active={running && i === lastIdx} onDecide={onDecide} titles={titles} />
              ))}
            </div>
          )}
          {running && run.steps.length === 0 && (
            <div className="ml-2 pl-3 border-l border-gray-200 flex items-center gap-1.5 text-[11px] text-gray-400">
              <Loader2 className="w-3 h-3 animate-spin" />
              思考中…
            </div>
          )}

          {final.length > 0 && (
            <div className="space-y-2">
              {final.map((s) => (
                <StepView key={s.key} step={s} active={false} onDecide={onDecide} titles={titles} />
              ))}
            </div>
          )}

          {artifacts.length > 0 && (
            <div className="space-y-2">
              {artifacts.map((c) => (
                <ArtifactCard key={c.id} call={c} />
              ))}
            </div>
          )}

          {!running && finalText && (
            <div className="flex items-center gap-1 text-[11px] text-gray-400">
              <CopyButton text={finalText} />
              {footer && <span>{footer}</span>}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
