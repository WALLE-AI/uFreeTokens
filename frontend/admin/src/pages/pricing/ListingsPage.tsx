import { Checkbox } from '../../components/ui/index';
import { useEffect, useState } from 'react';
import { Link } from 'react-router';
import { ExternalLink, EyeOff, Gift, PackagePlus, RotateCw, Upload } from 'lucide-react';
import { batchDismissListings, listPendingListings } from '../../api/pricing';
import { Button, ConfirmDialog, DataState, Field, PageHeader, Pills, StatusBadge, Textarea, useToast } from '../../components/ui';
import { useAsync } from '../../hooks/useAsync';
import { useQueryParams } from '../../hooks/useQueryState';
import { refreshTodoCounts } from '../../hooks/useTodoCounts';
import { cn } from '../../lib/cn';
import { formatDateTime, formatRelative } from '../../lib/time';
import type { ListingOrigin, PendingListing } from '../../types';
import { PublishListingDrawer } from './PublishListingDrawer';
import { PriceSyncDisabledCard, friendlyError, isNotConfigured } from './shared';
import { fetchAllPages } from '../../api/pickers';
import { Can } from '../../components/ui/Can';

// 待上架模型（UI_DESIGN.md §5.3）：上游出现、但平台还没有渠道的模型。
// 来源两类：价格源发现的新模型（price_source）；优惠识别出的免费模型（free_offer，自动进队列，
// 参数已按来源预填，上游免费结束后候选置为"免费已结束"，已上架的渠道由系统自动停用）。
// URL：?status=pending|published|dismissed|expired &origin=free_offer|price_source &id=（直接打开上架表单）

const STATUS_TABS = [
  { value: 'pending', label: '待处理' },
  { value: 'published', label: '已上架' },
  { value: 'dismissed', label: '已忽略' },
  { value: 'expired', label: '免费已结束' },
];

const ORIGIN_TABS = [
  { value: '', label: '全部来源' },
  { value: 'free_offer', label: '免费模型' },
  { value: 'price_source', label: '新模型发现' },
];

export default function ListingsPage() {
  const toast = useToast();
  const [params, setParams] = useQueryParams();
  const status = params.status && STATUS_TABS.some((t) => t.value === params.status) ? params.status : 'pending';

  const [tick, setTick] = useState(0);
  const providerFilter = params.provider_id ? Number(params.provider_id) : undefined;
  const origin = ORIGIN_TABS.some((t) => t.value === params.origin) ? (params.origin as ListingOrigin | '') : '';
  const list = useAsync(
    // 逐页取完（最多 1000 条），不再只取第一页 100 条后静默截断
    (signal) =>
      fetchAllPages((page, page_size) => listPendingListings({ status, provider_id: providerFilter, origin: origin || undefined, page, page_size }, signal)),
    [status, providerFilter, origin, tick],
  );
  const items = list.data?.data ?? [];

  const [selected, setSelected] = useState<Set<number>>(new Set());
  useEffect(() => setSelected(new Set()), [status]);

  const [publishing, setPublishing] = useState<PendingListing | null>(null);
  // ?id=：从优惠雷达"去上架"跳过来时直接打开该候选的上架表单（只打开一次，关掉后清掉参数）
  useEffect(() => {
    if (!params.id || publishing) return;
    const target = items.find((l) => String(l.id) === params.id);
    if (target?.status === 'pending') setPublishing(target);
  }, [params.id, items, publishing]);
  const closePublish = () => {
    setPublishing(null);
    if (params.id) setParams({ id: null });
  };
  const [dismissTargets, setDismissTargets] = useState<PendingListing[] | null>(null);
  const [dismissReason, setDismissReason] = useState('');
  const [dismissing, setDismissing] = useState(false);

  const openDismiss = (targets: PendingListing[]) => {
    setDismissReason('');
    setDismissTargets(targets);
  };

  const runDismiss = async () => {
    if (!dismissTargets) return;
    setDismissing(true);
    let ok = 0;
    const failed: string[] = [];
    // 一次请求批量忽略（服务端逐条独立处理并逐条返回结果）
    try {
      const { results } = await batchDismissListings(
        dismissTargets.map((l) => l.id),
        dismissReason.trim() || undefined,
      );
      const byId = new Map(dismissTargets.map((l) => [l.id, l]));
      for (const r of results) {
        if (r.ok) ok++;
        else failed.push(`${byId.get(r.id)?.upstream_model ?? `#${r.id}`}：${r.error?.message ?? '失败'}`);
      }
    } catch (err) {
      failed.push(friendlyError(err));
    }
    setDismissing(false);
    setDismissTargets(null);
    setSelected(new Set());
    setTick((t) => t + 1);
    refreshTodoCounts();
    if (ok) toast.success(`已忽略 ${ok} 个模型`);
    if (failed.length) toast.error(`${failed.length} 个模型忽略失败`, failed.join('；'));
  };

  if (isNotConfigured(list.error)) {
    return (
      <>
        <PageHeader title="待上架模型" description="上游出现、但平台尚未上架的新模型" />
        <PriceSyncDisabledCard />
      </>
    );
  }

  const selectedItems = items.filter((l) => selected.has(l.id));

  return (
    <>
      <PageHeader
        title="待上架模型"
        description="价格同步发现的、上游已有但平台还没有渠道的模型。补齐元数据并定价后一键上架，或忽略。"
        actions={
          <Button icon={<RotateCw className="w-3.5 h-3.5" />} loading={list.refreshing} onClick={() => setTick((t) => t + 1)}>
            刷新
          </Button>
        }
      />

      <div className="flex flex-wrap items-center gap-2 mb-4">
        <Pills
          options={STATUS_TABS.map((t) => ({ ...t, count: t.value === status ? list.data?.total : undefined }))}
          value={status}
          onChange={(v) => setParams({ status: v === 'pending' ? null : v })}
        />
        <Pills options={ORIGIN_TABS} value={origin} onChange={(v) => setParams({ origin: v || null })} />
        <div className="flex-1" />
        {status === 'pending' && selected.size > 0 && (
          <div className="flex items-center gap-2 text-xs">
            <span className="text-gray-700 font-medium">{selected.size} 项已选</span>
            <Can perm="pricing:write"><Button size="sm" icon={<EyeOff className="w-3.5 h-3.5" />} onClick={() => openDismiss(selectedItems)}>
              批量忽略
            </Button></Can>
            <Button size="sm" variant="ghost" onClick={() => setSelected(new Set())}>
              取消选择
            </Button>
          </div>
        )}
      </div>

      <DataState
        loading={list.loading}
        error={list.error}
        onRetry={list.reload}
        empty={items.length === 0}
        skeleton="cards"
        emptyIcon={<PackagePlus className="w-8 h-8" />}
        emptyTitle={status === 'pending' ? '🎉 暂无待上架的新模型' : status === 'published' ? '还没有通过这里上架的模型' : '没有被忽略的模型'}
        emptyDescription={
          status === 'pending' ? '价格源观测到供应商下没有渠道的新模型、或优惠识别出平台已接入供应商的免费模型时，会出现在这里' : undefined
        }
      >
        <div className="grid grid-cols-1 xl:grid-cols-2 gap-3">
          {items.map((l) => (
            <ListingCard
              key={l.id}
              l={l}
              checked={selected.has(l.id)}
              onCheck={(v) =>
                setSelected((prev) => {
                  const next = new Set(prev);
                  if (v) next.add(l.id);
                  else next.delete(l.id);
                  return next;
                })
              }
              onPublish={() => setPublishing(l)}
              onDismiss={() => openDismiss([l])}
            />
          ))}
        </div>
      </DataState>

      <PublishListingDrawer
        listing={publishing}
        onClose={closePublish}
        onPublished={(res, name) => {
          closePublish();
          setTick((t) => t + 1);
          refreshTodoCounts();
          toast.success(
            <span>
              已上架 {name}（模型 #{res.virtual_model_id}，渠道 #{res.channel_id}）
              {res.metadata_created && '，已自动生成展示元数据'}
              <Link to={`/models/${res.virtual_model_id}`} className="underline ml-1 text-purple-200">
                查看
              </Link>
            </span>,
          );
        }}
      />

      <ConfirmDialog
        open={!!dismissTargets}
        onClose={() => setDismissTargets(null)}
        onConfirm={runDismiss}
        loading={dismissing}
        level="danger"
        confirmLabel={`忽略 ${dismissTargets?.length ?? 0} 个模型`}
        title="忽略待上架模型"
      >
        <p>
          忽略后，<b>该模型再次被价格源观测到也不会重新进入队列</b>。如需上架只能手动创建虚拟模型与渠道。
        </p>
        <ul className="bg-gray-50 border border-gray-200 rounded-lg p-2 max-h-40 overflow-y-auto space-y-0.5">
          {dismissTargets?.map((l) => (
            <li key={l.id} className="font-mono text-[11px] text-gray-700">
              {l.upstream_model} <span className="text-gray-400">· {l.provider_code}</span>
            </li>
          ))}
        </ul>
        <Field label="原因（可选，记入审计日志）">
          <Textarea rows={2} value={dismissReason} onChange={(e) => setDismissReason(e.target.value)} placeholder="例如：内测模型，不对外提供" />
        </Field>
      </ConfirmDialog>
    </>
  );
}

function ListingCard({
  l,
  checked,
  onCheck,
  onPublish,
  onDismiss,
}: {
  l: PendingListing;
  checked: boolean;
  onCheck: (v: boolean) => void;
  onPublish: () => void;
  onDismiss: () => void;
}) {
  const pending = l.status === 'pending';
  const s = l.suggested;
  return (
    <div
      className={cn(
        'bg-white border rounded-xl p-4 shadow-xs transition-colors',
        checked ? 'border-purple-300 bg-purple-50/30' : 'border-gray-200 hover:border-purple-200',
      )}
    >
      <div className="flex items-start gap-2.5">
        {pending && (
          <div className="pt-0.5">
            <Checkbox label={`选择 ${l.upstream_model}`} checked={checked} onChange={onCheck} />
          </div>
        )}
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2 flex-wrap">
            {l.free ? <Gift className="w-3.5 h-3.5 text-emerald-600" /> : <span className="text-blue-600 text-xs">✦</span>}
            <span className="font-mono text-xs font-semibold text-gray-900 break-all">{l.upstream_model}</span>
            <StatusBadge kind="listing" value={l.status} />
            {l.free && (
              <span className="inline-flex px-1.5 py-0.5 rounded border text-[10px] font-medium bg-emerald-50 text-emerald-700 border-emerald-200">免费</span>
            )}
            {l.attached && <span className="text-[10px] text-gray-500">挂到已有同名模型</span>}
            {l.status === 'pending' && l.existing_virtual_model_id && l.existing_virtual_model_status !== 'deprecated' && (
              <span className="text-[10px] text-blue-600">已有同名虚拟模型，上架将只新增渠道</span>
            )}
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-gray-500">
            <Link to={`/providers/${l.provider_id}`} className="hover:text-purple-700">
              {l.provider_name}（{l.provider_code}）
            </Link>
            <span className="px-1.5 py-0.5 rounded bg-gray-100 text-gray-600 font-mono text-[10px]">来源 {l.source_level}</span>
            <span title={formatDateTime(l.first_observed_at)}>首次发现 {formatRelative(l.first_observed_at)}</span>
            <span title={formatDateTime(l.last_observed_at)}>最近 {formatRelative(l.last_observed_at)}</span>
            {l.offer_id && (
              <Link to={`/pricing/offers?status=all&id=${l.offer_id}`} className="hover:text-purple-700">
                优惠情报 #{l.offer_id}
              </Link>
            )}
          </div>
          {l.observed_meta && <MetaLine l={l} />}
          <div className="mt-2 text-xs text-gray-700">
            上游价{' '}
            <span className="font-mono">
              {s.input_price ?? '—'} / {s.output_price ?? '—'}
            </span>{' '}
            <span className="text-[11px] text-gray-400">
              {s.currency || l.observed_spec.currency} · 输入 / 输出 每百万 tokens
            </span>
            {l.observed_spec.components.length > 2 && (
              <span className="ml-1 text-[11px] text-gray-400">（另有 {l.observed_spec.components.length - 2} 个计量项）</span>
            )}
          </div>
          {l.status === 'published' && l.published_virtual_model_id && (
            <div className="mt-2 flex items-center gap-3 text-[11px]">
              <Link to={`/models/${l.published_virtual_model_id}`} className="inline-flex items-center gap-1 text-purple-600 hover:text-purple-700">
                虚拟模型 #{l.published_virtual_model_id} <ExternalLink className="w-3 h-3" />
              </Link>
              {l.published_channel_id && (
                <Link to={`/channels/${l.published_channel_id}`} className="inline-flex items-center gap-1 text-purple-600 hover:text-purple-700">
                  渠道 #{l.published_channel_id} <ExternalLink className="w-3 h-3" />
                </Link>
              )}
              <span className="text-gray-400">{formatDateTime(l.decided_at)}</span>
            </div>
          )}
          {l.retired_at && (
            <div className="mt-2 text-[11px] text-amber-700">
              上游免费已结束，系统已于 {formatDateTime(l.retired_at)} 自动停用渠道{!l.attached && '（若模型已无其它渠道，同时标为废弃）'}。如需继续提供，请到渠道页调整后手动启用。
            </div>
          )}
          {l.status === 'dismissed' && <div className="mt-2 text-[11px] text-gray-400">忽略于 {formatDateTime(l.decided_at)}</div>}
          {l.status === 'expired' && <div className="mt-2 text-[11px] text-gray-400">上游已不再免费（{formatDateTime(l.decided_at)}）；重新免费时会自动回到待处理</div>}
        </div>
      </div>
      {pending && (
        <div className="mt-3 flex justify-end gap-2">
          <Can perm="pricing:write"><Button size="sm" variant="ghost" icon={<EyeOff className="w-3.5 h-3.5" />} onClick={onDismiss}>
            忽略
          </Button></Can>
          <Can perm="pricing:write"><Button size="sm" variant="dark" icon={<Upload className="w-3.5 h-3.5" />} onClick={onPublish}>
            上架…
          </Button></Can>
        </div>
      )}
    </div>
  );
}

// 来源给出的模型参数摘要（上架表单会按这些预填）
function MetaLine({ l }: { l: PendingListing }) {
  const m = l.observed_meta;
  if (!m) return null;
  const parts = [
    m.type,
    m.context_window ? `上下文 ${m.context_window >= 1000 ? `${Math.round(m.context_window / 1000)}K` : m.context_window}` : null,
    m.max_output ? `最大输出 ${m.max_output >= 1000 ? `${Math.round(m.max_output / 1000)}K` : m.max_output}` : null,
    m.capabilities?.length ? m.capabilities.join(' / ') : null,
  ].filter(Boolean);
  if (parts.length === 0) return null;
  return <div className="mt-1 text-[11px] text-gray-500">参数：{parts.join(' · ')}</div>;
}
