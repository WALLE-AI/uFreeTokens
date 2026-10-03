import { useState } from 'react';
import { Check, ExternalLink, Pencil, Sparkles, X } from 'lucide-react';
import { Button, ConfirmDialog, Input, JsonDiff, Textarea, useToast } from '../../../components/ui';
import { useCan } from '../../../api/auth';
import { cn } from '../../../lib/cn';
import type { Permission } from '../../../types';
import { pretty } from './ToolCallCard';

// ApprovalCard：写操作审批卡（设计 §19.5）——智能体唯一的写入口。展示摘要、理由、证据、参数与
// before/after；按钮旁显示所需权限，无权限置灰。状态：pending / editing / executing / executed /
// stale / failed / rejected / superseded。批准 blocked 调价等高风险项需要二次确认。

export interface ApprovalView {
  id: string;
  tool: string;
  args: unknown;
  summary: string;
  before?: unknown;
  after?: unknown;
  permission: string;
  rationale?: string;
  confidence?: number | null;
  evidence?: unknown;
  target_type?: string;
  target_id?: string;
  status: string;
  decided_by_name?: string;
  decision_note?: string;
  result?: unknown;
}

const STATUS: Record<string, { label: string; cls: string }> = {
  pending_approval: { label: '待审批', cls: 'bg-amber-50 text-amber-700 border-amber-200' },
  approved: { label: '执行中', cls: 'bg-purple-50 text-purple-700 border-purple-200' },
  executed: { label: '已执行', cls: 'bg-emerald-50 text-emerald-700 border-emerald-200' },
  rejected: { label: '已拒绝', cls: 'bg-gray-50 text-gray-600 border-gray-200' },
  stale: { label: '已失效（对象已变化）', cls: 'bg-amber-50 text-amber-700 border-amber-200' },
  failed: { label: '执行失败', cls: 'bg-rose-50 text-rose-700 border-rose-200' },
  superseded: { label: '已作废', cls: 'bg-gray-50 text-gray-500 border-gray-200 italic' },
};

function isHighRisk(v: ApprovalView): boolean {
  const a = (v.args ?? {}) as Record<string, unknown>;
  return (v.tool === 'approve_price_change' && a.confirm_blocked === true) || v.tool === 'adopt_offer' || v.tool === 'update_price_source_config';
}

function evidenceList(e: unknown): Array<{ url: string; quote: string }> {
  return Array.isArray(e) ? (e as Array<{ url: string; quote: string }>).filter((x) => x && x.url) : [];
}

function stripMeta(args: unknown): Record<string, unknown> {
  const a = { ...((args ?? {}) as Record<string, unknown>) };
  delete a.rationale;
  delete a.confidence;
  delete a.evidence;
  return a;
}

// ArgsEditor：扁平的标量参数逐字段编辑，复杂对象回退为 JSON 编辑框。
function ArgsEditor({ value, onChange, error }: { value: Record<string, unknown>; onChange: (v: Record<string, unknown> | null, raw?: string) => void; error?: string }) {
  const flat = Object.values(value).every((v) => v === null || ['string', 'number', 'boolean'].includes(typeof v));
  const [raw, setRaw] = useState(() => JSON.stringify(value, null, 2));
  if (!flat) {
    return (
      <div>
        <Textarea
          mono
          rows={8}
          value={raw}
          onChange={(e) => {
            setRaw(e.target.value);
            try {
              onChange(JSON.parse(e.target.value) as Record<string, unknown>);
            } catch {
              onChange(null, e.target.value);
            }
          }}
        />
        {error && <div className="text-[11px] text-rose-600 mt-1">{error}</div>}
      </div>
    );
  }
  return (
    <div className="space-y-1.5">
      {Object.entries(value).map(([k, v]) => (
        <label key={k} className="flex items-center gap-2">
          <span className="w-36 shrink-0 font-mono text-[11px] text-gray-500 truncate">{k}</span>
          {typeof v === 'boolean' ? (
            <input type="checkbox" checked={v} onChange={(e) => onChange({ ...value, [k]: e.target.checked })} />
          ) : (
            <Input
              mono={typeof v === 'number'}
              value={v === null ? '' : String(v)}
              onChange={(e) => onChange({ ...value, [k]: typeof v === 'number' && e.target.value !== '' && !Number.isNaN(Number(e.target.value)) ? Number(e.target.value) : e.target.value })}
            />
          )}
        </label>
      ))}
      {error && <div className="text-[11px] text-rose-600">{error}</div>}
    </div>
  );
}

export function ApprovalCard({
  view,
  onDecide,
  compact,
  showSessionLink,
}: {
  view: ApprovalView;
  onDecide: (decision: 'approve' | 'reject', opts: { note?: string; args?: unknown }) => Promise<void>;
  compact?: boolean;
  showSessionLink?: React.ReactNode;
}) {
  const toast = useToast();
  const allowed = useCan(view.permission as Permission);
  const [busy, setBusy] = useState<'approve' | 'reject' | null>(null);
  const [note, setNote] = useState('');
  const [rejecting, setRejecting] = useState(false);
  const [editing, setEditing] = useState(false);
  const [edited, setEdited] = useState<Record<string, unknown> | null>(null);
  const [editErr, setEditErr] = useState<string | undefined>();
  const [showArgs, setShowArgs] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const pending = view.status === 'pending_approval';
  const st = STATUS[view.status] ?? { label: view.status, cls: 'bg-gray-50 text-gray-600 border-gray-200' };
  const evidence = evidenceList(view.evidence);

  const run = async (decision: 'approve' | 'reject') => {
    setBusy(decision);
    try {
      const args = editing ? edited : undefined;
      if (editing && !edited) {
        setEditErr('参数不是合法的 JSON');
        return;
      }
      await onDecide(decision, { note, args: args ?? undefined });
      setEditing(false);
      setRejecting(false);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      if (editing) setEditErr(msg);
      toast.error(decision === 'approve' ? '审批失败' : '拒绝失败', msg);
    } finally {
      setBusy(null);
      setConfirm(false);
    }
  };

  return (
    <div className={cn('border rounded-xl bg-white shadow-2xs', pending ? 'border-amber-300' : 'border-gray-200')}>
      <div className="px-3 py-2 border-b border-gray-100 flex items-center gap-2">
        <span className={cn('px-1.5 py-0.5 rounded border text-[10px] font-medium', st.cls)}>{st.label}</span>
        <span className="text-xs font-medium text-gray-900 truncate flex-1">{view.summary}</span>
        {typeof view.confidence === 'number' && (
          <span className={cn('flex items-center gap-0.5 text-[10px] font-mono', view.confidence < 0.6 ? 'text-amber-600' : 'text-purple-600')}>
            <Sparkles className="w-3 h-3" />
            {view.confidence.toFixed(2)}
          </span>
        )}
      </div>
      <div className="px-3 py-2 space-y-2 text-xs">
        {view.rationale && <p className="text-gray-700">{view.rationale}</p>}
        {evidence.length > 0 && (
          <div className="space-y-1">
            <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold">依据（已校验原文）</div>
            {evidence.map((e, i) => (
              <div key={i} className="border-l-2 border-purple-200 pl-2">
                <div className="text-gray-700">「{e.quote}」</div>
                <a href={e.url} target="_blank" rel="noreferrer noopener" className="text-[11px] text-purple-700 hover:underline inline-flex items-center gap-0.5 break-all">
                  {e.url}
                  <ExternalLink className="w-3 h-3" />
                </a>
              </div>
            ))}
          </div>
        )}
        {!compact && (view.before != null || view.after != null) && (
          <div>
            <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold mb-0.5">变更预览</div>
            <div className="max-h-56 overflow-y-auto border border-gray-100 rounded-lg">
              <JsonDiff before={view.before ?? null} after={view.after ?? null} hideUnchanged />
            </div>
          </div>
        )}
        <button type="button" onClick={() => setShowArgs((s) => !s)} className="text-[11px] text-gray-500 hover:text-gray-800 cursor-pointer">
          {showArgs ? '▾' : '▸'} 参数（<span className="font-mono">{view.tool}</span>）
        </button>
        {showArgs && !editing && <pre className="bg-gray-50 border border-gray-200 rounded p-1.5 overflow-x-auto font-mono text-[10.5px] max-h-48">{pretty(stripMeta(view.args))}</pre>}
        {editing && (
          <ArgsEditor
            value={edited ?? stripMeta(view.args)}
            error={editErr}
            onChange={(v) => {
              setEdited(v);
              setEditErr(v ? undefined : '参数不是合法的 JSON');
            }}
          />
        )}
        <div className="text-[11px] text-gray-500">
          需要权限 <span className="font-mono">{view.permission}</span>{' '}
          {allowed ? <span className="text-emerald-600">✓</span> : <span className="text-amber-600">（你没有该权限，需要有权限的同事处理）</span>}
        </div>
        {!pending && (view.decided_by_name || view.decision_note) && (
          <div className="text-[11px] text-gray-500">
            {view.decided_by_name && <>处理人 {view.decided_by_name}</>}
            {view.decision_note && <>：{view.decision_note}</>}
          </div>
        )}
        {view.status === 'failed' && view.result != null && <pre className="bg-rose-50 border border-rose-100 rounded p-1.5 font-mono text-[10.5px] overflow-x-auto">{pretty(view.result)}</pre>}
        {showSessionLink}
      </div>
      {pending && (
        <div className="px-3 py-2 border-t border-gray-100 space-y-2">
          {rejecting && <Input autoFocus placeholder="拒绝原因（会回传给智能体）" value={note} onChange={(e) => setNote(e.target.value)} />}
          <div className="flex items-center gap-1.5">
            {rejecting ? (
              <>
                <Button size="sm" variant="danger" loading={busy === 'reject'} disabled={!allowed} onClick={() => void run('reject')} icon={<X className="w-3 h-3" />}>
                  确认拒绝
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setRejecting(false)}>
                  取消
                </Button>
              </>
            ) : (
              <>
                <Button size="sm" disabled={!allowed || !!busy} onClick={() => setRejecting(true)} icon={<X className="w-3 h-3" />}>
                  拒绝
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  disabled={!allowed || !!busy}
                  onClick={() => {
                    setEditing((e) => !e);
                    setEdited(stripMeta(view.args));
                    setEditErr(undefined);
                  }}
                  icon={<Pencil className="w-3 h-3" />}
                >
                  {editing ? '取消编辑' : '编辑'}
                </Button>
                <span className="flex-1" />
                <Button
                  size="sm"
                  variant="primary"
                  loading={busy === 'approve'}
                  disabled={!allowed || !!busy}
                  onClick={() => (isHighRisk(view) ? setConfirm(true) : void run('approve'))}
                  icon={<Check className="w-3 h-3" />}
                >
                  {editing ? '保存并批准' : '批准执行'}
                </Button>
              </>
            )}
          </div>
        </div>
      )}
      <ConfirmDialog
        open={confirm}
        onClose={() => setConfirm(false)}
        onConfirm={() => run('approve')}
        loading={busy === 'approve'}
        level="danger"
        title="确认执行高风险操作"
        confirmLabel="确认批准"
      >
        <p>{view.summary}</p>
        <p className="text-gray-500">该操作影响价格或数据源配置，执行后以你的身份记入审计日志。</p>
      </ConfirmDialog>
    </div>
  );
}
