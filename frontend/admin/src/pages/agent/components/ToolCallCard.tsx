import { useState } from 'react';
import { CheckCircle2, ChevronRight, CircleAlert, Loader2, ShieldOff, Wrench } from 'lucide-react';
import { cn } from '../../../lib/cn';

// 工具调用折叠卡（设计 §6.1）：名称、状态、耗时、摘要；展开看参数与结果。

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

function StatusIcon({ status }: { status: string }) {
  if (status === 'running') return <Loader2 className="w-3.5 h-3.5 text-purple-500 animate-spin" />;
  if (status === 'done') return <CheckCircle2 className="w-3.5 h-3.5 text-emerald-500" />;
  if (status === 'denied') return <ShieldOff className="w-3.5 h-3.5 text-amber-500" />;
  return <CircleAlert className="w-3.5 h-3.5 text-rose-500" />;
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

export function ToolCallCard({ call }: { call: ToolCallView }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="border border-gray-200 rounded-lg bg-gray-50/60 text-[11px]">
      <button type="button" onClick={() => setOpen((o) => !o)} className="w-full flex items-center gap-1.5 px-2.5 py-1.5 text-left cursor-pointer">
        <ChevronRight className={cn('w-3 h-3 text-gray-400 transition-transform', open && 'rotate-90')} />
        <Wrench className="w-3 h-3 text-gray-400" />
        <span className="font-mono text-gray-700 truncate">{call.tool}</span>
        <StatusIcon status={call.status} />
        <span className="text-gray-500 truncate flex-1">{call.summary}</span>
        {call.duration_ms ? <span className="text-gray-400 font-mono shrink-0">{call.duration_ms}ms</span> : null}
      </button>
      {open && (
        <div className="px-2.5 pb-2 space-y-1.5">
          <div>
            <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold mb-0.5">参数</div>
            <pre className="bg-white border border-gray-200 rounded p-1.5 overflow-x-auto font-mono text-[10.5px] max-h-40">{pretty(call.args)}</pre>
          </div>
          {call.result !== undefined && call.result !== null && (
            <div>
              <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold mb-0.5">
                结果{call.http_status ? `（HTTP ${call.http_status}）` : ''}
              </div>
              <pre className="bg-white border border-gray-200 rounded p-1.5 overflow-x-auto font-mono text-[10.5px] max-h-60">{pretty(call.result)}</pre>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
