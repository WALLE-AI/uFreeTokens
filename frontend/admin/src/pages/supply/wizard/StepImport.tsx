import { describeError } from '../../../api/errors';
import { useRef, useState } from 'react';
import { Link, useNavigate } from 'react-router';
import { CheckCircle2, Circle, Loader2, XCircle } from 'lucide-react';
import { importModels } from '../../../api/catalog';
import { Button } from '../../../components/ui';
import { useAsync } from '../../../hooks/useAsync';
import type { ImportModelItemInput, ImportModelRow } from '../../../types';
import { KIND_INFO, resolvedAccountId, resolvedProviderId, type ImportResult, type RowConfig, type WizardState } from './state';
import { StepFooter, type StepProps } from './ui';
import { unitLabel } from '../../pricing/shared';

// 每批提交的模型数：服务端逐个模型各自一个事务导入，分批只是为了进度可见、可中途停止。
const IMPORT_BATCH = 10;

// importItem 把向导的一行配置转成批量导入接口的条目。售价没有手工覆盖时只传加价率，
// 由服务端用 decimal 计算（不再在浏览器里用浮点数算售价并提交）。
function importItem(id: string, row: RowConfig, s: WizardState): ImportModelItemInput {
  const opt = (v: string | null) => (v !== null && v.trim() !== '' ? v.trim() : undefined);
  const kind = row.kind ?? 'chat';
  const info = KIND_INFO[kind];
  const base: ImportModelItemInput = {
    upstream_model: id,
    name: row.name.trim(),
    family: row.family.trim(),
    type: info.type,
    context_window: Number(row.contextWindow),
    max_output: Number(row.maxOutput),
    markup_percent: opt(row.markup) ?? opt(s.globalMarkup),
    keep_existing_sell: row.keepSell,
  };
  if (kind === 'chat') {
    return {
      ...base,
      capabilities: row.vision ? ['stream', 'vision'] : ['stream'],
      cost_input: opt(row.costIn),
      cost_output: opt(row.costOut),
      sell_input: opt(row.sellIn),
      sell_output: opt(row.sellOut),
    };
  }
  // 非对话模型按计量项定价：一个计量项（张 / 百万字符 / 秒 / 百万 token）
  const comp = (price: string | undefined) => (price === undefined ? [] : [{ meter: info.meter!, unit: info.unit!, price }]);
  return {
    ...base,
    capabilities: kind === 'tts' ? ['tts'] : kind === 'asr' ? ['asr'] : [],
    cost_components: comp(opt(row.costIn)),
    sell_components: comp(opt(row.sellIn)),
    param_overrides: kind === 'tts' && row.voicePrefix ? { $voice_prefix_upstream_model: true } : undefined,
  };
}

function toResult(r: ImportModelRow): ImportResult {
  if (r.ok && r.result) {
    return { state: 'ok', vmId: r.result.virtual_model_id, channelId: r.result.channel_id, createdVm: r.result.created_vm, createdChannel: r.result.created_channel };
  }
  return { state: 'error', error: r.error?.message ?? (r.errors.join('、') || '导入失败') };
}

// ⑤ 确认导入：先调用 import-models?dry_run 让服务端核算售价与毛利、确认平台现状，
// 再分批正式导入（每个模型在服务端各自一个事务：建/复用虚拟模型 → 建/复用渠道 →
// 成本价 → 售价，失败不留半成品），逐行显示结果，部分失败时只重试失败项。
export function StepImport({ state, update, goto, onRestart, mode = 'onboard' }: StepProps & { onRestart: () => void }) {
  const navigate = useNavigate();
  const accountId = resolvedAccountId(state);
  const providerId = resolvedProviderId(state);
  const [running, setRunning] = useState(false);
  const cancelRef = useRef(false);

  const ids = state.selected.filter((id) => state.rows[id]);
  const createVmCount = ids.filter((id) => state.platformStatus[id] === 'new').length;
  const createChannelCount = ids.filter((id) => state.platformStatus[id] !== 'listed').length;
  const sellCount = ids.filter((id) => !state.rows[id].keepSell).length;

  const setResults = (rs: Record<string, ImportResult>) => update((s) => ({ ...s, results: { ...s.results, ...rs } }));

  // 服务端核算（dry_run）：售价、毛利、平台现状与逐行错误，全部以服务端 decimal 结果为准
  const plan = useAsync(
    (signal) =>
      accountId === null || ids.length === 0
        ? Promise.resolve(null)
        : importModels(accountId, { dry_run: true, currency: state.costCurrency, markup_percent: state.globalMarkup, items: ids.map((id) => importItem(id, state.rows[id], state)) }, signal),
    [accountId, ids.join('\n'), state.costCurrency],
  );
  const planById = new Map((plan.data?.items ?? []).map((r) => [r.upstream_model, r]));
  const fxMissing = plan.data?.fx_missing ?? false;

  const run = async (onlyFailed: boolean) => {
    if (accountId === null) return;
    setRunning(true);
    cancelRef.current = false;
    const targets = ids.filter((id) => {
      const r = state.results[id];
      return onlyFailed ? r?.state === 'error' : r?.state !== 'ok';
    });
    for (let i = 0; i < targets.length && !cancelRef.current; i += IMPORT_BATCH) {
      const batch = targets.slice(i, i + IMPORT_BATCH);
      setResults(Object.fromEntries(batch.map((id) => [id, { state: 'running' } as ImportResult])));
      try {
        const res = await importModels(accountId, {
          dry_run: false,
          currency: state.costCurrency,
          markup_percent: state.globalMarkup,
          items: batch.map((id) => importItem(id, state.rows[id], state)),
        });
        setResults(Object.fromEntries(res.items.map((r) => [r.upstream_model, toResult(r)])));
      } catch (err) {
        setResults(Object.fromEntries(batch.map((id) => [id, { state: 'error', error: describeError(err) } as ImportResult])));
      }
    }
    setRunning(false);
    update((s) => ({ ...s, finished: ids.every((id) => s.results[id]?.state === 'ok') }));
  };

  const results = ids.map((id) => state.results[id]?.state ?? 'pending');
  const okCount = results.filter((r) => r === 'ok').length;
  const errCount = results.filter((r) => r === 'error').length;
  const started = results.some((r) => r !== 'pending');
  const allOk = okCount === ids.length && ids.length > 0;

  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
        <Summary label="虚拟模型（新建）" value={createVmCount} />
        <Summary label="渠道（新建）" value={createChannelCount} />
        <Summary label="价格版本" value={ids.length + sellCount} sub={`成本价 ${ids.length} + 售价 ${sellCount}`} />
        <Summary label="进度" value={`${okCount}/${ids.length}`} sub={errCount > 0 ? `${errCount} 项失败` : undefined} warn={errCount > 0} />
      </div>

      {fxMissing && <div className="bg-rose-50 border border-rose-200 text-rose-700 rounded-xl p-3 text-xs">缺少 {state.costCurrency}→CNY 汇率，请回到上一步设置。</div>}
      {plan.error ? <div className="bg-rose-50 border border-rose-200 text-rose-700 rounded-xl p-3 text-xs">服务端核算失败：{describeError(plan.error)}</div> : null}

      <div className="bg-white border border-gray-200 rounded-xl shadow-xs divide-y divide-gray-100 max-h-[28rem] overflow-y-auto">
        {ids.map((id) => {
          const row = state.rows[id];
          const r = state.results[id] ?? { state: 'pending' as const };
          return (
            <div key={id} className="px-4 py-2.5 flex items-start gap-3 text-xs">
              <span className="mt-0.5">
                {r.state === 'ok' ? (
                  <CheckCircle2 className="w-3.5 h-3.5 text-emerald-600" />
                ) : r.state === 'error' ? (
                  <XCircle className="w-3.5 h-3.5 text-rose-600" />
                ) : r.state === 'running' ? (
                  <Loader2 className="w-3.5 h-3.5 text-purple-600 animate-spin" />
                ) : (
                  <Circle className="w-3.5 h-3.5 text-gray-300" />
                )}
              </span>
              <div className="min-w-0 flex-1">
                <div className="font-mono text-gray-900 truncate">{row.name}</div>
                <div className="text-[11px] text-gray-400 font-mono truncate">← {id}</div>
                {r.state === 'error' && <div className="text-[11px] text-rose-600 mt-0.5 break-all">{r.error}</div>}
                {r.state === 'pending' && planById.get(id) && <PlanLine row={planById.get(id)!} />}
              </div>
              {r.state === 'ok' && (
                <div className="text-[11px] text-gray-500 text-right shrink-0">
                  <Link to={`/models/${r.vmId}`} className="text-purple-600 hover:underline">
                    模型 #{r.vmId}
                  </Link>
                  {r.createdVm ? '（新建）' : '（已存在）'} ·{' '}
                  <Link to={`/channels/${r.channelId}`} className="text-purple-600 hover:underline">
                    渠道 #{r.channelId}
                  </Link>
                  {r.createdChannel ? '（新建）' : '（已存在）'}
                </div>
              )}
            </div>
          );
        })}
      </div>

      {allOk ? (
        <div className="flex items-center gap-2 pt-4 border-t border-gray-100">
          <div className="text-xs text-emerald-700 flex items-center gap-1.5">
            <CheckCircle2 className="w-3.5 h-3.5" /> 全部 {ids.length} 个模型导入完成，约 10 秒后网关生效
          </div>
          <div className="ml-auto flex items-center gap-2">
            <Button onClick={onRestart}>{mode === 'append' ? '继续添加模型' : '继续接入'}</Button>
            {mode === 'append' && providerId !== null ? (
              <Button variant="primary" onClick={() => navigate(`/providers/${providerId}#models`)}>
                返回供应商
              </Button>
            ) : (
              <Button
                variant="primary"
                onClick={() => navigate(ids.length === 1 && state.results[ids[0]]?.vmId ? `/models/${state.results[ids[0]]?.vmId}` : '/models')}
              >
                查看已导入模型
              </Button>
            )}
          </div>
        </div>
      ) : (
        <StepFooter
          onBack={running || started ? undefined : () => goto(4)}
          extra={
            <>
              {running && (
                <Button variant="ghost" onClick={() => (cancelRef.current = true)}>
                  当前项完成后停止
                </Button>
              )}
              {!running && errCount > 0 && (
                <Button onClick={() => void run(true)} disabled={fxMissing}>
                  仅重试失败项（{errCount}）
                </Button>
              )}
            </>
          }
          onNext={() => void run(false)}
          nextDisabled={running || fxMissing || plan.loading || accountId === null}
          nextLoading={running}
          nextLabel={started ? '继续导入未完成项' : `开始导入 ${ids.length} 个模型`}
        />
      )}
    </div>
  );
}

// PlanLine 显示服务端核算的售价、毛利与错误（dry_run 结果）。
function PlanLine({ row }: { row: ImportModelRow }) {
  if (row.errors.length > 0) return <div className="text-[11px] text-rose-600 mt-0.5">{row.errors.join('、')}</div>;
  const margin = row.margin_ratio !== null ? `${(Number(row.margin_ratio) * 100).toFixed(2)}%` : '—';
  return (
    <div className="text-[11px] text-gray-500 mt-0.5 font-mono">
      {!row.publish_sell_price
        ? '保留现有售价'
        : row.components?.length
          ? `售价 ${row.components.map((c) => `¥${c.sell} ${unitLabel(c.unit)}`).join('、')} · 毛利 ${margin}`
          : `售价 ¥${row.sell_input} / ¥${row.sell_output} · 毛利 ${margin}`}
    </div>
  );
}

function Summary({ label, value, sub, warn }: { label: string; value: React.ReactNode; sub?: string; warn?: boolean }) {
  return (
    <div className="bg-gray-50 border border-gray-200 rounded-xl p-3.5">
      <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold">{label}</div>
      <div className={`text-lg font-bold font-mono mt-0.5 ${warn ? 'text-rose-600' : 'text-gray-900'}`}>{value}</div>
      {sub && <div className="text-[11px] text-gray-500">{sub}</div>}
    </div>
  );
}
