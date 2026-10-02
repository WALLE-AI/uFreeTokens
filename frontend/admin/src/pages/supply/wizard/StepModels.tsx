import { describeError } from '../../../api/errors';
import { useEffect, useMemo, useState } from 'react';
import { CheckSquare, Plus, RotateCw, Square } from 'lucide-react';
import { importModels, listUpstreamModels } from '../../../api/catalog';
import { lookupReferencePrices } from '../../../api/pricing';
import { Button, Input, Pills, SearchInput } from '../../../components/ui';
import { cn } from '../../../lib/cn';
import type { UpstreamModel } from '../../../types';
import { upstreamErrorHint } from '../common';
import { defaultRow, resolvedAccountId, type PlatformStatus } from './state';
import { StepFooter, type StepProps } from './ui';

const STATUS_LABEL: Record<PlatformStatus, { label: string; className: string; title: string }> = {
  new: { label: '新模型', className: 'bg-blue-50 text-blue-700 border-blue-200', title: '平台还没有同名虚拟模型' },
  vm_exists: { label: '已有虚拟模型', className: 'bg-amber-50 text-amber-700 border-amber-200', title: '已有同名虚拟模型，导入会为它新增一条走这个账号的渠道' },
  listed: { label: '已上架', className: 'bg-gray-50 text-gray-500 border-gray-200', title: '这个账号下已有对应渠道，不可重复导入' },
};

// ③ 选择模型：上游模型 + 本平台状态 + 参考价
export function StepModels({ state, update, goto, mode = 'onboard' }: StepProps) {
  const accountId = resolvedAccountId(state);
  const [q, setQ] = useState('');
  const [filter, setFilter] = useState<'all' | PlatformStatus>('all');
  const [manual, setManual] = useState('');
  const [progress, setProgress] = useState<{ done: number; total: number } | null>(null);
  const [loadingModels, setLoadingModels] = useState(false);
  const [modelsError, setModelsError] = useState<string | null>(null);
  const [refLoading, setRefLoading] = useState(false);
  const isOpenAI = state.provider.protocol === 'openai';

  // 从第 ② 步直接"下一步"跳过测试时，这里补拉一次上游模型
  useEffect(() => {
    if (!isOpenAI || state.models.length > 0 || accountId === null || state.connection.tested) return;
    const controller = new AbortController();
    setLoadingModels(true);
    listUpstreamModels(accountId, controller.signal)
      .then((res) =>
        update((s) => ({ ...s, models: res.data, connection: { ok: true, count: res.data.length, error: null, tested: true }, statusChecked: false, refChecked: false })),
      )
      .catch((err) => !controller.signal.aborted && setModelsError(upstreamErrorHint(err)))
      .finally(() => !controller.signal.aborted && setLoadingModels(false));
    return () => controller.abort();
  }, [isOpenAI, state.models.length, accountId, state.connection.tested, update]);

  // 核对本平台状态：同名虚拟模型是否存在、这个账号下是否已有渠道。用批量导入接口的
  // dry_run 一次查一批（每批 200 个），代替逐个模型两次查询。
  useEffect(() => {
    if (state.statusChecked || state.models.length === 0 || accountId === null) return;
    const controller = new AbortController();
    const result: Record<string, PlatformStatus> = {};
    const total = state.models.length;
    setProgress({ done: 0, total });
    (async () => {
      for (let i = 0; i < total && !controller.signal.aborted; i += 200) {
        const batch = state.models.slice(i, i + 200);
        try {
          const res = await importModels(
            accountId,
            { dry_run: true, currency: 'USD', markup_percent: '0', items: batch.map((m) => ({ upstream_model: m.id })) },
            controller.signal,
          );
          for (const r of res.items) result[r.upstream_model] = r.status;
        } catch {
          for (const m of batch) result[m.id] ??= 'new'; // 查询失败按新模型处理，导入时服务端还会再核对
        }
        setProgress({ done: Math.min(i + 200, total), total });
      }
    })().then(() => {
      if (controller.signal.aborted) return;
      setProgress(null);
      update((s) => ({
        ...s,
        platformStatus: { ...s.platformStatus, ...result },
        statusChecked: true,
        selected: s.selected.filter((id) => result[id] !== 'listed'),
      }));
    });
    return () => controller.abort();
  }, [state.statusChecked, state.models, accountId, update]);

  const lookup = async () => {
    const ids = state.models.map((m) => m.id);
    if (ids.length === 0) return;
    setRefLoading(true);
    try {
      const res = await lookupReferencePrices(ids);
      const errs = [res.openrouter_error && `OpenRouter：${res.openrouter_error}`, res.litellm_error && `LiteLLM：${res.litellm_error}`].filter(Boolean);
      update((s) => ({ ...s, refPrices: res.data, refError: errs.length ? errs.join('；') : null, refChecked: true }));
    } catch (err) {
      update((s) => ({ ...s, refError: describeError(err, '参考价查询失败'), refChecked: true }));
    } finally {
      setRefLoading(false);
    }
  };

  // 进入本步自动查参考价（只查一次；可手动重查）
  useEffect(() => {
    if (!state.refChecked && state.models.length > 0 && !refLoading) void lookup();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state.refChecked, state.models.length]);

  const addManual = () => {
    const ids = manual
      .split(/[\s,]+/)
      .map((s) => s.trim())
      .filter(Boolean);
    if (ids.length === 0) return;
    update((s) => {
      const existing = new Set(s.models.map((m) => m.id));
      const added: UpstreamModel[] = ids.filter((id) => !existing.has(id)).map((id) => ({ id, owned_by: '' }));
      return { ...s, models: [...s.models, ...added], statusChecked: false, refChecked: false };
    });
    setManual('');
  };

  const counts = useMemo(() => {
    const c = { all: state.models.length, new: 0, vm_exists: 0, listed: 0 };
    for (const m of state.models) {
      const st = state.platformStatus[m.id];
      if (st) c[st]++;
    }
    return c;
  }, [state.models, state.platformStatus]);

  const shown = state.models.filter((m) => {
    if (q && !m.id.toLowerCase().includes(q.toLowerCase())) return false;
    if (filter !== 'all' && state.platformStatus[m.id] !== filter) return false;
    return true;
  });

  const selected = new Set(state.selected);
  const toggle = (id: string) =>
    update((s) => ({ ...s, selected: s.selected.includes(id) ? s.selected.filter((x) => x !== id) : [...s.selected, id] }));
  const selectAll = (pred: (id: string) => boolean) =>
    update((s) => ({ ...s, selected: Array.from(new Set([...s.selected, ...shown.map((m) => m.id).filter(pred)])) }));

  const next = () => {
    update((s) => {
      const rows = { ...s.rows };
      for (const id of s.selected) {
        if (!rows[id]) {
          const m = s.models.find((x) => x.id === id);
          if (m) rows[id] = defaultRow(m, s.refPrices[id], s.platformStatus[id]);
        }
      }
      return { ...s, rows };
    });
    goto(4);
  };

  return (
    <div className="space-y-4">
      {loadingModels && <div className="text-xs text-gray-400">正在拉取上游模型列表…</div>}
      {modelsError && <div className="bg-rose-50 border border-rose-200 text-rose-700 rounded-xl p-3 text-xs">{modelsError}</div>}

      <div className="flex flex-wrap items-center gap-2">
        <SearchInput className="w-full sm:w-64" placeholder="搜索模型 ID…" value={q} onChange={(e) => setQ(e.target.value)} />
        <Pills
          value={filter}
          onChange={(v) => setFilter(v as typeof filter)}
          options={[
            { value: 'all', label: '全部', count: counts.all },
            { value: 'new', label: '新模型', count: counts.new },
            { value: 'vm_exists', label: '已有虚拟模型', count: counts.vm_exists },
            { value: 'listed', label: '已上架', count: counts.listed },
          ]}
        />
        <div className="ml-auto flex items-center gap-2">
          <Button size="sm" onClick={() => selectAll((id) => state.platformStatus[id] === 'new')} disabled={!state.statusChecked}>
            全选新模型
          </Button>
          <Button size="sm" onClick={() => selectAll((id) => state.platformStatus[id] !== 'listed')} disabled={!state.statusChecked}>
            全选可导入
          </Button>
          <Button size="sm" variant="ghost" icon={<RotateCw className="w-3.5 h-3.5" />} loading={refLoading} onClick={() => void lookup()}>
            重新查询参考价
          </Button>
        </div>
      </div>

      {progress && (
        <div className="text-[11px] text-gray-500">
          正在核对本平台状态 {progress.done}/{progress.total}…
          <div className="h-1 bg-gray-100 rounded-full mt-1 overflow-hidden">
            <div className="h-full bg-purple-500 transition-all" style={{ width: `${(progress.done / Math.max(progress.total, 1)) * 100}%` }} />
          </div>
        </div>
      )}
      {state.refError && (
        <div className="bg-amber-50/80 border border-amber-200 rounded-xl p-3 text-xs text-amber-900">
          参考价查询部分失败（不影响继续，下一步可手动填写成本价）：{state.refError}
        </div>
      )}

      <div className="bg-white border border-gray-200 rounded-xl shadow-xs overflow-hidden">
        <div className="max-h-[28rem] overflow-y-auto">
          <table className="w-full text-xs">
            <thead className="sticky top-0 bg-gray-50 z-10">
              <tr className="border-b border-gray-200 text-[10px] text-gray-400 uppercase tracking-wider font-semibold">
                <th className="w-10 px-4 py-2.5" />
                <th className="px-4 py-2.5 text-left">上游模型</th>
                <th className="px-4 py-2.5 text-left">OwnedBy</th>
                <th className="px-4 py-2.5 text-left">本平台状态</th>
                <th className="px-4 py-2.5 text-right">参考价 input / output（USD/1M）</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {shown.length === 0 ? (
                <tr>
                  <td colSpan={5} className="px-4 py-10 text-center text-gray-400">
                    {state.models.length === 0 ? '还没有模型：测试连接拉取上游模型，或在下方手动添加' : '没有符合条件的模型'}
                  </td>
                </tr>
              ) : (
                shown.map((m) => {
                  const st = state.platformStatus[m.id];
                  const listed = st === 'listed';
                  const ref = state.refPrices[m.id];
                  const isSel = selected.has(m.id);
                  return (
                    <tr
                      key={m.id}
                      onClick={() => !listed && toggle(m.id)}
                      className={cn(listed ? 'text-gray-400' : 'cursor-pointer hover:bg-gray-50/70', isSel && 'bg-purple-50/40')}
                    >
                      <td className="px-4 py-2">
                        {listed ? (
                          <Square className="w-3.5 h-3.5 text-gray-200" />
                        ) : isSel ? (
                          <CheckSquare className="w-3.5 h-3.5 text-purple-600" />
                        ) : (
                          <Square className="w-3.5 h-3.5 text-gray-400" />
                        )}
                      </td>
                      <td className={cn('px-4 py-2 font-mono', !listed && 'text-gray-900')}>{m.id}</td>
                      <td className="px-4 py-2 text-gray-500">{m.owned_by || '—'}</td>
                      <td className="px-4 py-2">
                        {st ? (
                          <span title={STATUS_LABEL[st].title} className={cn('inline-flex px-2 py-0.5 rounded-full border text-[11px] font-medium', STATUS_LABEL[st].className)}>
                            {STATUS_LABEL[st].label}
                          </span>
                        ) : (
                          <span className="text-gray-300">核对中…</span>
                        )}
                      </td>
                      <td className="px-4 py-2 text-right font-mono whitespace-nowrap">
                        {!state.refChecked ? (
                          <span className="text-gray-300">查询中…</span>
                        ) : ref?.matched ? (
                          <span className="text-gray-900" title={`来源：${ref.source}`}>
                            ${ref.input} / ${ref.output}
                            <span className="ml-1.5 text-[10px] text-gray-400 font-sans">{ref.source}</span>
                          </span>
                        ) : (
                          <span className="text-gray-400">—</span>
                        )}
                      </td>
                    </tr>
                  );
                })
              )}
            </tbody>
          </table>
        </div>
      </div>

      <div className="flex items-end gap-2 max-w-2xl">
        <div className="flex-1">
          <label className="block text-xs font-medium text-gray-700 mb-1">手动添加模型 ID</label>
          <Input
            mono
            value={manual}
            onChange={(e) => setManual(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && addManual()}
            placeholder={isOpenAI ? '上游列表里没有的模型，多个用空格或逗号分隔' : `${state.provider.protocol} 协议无法自动拉取，请填写模型 ID，多个用空格或逗号分隔`}
          />
        </div>
        <Button icon={<Plus className="w-3.5 h-3.5" />} onClick={addManual} disabled={!manual.trim()}>
          添加
        </Button>
      </div>

      <StepFooter
        onBack={mode === 'append' ? undefined : () => goto(2)}
        onNext={next}
        nextDisabled={state.selected.length === 0 || !state.statusChecked}
        nextLabel={`下一步：为 ${state.selected.length} 个模型定价`}
      />
    </div>
  );
}
