import { useState } from 'react';
import { Link, useNavigate } from 'react-router';
import { createChannel } from '../../api/catalog';
import { errorMessage } from '../../api/errors';
import { DataTable, Field, FormModal, Input, ShareBar, StatusBadge, useToast, type Column } from '../../components/ui';
import type { ChannelSummary, ProviderAccountSummary, VirtualModelDetail } from '../../types';
import { ProviderAccountPicker } from './ProviderAccountPicker';
import { MarginText, formatPrice } from './shared';

// 虚拟模型详情页的"渠道"段（UI_DESIGN.md §5.4 第 4 点）：渠道表 + 按优先级分组的流量占比条。
// 路由先选优先级最小（最优先）的一组，组内按权重分流；只有前一组全部不可用才落到下一组。

export function ModelChannelsSection({
  model,
  onChanged,
  adding,
  setAdding,
}: {
  model: VirtualModelDetail;
  onChanged: () => void;
  adding: boolean;
  setAdding: (v: boolean) => void;
}) {
  const navigate = useNavigate();

  const activeByPriority = new Map<number, ChannelSummary[]>();
  for (const c of model.channels) {
    if (c.status !== 'active') continue;
    activeByPriority.set(c.priority, [...(activeByPriority.get(c.priority) ?? []), c]);
  }
  const groups = Array.from(activeByPriority.entries()).sort((a, b) => a[0] - b[0]);

  const columns: Column<ChannelSummary>[] = [
    { key: 'id', header: 'ID', numeric: true, render: (c) => <span className="text-gray-400">#{c.id}</span> },
    {
      key: 'account',
      header: '供应商 / 上游账号',
      render: (c) => (
        <span className="whitespace-nowrap">
          {c.provider_code}
          <span className="text-gray-300"> / </span>
          <span className="text-gray-600">{c.provider_account_name}</span>
        </span>
      ),
    },
    { key: 'upstream', header: '上游模型', render: (c) => <span className="font-mono text-gray-600">{c.upstream_model}</span> },
    { key: 'priority', header: '优先级', numeric: true, render: (c) => c.priority },
    { key: 'weight', header: '权重', numeric: true, render: (c) => c.weight },
    {
      key: 'cost',
      header: '成本 ¥/1M',
      numeric: true,
      render: (c) =>
        !c.cost_price_cny ? (
          <span className="text-amber-700 text-[11px] font-sans">未设成本价</span>
        ) : c.cost_price_cny.fx_missing ? (
          <span className="text-amber-700 text-[11px] font-sans">缺汇率</span>
        ) : (
          <span>
            ¥{formatPrice(c.cost_price_cny.input)} <span className="text-gray-300">/</span> ¥{formatPrice(c.cost_price_cny.output)}
          </span>
        ),
    },
    { key: 'margin', header: '毛利率', numeric: true, render: (c) => <MarginText ratio={c.margin_ratio} /> },
    { key: 'status', header: '状态', render: (c) => <StatusBadge kind="channel" value={c.status} /> },
    {
      key: 'pending',
      header: '待审调价',
      render: (c) =>
        c.pending_change_request_id ? (
          <Link
            to={`/pricing/changes?id=${c.pending_change_request_id}`}
            onClick={(e) => e.stopPropagation()}
            className="px-2 py-0.5 rounded-full text-[11px] border bg-purple-50 text-purple-700 border-purple-200 hover:bg-purple-100"
          >
            #{c.pending_change_request_id}
          </Link>
        ) : (
          <span className="text-gray-300">—</span>
        ),
    },
  ];

  return (
    <div className="space-y-3">
      {groups.length > 0 && (
        <div className="bg-white border border-gray-200 rounded-xl p-4 shadow-xs space-y-3">
          <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold">流量分配（active 渠道，按优先级分组）</div>
          {groups.map(([prio, chs], i) => (
            <div key={prio} className="space-y-1.5">
              <div className="flex items-center gap-2 text-[11px]">
                <span className="font-mono text-gray-900">优先级 {prio}</span>
                <span className="text-gray-400">{i === 0 ? '主路由' : '前一组全部不可用时启用'}</span>
              </div>
              <ShareBar segments={chs.map((c) => ({ label: `#${c.id} ${c.provider_account_name}/${c.upstream_model}`, value: c.weight }))} />
              <div className="flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-gray-500">
                {chs.map((c) => {
                  const total = chs.reduce((s, x) => s + x.weight, 0);
                  return (
                    <span key={c.id}>
                      #{c.id} {c.provider_account_name}
                      <span className="font-mono text-gray-900 ml-1">{total > 0 ? ((c.weight / total) * 100).toFixed(0) : 0}%</span>
                    </span>
                  );
                })}
              </div>
            </div>
          ))}
        </div>
      )}
      <DataTable
        columns={columns}
        rows={model.channels}
        rowKey={(c) => c.id}
        onRowClick={(c) => navigate(`/channels/${c.id}`)}
        empty="还没有渠道——该模型当前无法被调用"
      />
      <AddChannelModal open={adding} onClose={() => setAdding(false)} model={model} onCreated={onChanged} />
    </div>
  );
}

export function AddChannelModal({ open, onClose, model, onCreated }: { open: boolean; onClose: () => void; model: VirtualModelDetail; onCreated: () => void }) {
  const toast = useToast();
  const navigate = useNavigate();
  const [account, setAccount] = useState<ProviderAccountSummary | null>(null);
  const [upstream, setUpstream] = useState('');
  const [priority, setPriority] = useState('0');
  const [weight, setWeight] = useState('100');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const intErr = (v: string, min: number) => (!/^-?\d+$/.test(v.trim()) || Number(v) < min ? `需为 ≥ ${min} 的整数` : undefined);
  const priorityErr = intErr(priority, 0);
  const weightErr = intErr(weight, 1);

  const reset = () => {
    setAccount(null);
    setUpstream('');
    setPriority('0');
    setWeight('100');
    setError(null);
  };

  const submit = async () => {
    if (!account) return;
    setSubmitting(true);
    setError(null);
    try {
      const ch = await createChannel({
        virtual_model_id: model.id,
        provider_account_id: account.id,
        upstream_model: upstream.trim(),
        priority: Number(priority),
        weight: Number(weight),
      });
      toast.success(`渠道 #${ch.id} 已创建，请继续设置成本价`);
      reset();
      onClose();
      onCreated();
      navigate(`/channels/${ch.id}#cost`);
    } catch (err) {
      setError(errorMessage(err, '创建失败'));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <FormModal
      open={open}
      onClose={() => {
        reset();
        onClose();
      }}
      title="添加渠道"
      description={
        <>
          为 <span className="font-mono text-gray-900">{model.name}</span> 接入一个上游模型。创建后需要再为渠道发布成本价，才能计算毛利。
        </>
      }
      width="lg"
      onSubmit={submit}
      submitLabel="创建渠道"
      submitting={submitting}
      submitDisabled={!account || !upstream.trim() || !!priorityErr || !!weightErr}
      error={error}
    >
      <Field label="上游账号" required>
        <ProviderAccountPicker value={account} onChange={setAccount} />
      </Field>
      <Field label="上游模型名" required hint="发往上游时使用的 model 字段，例如 deepseek-chat">
        <Input mono value={upstream} onChange={(e) => setUpstream(e.target.value)} placeholder="upstream-model-name" />
      </Field>
      <div className="grid grid-cols-2 gap-4">
        <Field label="优先级" hint="数字越小越优先，0 = 主路由" error={priorityErr}>
          <Input mono value={priority} invalid={!!priorityErr} onChange={(e) => setPriority(e.target.value)} />
        </Field>
        <Field label="权重" hint="同优先级内按权重分流" error={weightErr}>
          <Input mono value={weight} invalid={!!weightErr} onChange={(e) => setWeight(e.target.value)} />
        </Field>
      </div>
    </FormModal>
  );
}
