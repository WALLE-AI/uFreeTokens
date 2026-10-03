import { useCallback, useEffect, useState } from 'react';
import { Link, useNavigate } from 'react-router';
import { ActionMenu, DataState, Field, FormModal, Input, PageHeader, Switch, useToast } from '../../components/ui';
import { listAgentJobs, runAgentJob, updateAgentJob, type AgentJob } from '../../api/agent';
import { errorMessage } from '../../api/errors';
import { useAgentMeta } from '../../agent/agentStore';
import { cn } from '../../lib/cn';
import { formatFromNow, formatRelative } from '../../lib/time';

// /agent/jobs 智能作业（设计 §19.4c，实施方案 M3-F01，权限 agent:admin）：启停、调度、预算、
// 立即运行、最近运行会话；拒绝率熔断后显示「熔断」，重新启用即清除。交互仿照数据源列表。

function pct(v: number | null) {
  return v === null ? '—' : `${Math.round(v * 100)}%`;
}

export default function JobsPage() {
  const toast = useToast();
  const navigate = useNavigate();
  const meta = useAgentMeta();
  const [list, setList] = useState<AgentJob[] | null>(null);
  const [err, setErr] = useState<unknown>(null);
  const [editing, setEditing] = useState<AgentJob | null>(null);
  const [form, setForm] = useState({ schedule: '', budget: '', maxItems: '', model: '' });
  const [saving, setSaving] = useState(false);

  const load = useCallback(() => {
    listAgentJobs()
      .then((r) => {
        setList(r.data ?? []);
        setErr(null);
      })
      .catch(setErr);
  }, []);
  useEffect(() => {
    load();
  }, [load]);

  const toggle = async (j: AgentJob) => {
    try {
      await updateAgentJob(j.id, { enabled: !j.enabled, schedule: null, model: null, daily_token_budget: null, max_items_per_run: null });
      load();
    } catch (e) {
      toast.error('操作失败', errorMessage(e));
    }
  };
  const run = async (j: AgentJob) => {
    try {
      await runAgentJob(j.id);
      toast.success('已排队，worker 下一分钟执行');
      load();
    } catch (e) {
      toast.error('操作失败', errorMessage(e));
    }
  };
  const openEdit = (j: AgentJob) => {
    setEditing(j);
    setForm({ schedule: j.schedule, budget: String(j.daily_token_budget), maxItems: String(j.max_items_per_run), model: j.model ?? '' });
  };
  const save = async () => {
    if (!editing) return;
    setSaving(true);
    try {
      await updateAgentJob(editing.id, {
        enabled: null,
        schedule: form.schedule,
        model: form.model,
        daily_token_budget: Number(form.budget) || 0,
        max_items_per_run: Number(form.maxItems) || 1,
      });
      setEditing(null);
      load();
    } catch (e) {
      toast.error('保存失败', errorMessage(e));
    } finally {
      setSaving(false);
    }
  };
  const pbTitle = (name: string) => meta?.playbooks?.find((p) => p.name === name)?.title ?? name;

  return (
    <div className="space-y-4">
      <PageHeader
        title="智能作业"
        description={`后台智能体以只读身份（agent-bot）定时或按事件预审，提案进入收件箱等待人工审批。${meta?.jobs_enabled ? '' : '当前 agent.jobs_enabled=false，worker 不会运行作业。'}`}
      />
      <DataState loading={!list && !err} error={err} onRetry={load} empty={list?.length === 0}>
        <div className="border border-gray-200 rounded-xl bg-white overflow-x-auto">
          <table className="w-full text-xs">
            <thead className="bg-gray-50 text-gray-500">
              <tr>
                {['作业', '剧本', '触发', '下次运行', '今日 Token', '待审', '拒绝率', '状态', ''].map((h) => (
                  <th key={h} className="text-left font-medium px-3 py-2 whitespace-nowrap">
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {(list ?? []).map((j) => {
                const broken = j.paused_reason === 'circuit_breaker';
                return (
                  <tr key={j.id}>
                    <td className="px-3 py-2">
                      <div className="text-gray-900 font-medium">{j.name || j.code}</div>
                      <div className="text-[11px] text-gray-400 font-mono">{j.code}</div>
                    </td>
                    <td className="px-3 py-2 text-gray-600">{pbTitle(j.playbook)}</td>
                    <td className="px-3 py-2 text-gray-600 whitespace-nowrap">
                      {j.trigger_query ? '事件' : ''}
                      {j.trigger_query && j.schedule ? ' + ' : ''}
                      <span className="font-mono">{j.schedule}</span>
                    </td>
                    <td className="px-3 py-2 text-gray-600 whitespace-nowrap">{j.enabled ? formatFromNow(j.next_run_at) : '—'}</td>
                    <td className="px-3 py-2 font-mono text-gray-600 whitespace-nowrap">
                      {(j.tokens_today / 1000).toFixed(0)}k / {(j.daily_token_budget / 1_000_000).toFixed(1)}M
                    </td>
                    <td className="px-3 py-2">
                      {j.pending_count > 0 ? (
                        <Link to="/agent/inbox" className="text-purple-700 hover:underline">
                          {j.pending_count}
                        </Link>
                      ) : (
                        <span className="text-gray-400">0</span>
                      )}
                    </td>
                    <td className={cn('px-3 py-2 font-mono', (j.rejected_ratio ?? 0) > 0.25 ? 'text-amber-600' : 'text-gray-600')}>
                      {pct(j.rejected_ratio)}
                      {j.decided_in_ratio > 0 && <span className="text-gray-400 text-[10px]">（{j.decided_in_ratio}）</span>}
                    </td>
                    <td className="px-3 py-2 whitespace-nowrap">
                      {broken ? (
                        <span className="text-rose-600">⏸ 熔断</span>
                      ) : (
                        <Switch checked={j.enabled} onChange={() => void toggle(j)} label={j.enabled ? '启用' : '停用'} />
                      )}
                      {j.last_run_at && (
                        <div className="text-[10px] text-gray-400" title={j.last_error}>
                          {formatRelative(j.last_run_at)} · {j.last_status}
                          {j.run_requested && ' · 排队中'}
                        </div>
                      )}
                    </td>
                    <td className="px-3 py-2 text-right">
                      <ActionMenu
                        items={[
                          { label: '立即运行', onClick: () => void run(j) },
                          { label: '编辑调度与预算', onClick: () => openEdit(j) },
                          ...(j.last_session_id ? [{ label: '查看最近运行', onClick: () => navigate(`/agent/${j.last_session_id}`) }] : []),
                          broken
                            ? { label: '解除熔断并启用', onClick: () => void toggle({ ...j, enabled: false }) }
                            : { label: j.enabled ? '停用' : '启用', onClick: () => void toggle(j) },
                        ]}
                      />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </DataState>
      <FormModal
        open={!!editing}
        onClose={() => setEditing(null)}
        title={`编辑作业 · ${editing?.name ?? ''}`}
        onSubmit={() => void save()}
        submitting={saving}
      >
        <div className="space-y-3">
          <Field label="调度" hint="@hourly / @daily / @every 30m / cron；留空表示仅事件或手动触发">
            <Input mono value={form.schedule} onChange={(e) => setForm({ ...form, schedule: e.target.value })} />
          </Field>
          <Field label="每日 Token 预算">
            <Input mono value={form.budget} onChange={(e) => setForm({ ...form, budget: e.target.value })} />
          </Field>
          <Field label="每次最多处理对象数">
            <Input mono value={form.maxItems} onChange={(e) => setForm({ ...form, maxItems: e.target.value })} />
          </Field>
          <Field label="模型（留空用 agent.batch_model）">
            <Input mono value={form.model} onChange={(e) => setForm({ ...form, model: e.target.value })} />
          </Field>
        </div>
      </FormModal>
    </div>
  );
}
