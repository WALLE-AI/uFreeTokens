import { Checkbox } from '../../components/ui/index';
import { useEffect, useState } from 'react';
import { Link } from 'react-router';
import { ExternalLink, EyeOff, PackagePlus, RotateCw, Upload } from 'lucide-react';
import { dismissListing, listPendingListings } from '../../api/pricing';
import { Button, ConfirmDialog, DataState, Field, PageHeader, Pills, StatusBadge, Textarea, useToast } from '../../components/ui';
import { useAsync } from '../../hooks/useAsync';
import { useQueryParams } from '../../hooks/useQueryState';
import { refreshTodoCounts } from '../../hooks/useTodoCounts';
import { cn } from '../../lib/cn';
import { formatDateTime, formatRelative } from '../../lib/time';
import type { PendingListing } from '../../types';
import { PublishListingDrawer } from './PublishListingDrawer';
import { PriceSyncDisabledCard, friendlyError, isNotConfigured } from './shared';

// 待上架模型（UI_DESIGN.md §5.3）：上游出现、但平台还没有渠道的模型。
// URL：?status=pending|published|dismissed

const STATUS_TABS = [
  { value: 'pending', label: '待处理' },
  { value: 'published', label: '已上架' },
  { value: 'dismissed', label: '已忽略' },
];

export default function ListingsPage() {
  const toast = useToast();
  const [params, setParams] = useQueryParams();
  const status = params.status && STATUS_TABS.some((t) => t.value === params.status) ? params.status : 'pending';

  const [tick, setTick] = useState(0);
  const providerFilter = params.provider_id ? Number(params.provider_id) : undefined;
  const list = useAsync(
    (signal) => listPendingListings({ status, provider_id: providerFilter, page_size: 100 }, signal),
    [status, providerFilter, tick],
  );
  const items = list.data?.data ?? [];

  const [selected, setSelected] = useState<Set<number>>(new Set());
  useEffect(() => setSelected(new Set()), [status]);

  const [publishing, setPublishing] = useState<PendingListing | null>(null);
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
    for (const l of dismissTargets) {
      try {
        await dismissListing(l.id, dismissReason.trim() || undefined);
        ok++;
      } catch (err) {
        failed.push(`${l.upstream_model}：${friendlyError(err)}`);
      }
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
        <div className="flex-1" />
        {status === 'pending' && selected.size > 0 && (
          <div className="flex items-center gap-2 text-xs">
            <span className="text-gray-700 font-medium">{selected.size} 项已选</span>
            <Button size="sm" icon={<EyeOff className="w-3.5 h-3.5" />} onClick={() => openDismiss(selectedItems)}>
              批量忽略
            </Button>
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
        emptyDescription={status === 'pending' ? '价格源观测到供应商下没有渠道的新模型时，会出现在这里' : undefined}
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
        onClose={() => setPublishing(null)}
        onPublished={(res, name) => {
          setPublishing(null);
          setTick((t) => t + 1);
          refreshTodoCounts();
          toast.success(
            <span>
              已上架 {name}（模型 #{res.virtual_model_id}，渠道 #{res.channel_id}）
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
            <span className="text-blue-600 text-xs">✦</span>
            <span className="font-mono text-xs font-semibold text-gray-900 break-all">{l.upstream_model}</span>
            <StatusBadge kind="listing" value={l.status} />
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-gray-500">
            <Link to={`/providers/${l.provider_id}`} className="hover:text-purple-700">
              {l.provider_name}（{l.provider_code}）
            </Link>
            <span className="px-1.5 py-0.5 rounded bg-gray-100 text-gray-600 font-mono text-[10px]">来源 {l.source_level}</span>
            <span title={formatDateTime(l.first_observed_at)}>首次发现 {formatRelative(l.first_observed_at)}</span>
            <span title={formatDateTime(l.last_observed_at)}>最近 {formatRelative(l.last_observed_at)}</span>
          </div>
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
          {l.status === 'dismissed' && <div className="mt-2 text-[11px] text-gray-400">忽略于 {formatDateTime(l.decided_at)}</div>}
        </div>
      </div>
      {pending && (
        <div className="mt-3 flex justify-end gap-2">
          <Button size="sm" variant="ghost" icon={<EyeOff className="w-3.5 h-3.5" />} onClick={onDismiss}>
            忽略
          </Button>
          <Button size="sm" variant="dark" icon={<Upload className="w-3.5 h-3.5" />} onClick={onPublish}>
            上架…
          </Button>
        </div>
      )}
    </div>
  );
}
