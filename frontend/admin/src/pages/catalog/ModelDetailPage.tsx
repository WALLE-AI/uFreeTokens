import { ActionMenu, ChipToggleGroup, HeroStat, InfoGrid, Section, StickyActionBar } from '../../components/ui/index';
import { useMemo, useState } from 'react';
import { useParams } from 'react-router';
import { Info } from 'lucide-react';
import { getVirtualModel, listSellPriceBooks, setSellPrice, updateVirtualModel } from '../../api/catalog';
import { errorMessage } from '../../api/errors';
import { AuditTimeline } from '../../components/audit/AuditTimeline';
import { PriceComponentEditor } from '../../components/pricing/PriceComponentEditor';
import { UsageTrend } from '../../components/stats/UsageTrend';
import { AnchorNav, Button, ConfirmDialog, DataState, EmptyState, Field, FormModal, Input, Select, StatusBadge, useToast } from '../../components/ui';
import { useAsync } from '../../hooks/useAsync';
import { formatDateTime } from '../../lib/time';
import type { ModelStatus, ModelType, Tier, VirtualModelDetail } from '../../types';
import { ModelChannelsSection } from './ModelChannelsSection';
import { ModelMetadataSection } from './ModelMetadataSection';
import { PriceBookHistory, PriceComponentsTable } from './PriceBookViews';
import { MODEL_TYPE_OPTIONS, MarginText, TIER_OPTIONS, TagInput, formatContext, formatPrice } from './shared';
import { Can } from '../../components/ui/Can';

// 虚拟模型详情（UI_DESIGN.md §3.2 详情模板 + §5.4）。

const ANCHORS = [
  { id: 'basic', label: '基本信息' },
  { id: 'usage', label: '用量趋势' },
  { id: 'price', label: '售价' },
  { id: 'channels', label: '渠道' },
  { id: 'metadata', label: '展示元数据' },
  { id: 'audit', label: '操作记录' },
];

const STATUS_CHANGE_HINT: Record<ModelStatus, string> = {
  active: '模型将重新出现在公开模型库（对其可见 tier）并可被调用。',
  hidden: '模型将从公开模型库消失；已接入的用户仍可按模型名调用。',
  deprecated: '模型在公开模型库中标记为"已废弃"；已接入的用户仍可调用。',
};
const STATUS_LABEL: Record<ModelStatus, string> = { active: '已上架', hidden: '未公开', deprecated: '已废弃' };

export default function ModelDetailPage() {
  const id = Number(useParams().id);
  const toast = useToast();
  const [auditKey, setAuditKey] = useState(0);
  const detail = useAsync((signal) => getVirtualModel(id, signal), [id]);
  const books = useAsync((signal) => listSellPriceBooks(id, 20, signal), [id]);
  const refresh = () => {
    detail.reload();
    books.reload();
    setAuditKey((k) => k + 1);
  };

  const [statusTarget, setStatusTarget] = useState<ModelStatus | null>(null);
  const [statusSaving, setStatusSaving] = useState(false);
  const [editingBasic, setEditingBasic] = useState(false);
  const [editingPrice, setEditingPrice] = useState(false);
  const [addingChannel, setAddingChannel] = useState(false);

  const m = detail.data;

  // 售价编辑器的对比基准：各 active 渠道中最高的 CNY 成本（含汇率与倍率）
  const costRef = useMemo(() => {
    if (!m) return null;
    let input: number | null = null;
    let output: number | null = null;
    for (const c of m.channels) {
      if (c.status !== 'active' || !c.cost_price_cny || c.cost_price_cny.fx_missing) continue;
      if (c.cost_price_cny.input !== null) input = Math.max(input ?? 0, Number(c.cost_price_cny.input));
      if (c.cost_price_cny.output !== null) output = Math.max(output ?? 0, Number(c.cost_price_cny.output));
    }
    return { input, output, label: '各 active 渠道中最高的成本（CNY，含汇率与倍率）' };
  }, [m]);

  const changeStatus = async () => {
    if (!statusTarget) return;
    setStatusSaving(true);
    try {
      await updateVirtualModel(id, { status: statusTarget });
      toast.success(`已设为「${STATUS_LABEL[statusTarget]}」`);
      setStatusTarget(null);
      refresh();
    } catch (err) {
      toast.error('修改状态失败', errorMessage(err));
    } finally {
      setStatusSaving(false);
    }
  };

  return (
    <DataState loading={detail.loading} error={detail.error} onRetry={detail.reload} skeleton="cards">
      {m && (
        <div>
          <StickyActionBar
            backTo="/models"
            backLabel="虚拟模型"
            title={m.name}
            badges={
              <>
                <StatusBadge kind="virtual_model" value={m.status} />
                {detail.refreshing && <span className="text-[11px] text-gray-400">刷新中…</span>}
              </>
            }
            actions={
              <>
                <Can perm="catalog:write"><Button onClick={() => setEditingBasic(true)}>编辑基本信息</Button></Can>
                <Can perm="pricing:write"><Button variant="primary" onClick={() => setEditingPrice(true)}>
                  调整售价
                </Button></Can>
                <ActionMenu
                  items={(['active', 'hidden', 'deprecated'] as ModelStatus[]).map((s) => ({
                    label: `设为「${STATUS_LABEL[s]}」`,
                    disabled: m.status === s,
                    danger: s !== 'active',
                    onClick: () => setStatusTarget(s),
                  }))}
                />
              </>
            }
          />

          <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 mb-8">
            <HeroStat
              label="售价 ¥/1M（入 / 出）"
              value={m.sell_price ? `${formatPrice(m.sell_price.input)} / ${formatPrice(m.sell_price.output)}` : <span className="text-amber-700 text-sm">未设置</span>}
              sub={m.sell_price ? `生效于 ${formatDateTime(m.sell_price.effective_from).slice(0, 16)}` : '无售价无法计费'}
            />
            <HeroStat label="最低毛利率" value={<MarginText ratio={m.min_margin_ratio} />} sub="各 active 渠道中最低" />
            <HeroStat
              label="渠道"
              value={
                <span className={m.active_channel_count === 0 ? 'text-rose-600' : ''}>
                  {m.active_channel_count}
                  <span className="text-gray-300 text-sm">/{m.channel_count}</span>
                </span>
              }
              sub="active / 全部"
            />
            <HeroStat label="上下文" value={formatContext(m.context_window)} sub={`最大输出 ${formatContext(m.max_output)}`} />
          </div>

          <div className="flex gap-8">
            <AnchorNav items={ANCHORS} />
            <div className="flex-1 min-w-0 space-y-10">
              <Section id="basic" title="基本信息" actions={<Can perm="catalog:write"><Button size="sm" onClick={() => setEditingBasic(true)}>编辑</Button></Can>}>
                <div className="bg-white border border-gray-200 rounded-xl p-5 shadow-xs">
                  <InfoGrid
                    items={[
                      { label: '模型 ID', value: <span className="font-mono">{m.name}</span> },
                      { label: '系列', value: m.family },
                      { label: '类型', value: m.type },
                      { label: '上下文窗口', value: <span className="font-mono">{m.context_window.toLocaleString('en-US')}</span> },
                      { label: '最大输出', value: <span className="font-mono">{m.max_output.toLocaleString('en-US')}</span> },
                      { label: '可见 tier', value: m.visible_tiers.join('、') || '—' },
                      { label: '能力', value: m.capabilities.length ? m.capabilities.join('、') : '—' },
                      { label: '别名', value: m.aliases.length ? <span className="font-mono">{m.aliases.join('、')}</span> : '—' },
                    ]}
                  />
                </div>
              </Section>

              <Section id="usage" title="用量趋势">
                <UsageTrend filter={{ virtual_model: m.name }} defaultMetric="revenue" />
              </Section>

              <Section id="price" title="售价" actions={<Can perm="pricing:write"><Button size="sm" variant="primary" onClick={() => setEditingPrice(true)}>调整售价</Button></Can>}>
                <div className="space-y-3">
                  <div className="bg-blue-50 border border-blue-200 text-blue-700 rounded-xl p-3 text-xs flex items-start gap-2">
                    <Info className="w-4 h-4 shrink-0" />
                    运行时每个模型只有一个生效售价，不区分 tier（free / pro / enterprise 用户按同一价格计费）。历史版本上的 tier 字段仅作记录。
                  </div>
                  {m.sell_price_book ? (
                    <>
                      <div className="text-[11px] text-gray-500">
                        当前生效版本 <span className="font-mono">#{m.sell_price_book.id}</span>，生效于 {formatDateTime(m.sell_price_book.effective_from)}
                      </div>
                      <PriceComponentsTable components={m.sell_price_book.components} currency={m.sell_price_book.currency} />
                    </>
                  ) : (
                    <EmptyState title="尚未设置售价" description="没有生效售价的模型无法计费" action={<Can perm="pricing:write"><Button variant="primary" onClick={() => setEditingPrice(true)}>设置售价</Button></Can>} />
                  )}
                  <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold pt-2">历史版本</div>
                  <DataState loading={books.loading} error={books.error} onRetry={books.reload} skeleton="text">
                    <PriceBookHistory books={books.data?.data ?? []} />
                  </DataState>
                </div>
              </Section>

              <Section id="channels" title="渠道" actions={<Can perm="catalog:write"><Button size="sm" onClick={() => setAddingChannel(true)}>添加渠道</Button></Can>}>
                <ModelChannelsSection model={m} onChanged={refresh} adding={addingChannel} setAdding={setAddingChannel} />
              </Section>

              <Section id="metadata" title="展示元数据">
                <ModelMetadataSection model={m} onSaved={refresh} />
              </Section>

              <Section id="audit" title="操作记录">
                <AuditTimeline targetType="virtual_model" targetId={m.id} reloadKey={auditKey} />
              </Section>
            </div>
          </div>

          <ConfirmDialog
            open={statusTarget !== null}
            onClose={() => setStatusTarget(null)}
            onConfirm={changeStatus}
            loading={statusSaving}
            level={statusTarget === 'active' ? 'normal' : 'danger'}
            title={statusTarget ? `将模型设为「${STATUS_LABEL[statusTarget]}」？` : ''}
            confirmLabel="确认修改"
          >
            {statusTarget && (
              <>
                <p>{STATUS_CHANGE_HINT[statusTarget]}</p>
                <p className="text-[11px] text-gray-500">gateway 目录快照约 10 秒内刷新，公开模型库另有约 60 秒缓存。</p>
              </>
            )}
          </ConfirmDialog>

          <EditBasicModal open={editingBasic} onClose={() => setEditingBasic(false)} model={m} onSaved={refresh} />

          <PriceComponentEditor
            open={editingPrice}
            onClose={() => setEditingPrice(false)}
            kind="sell"
            title="调整售价"
            description={
              <>
                <span className="font-mono">{m.name}</span> · 发布后立即对所有用户生效
              </>
            }
            current={m.sell_price_book?.components ?? []}
            currency="CNY"
            reference={costRef}
            onSubmit={async (_currency, components) => {
              const res = await setSellPrice(id, components);
              toast.success(`售价已发布（版本 #${res.price_book_id}）`);
              refresh();
            }}
          />
        </div>
      )}
    </DataState>
  );
}

function EditBasicModal({ open, onClose, model, onSaved }: { open: boolean; onClose: () => void; model: VirtualModelDetail; onSaved: () => void }) {
  const toast = useToast();
  const [tiers, setTiers] = useState<Tier[]>(model.visible_tiers);
  const [caps, setCaps] = useState<string[]>(model.capabilities);
  const [type, setType] = useState<string>(model.type);
  const [aliases, setAliases] = useState<string[]>(model.aliases);
  const [ctx, setCtx] = useState(String(model.context_window));
  const [maxOut, setMaxOut] = useState(String(model.max_output));
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // 打开时用最新数据重置
  const [lastOpen, setLastOpen] = useState(false);
  if (open && !lastOpen) {
    setLastOpen(true);
    setTiers(model.visible_tiers);
    setCaps(model.capabilities);
    setType(model.type);
    setAliases(model.aliases);
    setCtx(String(model.context_window));
    setMaxOut(String(model.max_output));
    setError(null);
  } else if (!open && lastOpen) {
    setLastOpen(false);
  }

  const posInt = (v: string) => (/^\d+$/.test(v.trim()) && Number(v) > 0 ? undefined : '需为正整数');
  const ctxErr = posInt(ctx);
  const maxErr = posInt(maxOut);

  const submit = async () => {
    const body: Parameters<typeof updateVirtualModel>[1] = {};
    const same = (a: string[], b: string[]) => a.length === b.length && a.every((x, i) => x === b[i]);
    if (!same(tiers, model.visible_tiers)) body.visible_tiers = tiers;
    if (!same(caps, model.capabilities)) body.capabilities = caps;
    if (type !== model.type) body.type = type as ModelType;
    if (!same(aliases, model.aliases)) body.aliases = aliases;
    if (Number(ctx) !== model.context_window) body.context_window = Number(ctx);
    if (Number(maxOut) !== model.max_output) body.max_output = Number(maxOut);
    if (Object.keys(body).length === 0) {
      onClose();
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      await updateVirtualModel(model.id, body);
      toast.success('基本信息已更新');
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
      title="编辑基本信息"
      description="模型 ID（name）是对外 API 的模型标识，不可修改"
      width="lg"
      onSubmit={submit}
      submitLabel="保存"
      submitting={submitting}
      submitDisabled={tiers.length === 0 || !!ctxErr || !!maxErr}
      error={error}
    >
      <Field label="可见 tier" required hint="至少选择一个；要让模型对所有人不可见请把状态设为「未公开」" error={tiers.length === 0 ? '至少选择一个 tier' : undefined}>
        <ChipToggleGroup options={TIER_OPTIONS} value={tiers} onChange={setTiers} />
      </Field>
      <div className="grid grid-cols-2 gap-4">
        <Field label="上下文窗口" required error={ctxErr}>
          <Input mono value={ctx} invalid={!!ctxErr} onChange={(e) => setCtx(e.target.value)} />
        </Field>
        <Field label="最大输出" required error={maxErr}>
          <Input mono value={maxOut} invalid={!!maxErr} onChange={(e) => setMaxOut(e.target.value)} />
        </Field>
      </div>
      <Field
        label="类型"
        hint="决定模型可用于哪个接口；改类型后请按新类型发布售价（图像按张、语音合成按百万字符、语音识别按秒）。语音模型需在能力里填 tts 或 asr"
      >
        <Select value={type} onChange={(e) => setType(e.target.value)} options={MODEL_TYPE_OPTIONS} />
      </Field>
      <Field label="能力" hint="例如 stream、tools、vision（可输入图片）、tts（语音合成）、asr（语音识别）；回车或逗号添加">
        <TagInput value={caps} onChange={setCaps} placeholder="添加能力…" />
      </Field>
      <Field label="别名" hint="用户也可以用这些名称调用该模型">
        <TagInput value={aliases} onChange={setAliases} placeholder="添加别名…" />
      </Field>
    </FormModal>
  );
}
