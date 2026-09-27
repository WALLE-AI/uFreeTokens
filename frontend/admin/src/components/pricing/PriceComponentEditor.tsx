import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { AlertTriangle, ChevronDown, ChevronRight, Plus, Trash2 } from 'lucide-react';
import { errorMessage } from '../../api/errors';
import { cn } from '../../lib/cn';
import type { Meter, PriceComponent, PriceComponentInput, PriceUnit } from '../../types';
import { Button, ConfirmDialog, IconButton, Input, Modal, Select } from '../ui';

// PriceComponentEditor：售价与成本价共用的价格组件编辑器（UI_DESIGN.md §5.4 第 3 点）。
//
// - 表格化编辑 meter / unit / unit_price；service_tier、分档、时段窗口折叠在"高级"里。
// - 基础行（input/output、per_1m_tokens、default、分档起点 0、无时段）右侧实时显示对比价与毛利率：
//   kind="sell"：reference 是该模型各 active 渠道中最高的 CNY 成本；售价低于成本整行标红并阻止提交。
//   kind="cost"：reference 是当前 CNY 售价；toCNY 把新成本折算成 CNY 后比较，负毛利只警告
//   （成本是上游事实，不能因为亏钱就拒绝录入）。
// - 提交前弹出新旧对比确认框，确认后调用 onSubmit；价格是版本化的，每次提交都是追加新版本。

type Row = {
  key: number;
  meter: Meter;
  unit: PriceUnit;
  unit_price: string;
  service_tier: string;
  tier_min_input: string;
  tier_max_input: string;
  window_start_min: string;
  window_end_min: string;
};

const METERS: Array<{ value: Meter; label: string }> = [
  { value: 'input', label: 'input 输入' },
  { value: 'output', label: 'output 输出' },
  { value: 'input_cache_read', label: 'input_cache_read 缓存读' },
  { value: 'input_cache_write', label: 'input_cache_write 缓存写' },
  { value: 'output_reasoning', label: 'output_reasoning 推理' },
  { value: 'request', label: 'request 按次' },
];

const UNITS: Array<{ value: PriceUnit; label: string }> = [
  { value: 'per_1m_tokens', label: '每百万 token' },
  { value: 'per_request', label: '每次请求' },
  { value: 'per_image', label: '每张图' },
  { value: 'per_second', label: '每秒' },
];

const DECIMAL_RE = /^\d+(\.\d{1,10})?$/;
const INT_RE = /^\d+$/;

let seq = 0;
function toRow(c: Partial<PriceComponent> & { meter: Meter; unit: PriceUnit }): Row {
  return {
    key: ++seq,
    meter: c.meter,
    unit: c.unit,
    unit_price: c.unit_price ?? '',
    service_tier: c.service_tier ?? 'default',
    tier_min_input: String(c.tier_min_input ?? 0),
    tier_max_input: c.tier_max_input == null ? '' : String(c.tier_max_input),
    window_start_min: c.window_start_min == null ? '' : String(c.window_start_min),
    window_end_min: c.window_end_min == null ? '' : String(c.window_end_min),
  };
}

function isBase(r: { meter: string; unit: string; service_tier: string; tier_min_input: string | number; window_start_min: string | number | null }) {
  return (
    (r.meter === 'input' || r.meter === 'output') &&
    r.unit === 'per_1m_tokens' &&
    (r.service_tier === '' || r.service_tier === 'default') &&
    Number(r.tier_min_input || 0) === 0 &&
    (r.window_start_min === '' || r.window_start_min === null)
  );
}

function rowIdentity(r: { meter: string; service_tier: string; tier_min_input: string | number; window_start_min: string | number | null }) {
  const ws = r.window_start_min === '' || r.window_start_min === null ? '-' : String(r.window_start_min);
  return `${r.meter}|${r.service_tier || 'default'}|${Number(r.tier_min_input || 0)}|${ws}`;
}

export interface PriceEditorReference {
  input: number | null;
  output: number | null;
  label: string; // 例如 "最高渠道成本（CNY）" / "当前售价（CNY）"
}

export interface PriceComponentEditorProps {
  open: boolean;
  onClose: () => void;
  kind: 'sell' | 'cost';
  title: ReactNode;
  description?: ReactNode;
  current: PriceComponent[]; // 当前生效版本（用于预填与新旧对比）
  currency: string;
  currencyOptions?: string[]; // 提供时可切换币种（成本价）；售价固定 CNY
  reference?: PriceEditorReference | null;
  // 把本编辑器币种下的单价折算成 CNY（成本价：× 汇率 × 倍率）；返回 null 表示无法折算
  toCNY?: (price: number, currency: string) => number | null;
  onSubmit: (currency: string, components: PriceComponentInput[]) => Promise<void>;
}

export function PriceComponentEditor({
  open,
  onClose,
  kind,
  title,
  description,
  current,
  currency: initialCurrency,
  currencyOptions,
  reference,
  toCNY,
  onSubmit,
}: PriceComponentEditorProps) {
  const [rows, setRows] = useState<Row[]>([]);
  const [currency, setCurrency] = useState(initialCurrency);
  const [advanced, setAdvanced] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  // 只在"打开"的那一刻用当前版本预填；打开期间 current/currency 的引用变化（父组件重渲染）不能冲掉编辑中的内容
  const wasOpen = useRef(false);
  useEffect(() => {
    const justOpened = open && !wasOpen.current;
    wasOpen.current = open;
    if (!justOpened) return;
    const init = current.length > 0 ? current.map(toRow) : [toRow({ meter: 'input', unit: 'per_1m_tokens' }), toRow({ meter: 'output', unit: 'per_1m_tokens' })];
    setRows(init);
    setCurrency(initialCurrency);
    setAdvanced(current.some((c) => !isBase(c) && (c.service_tier !== 'default' || c.tier_min_input !== 0 || c.window_start_min !== null)));
    setSubmitError(null);
    setConfirming(false);
  }, [open, current, initialCurrency]);

  const update = (key: number, patch: Partial<Row>) => setRows((rs) => rs.map((r) => (r.key === key ? { ...r, ...patch } : r)));

  // 每行的校验与毛利分析
  const analysis = useMemo(() => {
    const seen = new Map<string, number>();
    rows.forEach((r) => seen.set(rowIdentity(r), (seen.get(rowIdentity(r)) ?? 0) + 1));
    return rows.map((r) => {
      const errors: string[] = [];
      if (!DECIMAL_RE.test(r.unit_price.trim())) errors.push('单价需为非负数，最多 10 位小数');
      if (!INT_RE.test(r.tier_min_input.trim() || '0')) errors.push('分档起点需为整数');
      if (r.tier_max_input.trim() && !INT_RE.test(r.tier_max_input.trim())) errors.push('分档终点需为整数');
      const ws = r.window_start_min.trim();
      const we = r.window_end_min.trim();
      if ((ws === '') !== (we === '')) errors.push('时段窗口需同时填写起止');
      if ((ws && (!INT_RE.test(ws) || Number(ws) > 1440)) || (we && (!INT_RE.test(we) || Number(we) > 1440))) errors.push('时段为 0–1440 分钟');
      if ((seen.get(rowIdentity(r)) ?? 0) > 1) errors.push('与其他行重复（计量项 + 档位 + 分档起点 + 时段）');

      let refPrice: number | null = null;
      let cnyPrice: number | null = null;
      let margin: number | null = null;
      let negative = false;
      if (reference && isBase(r) && DECIMAL_RE.test(r.unit_price.trim())) {
        const p = Number(r.unit_price);
        refPrice = r.meter === 'input' ? reference.input : reference.output;
        if (kind === 'sell') {
          cnyPrice = p;
          if (refPrice !== null && p > 0) margin = 1 - refPrice / p;
          negative = refPrice !== null && p < refPrice;
        } else {
          cnyPrice = toCNY ? toCNY(p, currency) : currency === 'CNY' ? p : null;
          if (cnyPrice !== null && refPrice !== null && refPrice > 0) margin = 1 - cnyPrice / refPrice;
          negative = margin !== null && margin < 0;
        }
      }
      return { errors, refPrice, cnyPrice, margin, negative };
    });
  }, [rows, reference, kind, toCNY, currency]);

  const hasErrors = rows.length === 0 || analysis.some((a) => a.errors.length > 0);
  const blockingNegative = kind === 'sell' && analysis.some((a) => a.negative);
  const warnNegative = kind === 'cost' && analysis.some((a) => a.negative);

  const components: PriceComponentInput[] = rows.map((r) => ({
    meter: r.meter,
    unit: r.unit,
    unit_price: r.unit_price.trim(),
    service_tier: r.service_tier.trim() || 'default',
    tier_min_input: Number(r.tier_min_input || 0),
    tier_max_input: r.tier_max_input.trim() ? Number(r.tier_max_input) : null,
    window_start_min: r.window_start_min.trim() ? Number(r.window_start_min) : null,
    window_end_min: r.window_end_min.trim() ? Number(r.window_end_min) : null,
  }));

  const addBasePair = () => {
    const have = new Set(rows.filter(isBase).map((r) => r.meter));
    const add: Row[] = [];
    if (!have.has('input')) add.push(toRow({ meter: 'input', unit: 'per_1m_tokens' }));
    if (!have.has('output')) add.push(toRow({ meter: 'output', unit: 'per_1m_tokens' }));
    if (add.length === 0) add.push(toRow({ meter: 'input_cache_read', unit: 'per_1m_tokens' }));
    setRows((rs) => [...rs, ...add]);
  };

  const submit = async () => {
    setSubmitting(true);
    setSubmitError(null);
    try {
      await onSubmit(currency, components);
      setConfirming(false);
      onClose();
    } catch (err) {
      setSubmitError(errorMessage(err, '发布失败'));
      setConfirming(false);
    } finally {
      setSubmitting(false);
    }
  };

  const sym = currency === 'CNY' ? '¥' : currency === 'USD' ? '$' : `${currency} `;

  return (
    <>
      <Modal
        open={open && !confirming}
        onClose={onClose}
        busy={submitting}
        width="xl"
        title={title}
        description={description}
        footer={
          <>
            <Button onClick={onClose}>取消</Button>
            <Button variant="primary" disabled={hasErrors || blockingNegative} onClick={() => setConfirming(true)}>
              下一步：确认变更
            </Button>
          </>
        }
      >
        <div className="space-y-3">
          <div className="flex flex-wrap items-center gap-3">
            {currencyOptions ? (
              <label className="flex items-center gap-2 text-xs text-gray-600">
                币种
                <Select value={currency} onChange={(e) => setCurrency(e.target.value)} options={currencyOptions.map((c) => ({ value: c, label: c }))} />
              </label>
            ) : (
              <span className="text-xs text-gray-500">
                币种 <span className="font-mono text-gray-900">{currency}</span>
              </span>
            )}
            <button
              type="button"
              onClick={() => setAdvanced((v) => !v)}
              className="inline-flex items-center gap-1 text-xs text-gray-500 hover:text-gray-800 cursor-pointer"
            >
              {advanced ? <ChevronDown className="w-3 h-3" /> : <ChevronRight className="w-3 h-3" />}
              高级（档位 / 分档 / 时段）
            </button>
            {reference && <span className="ml-auto text-[11px] text-gray-400">对比：{reference.label}</span>}
          </div>

          <div className="overflow-x-auto border border-gray-200 rounded-xl">
            <table className="w-full text-xs">
              <thead>
                <tr className="bg-gray-50 border-b border-gray-200 text-[10px] text-gray-400 uppercase tracking-wider font-semibold">
                  <th className="px-3 py-2 text-left">计量项</th>
                  <th className="px-3 py-2 text-left">单位</th>
                  {advanced && (
                    <>
                      <th className="px-3 py-2 text-left">档位</th>
                      <th className="px-3 py-2 text-left">分档 token</th>
                      <th className="px-3 py-2 text-left">时段（分钟）</th>
                    </>
                  )}
                  <th className="px-3 py-2 text-right">单价（{currency}）</th>
                  {reference && (
                    <>
                      <th className="px-3 py-2 text-right">{kind === 'sell' ? '成本 CNY' : '折合 CNY / 售价'}</th>
                      <th className="px-3 py-2 text-right">毛利率</th>
                    </>
                  )}
                  <th className="w-8" />
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100">
                {rows.map((r, i) => {
                  const a = analysis[i];
                  return (
                    <tr key={r.key} className={cn(a.negative && (kind === 'sell' ? 'bg-rose-50' : 'bg-amber-50/60'))}>
                      <td className="px-3 py-2 align-top">
                        <Select value={r.meter} onChange={(e) => update(r.key, { meter: e.target.value as Meter })} options={METERS} className="w-44" />
                      </td>
                      <td className="px-3 py-2 align-top">
                        <Select value={r.unit} onChange={(e) => update(r.key, { unit: e.target.value as PriceUnit })} options={UNITS} className="w-32" />
                      </td>
                      {advanced && (
                        <>
                          <td className="px-3 py-2 align-top">
                            <Input className="w-24" value={r.service_tier} onChange={(e) => update(r.key, { service_tier: e.target.value })} placeholder="default" />
                          </td>
                          <td className="px-3 py-2 align-top">
                            <div className="flex items-center gap-1">
                              <Input mono className="w-20" value={r.tier_min_input} onChange={(e) => update(r.key, { tier_min_input: e.target.value })} placeholder="0" />
                              <span className="text-gray-400">–</span>
                              <Input mono className="w-20" value={r.tier_max_input} onChange={(e) => update(r.key, { tier_max_input: e.target.value })} placeholder="∞" />
                            </div>
                          </td>
                          <td className="px-3 py-2 align-top">
                            <div className="flex items-center gap-1">
                              <Input mono className="w-16" value={r.window_start_min} onChange={(e) => update(r.key, { window_start_min: e.target.value })} placeholder="起" />
                              <span className="text-gray-400">–</span>
                              <Input mono className="w-16" value={r.window_end_min} onChange={(e) => update(r.key, { window_end_min: e.target.value })} placeholder="止" />
                            </div>
                          </td>
                        </>
                      )}
                      <td className="px-3 py-2 align-top">
                        <div className="relative">
                          <span className="absolute left-2.5 top-1/2 -translate-y-1/2 text-gray-400 pointer-events-none">{sym}</span>
                          <Input
                            mono
                            className="w-32 pl-6 text-right ml-auto"
                            value={r.unit_price}
                            invalid={r.unit_price !== '' && !DECIMAL_RE.test(r.unit_price.trim())}
                            onChange={(e) => update(r.key, { unit_price: e.target.value })}
                            placeholder="0.00"
                          />
                        </div>
                        {a.errors.length > 0 && r.unit_price !== '' && <p className="text-[11px] text-rose-600 mt-1 text-right">{a.errors[0]}</p>}
                      </td>
                      {reference && (
                        <>
                          <td className="px-3 py-2 align-top text-right font-mono text-gray-600 whitespace-nowrap pt-3.5">
                            {kind === 'sell'
                              ? a.refPrice !== null
                                ? `¥${a.refPrice.toLocaleString('en-US', { maximumFractionDigits: 6 })}`
                                : '—'
                              : a.cnyPrice !== null
                                ? `¥${a.cnyPrice.toLocaleString('en-US', { maximumFractionDigits: 6 })} / ${a.refPrice !== null ? `¥${a.refPrice}` : '—'}`
                                : isBase(r) && currency !== 'CNY'
                                  ? '缺汇率'
                                  : '—'}
                          </td>
                          <td className="px-3 py-2 align-top text-right font-mono whitespace-nowrap pt-3.5">
                            {a.margin === null ? (
                              <span className="text-gray-400">—</span>
                            ) : (
                              <span className={a.margin < 0 ? 'text-rose-600 font-medium' : a.margin < 0.1 ? 'text-amber-700' : 'text-gray-900'}>
                                {(a.margin * 100).toFixed(1)}%
                              </span>
                            )}
                          </td>
                        </>
                      )}
                      <td className="px-2 py-2 align-top pt-3">
                        <IconButton label="删除该行" danger onClick={() => setRows((rs) => rs.filter((x) => x.key !== r.key))}>
                          <Trash2 className="w-3.5 h-3.5" />
                        </IconButton>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>

          <div className="flex items-center gap-2">
            <Button size="sm" icon={<Plus className="w-3 h-3" />} onClick={addBasePair}>
              添加 input + output
            </Button>
            <Button size="sm" variant="ghost" icon={<Plus className="w-3 h-3" />} onClick={() => setRows((rs) => [...rs, toRow({ meter: 'input_cache_read', unit: 'per_1m_tokens' })])}>
              添加一行
            </Button>
          </div>

          {blockingNegative && (
            <div className="bg-rose-50 border border-rose-200 text-rose-700 rounded-xl p-3 text-xs flex items-start gap-2">
              <AlertTriangle className="w-4 h-4 shrink-0" />
              售价低于渠道成本（标红行），发布后会亏损，不能提交。请调高售价或先处理高成本渠道。
            </div>
          )}
          {warnNegative && (
            <div className="bg-amber-50/80 border border-amber-200 rounded-xl p-3 flex items-start gap-2 text-xs text-amber-900">
              <AlertTriangle className="w-4 h-4 shrink-0" />
              新成本折合后高于当前售价，该渠道将出现负毛利。成本价是上游事实，可以发布，但请同步检查售价。
            </div>
          )}
          {submitError && <div className="bg-rose-50 border border-rose-200 text-rose-700 rounded-xl p-3 text-xs">{submitError}</div>}
          <p className="text-[11px] text-gray-400">价格是版本化的：发布会追加一个立即生效的新版本，旧版本永久保留在历史里，不能修改。</p>
        </div>
      </Modal>

      <ConfirmDialog
        open={open && confirming}
        onClose={() => setConfirming(false)}
        onConfirm={submit}
        loading={submitting}
        level={warnNegative ? 'danger' : 'normal'}
        confirmLabel="确认发布"
        title={kind === 'sell' ? '确认发布新售价' : '确认发布新成本价'}
      >
        <PriceDiffTable current={current} next={components} currency={currency} />
        <p className="text-[11px] text-gray-500">发布后立即生效（gateway 目录快照约 10 秒内刷新），并记录到审计日志。</p>
      </ConfirmDialog>
    </>
  );
}

// PriceDiffTable：新旧价格组件逐项对比（确认框、历史版本对比共用）
export function PriceDiffTable({
  current,
  next,
  currency,
}: {
  current: Array<Pick<PriceComponent, 'meter' | 'unit' | 'service_tier' | 'tier_min_input' | 'window_start_min' | 'unit_price'>>;
  next: Array<Pick<PriceComponentInput, 'meter' | 'unit' | 'unit_price'> & Partial<PriceComponent>>;
  currency?: string;
}) {
  const norm = (c: { meter: string; service_tier?: string | null; tier_min_input?: number | null; window_start_min?: number | null }) =>
    rowIdentity({ meter: c.meter, service_tier: c.service_tier ?? 'default', tier_min_input: c.tier_min_input ?? 0, window_start_min: c.window_start_min ?? null });
  const oldMap = new Map(current.map((c) => [norm(c), c]));
  const newMap = new Map(next.map((c) => [norm(c), c]));
  const keys = Array.from(new Set([...oldMap.keys(), ...newMap.keys()]));
  return (
    <div className="overflow-x-auto border border-gray-200 rounded-xl">
      <table className="w-full text-xs">
        <thead>
          <tr className="bg-gray-50 border-b border-gray-200 text-[10px] text-gray-400 uppercase tracking-wider font-semibold">
            <th className="px-3 py-2 text-left">计量项</th>
            <th className="px-3 py-2 text-right">当前</th>
            <th className="px-3 py-2 text-right">新{currency ? `（${currency}）` : ''}</th>
            <th className="px-3 py-2 text-right">变化</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-gray-100">
          {keys.map((k) => {
            const o = oldMap.get(k);
            const n = newMap.get(k);
            const [meter, tier, tmin, ws] = k.split('|');
            const ov = o ? Number(o.unit_price) : null;
            const nv = n ? Number(n.unit_price) : null;
            const ratio = ov !== null && nv !== null && ov !== 0 ? (nv - ov) / ov : null;
            return (
              <tr key={k} className={cn(!o && 'bg-emerald-50', !n && 'bg-rose-50')}>
                <td className="px-3 py-2">
                  <span className="font-mono text-gray-900">{meter}</span>
                  {(tier !== 'default' || tmin !== '0' || ws !== '-') && (
                    <span className="ml-1 text-[11px] text-gray-400">
                      {tier !== 'default' && tier}
                      {tmin !== '0' && ` ≥${tmin}`}
                      {ws !== '-' && ` @${ws}min`}
                    </span>
                  )}
                </td>
                <td className={cn('px-3 py-2 text-right font-mono', !n ? 'line-through text-rose-600' : 'text-gray-400')}>{o ? o.unit_price : '—'}</td>
                <td className="px-3 py-2 text-right font-mono text-gray-900 font-medium">{n ? n.unit_price : '—'}</td>
                <td className="px-3 py-2 text-right font-mono">
                  {!o ? (
                    <span className="text-emerald-700">新增</span>
                  ) : !n ? (
                    <span className="text-rose-600">移除</span>
                  ) : ratio === null ? (
                    <span className="text-gray-400">—</span>
                  ) : ratio === 0 ? (
                    <span className="text-gray-400">不变</span>
                  ) : (
                    <span className={ratio > 0 ? 'text-rose-600' : 'text-emerald-700'}>
                      {ratio > 0 ? '▲' : '▼'} {Math.abs(ratio * 100).toFixed(1)}%
                    </span>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
