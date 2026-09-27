import { describeError } from '../../api/errors';
import { useState } from 'react';
import { Plus } from 'lucide-react';
import { listFXRates, listLatestFXRates, setFXRate } from '../../api/catalog';
import { listPriceSources } from '../../api/pricing';
import {
  Button,
  ConfirmDialog,
  DataState,
  DataTable,
  Field,
  FormModal,
  Input,
  PageHeader,
  SectionTitle,
  Select,
  useToast,
  type Column,
} from '../../components/ui';
import { useAsync } from '../../hooks/useAsync';
import { useQueryParams } from '../../hooks/useQueryState';
import type { FXRate } from '../../types';
import { CreateSourceModal, PriceSourcesTable } from './sources';

// /pricing/sources：价格源 & 汇率（UI_DESIGN.md §1.1 目录与定价分组）
export default function SourcesPage() {
  const [params, setParams] = useQueryParams();
  const [sourcesKey, setSourcesKey] = useState(0);
  const [fxKey, setFxKey] = useState(0);
  const enabledFilter = params.enabled === 'true' ? true : params.enabled === 'false' ? false : undefined;

  const sources = useAsync((signal) => listPriceSources({ enabled: enabledFilter }, signal), [enabledFilter, sourcesKey]);
  const latest = useAsync((signal) => listLatestFXRates(signal), [fxKey]);
  const history = useAsync((signal) => listFXRates({ base: params.fx_base || undefined, limit: 60 }, signal), [params.fx_base, fxKey]);

  const [creatingSource, setCreatingSource] = useState(false);
  const [settingFx, setSettingFx] = useState<Partial<FXRate> | null>(null);

  const historyColumns: Column<FXRate>[] = [
    { key: 'pair', header: '币种对', render: (r) => <span className="font-mono">{r.base} → {r.quote}</span> },
    { key: 'rate', header: '汇率', numeric: true, render: (r) => r.rate },
    { key: 'effective_date', header: '生效日期', render: (r) => <span className="font-mono">{r.effective_date.slice(0, 10)}</span> },
    { key: 'source', header: '来源', render: (r) => <span className="text-gray-500">{r.source}</span> },
  ];

  return (
    <div>
      <PageHeader
        title="价格源 & 汇率"
        description="价格源决定上游价格从哪里来、可信度多高；汇率用于把非人民币成本价折算成人民币来计算毛利"
      />

      <section className="mb-10">
        <SectionTitle
          actions={
            <>
              <Select
                value={params.enabled ?? ''}
                placeholder="全部状态"
                options={[
                  { value: 'true', label: '已启用' },
                  { value: 'false', label: '已停用' },
                ]}
                onChange={(e) => setParams({ enabled: e.target.value })}
              />
              <Button size="sm" icon={<Plus className="w-3.5 h-3.5" />} onClick={() => setCreatingSource(true)}>
                新增价格源
              </Button>
            </>
          }
        >
          价格源
        </SectionTitle>
        <DataState loading={sources.loading} error={sources.error} onRetry={sources.reload}>
          {sources.data && <PriceSourcesTable sources={sources.data.data} showProvider onChanged={() => setSourcesKey((k) => k + 1)} />}
        </DataState>
      </section>

      <section>
        <SectionTitle
          actions={
            <Button size="sm" variant="primary" icon={<Plus className="w-3.5 h-3.5" />} onClick={() => setSettingFx({ quote: 'CNY' })}>
              设置汇率
            </Button>
          }
        >
          汇率
        </SectionTitle>

        <DataState
          loading={latest.loading}
          error={latest.error}
          onRetry={latest.reload}
          empty={latest.data?.data.length === 0}
          emptyTitle="还没有汇率"
          emptyDescription="成本价是 USD 等外币时，必须配置对人民币的汇率才能计算毛利"
          skeleton="cards"
        >
          <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 gap-3 mb-5">
            {latest.data?.data.map((r) => (
              <button
                key={`${r.base}-${r.quote}`}
                type="button"
                onClick={() => setParams({ fx_base: params.fx_base === r.base ? null : r.base })}
                className={`text-left rounded-xl p-4 border transition-colors cursor-pointer ${
                  params.fx_base === r.base ? 'bg-purple-50/60 border-purple-200' : 'bg-gray-50 border-gray-200 hover:border-purple-200'
                }`}
              >
                <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold">
                  1 {r.base} = ? {r.quote}
                </div>
                <div className="text-2xl font-bold font-mono text-gray-900 mt-1">{r.rate}</div>
                <div className="text-[11px] text-gray-500 mt-1">
                  {r.effective_date.slice(0, 10)} 起 · {r.source}
                </div>
                <span
                  role="button"
                  tabIndex={0}
                  className="inline-block mt-2 text-[11px] text-purple-600 hover:underline"
                  onClick={(e) => {
                    e.stopPropagation();
                    setSettingFx({ base: r.base, quote: r.quote, rate: r.rate });
                  }}
                  onKeyDown={(e) => e.key === 'Enter' && setSettingFx({ base: r.base, quote: r.quote, rate: r.rate })}
                >
                  更新汇率
                </span>
              </button>
            ))}
          </div>
        </DataState>

        <div className="flex items-center justify-between mb-2">
          <h3 className="text-xs font-medium text-gray-700">
            历史记录{params.fx_base ? `（${params.fx_base}）` : '（全部币种）'}
          </h3>
          {params.fx_base && (
            <button type="button" className="text-xs text-purple-600 hover:text-purple-700 cursor-pointer" onClick={() => setParams({ fx_base: null })}>
              查看全部币种
            </button>
          )}
        </div>
        <DataState loading={history.loading} error={history.error} onRetry={history.reload}>
          {history.data && (
            <DataTable columns={historyColumns} rows={history.data.data} rowKey={(r) => `${r.base}-${r.quote}-${r.effective_date}`} empty="暂无记录" />
          )}
        </DataState>
      </section>

      <CreateSourceModal open={creatingSource} onClose={() => setCreatingSource(false)} onSaved={() => setSourcesKey((k) => k + 1)} />
      <FXRateModal
        initial={settingFx}
        onClose={() => setSettingFx(null)}
        onSaved={() => setFxKey((k) => k + 1)}
      />
    </div>
  );
}

// 设置汇率：同一 (base, quote, 日期) 是 upsert（同一天写错了可以直接改）。
// 提交前二次确认——汇率影响该币种所有成本价的折算与毛利。
export function FXRateModal({ initial, onClose, onSaved }: { initial: Partial<FXRate> | null; onClose: () => void; onSaved: () => void }) {
  const toast = useToast();
  const [base, setBase] = useState('');
  const [quote, setQuote] = useState('CNY');
  const [rate, setRate] = useState('');
  const [date, setDate] = useState('');
  const [source, setSource] = useState('manual');
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [lastInit, setLastInit] = useState<Partial<FXRate> | null>(null);

  if (initial !== lastInit) {
    setLastInit(initial);
    if (initial) {
      setBase(initial.base ?? '');
      setQuote(initial.quote ?? 'CNY');
      setRate(initial.rate ?? '');
      setDate(new Date().toISOString().slice(0, 10));
      setSource('manual');
      setError(null);
    }
  }

  const rateNum = Number(rate);
  const valid = /^[A-Za-z]{3,8}$/.test(base.trim()) && /^[A-Za-z]{3,8}$/.test(quote.trim()) && rateNum > 0 && !!date;

  const save = async () => {
    setBusy(true);
    setError(null);
    try {
      await setFXRate({
        base: base.trim().toUpperCase(),
        quote: quote.trim().toUpperCase(),
        rate: rate.trim(),
        source: source.trim() || 'manual',
        effective_date: `${date}T00:00:00Z`,
      });
      toast.success(`已设置 1 ${base.toUpperCase()} = ${rate} ${quote.toUpperCase()}（${date} 起）`);
      setConfirming(false);
      onSaved();
      onClose();
    } catch (err) {
      setConfirming(false);
      setError(describeError(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <FormModal
        open={!!initial && !confirming}
        onClose={onClose}
        title="设置汇率"
        description="同一币种对同一天重复设置会覆盖当天的值"
        onSubmit={() => setConfirming(true)}
        submitDisabled={!valid}
        submitLabel="下一步"
        error={error}
      >
        <div className="grid grid-cols-2 gap-3">
          <Field label="原币种 base" required>
            <Input mono value={base} onChange={(e) => setBase(e.target.value.toUpperCase())} placeholder="USD" autoFocus />
          </Field>
          <Field label="目标币种 quote" required hint="平台以人民币结算">
            <Input mono value={quote} onChange={(e) => setQuote(e.target.value.toUpperCase())} />
          </Field>
        </div>
        <Field label="汇率" required hint={base && quote ? `1 ${base} = ${rate || '?'} ${quote}` : undefined} error={rate && !(rateNum > 0) ? '必须是大于 0 的数字' : undefined}>
          <Input mono value={rate} onChange={(e) => setRate(e.target.value)} placeholder="7.2" />
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="生效日期" required>
            <Input type="date" value={date} onChange={(e) => setDate(e.target.value)} />
          </Field>
          <Field label="来源">
            <Input value={source} onChange={(e) => setSource(e.target.value)} />
          </Field>
        </div>
      </FormModal>
      <ConfirmDialog
        open={!!initial && confirming}
        onClose={() => setConfirming(false)}
        onConfirm={save}
        loading={busy}
        level="danger"
        confirmLabel="确认设置"
        title={`设置 1 ${base} = ${rate} ${quote}？`}
      >
        <p>
          汇率会影响所有以 <span className="font-mono font-semibold">{base}</span> 计价的成本价的人民币折算、毛利计算和调价影响评估，
          {date} 起生效（网关约 10 秒内刷新）。
        </p>
      </ConfirmDialog>
    </>
  );
}
