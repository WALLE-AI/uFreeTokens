import { describeError } from '../../../api/errors';
import { useState } from 'react';
import { AlertTriangle, RotateCcw } from 'lucide-react';
import { listLatestFXRates, setFXRate } from '../../../api/catalog';
import { Button, Input, useToast } from '../../../components/ui';
import { useAsync } from '../../../hooks/useAsync';
import { cn } from '../../../lib/cn';
import { fmtPrice } from '../common';
import { computeRow, resolvedMultiplier, type RowConfig } from './state';
import { StepFooter, type StepProps } from './ui';

// ④ 定价：参考成本 USD → 折合 CNY（× 汇率 × 成本倍率）→ 加价率 → 售价 CNY → 毛利率
export function StepPricing({ state, update, goto }: StepProps) {
  const toast = useToast();
  const [fxKey, setFxKey] = useState(0);
  const fxRates = useAsync((signal) => listLatestFXRates(signal), [fxKey]);
  const usd = fxRates.data?.data.find((r) => r.base === 'USD' && r.quote === 'CNY');
  const fx = usd ? Number(usd.rate) : null;
  const multiplier = resolvedMultiplier(state);
  const [fxDraft, setFxDraft] = useState('');
  const [fxSaving, setFxSaving] = useState(false);

  const ids = state.selected.filter((id) => state.rows[id]);
  const computed = ids.map((id) => ({ id, row: state.rows[id], p: computeRow(state.rows[id], fx, multiplier, state.globalMarkup, state.refPrices[id]) }));
  const invalid = computed.filter((c) => c.p.errors.length > 0);
  const negative = computed.filter((c) => !c.row.keepSell && c.p.margin !== null && c.p.margin < 0);

  const setRow = (id: string, patch: Partial<RowConfig>) => update((s) => ({ ...s, rows: { ...s.rows, [id]: { ...s.rows[id], ...patch } } }));
  const setAllRows = (fn: (id: string, r: RowConfig) => Partial<RowConfig>) =>
    update((s) => {
      const rows = { ...s.rows };
      for (const id of s.selected) if (rows[id]) rows[id] = { ...rows[id], ...fn(id, rows[id]) };
      return { ...s, rows };
    });

  const saveFx = async () => {
    setFxSaving(true);
    try {
      await setFXRate({ base: 'USD', quote: 'CNY', rate: fxDraft.trim(), source: 'manual' });
      toast.success(`已设置 1 USD = ${fxDraft} CNY`);
      setFxKey((k) => k + 1);
    } catch (err) {
      toast.error('设置汇率失败', describeError(err));
    } finally {
      setFxSaving(false);
    }
  };

  // 售价向上取整到 0.1 元（运营常用的"好看价格"）
  const roundUp = () =>
    setAllRows((id, r) => {
      const p = computeRow(r, fx, multiplier, state.globalMarkup, state.refPrices[id]);
      const up = (v: number | null) => (v === null ? null : String(Math.ceil(v * 10 - 1e-9) / 10));
      return { sellIn: up(p.sellIn), sellOut: up(p.sellOut) };
    });

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-x-6 gap-y-3 bg-gray-50 border border-gray-200 rounded-xl p-4 text-xs">
        <div>
          <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold">汇率 USD → CNY</div>
          {fxRates.loading ? (
            <div className="text-gray-400 mt-1">加载中…</div>
          ) : usd ? (
            <div className="font-mono text-gray-900 mt-1">
              {usd.rate} <span className="text-[11px] text-gray-400 font-sans">（{usd.effective_date.slice(0, 10)}）</span>
            </div>
          ) : (
            <div className="flex items-center gap-2 mt-1">
              <span className="text-amber-700">未配置</span>
              <Input mono className="w-20 py-1" value={fxDraft} onChange={(e) => setFxDraft(e.target.value)} placeholder="7.2" />
              <Button size="sm" loading={fxSaving} disabled={!(Number(fxDraft) > 0)} onClick={saveFx}>
                设置汇率
              </Button>
            </div>
          )}
        </div>
        <div>
          <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold">成本倍率</div>
          <div className="font-mono text-gray-900 mt-1">×{multiplier}</div>
        </div>
        <div className="flex-1 min-w-64">
          <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold">全局加价率</div>
          <div className="flex items-center gap-3 mt-1">
            <input
              type="range"
              min={0}
              max={200}
              step={1}
              value={Number(state.globalMarkup) || 0}
              onChange={(e) => update((s) => ({ ...s, globalMarkup: e.target.value }))}
              className="flex-1"
            />
            <div className="flex items-center gap-1">
              <Input mono className="w-16 py-1 text-right" value={state.globalMarkup} onChange={(e) => update((s) => ({ ...s, globalMarkup: e.target.value.replace(/[^\d.]/g, '') }))} />
              <span className="text-gray-500">%</span>
            </div>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" onClick={roundUp} disabled={fx === null}>
            售价向上取整到 0.1 元
          </Button>
          <Button size="sm" variant="ghost" icon={<RotateCcw className="w-3.5 h-3.5" />} onClick={() => setAllRows(() => ({ sellIn: null, sellOut: null, markup: null }))}>
            全部恢复自动计算
          </Button>
        </div>
      </div>

      <p className="text-[11px] text-gray-400">
        成本价以 USD 发布到渠道，运行时按汇率与成本倍率折算；售价以人民币发布，每个模型只有一个生效售价（运行时不区分 tier）。
      </p>

      <div className="overflow-x-auto bg-white border border-gray-200 rounded-xl shadow-xs">
        <table className="w-full text-xs">
          <thead>
            <tr className="bg-gray-50 border-b border-gray-200 text-[10px] text-gray-400 uppercase tracking-wider font-semibold">
              <th className="px-3 py-2.5 text-left">模型</th>
              <th className="px-3 py-2.5 text-left">family / 上下文 / 最大输出</th>
              <th className="px-3 py-2.5 text-right">成本 USD/1M（in / out）</th>
              <th className="px-3 py-2.5 text-right">折合 CNY</th>
              <th className="px-3 py-2.5 text-right">加价率</th>
              <th className="px-3 py-2.5 text-right">售价 CNY/1M（in / out）</th>
              <th className="px-3 py-2.5 text-right">毛利率</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100">
            {computed.map(({ id, row, p }) => {
              const neg = !row.keepSell && p.margin !== null && p.margin < 0;
              const needCost = p.missingRef && (!row.costIn || !row.costOut);
              return (
                <tr key={id} className={cn(neg ? 'bg-rose-50/60' : needCost ? 'bg-amber-50/50' : undefined)}>
                  <td className="px-3 py-2 align-top min-w-56">
                    <Input mono className="py-1" value={row.name} onChange={(e) => setRow(id, { name: e.target.value })} title="虚拟模型名（对外的 model ID）" />
                    <div className="text-[11px] text-gray-400 font-mono mt-1 truncate" title={id}>
                      ← {id}
                    </div>
                    {state.platformStatus[id] === 'vm_exists' && (
                      <label className="flex items-center gap-1.5 mt-1 text-[11px] text-amber-700">
                        <input type="checkbox" checked={row.keepSell} onChange={(e) => setRow(id, { keepSell: e.target.checked })} />
                        已有同名虚拟模型：保留其现有售价
                      </label>
                    )}
                    {p.missingRef && <div className="text-[11px] text-amber-700 mt-1">未匹配参考价，需手动填写成本</div>}
                    {p.errors.length > 0 && (
                      <div className="text-[11px] text-rose-600 mt-1 flex items-center gap-1">
                        <AlertTriangle className="w-3 h-3" /> {p.errors.join('、')}
                      </div>
                    )}
                  </td>
                  <td className="px-3 py-2 align-top">
                    <div className="flex gap-1.5">
                      <Input className="py-1 w-24" value={row.family} onChange={(e) => setRow(id, { family: e.target.value })} placeholder="family" />
                      <Input mono className="py-1 w-20 text-right" value={row.contextWindow} onChange={(e) => setRow(id, { contextWindow: e.target.value.replace(/\D/g, '') })} title="上下文窗口" />
                      <Input mono className="py-1 w-16 text-right" value={row.maxOutput} onChange={(e) => setRow(id, { maxOutput: e.target.value.replace(/\D/g, '') })} title="最大输出" />
                    </div>
                  </td>
                  <td className="px-3 py-2 align-top">
                    <div className="flex gap-1.5 justify-end">
                      <PriceInput value={row.costIn} onChange={(v) => setRow(id, { costIn: v })} warn={needCost && !row.costIn} />
                      <PriceInput value={row.costOut} onChange={(v) => setRow(id, { costOut: v })} warn={needCost && !row.costOut} />
                    </div>
                  </td>
                  <td className="px-3 py-2 align-top text-right font-mono text-gray-500 whitespace-nowrap pt-3">
                    {fmtPrice(p.costInCNY)} / {fmtPrice(p.costOutCNY)}
                  </td>
                  <td className="px-3 py-2 align-top">
                    <div className="flex items-center justify-end gap-1">
                      <Input
                        mono
                        className="py-1 w-14 text-right"
                        value={row.markup ?? ''}
                        placeholder={state.globalMarkup}
                        onChange={(e) => setRow(id, { markup: e.target.value.replace(/[^\d.]/g, '') || null, sellIn: null, sellOut: null })}
                      />
                      <span className="text-gray-400">%</span>
                    </div>
                  </td>
                  <td className="px-3 py-2 align-top">
                    {row.keepSell ? (
                      <div className="text-right text-gray-400 pt-1">保留现有售价</div>
                    ) : (
                      <div className="flex gap-1.5 justify-end items-center">
                        <PriceInput value={row.sellIn ?? (p.sellIn === null ? '' : String(p.sellIn))} manual={row.sellIn !== null} onChange={(v) => setRow(id, { sellIn: v })} />
                        <PriceInput value={row.sellOut ?? (p.sellOut === null ? '' : String(p.sellOut))} manual={row.sellOut !== null} onChange={(v) => setRow(id, { sellOut: v })} />
                        {(row.sellIn !== null || row.sellOut !== null) && (
                          <button
                            type="button"
                            title="恢复自动计算"
                            className="p-1 text-gray-400 hover:text-gray-700 cursor-pointer"
                            onClick={() => setRow(id, { sellIn: null, sellOut: null })}
                          >
                            <RotateCcw className="w-3 h-3" />
                          </button>
                        )}
                      </div>
                    )}
                  </td>
                  <td
                    className={cn(
                      'px-3 py-2 align-top text-right font-mono font-medium pt-3',
                      row.keepSell ? 'text-gray-300' : p.margin === null ? 'text-gray-400' : p.margin < 0 ? 'text-rose-600' : 'text-emerald-600',
                    )}
                  >
                    {row.keepSell || p.margin === null ? '—' : `${(p.margin * 100).toFixed(1)}%`}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      {(invalid.length > 0 || negative.length > 0) && (
        <div className="bg-rose-50 border border-rose-200 text-rose-700 rounded-xl p-3 text-xs">
          还有 {invalid.length} 行需要处理{negative.length > 0 ? `（其中 ${negative.length} 行为负毛利，禁止导入）` : ''}，修正后才能继续。
        </div>
      )}

      <StepFooter onBack={() => goto(3)} onNext={() => goto(5)} nextDisabled={invalid.length > 0 || computed.length === 0} nextLabel="下一步：确认导入" />
    </div>
  );
}

function PriceInput({ value, onChange, warn, manual }: { value: string; onChange: (v: string) => void; warn?: boolean; manual?: boolean }) {
  return (
    <Input
      mono
      className={cn('py-1 w-20 text-right', manual && 'bg-purple-50/40')}
      invalid={warn}
      value={value}
      title={manual ? '手工设置（不随加价率变化）' : undefined}
      onChange={(e) => onChange(e.target.value.replace(/[^\d.]/g, ''))}
      placeholder="0.00"
    />
  );
}
