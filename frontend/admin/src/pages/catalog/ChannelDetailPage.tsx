import { ChipToggleGroup, HeroStat, InfoGrid, Section, StickyActionBar } from '../../components/ui/index';
import { useMemo, useState } from 'react';
import { Link, useParams } from 'react-router';
import { AlertTriangle, ArrowRight } from 'lucide-react';
import { getChannel, listLatestFXRates, setCostPrice, updateChannel } from '../../api/catalog';
import { ApiError, errorMessage } from '../../api/errors';
import { AuditTimeline } from '../../components/audit/AuditTimeline';
import { PriceComponentEditor } from '../../components/pricing/PriceComponentEditor';
import { UsageTrend } from '../../components/stats/UsageTrend';
import { AnchorNav, Button, ConfirmDialog, DataState, EmptyState, Field, FormModal, Input, StatusBadge, Textarea, useToast } from '../../components/ui';
import { useAsync } from '../../hooks/useAsync';
import { formatDateTime, formatRelative } from '../../lib/time';
import type { ChannelDetail, PriceObservation, Tier } from '../../types';
import { PriceBookHistory, PriceComponentsTable } from './PriceBookViews';
import { MarginText, TIER_OPTIONS, currencySymbol, formatPrice, parseIdList } from './shared';
import { Can } from '../../components/ui/Can';

// 渠道详情（UI_DESIGN.md §3.2 详情模板）：路由配置、成本价、价格观测、待审调价、操作记录。

const ANCHORS = [
  { id: 'usage', label: '用量趋势' },
  { id: 'routing', label: '路由配置' },
  { id: 'cost', label: '成本价' },
  { id: 'observations', label: '价格观测' },
  { id: 'audit', label: '操作记录' },
];

function isLastChannelError(err: unknown) {
  return err instanceof ApiError && err.status === 409 && /last active channel/i.test(err.detail);
}

export default function ChannelDetailPage() {
  const id = Number(useParams().id);
  const toast = useToast();
  const [auditKey, setAuditKey] = useState(0);
  const detail = useAsync((signal) => getChannel(id, signal), [id]);
  const fx = useAsync((signal) => listLatestFXRates(signal), []);
  const refresh = () => {
    detail.reload();
    setAuditKey((k) => k + 1);
  };

  const [statusConfirm, setStatusConfirm] = useState<'enable' | 'disable' | 'force' | null>(null);
  const [statusSaving, setStatusSaving] = useState(false);
  const [editingRouting, setEditingRouting] = useState(false);
  const [editingCost, setEditingCost] = useState(false);

  const c = detail.data;

  const fxMap = useMemo(() => {
    const m = new Map<string, number>();
    for (const r of fx.data?.data ?? []) if (r.quote === 'CNY') m.set(r.base, Number(r.rate));
    return m;
  }, [fx.data]);

  const currentCostBook = c?.cost_price_history.find((b) => b.is_current) ?? null;

  const setStatus = async (status: 'active' | 'disabled', force = false) => {
    setStatusSaving(true);
    try {
      await updateChannel(id, { status, force: force || undefined });
      toast.success(status === 'active' ? '渠道已启用' : '渠道已停用');
      setStatusConfirm(null);
      refresh();
    } catch (err) {
      if (status === 'disabled' && !force && isLastChannelError(err)) {
        setStatusConfirm('force');
      } else {
        toast.error('修改状态失败', errorMessage(err));
        setStatusConfirm(null);
      }
    } finally {
      setStatusSaving(false);
    }
  };

  return (
    <DataState loading={detail.loading} error={detail.error} onRetry={detail.reload} skeleton="cards">
      {c && (
        <div>
          <StickyActionBar
            backTo="/channels"
            backLabel="渠道"
            title={`#${c.id} ${c.provider_account_name} / ${c.upstream_model}`}
            badges={
              <>
                <StatusBadge kind="channel" value={c.status} />
                {c.allowed_account_ids && c.allowed_account_ids.length > 0 && (
                  <span className="px-2 py-0.5 rounded-full text-[11px] border bg-blue-50 text-blue-700 border-blue-200">专属</span>
                )}
              </>
            }
            actions={
              <>
                {c.status === 'active' ? (
                  <Can perm="catalog:write"><Button onClick={() => setStatusConfirm('disable')}>停用渠道</Button></Can>
                ) : (
                  <Button onClick={() => setStatusConfirm('enable')}>启用渠道</Button>
                )}
                <Can perm="catalog:write"><Button onClick={() => setEditingRouting(true)}>编辑路由</Button></Can>
                <Can perm="pricing:write"><Button variant="primary" onClick={() => setEditingCost(true)}>
                  发布新成本价
                </Button></Can>
              </>
            }
          />

          <div className="text-xs text-gray-500 mb-4 flex flex-wrap items-center gap-2">
            <span>虚拟模型</span>
            <Link to={`/models/${c.virtual_model_id}`} className="font-mono text-gray-900 hover:text-purple-600">
              {c.virtual_model_name}
            </Link>
            <ArrowRight className="w-3 h-3 text-gray-300" />
            <Link to={`/providers/${c.provider_id}`} className="text-gray-900 hover:text-purple-600">
              {c.provider_code}
            </Link>
            <span className="text-gray-300">/</span>
            <span className="text-gray-900">{c.provider_account_name}</span>
            <span className="text-gray-300">/</span>
            <span className="font-mono text-gray-900">{c.upstream_model}</span>
          </div>

          {c.pending_change_request_id && (
            <div className="bg-purple-50/60 border border-purple-100 rounded-xl p-3 mb-6 flex items-center gap-2 text-xs text-purple-700">
              <AlertTriangle className="w-4 h-4" />
              该渠道有一条待审批的调价申请
              <Link to={`/pricing/changes?id=${c.pending_change_request_id}`} className="ml-auto font-medium hover:underline">
                去审批 #{c.pending_change_request_id} →
              </Link>
            </div>
          )}

          <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 mb-8">
            <HeroStat
              label="成本 ¥/1M（入 / 出）"
              value={
                !c.cost_price_cny ? (
                  <span className="text-amber-700 text-sm">未设置</span>
                ) : c.cost_price_cny.fx_missing ? (
                  <span className="text-amber-700 text-sm">缺汇率</span>
                ) : (
                  `${formatPrice(c.cost_price_cny.input)} / ${formatPrice(c.cost_price_cny.output)}`
                )
              }
              sub={c.cost_price ? `原币种 ${c.cost_price.currency} × 倍率 ${c.cost_multiplier}` : '无法计算毛利'}
            />
            <HeroStat
              label="售价 ¥/1M（入 / 出）"
              value={c.sell_price ? `${formatPrice(c.sell_price.input)} / ${formatPrice(c.sell_price.output)}` : <span className="text-amber-700 text-sm">未设置</span>}
              sub="虚拟模型当前售价"
            />
            <HeroStat label="毛利率" value={<MarginText ratio={c.margin_ratio} />} sub="输入 / 输出中较低者" />
            <HeroStat label="优先级 / 权重" value={`${c.priority} / ${c.weight}`} sub="数字越小越优先" />
          </div>

          <div className="flex gap-8">
            <AnchorNav items={ANCHORS} />
            <div className="flex-1 min-w-0 space-y-10">
              <Section id="usage" title="用量趋势">
                <UsageTrend filter={{ channel_id: c.id }} defaultMetric="requests" />
              </Section>

              <Section id="routing" title="路由配置" actions={<Can perm="catalog:write"><Button size="sm" onClick={() => setEditingRouting(true)}>编辑</Button></Can>}>
                <div className="bg-white border border-gray-200 rounded-xl p-5 shadow-xs">
                  <InfoGrid
                    items={[
                      { label: '优先级', value: <span className="font-mono">{c.priority}</span> },
                      { label: '权重', value: <span className="font-mono">{c.weight}</span> },
                      { label: '允许的 tier', value: c.allowed_tiers?.length ? c.allowed_tiers.join('、') : <span className="text-gray-400">不限</span> },
                      {
                        label: '专属账户',
                        value: c.allowed_account_ids?.length ? (
                          <span className="font-mono">
                            {c.allowed_account_ids.map((a, i) => (
                              <span key={a}>
                                {i > 0 && '、'}
                                <Link to={`/accounts/${a}`} className="hover:text-purple-600">
                                  #{a}
                                </Link>
                              </span>
                            ))}
                          </span>
                        ) : (
                          <span className="text-gray-400">公共渠道</span>
                        ),
                      },
                      { label: '实验分组', value: c.experiment_key ? `${c.experiment_key} / ${c.variant_label ?? '—'}` : <span className="text-gray-400">—</span> },
                      { label: '成本倍率', value: <span className="font-mono">×{c.cost_multiplier}</span> },
                    ]}
                  />
                </div>
              </Section>

              <Section id="cost" title="成本价" actions={<Can perm="pricing:write"><Button size="sm" variant="primary" onClick={() => setEditingCost(true)}>发布新成本价</Button></Can>}>
                <div className="space-y-3">
                  {currentCostBook ? (
                    <>
                      <CostExplanation channel={c} />
                      <PriceComponentsTable components={currentCostBook.components} currency={currentCostBook.currency} />
                    </>
                  ) : (
                    <EmptyState
                      title="尚未设置成本价"
                      description="没有成本价就无法计算毛利，调价审批也没有对比基线"
                      action={<Can perm="pricing:write"><Button variant="primary" onClick={() => setEditingCost(true)}>设置成本价</Button></Can>}
                    />
                  )}
                  <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold pt-2">历史版本</div>
                  <PriceBookHistory books={c.cost_price_history} />
                </div>
              </Section>

              <Section id="observations" title="最近价格观测">
                <ObservationsTable observations={c.recent_observations} />
              </Section>

              <Section id="audit" title="操作记录">
                <AuditTimeline targetType="channel" targetId={c.id} reloadKey={auditKey} />
              </Section>
            </div>
          </div>

          <ConfirmDialog
            open={statusConfirm === 'enable' || statusConfirm === 'disable'}
            onClose={() => setStatusConfirm(null)}
            onConfirm={() => setStatus(statusConfirm === 'enable' ? 'active' : 'disabled')}
            loading={statusSaving}
            level={statusConfirm === 'disable' ? 'danger' : 'normal'}
            title={statusConfirm === 'disable' ? '停用该渠道？' : '启用该渠道？'}
            confirmLabel={statusConfirm === 'disable' ? '停用' : '启用'}
          >
            {statusConfirm === 'disable'
              ? '停用后请求不再路由到这个上游，同优先级的其他渠道会承接其流量。'
              : '启用后该渠道会按优先级与权重重新参与路由。'}
          </ConfirmDialog>

          <ConfirmDialog
            open={statusConfirm === 'force'}
            onClose={() => setStatusConfirm(null)}
            onConfirm={() => setStatus('disabled', true)}
            loading={statusSaving}
            level="typed"
            confirmText={c.virtual_model_name}
            title="这是该模型最后一个可用渠道"
            confirmLabel="仍然停用"
          >
            <p>
              停用后 <span className="font-mono text-gray-900">{c.virtual_model_name}</span> 将没有任何 active 渠道，所有调用都会失败。
            </p>
          </ConfirmDialog>

          <EditRoutingModal open={editingRouting} onClose={() => setEditingRouting(false)} channel={c} onSaved={refresh} />

          <PriceComponentEditor
            open={editingCost}
            onClose={() => setEditingCost(false)}
            kind="cost"
            title="发布新成本价"
            description={
              <>
                渠道 #{c.id} · {c.provider_account_name}/{c.upstream_model} · 上游标价（倍率 ×{c.cost_multiplier} 在计算时另乘）
              </>
            }
            current={currentCostBook?.components ?? []}
            currency={currentCostBook?.currency ?? 'USD'}
            currencyOptions={Array.from(new Set(['CNY', 'USD', ...fxMap.keys(), ...(currentCostBook ? [currentCostBook.currency] : [])]))}
            reference={c.sell_price ? { input: numOrNull(c.sell_price.input), output: numOrNull(c.sell_price.output), label: '当前售价（CNY）' } : null}
            toCNY={(price, cur) => {
              const mult = Number(c.cost_multiplier);
              if (cur === 'CNY') return price * mult;
              const rate = fxMap.get(cur);
              return rate === undefined ? null : price * rate * mult;
            }}
            onSubmit={async (currency, components) => {
              const res = await setCostPrice(id, currency, components);
              toast.success(`成本价已发布（版本 #${res.price_book_id}）`);
              refresh();
            }}
          />
        </div>
      )}
    </DataState>
  );
}

function numOrNull(v: string | null) {
  return v === null ? null : Number(v);
}

function CostExplanation({ channel: c }: { channel: ChannelDetail }) {
  const cp = c.cost_price;
  const cny = c.cost_price_cny;
  if (!cp || !cny) return null;
  const sym = currencySymbol(cp.currency);
  return (
    <div className="bg-gray-50 border border-gray-200 rounded-xl p-3 text-xs text-gray-600 space-y-1">
      <div>
        当前生效版本 <span className="font-mono">#{cp.price_book_id}</span>，生效于 {formatDateTime(cp.effective_from)}
      </div>
      {cny.fx_missing ? (
        <div className="text-amber-700">缺少 {cp.currency}→CNY 汇率，无法折算人民币成本与毛利。请到「价格源 & 汇率」补录。</div>
      ) : (
        <div className="font-mono text-[11px]">
          输入 {sym}
          {formatPrice(cp.input)} × 汇率 {cny.fx_rate} × 倍率 {c.cost_multiplier} = ¥{formatPrice(cny.input)}
          <span className="text-gray-300 mx-2">|</span>
          输出 {sym}
          {formatPrice(cp.output)} × {cny.fx_rate} × {c.cost_multiplier} = ¥{formatPrice(cny.output)}
          {cny.fx_date && <span className="text-gray-400 ml-2 font-sans">（汇率日期 {cny.fx_date.slice(0, 10)}）</span>}
        </div>
      )}
    </div>
  );
}

// price_observations.spec 是 pricesync.PriceSpec 的原始 JSON（PascalCase 键，接口方案 §0.1）
interface RawSpec {
  Currency?: string;
  Components?: Array<{ Meter?: string; Unit?: string; ServiceTier?: string; TierMinInput?: number; UnitPrice?: string | number }>;
}

function ObservationsTable({ observations }: { observations: PriceObservation[] }) {
  if (observations.length === 0) {
    return <EmptyState title="暂无价格观测" description="价格源抓取到该上游模型的价格后会出现在这里" />;
  }
  return (
    <div className="overflow-x-auto bg-white border border-gray-200 rounded-xl shadow-xs">
      <table className="w-full text-xs">
        <thead>
          <tr className="bg-gray-50 border-b border-gray-200 text-[10px] text-gray-400 uppercase tracking-wider font-semibold">
            <th className="px-4 py-2.5 text-left">观测时间</th>
            <th className="px-4 py-2.5 text-left">来源</th>
            <th className="px-4 py-2.5 text-left">价格</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-gray-100">
          {observations.map((o) => {
            const spec = (o.spec ?? {}) as RawSpec;
            const sym = currencySymbol(spec.Currency);
            return (
              <tr key={o.id}>
                <td className="px-4 py-2.5 text-gray-600 whitespace-nowrap" title={formatDateTime(o.observed_at)}>
                  {formatRelative(o.observed_at)}
                </td>
                <td className="px-4 py-2.5 whitespace-nowrap">
                  <span className="font-mono text-gray-900">{o.source_level}</span>
                  <span className="text-gray-400 ml-1">
                    {o.source_kind} · 源 #{o.source_id}
                  </span>
                </td>
                <td className="px-4 py-2.5">
                  <div className="flex flex-wrap gap-x-4 gap-y-0.5 font-mono">
                    {(spec.Components ?? []).map((comp, i) => (
                      <span key={i}>
                        <span className="text-gray-400">{comp.Meter}</span>
                        {comp.ServiceTier && comp.ServiceTier !== 'default' && <span className="text-gray-400">[{comp.ServiceTier}]</span>}
                        {comp.TierMinInput ? <span className="text-gray-400">≥{comp.TierMinInput}</span> : null} {sym}
                        {formatPrice(comp.UnitPrice ?? null)}
                      </span>
                    ))}
                    {!spec.Components?.length && <span className="text-gray-400">（无价格组件）</span>}
                  </div>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function EditRoutingModal({ open, onClose, channel, onSaved }: { open: boolean; onClose: () => void; channel: ChannelDetail; onSaved: () => void }) {
  const toast = useToast();
  const [priority, setPriority] = useState('');
  const [weight, setWeight] = useState('');
  const [tiers, setTiers] = useState<Tier[]>([]);
  const [accounts, setAccounts] = useState('');
  const [overrides, setOverrides] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const [lastOpen, setLastOpen] = useState(false);
  if (open && !lastOpen) {
    setLastOpen(true);
    setPriority(String(channel.priority));
    setWeight(String(channel.weight));
    setTiers(channel.allowed_tiers ?? []);
    setAccounts((channel.allowed_account_ids ?? []).join(', '));
    setOverrides(formatOverrides(channel.param_overrides));
    setError(null);
  } else if (!open && lastOpen) {
    setLastOpen(false);
  }

  const priorityErr = /^-?\d+$/.test(priority.trim()) ? undefined : '需为整数';
  const weightErr = /^\d+$/.test(weight.trim()) ? undefined : '需为 ≥ 0 的整数';
  const accountIds = parseIdList(accounts);
  const accountsErr = accountIds === null ? '账户 ID 需为数字，用逗号或空格分隔' : undefined;
  let overridesObj: Record<string, unknown> | undefined;
  let overridesErr: string | undefined;
  if (!overrides.trim()) {
    overridesObj = {};
  } else {
    try {
      const v = JSON.parse(overrides);
      if (typeof v !== 'object' || v === null || Array.isArray(v)) overridesErr = '需为 JSON 对象，例如 {"temperature": 0.7}';
      else overridesObj = v as Record<string, unknown>;
    } catch {
      overridesErr = 'JSON 格式不正确';
    }
  }

  const submit = async () => {
    const body: Parameters<typeof updateChannel>[1] = {};
    if (Number(priority) !== channel.priority) body.priority = Number(priority);
    if (Number(weight) !== channel.weight) body.weight = Number(weight);
    const curTiers = channel.allowed_tiers ?? [];
    if (tiers.length !== curTiers.length || tiers.some((t) => !curTiers.includes(t))) body.allowed_tiers = tiers;
    const curAcc = channel.allowed_account_ids ?? [];
    if (accountIds && (accountIds.length !== curAcc.length || accountIds.some((a) => !curAcc.includes(a)))) body.allowed_account_ids = accountIds;
    if (overridesObj && formatOverrides(overridesObj) !== formatOverrides(channel.param_overrides)) body.param_overrides = overridesObj;
    if (Object.keys(body).length === 0) {
      onClose();
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      await updateChannel(channel.id, body);
      toast.success('路由配置已更新');
      onClose();
      onSaved();
    } catch (err) {
      setError(errorMessage(err, '保存失败'));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <FormModal
      open={open}
      onClose={onClose}
      title="编辑路由配置"
      width="lg"
      onSubmit={submit}
      submitLabel="保存"
      submitting={submitting}
      submitDisabled={!!priorityErr || !!weightErr || !!accountsErr || !!overridesErr}
      error={error}
    >
      <div className="grid grid-cols-2 gap-4">
        <Field label="优先级" hint="数字越小越优先" error={priorityErr}>
          <Input mono value={priority} invalid={!!priorityErr} onChange={(e) => setPriority(e.target.value)} />
        </Field>
        <Field label="权重" hint="同优先级内按权重分流；0 = 不分配流量" error={weightErr}>
          <Input mono value={weight} invalid={!!weightErr} onChange={(e) => setWeight(e.target.value)} />
        </Field>
      </div>
      <Field label="允许的 tier" hint="都不选 = 不限制">
        <ChipToggleGroup options={TIER_OPTIONS} value={tiers} onChange={setTiers} />
      </Field>
      <Field label="专属账户 ID" hint="留空 = 公共渠道；填写后只有这些账户能路由到该渠道" error={accountsErr}>
        <Input mono value={accounts} invalid={!!accountsErr} onChange={(e) => setAccounts(e.target.value)} placeholder="例如 1234, 5678" />
      </Field>
      <Field label="参数覆盖（param_overrides）" hint="JSON 对象，发往上游前合并进请求体；清空 = 移除全部覆盖" error={overridesErr}>
        <Textarea mono rows={4} value={overrides} invalid={!!overridesErr} onChange={(e) => setOverrides(e.target.value)} placeholder='{"temperature": 0.7}' />
      </Field>
    </FormModal>
  );
}

// formatOverrides 把参数覆盖格式化成稳定的 JSON 文本，用于预填和"是否有改动"的比较；空对象显示为空。
function formatOverrides(v: Record<string, unknown> | null | undefined): string {
  if (!v || Object.keys(v).length === 0) return '';
  return JSON.stringify(v, null, 2);
}
