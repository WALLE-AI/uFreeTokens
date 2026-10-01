import { useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router';
import { AlertTriangle, Gift, Info } from 'lucide-react';
import { listLatestFXRates } from '../../api/catalog';
import { publishListing } from '../../api/pricing';
import { Button, DetailDrawer, Field, Input, Select, useToast } from '../../components/ui';
import { useAsync } from '../../hooks/useAsync';
import { cn } from '../../lib/cn';
import type { ModelType, PendingListing, PublishListingResult, Tier } from '../../types';
import { useEnums } from '../../hooks/useEnums';
import { MarginText, errorDetail, friendlyError, meterLabel, unitLabel } from './shared';
import { listAllProviderAccounts } from '../../api/pickers';

// 待上架模型的"上架…"侧滑表单（UI_DESIGN.md §5.3）。
//
// 售价口径与后端 pricesync.PublishListing 一致：售价(CNY) = 观测单价 × 汇率 ×
// 上游账号 cost_multiplier × (1 + 加价率)，后端四舍五入到 6 位小数。非 CNY 观测
// 缺汇率时后端会拒绝上架（400），这里同样禁止提交。
//
// 虚拟模型名 = 上游原始模型名（不可改）。已有同名虚拟模型在用时，上架只挂一个新渠道、写成本价，售价不变；
// 否则新建虚拟模型并按加价率发布售价。
// 免费模型（观测单价全为 0）售价恒为 0，不需要加价率与汇率；上游免费结束后系统会自动停用渠道。
// 类型 / 上下文 / 最大输出 / 能力按来源给出的模型参数（observed_meta）预填。

const MODEL_TYPES: Array<{ value: ModelType; label: string }> = [
  { value: 'chat', label: 'chat 对话' },
  { value: 'embedding', label: 'embedding 向量' },
  { value: 'image', label: 'image 图像' },
  { value: 'audio', label: 'audio 音频' },
  { value: 'rerank', label: 'rerank 重排' },
];

// 字典取不到时的兜底值；正常情况下来自 GET /meta/enums。
const FALLBACK_CAPABILITIES = ['stream', 'tools', 'vision', 'json_mode', 'reasoning'];
const FALLBACK_TIERS: Tier[] = ['free', 'pro', 'enterprise'];

const META_SOURCE_LABELS: Record<string, string> = {
  openrouter_models: 'OpenRouter',
  modelsdev: 'models.dev',
  litellm_dataset: 'LiteLLM',
};

export function PublishListingDrawer({
  listing,
  onClose,
  onPublished,
}: {
  listing: PendingListing | null;
  onClose: () => void;
  onPublished: (res: PublishListingResult, name: string) => void;
}) {
  const toast = useToast();
  const enums = useEnums();
  const [family, setFamily] = useState('');
  const [type, setType] = useState<ModelType>('chat');
  const [contextWindow, setContextWindow] = useState('');
  const [maxOutput, setMaxOutput] = useState('');
  const [caps, setCaps] = useState<string[]>(['stream']);
  const [tiers, setTiers] = useState<Tier[]>(['free', 'pro', 'enterprise']);
  const [accountId, setAccountId] = useState('');
  const [markupPct, setMarkupPct] = useState(30);
  const [submitting, setSubmitting] = useState(false);
  const [touched, setTouched] = useState(false);

  useEffect(() => {
    if (!listing) return;
    const s = listing.suggested;
    setFamily(s.family);
    setType(s.type || 'chat');
    setContextWindow(s.context_window > 0 ? String(s.context_window) : '');
    setMaxOutput(s.max_output > 0 ? String(s.max_output) : '');
    setCaps(s.capabilities?.length ? s.capabilities : ['stream']);
    setTiers(['free', 'pro', 'enterprise']);
    setAccountId('');
    setMarkupPct(30);
    setTouched(false);
  }, [listing]);

  const providerId = listing?.provider_id;
  const accounts = useAsync(
    (signal) => (providerId ? listAllProviderAccounts(providerId, signal) : Promise.resolve(null)),
    [providerId],
  );
  const fx = useAsync((signal) => listLatestFXRates(signal), []);

  const activeAccounts = (accounts.data?.data ?? []).filter((a) => a.status === 'active');
  useEffect(() => {
    if (!accountId && activeAccounts.length === 1) setAccountId(String(activeAccounts[0].id));
  }, [activeAccounts, accountId]);
  const account = activeAccounts.find((a) => String(a.id) === accountId);

  const currency = listing?.observed_spec.currency || 'CNY';
  const fxRate = useMemo(() => {
    if (currency === 'CNY') return 1;
    const r = fx.data?.data.find((x) => x.base === currency && x.quote === 'CNY');
    return r ? Number(r.rate) : null;
  }, [fx.data, currency]);
  const multiplier = account ? Number(account.cost_multiplier) : 1;
  const free = !!listing?.free;
  // 已有同名、在用的虚拟模型：只挂渠道，不新建、不改售价（deprecated 的会被重新启用并重新定价）
  const attach = !!listing?.existing_virtual_model_id && listing.existing_virtual_model_status !== 'deprecated';
  // 免费模型售价恒为 0，加价率无意义
  const factor = free ? 1 : 1 + markupPct / 100;
  // 挂到已有虚拟模型不发布售价；免费模型售价为 0：两种情况都不需要汇率
  const needFx = !attach && !free;

  // 每个计量项：原币种成本 → CNY 成本（×汇率×倍率）→ 售价（CNY 成本 × (1+加价率)）
  const rows = (listing?.observed_spec.components ?? []).map((c) => {
    const price = Number(c.unit_price);
    const costCNY = price === 0 ? 0 : fxRate === null ? null : price * fxRate * multiplier;
    const sell = costCNY === null ? null : costCNY * factor;
    const margin = costCNY === null || !sell ? null : 1 - costCNY / sell;
    return { c, price, costCNY, sell, margin };
  });
  const worstMargin = rows.reduce<number | null>((acc, r) => (r.margin === null ? acc : acc === null ? r.margin : Math.min(acc, r.margin)), null);
  const negative = !attach && worstMargin !== null && worstMargin < 0;

  const ctx = Number(contextWindow);
  const maxOut = Number(maxOutput);
  const errors = {
    family: !attach && !family.trim() ? '必填' : null,
    context: !attach && !(ctx > 0) ? '请填写正整数（来源没有给出上下文窗口）' : null,
    maxOut: attach ? null : !(maxOut > 0) ? '请填写正整数' : maxOut > ctx && ctx > 0 ? '不能大于上下文窗口' : null,
    account: !accountId ? '请选择上游账号' : null,
    tiers: !attach && tiers.length === 0 ? '至少选择一个可见分组' : null,
  };
  const invalid = Object.values(errors).some(Boolean) || negative || (needFx && fxRate === null);
  const meta = listing?.observed_meta;
  const metaSource = meta?.source ? (META_SOURCE_LABELS[meta.source] ?? meta.source) : null;

  const submit = async () => {
    setTouched(true);
    if (!listing || invalid) return;
    setSubmitting(true);
    try {
      const res = await publishListing(listing.id, {
        virtual_model: {
          family: family.trim(),
          type,
          context_window: Math.trunc(ctx),
          max_output: Math.trunc(maxOut),
          capabilities: caps,
          visible_tiers: tiers,
        },
        provider_account_id: Number(accountId),
        sell_markup: free ? '0' : (markupPct / 100).toFixed(4),
      });
      onPublished(res, listing.upstream_model);
    } catch (err) {
      toast.error(friendlyError(err, '上架失败'), errorDetail(err));
    } finally {
      setSubmitting(false);
    }
  };

  const toggle = <T,>(list: T[], v: T) => (list.includes(v) ? list.filter((x) => x !== v) : [...list, v]);
  const show = (e: string | null) => (touched ? e : null);

  return (
    <DetailDrawer
      open={!!listing}
      onClose={() => !submitting && onClose()}
      title={listing ? `上架 ${listing.upstream_model}` : ''}
      subtitle={listing ? `${listing.provider_name}（${listing.provider_code}）· 来源 ${listing.source_level}` : undefined}
      footer={
        <>
          <Button onClick={onClose} disabled={submitting}>
            取消
          </Button>
          <Button variant="primary" loading={submitting} disabled={touched && invalid} onClick={() => void submit()}>
            确认上架
          </Button>
        </>
      }
    >
      {listing && (
        <div className="space-y-5">
          {free && (
            <div className="bg-emerald-50 border border-emerald-200 rounded-xl p-3 text-xs text-emerald-800 flex gap-2">
              <Gift className="w-4 h-4 shrink-0" />
              <div>
                <b>免费模型</b>：上游成本为 0，新建虚拟模型时售价同为 0，用户可免费调用。
                {listing.origin === 'free_offer' && (
                  <>
                    上游免费结束（
                    {listing.offer_id ? (
                      <Link to={`/pricing/offers?status=all&id=${listing.offer_id}`} className="underline">
                        优惠情报 #{listing.offer_id}
                      </Link>
                    ) : (
                      '优惠情报'
                    )}
                    过期）后，系统会自动停用该渠道，避免继续 0 元售卖已收费的上游。
                  </>
                )}
              </div>
            </div>
          )}

          {attach ? (
            <div className="bg-blue-50 border border-blue-200 rounded-xl p-3 text-xs text-blue-900 flex gap-2">
              <Info className="w-4 h-4 shrink-0" />
              <div>
                已有同名虚拟模型{' '}
                <Link to={`/models/${listing.existing_virtual_model_id}`} className="underline font-mono">
                  {listing.upstream_model}
                </Link>
                ：本次只在它下面新增一个渠道并写入成本价，<b>售价保持不变</b>。
              </div>
            </div>
          ) : (
          <section className="space-y-3">
            <h4 className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold">虚拟模型</h4>
            {metaSource && (
              <p className="text-[11px] text-gray-500 flex items-center gap-1">
                <Info className="w-3.5 h-3.5" /> 类型、上下文、最大输出、能力已按 {metaSource} 给出的参数预填，请核对。
              </p>
            )}
            <Field label="模型名（对外 API 的 model 字段）" hint="固定为上游原始模型名">
              <div className="px-3 py-2 bg-gray-50 border border-gray-200 rounded-lg font-mono text-xs text-gray-700 break-all">{listing.upstream_model}</div>
            </Field>
            <div className="grid grid-cols-2 gap-3">
              <Field label="系列 family" required error={show(errors.family)}>
                <Input value={family} invalid={!!show(errors.family)} onChange={(e) => setFamily(e.target.value)} />
              </Field>
              <Field label="类型">
                <Select className="w-full" value={type} options={MODEL_TYPES} onChange={(e) => setType(e.target.value as ModelType)} />
              </Field>
              <Field label="上下文窗口（tokens）" required error={show(errors.context)}>
                <Input mono inputMode="numeric" placeholder="例如 128000" value={contextWindow} invalid={!!show(errors.context)} onChange={(e) => setContextWindow(e.target.value)} />
              </Field>
              <Field label="最大输出（tokens）" required error={show(errors.maxOut)}>
                <Input mono inputMode="numeric" placeholder="例如 8192" value={maxOutput} invalid={!!show(errors.maxOut)} onChange={(e) => setMaxOutput(e.target.value)} />
              </Field>
            </div>
            <Field label="能力">
              <ChipGroup options={enums?.capabilities ?? FALLBACK_CAPABILITIES} value={caps} onToggle={(v) => setCaps((l) => toggle(l, v))} />
            </Field>
            <Field label="可见分组" error={show(errors.tiers)}>
              <ChipGroup options={(enums?.tiers as Tier[] | undefined) ?? FALLBACK_TIERS} value={tiers} onToggle={(v) => setTiers((l) => toggle(l, v as Tier))} />
            </Field>
          </section>
          )}

          <section className="space-y-3">
            <h4 className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold">路由与定价</h4>
            <Field label="上游账号" required error={show(errors.account)} hint={account ? `成本倍率 ×${account.cost_multiplier}` : undefined}>
              {accounts.data && activeAccounts.length === 0 ? (
                <div className="bg-amber-50/80 border border-amber-200 rounded-xl p-3 text-xs text-amber-900">
                  该供应商还没有可用的上游账号。
                  <Link to={`/providers/${listing.provider_id}`} className="underline ml-1">
                    去供应商详情添加
                  </Link>
                </div>
              ) : (
                <Select
                  className="w-full"
                  value={accountId}
                  placeholder={accounts.loading ? '加载中…' : '选择上游账号'}
                  options={activeAccounts.map((a) => ({ value: String(a.id), label: `${a.name} · ${a.base_url}` }))}
                  onChange={(e) => setAccountId(e.target.value)}
                />
              )}
            </Field>
            {!attach && !free && (
            <Field label={`加价率 ${markupPct}%`} hint="售价 = CNY 成本（观测单价 × 汇率 × 账号倍率）× (1 + 加价率)">
              <input
                type="range"
                min={0}
                max={200}
                step={1}
                value={markupPct}
                onChange={(e) => setMarkupPct(Number(e.target.value))}
                className="purple-track"
                aria-label="加价率"
              />
              <div className="flex justify-between text-[10px] text-gray-400 font-mono mt-1">
                <span>0%</span>
                <span>100%</span>
                <span>200%</span>
              </div>
            </Field>
            )}

            <div className="border border-gray-200 rounded-xl overflow-hidden">
              <table className="w-full text-xs">
                <thead className="bg-gray-50 text-[10px] text-gray-400 uppercase tracking-wider font-semibold">
                  <tr>
                    <th className="px-3 py-2 text-left">计量</th>
                    <th className="px-3 py-2 text-right">观测单价</th>
                    <th className="px-3 py-2 text-right">真实成本 CNY</th>
                    {!attach && <th className="px-3 py-2 text-right">售价 CNY</th>}
                    {!attach && <th className="px-3 py-2 text-right">毛利率</th>}
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100">
                  {rows.map((r, i) => (
                    <tr key={i} className={cn(r.margin !== null && r.margin < 0 && 'bg-rose-50/60')}>
                      <td className="px-3 py-2">
                        {meterLabel(r.c.meter)} <span className="text-[11px] text-gray-400">{unitLabel(r.c.unit)}</span>
                      </td>
                      <td className="px-3 py-2 text-right font-mono text-gray-500">
                        {r.c.unit_price} <span className="text-[10px]">{currency}</span>
                      </td>
                      <td className="px-3 py-2 text-right font-mono">{r.costCNY === null ? '—' : `¥${fmt(r.costCNY)}`}</td>
                      {!attach && <td className="px-3 py-2 text-right font-mono text-gray-900 font-medium">{r.sell === null ? '—' : `¥${fmt(r.sell)}`}</td>}
                      {!attach && (
                        <td className="px-3 py-2 text-right">
                          <MarginText ratio={r.margin} />
                        </td>
                      )}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <p className="text-[11px] text-gray-400">
              真实成本 = 观测单价 × 汇率{currency !== 'CNY' && fxRate !== null ? `（${currency}→CNY ${fxRate}）` : ''} × 账号成本倍率（×{multiplier}）。
            </p>

            {needFx && fxRate === null && !fx.loading && (
              <div className="bg-amber-50/80 border border-amber-200 rounded-xl p-3 text-xs text-amber-900 flex gap-2">
                <Info className="w-4 h-4 shrink-0" />
                <div>
                  缺少 {currency}→CNY 汇率，无法计算真实成本。请先到
                  <Link to="/pricing/sources" className="underline mx-0.5">
                    价格源 & 汇率
                  </Link>
                  设置汇率。
                </div>
              </div>
            )}
            {negative && (
              <div className="bg-rose-50 border border-rose-200 text-rose-700 rounded-xl p-3 text-xs flex gap-2">
                <AlertTriangle className="w-4 h-4 shrink-0" />
                <div>
                  按当前加价率上架将<b>亏损</b>，已禁止提交。
                </div>
              </div>
            )}
          </section>

          {!attach && (
          <section className="space-y-2">
            <h4 className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold">用户在模型库里看到的卡片</h4>
            <ModelCardPreview
              name={listing.upstream_model}
              provider={listing.provider_name}
              contextWindow={ctx > 0 ? ctx : null}
              type={type}
              input={rows.find((r) => r.c.meter === 'input')?.sell ?? null}
              output={rows.find((r) => r.c.meter === 'output')?.sell ?? null}
            />
          </section>
          )}
        </div>
      )}
    </DetailDrawer>
  );
}

function fmt(n: number): string {
  return n >= 100 ? n.toFixed(2) : n >= 1 ? n.toFixed(4).replace(/0+$/, '').replace(/\.$/, '') : n.toPrecision(3);
}

function ChipGroup({ options, value, onToggle }: { options: string[]; value: string[]; onToggle: (v: string) => void }) {
  return (
    <div className="flex flex-wrap gap-1.5">
      {options.map((o) => {
        const on = value.includes(o);
        return (
          <button
            key={o}
            type="button"
            onClick={() => onToggle(o)}
            className={cn(
              'px-2.5 py-1 rounded-full text-[11px] border cursor-pointer',
              on ? 'bg-purple-50 text-purple-700 border-purple-200' : 'bg-white text-gray-500 border-gray-200 hover:border-gray-300',
            )}
          >
            {o}
          </button>
        );
      })}
    </div>
  );
}

// 轻量复刻 frontend/web ModelGridCard 的视觉（§11 一致性），用于上架前预览
function ModelCardPreview({
  name,
  provider,
  contextWindow,
  type,
  input,
  output,
}: {
  name: string;
  provider: string;
  contextWindow: number | null;
  type: string;
  input: number | null;
  output: number | null;
}) {
  const ctxDisplay = contextWindow ? (contextWindow >= 1000 ? `${Math.round(contextWindow / 1000)}K` : String(contextWindow)) : null;
  return (
    <div className="w-64 flex flex-col rounded-xl border border-gray-200 bg-white p-3.5 hover:border-purple-300 hover:shadow-md transition-all">
      <div className="flex items-center gap-1.5">
        <span className="w-5 h-5 rounded bg-purple-100 text-purple-700 text-[10px] font-semibold inline-flex items-center justify-center">
          {provider.slice(0, 1).toUpperCase()}
        </span>
        <span className="text-[11px] text-gray-400 truncate">{provider}</span>
      </div>
      <h3 className="mt-1.5 font-bold text-gray-900 text-xs leading-snug line-clamp-2">{name}</h3>
      <p className="mt-1.5 text-gray-400 text-xxs leading-relaxed line-clamp-3">（上架后可在模型详情页补充介绍文案）</p>
      <div className="flex flex-wrap items-center gap-1.5 mt-2.5 text-xxs text-gray-400">
        {ctxDisplay && <span className="px-1.5 py-0.5 bg-gray-100 text-gray-600 rounded font-medium">{ctxDisplay}</span>}
        <span className="px-1.5 py-0.5 bg-gray-100 text-gray-500 rounded uppercase tracking-wider text-[9px]">{type}</span>
      </div>
      <div className="mt-2 pt-2 border-t border-gray-100 text-[11px] text-gray-700 font-semibold space-y-0.5">
        <div className="truncate">{input !== null ? `¥${fmt(input)} / 百万输入 Token` : '—'}</div>
        {output !== null && <div className="truncate">¥{fmt(output)} / 百万输出 Token</div>}
      </div>
    </div>
  );
}
