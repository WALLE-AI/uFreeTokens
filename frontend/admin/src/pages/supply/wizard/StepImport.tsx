import { describeError } from '../../../api/errors';
import { useRef, useState } from 'react';
import { Link, useNavigate } from 'react-router';
import { CheckCircle2, Circle, Loader2, XCircle } from 'lucide-react';
import { createChannel, createVirtualModel, findChannel, findVirtualModelByName, listLatestFXRates, setCostPrice, setSellPrice } from '../../../api/catalog';
import { Button } from '../../../components/ui';
import { useAsync } from '../../../hooks/useAsync';
import { computeRow, resolvedAccountId, resolvedMultiplier, round4, type ImportResult, type RowConfig } from './state';
import { StepFooter, type StepProps } from './ui';

function priceString(n: number): string {
  return String(round4(n));
}

// ⑤ 确认导入：逐行"先查后建"（与 test_web 已验证的幂等逻辑一致），逐行显示结果，
// 部分失败时只重试失败项。
export function StepImport({ state, update, goto, onRestart }: StepProps & { onRestart: () => void }) {
  const navigate = useNavigate();
  const accountId = resolvedAccountId(state);
  const multiplier = resolvedMultiplier(state);
  const fxRates = useAsync((signal) => listLatestFXRates(signal), []);
  const usd = fxRates.data?.data.find((r) => r.base === 'USD' && r.quote === 'CNY');
  const fx = usd ? Number(usd.rate) : null;
  const [running, setRunning] = useState(false);
  const cancelRef = useRef(false);

  const ids = state.selected.filter((id) => state.rows[id]);
  const createVmCount = ids.filter((id) => state.platformStatus[id] === 'new').length;
  const createChannelCount = ids.filter((id) => state.platformStatus[id] !== 'listed').length;
  const sellCount = ids.filter((id) => !state.rows[id].keepSell).length;

  const setResult = (id: string, r: ImportResult) => update((s) => ({ ...s, results: { ...s.results, [id]: r } }));

  const importOne = async (id: string, row: RowConfig): Promise<ImportResult> => {
    const p = computeRow(row, fx, multiplier, state.globalMarkup, state.refPrices[id]);
    if (p.errors.length > 0) return { state: 'error', error: p.errors.join('、') };
    const name = row.name.trim();
    let createdVm = false;
    let createdChannel = false;
    let vm = await findVirtualModelByName(name);
    if (!vm) {
      vm = await createVirtualModel({
        name,
        family: row.family.trim(),
        type: 'chat',
        context_window: Number(row.contextWindow),
        max_output: Number(row.maxOutput),
        capabilities: ['stream'],
      });
      createdVm = true;
    }
    let ch = await findChannel(vm.id, accountId!, id);
    if (!ch) {
      ch = await createChannel({ virtual_model_id: vm.id, provider_account_id: accountId!, upstream_model: id });
      createdChannel = true;
    }
    await setCostPrice(ch.id, 'USD', [
      { meter: 'input', unit: 'per_1m_tokens', unit_price: row.costIn.trim() },
      { meter: 'output', unit: 'per_1m_tokens', unit_price: row.costOut.trim() },
    ]);
    if (!row.keepSell) {
      await setSellPrice(vm.id, [
        { meter: 'input', unit: 'per_1m_tokens', unit_price: priceString(p.sellIn!) },
        { meter: 'output', unit: 'per_1m_tokens', unit_price: priceString(p.sellOut!) },
      ]);
    }
    return { state: 'ok', vmId: vm.id, channelId: ch.id, createdVm, createdChannel };
  };

  const run = async (onlyFailed: boolean) => {
    if (accountId === null) return;
    setRunning(true);
    cancelRef.current = false;
    const targets = ids.filter((id) => {
      const r = state.results[id];
      return onlyFailed ? r?.state === 'error' : r?.state !== 'ok';
    });
    for (const id of targets) {
      if (cancelRef.current) break;
      setResult(id, { state: 'running' });
      try {
        setResult(id, await importOne(id, state.rows[id]));
      } catch (err) {
        setResult(id, { state: 'error', error: describeError(err) });
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

      {fx === null && !fxRates.loading && (
        <div className="bg-rose-50 border border-rose-200 text-rose-700 rounded-xl p-3 text-xs">缺少 USD→CNY 汇率，请回到上一步设置。</div>
      )}

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
            <Button onClick={onRestart}>继续接入</Button>
            <Button
              variant="primary"
              onClick={() => navigate(ids.length === 1 && state.results[ids[0]]?.vmId ? `/models/${state.results[ids[0]]?.vmId}` : '/models')}
            >
              查看已导入模型
            </Button>
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
                <Button onClick={() => void run(true)} disabled={fx === null}>
                  仅重试失败项（{errCount}）
                </Button>
              )}
            </>
          }
          onNext={() => void run(false)}
          nextDisabled={running || fx === null || accountId === null}
          nextLoading={running}
          nextLabel={started ? '继续导入未完成项' : `开始导入 ${ids.length} 个模型`}
        />
      )}
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
