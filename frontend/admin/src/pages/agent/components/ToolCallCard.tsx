import { useState } from 'react';
import { CheckCircle2, ChevronDown, CircleAlert, Loader2, ShieldOff } from 'lucide-react';
import { cn } from '../../../lib/cn';

// 工具调用步骤（设计 §6.1）：一行展示可读标题、工具名、摘要与耗时；展开看参数与结果代码块，
// 底部是执行状态（对应参考界面里「运行命令 → 代码块 → ✓ 运行成功」）。

export interface ToolCallView {
  id: string;
  tool: string;
  args: unknown;
  status: string;
  summary?: string;
  http_status?: number;
  duration_ms?: number;
  result?: unknown;
}

const STATUS_TEXT: Record<string, string> = { running: '运行中', done: '运行成功', denied: '无权限，已拦截', error: '运行失败' };

function StatusIcon({ status }: { status: string }) {
  if (status === 'running') return <Loader2 className="w-3.5 h-3.5 text-purple-500 animate-spin shrink-0" />;
  if (status === 'done') return <CheckCircle2 className="w-3.5 h-3.5 text-emerald-500 shrink-0" />;
  if (status === 'denied') return <ShieldOff className="w-3.5 h-3.5 text-amber-500 shrink-0" />;
  return <CircleAlert className="w-3.5 h-3.5 text-rose-500 shrink-0" />;
}

export function pretty(v: unknown): string {
  if (v === undefined || v === null) return '—';
  if (typeof v === 'string') {
    try {
      return JSON.stringify(JSON.parse(v), null, 2);
    } catch {
      return v;
    }
  }
  return JSON.stringify(v, null, 2);
}

export function ToolCallCard({ call, title }: { call: ToolCallView; title?: string }) {
  const [open, setOpen] = useState(false);
  const hasResult = call.result !== undefined && call.result !== null;
  return (
    <div className="text-[11px]">
      <button type="button" onClick={() => setOpen((o) => !o)} className="group w-full flex items-center gap-1.5 text-left cursor-pointer">
        <StatusIcon status={call.status} />
        <span className="font-medium text-gray-700 group-hover:text-gray-900 truncate shrink-0 max-w-[60%]">{title || call.tool}</span>
        {title && <span className="font-mono text-[10px] text-gray-400 truncate shrink-0 max-w-[35%]">{call.tool}</span>}
        <span className="text-gray-400 truncate flex-1 min-w-0">{call.summary}</span>
        {call.duration_ms ? <span className="text-gray-400 font-mono shrink-0">{call.duration_ms}ms</span> : null}
        <ChevronDown className={cn('w-3 h-3 text-gray-400 shrink-0 transition-transform', open && 'rotate-180')} />
      </button>
      {open && (
        <div className="mt-1.5 ml-5 rounded-lg border border-gray-200 bg-gray-50 overflow-hidden">
          <div className="px-2.5 pt-1.5 text-[10px] text-gray-400 font-mono">{call.tool}</div>
          <pre className="px-2.5 pb-2 overflow-x-auto font-mono text-[10.5px] text-gray-700 max-h-40">{pretty(call.args)}</pre>
          {hasResult && (
            <>
              <div className="px-2.5 pt-1.5 border-t border-gray-200 text-[10px] text-gray-400 font-mono">
                result{call.http_status ? ` · HTTP ${call.http_status}` : ''}
              </div>
              <pre className="px-2.5 pb-2 overflow-x-auto font-mono text-[10.5px] text-gray-700 max-h-60">{pretty(call.result)}</pre>
            </>
          )}
          <div className="px-2.5 py-1.5 border-t border-gray-200 flex items-center gap-1.5 text-gray-500">
            <StatusIcon status={call.status} />
            <span>{STATUS_TEXT[call.status] ?? call.status}</span>
            {call.duration_ms ? <span className="text-gray-400 font-mono">· {call.duration_ms}ms</span> : null}
          </div>
        </div>
      )}
    </div>
  );
}
