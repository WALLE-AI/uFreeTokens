import { Checkbox } from '../../components/ui/index';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Check, CheckCheck, Keyboard, RotateCw, Scale, X } from 'lucide-react';
import { listProviders } from '../../api/catalog';
import {
  approveChangeRequest,
  batchApproveChangeRequests,
  getChangeRequest,
  listChangeRequests,
  rejectChangeRequest,
} from '../../api/pricing';
import {
  Button,
  ConfirmDialog,
  DataState,
  EmptyState,
  Field,
  Modal,
  PageHeader,
  Pills,
  Select,
  Textarea,
  useToast,
} from '../../components/ui';
import { useAsync } from '../../hooks/useAsync';
import { isTypingTarget } from '../../hooks/useHotkeys';
import { useQueryParams } from '../../hooks/useQueryState';
import { refreshTodoCounts } from '../../hooks/useTodoCounts';
import { cn } from '../../lib/cn';
import { formatRelative } from '../../lib/time';
import type { BatchApproveResult, ChangeRequestSummary } from '../../types';
import { ChangeRequestDetailView } from './ChangeRequestDetail';
import { DirectionIcon, PriceSyncDisabledCard, RatioText, errorDetail, friendlyError, isNotConfigured } from './shared';

// 调价审批收件箱（UI_DESIGN.md §5.2）：左列表 + 右详情，键盘优先。
// URL：?tab=pending|blocked|history &direction= &provider_id= &id=（当前选中项）

const TABS = {
  pending: { label: '待审批', status: 'pending,blocked', sort: 'created_at' },
  blocked: { label: '已拦截', status: 'blocked', sort: 'created_at' },
  history: { label: '历史', status: 'approved,rejected,applied,auto_approved,superseded', sort: '-created_at' },
} as const;
type TabKey = keyof typeof TABS;

const DIRECTION_OPTIONS = [
  { value: 'up', label: '▲ 涨价' },
  { value: 'down', label: '▼ 降价' },
  { value: 'mixed', label: '⇅ 涨跌互现' },
  { value: 'new', label: '✦ 新增' },
  { value: 'removed', label: '✕ 移除' },
];

const BATCH_THRESHOLDS = [
  { value: '0.05', label: '变化 ≤ 5%' },
  { value: '0.10', label: '变化 ≤ 10%' },
  { value: '0.20', label: '变化 ≤ 20%' },
];

export default function PriceChangesPage() {
  const toast = useToast();
  const [params, setParams] = useQueryParams();
  const tab: TabKey = params.tab && params.tab in TABS ? (params.tab as TabKey) : 'pending';
  const direction = params.direction ?? '';
  const providerId = params.provider_id ?? '';
  const selectedId = params.id ? Number(params.id) : null;

  const [listTick, setListTick] = useState(0);
  const list = useAsync(
    (signal) =>
      listChangeRequests(
        {
          status: TABS[tab].status,
          direction: direction || undefined,
          provider_id: providerId ? Number(providerId) : undefined,
          sort: TABS[tab].sort,
          page_size: 100,
        },
        signal,
      ),
    [tab, direction, providerId, listTick],
  );
  const providers = useAsync((signal) => listProviders({ page_size: 100 }, signal), []);

  // blocked 置顶（UI_DESIGN.md §5.2），其余保持后端顺序（最早的优先处理）
  const items = useMemo(() => {
    const data = list.data?.data ?? [];
    if (tab !== 'pending') return data;
    return [...data.filter((c) => c.status === 'blocked'), ...data.filter((c) => c.status !== 'blocked')];
  }, [list.data, tab]);

  const select = useCallback((id: number | null) => setParams({ id: id === null ? null : String(id) }, { keepPage: true }), [setParams]);

  // 列表加载后没有选中项（或选中项不在当前列表里）时，默认选第一条
  useEffect(() => {
    if (!list.data) return;
    if (items.length === 0) return;
    if (selectedId === null) select(items[0].id);
  }, [list.data, items, selectedId, select]);

  const [detailTick, setDetailTick] = useState(0);
  const detail = useAsync(
    (signal) => (selectedId ? getChangeRequest(selectedId, signal) : Promise.resolve(null)),
    [selectedId, detailTick],
  );

  // ---------- 操作 ----------
  const reasonRef = useRef<HTMLTextAreaElement>(null);
  const [reason, setReason] = useState('');
  const [reasonError, setReasonError] = useState<string | null>(null);
  const [acting, setActing] = useState<'approve' | 'reject' | null>(null);
  const [confirmBlocked, setConfirmBlocked] = useState(false);
  const [fadingId, setFadingId] = useState<number | null>(null);

  useEffect(() => {
    setReason('');
    setReasonError(null);
  }, [selectedId]);

  const current = detail.data ?? null;
  const actionable = !!current && (current.status === 'pending' || current.status === 'blocked') && current.id === selectedId;

  const nextIdAfter = (id: number): number | null => {
    const idx = items.findIndex((c) => c.id === id);
    if (idx < 0) return items[0]?.id ?? null;
    return items[idx + 1]?.id ?? items[idx - 1]?.id ?? null;
  };

  // 处理完一条：淡出 200ms → 刷新列表 → 自动跳到下一条
  const afterDecision = (id: number) => {
    setFadingId(id);
    const next = tab === 'history' ? id : nextIdAfter(id);
    setTimeout(() => {
      setFadingId(null);
      select(next);
      setListTick((t) => t + 1);
      setDetailTick((t) => t + 1);
    }, 200);
  };

  const doApprove = async (confirmedBlocked: boolean) => {
    if (!current) return;
    setActing('approve');
    try {
      const res = await approveChangeRequest(current.id, { reason: reason.trim() || undefined, confirm_blocked: confirmedBlocked || undefined });
      refreshTodoCounts();
      toast.success(`已批准 #${current.id}，新成本价版本 #${res.applied_book_id} 已生效`);
      setConfirmBlocked(false);
      afterDecision(current.id);
    } catch (err) {
      toast.error(friendlyError(err, '批准失败'), errorDetail(err));
    } finally {
      setActing(null);
    }
  };

  const approve = () => {
    if (!actionable || acting) return;
    if (current!.status === 'blocked') setConfirmBlocked(true);
    else void doApprove(false);
  };

  const reject = async () => {
    if (!actionable || acting || !current) return;
    if (!reason.trim()) {
      setReasonError('驳回必须填写原因');
      reasonRef.current?.focus();
      return;
    }
    setActing('reject');
    try {
      await rejectChangeRequest(current.id, { reason: reason.trim() });
      refreshTodoCounts();
      toast.success(`已驳回 #${current.id}`);
      afterDecision(current.id);
    } catch (err) {
      toast.error(friendlyError(err, '驳回失败'), errorDetail(err));
    } finally {
      setActing(null);
    }
  };

  // ---------- 键盘：J/K 切换、A 批准、R 驳回（聚焦原因框） ----------
  const keyRef = useRef({ approve, items, selectedId, select, actionable });
  keyRef.current = { approve, items, selectedId, select, actionable };
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      if (isTypingTarget(e.target)) return;
      if (document.body.dataset.modalOpen === 'true') return;
      const k = keyRef.current;
      const key = e.key.toLowerCase();
      if (key === 'j' || key === 'k') {
        e.preventDefault();
        if (k.items.length === 0) return;
        const idx = k.items.findIndex((c) => c.id === k.selectedId);
        const nextIdx = key === 'j' ? Math.min(idx + 1, k.items.length - 1) : Math.max(idx - 1, 0);
        k.select(k.items[idx < 0 ? 0 : nextIdx].id);
      } else if (key === 'a' && k.actionable) {
        e.preventDefault();
        k.approve();
      } else if (key === 'r' && k.actionable) {
        e.preventDefault();
        reasonRef.current?.focus();
      }
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, []);

  // ---------- 批量批准 ----------
  const [checked, setChecked] = useState<Set<number>>(new Set());
  const [threshold, setThreshold] = useState('0.10');
  const [batchOpen, setBatchOpen] = useState(false);
  const [batchReason, setBatchReason] = useState('');
  const [batchRunning, setBatchRunning] = useState(false);
  const [batchResults, setBatchResults] = useState<BatchApproveResult[] | null>(null);

  useEffect(() => setChecked(new Set()), [tab, direction, providerId]);

  const checkedItems = items.filter((c) => checked.has(c.id));
  const overThreshold = (c: ChangeRequestSummary) => Math.abs(Number(c.max_change_ratio)) > Number(threshold);

  const runBatch = async () => {
    setBatchRunning(true);
    try {
      const res = await batchApproveChangeRequests({
        ids: checkedItems.map((c) => c.id),
        reason: batchReason.trim() || undefined,
        max_abs_change_ratio: threshold,
      });
      setBatchResults(res.results);
      setBatchOpen(false);
      setChecked(new Set());
      const ok = res.results.filter((r) => r.ok).length;
      refreshTodoCounts();
      if (ok > 0) toast.success(`已批准 ${ok} 条调价`);
      setListTick((t) => t + 1);
      setDetailTick((t) => t + 1);
    } catch (err) {
      toast.error(friendlyError(err, '批量批准失败'), errorDetail(err));
    } finally {
      setBatchRunning(false);
    }
  };

  // ---------- 渲染 ----------
  if (isNotConfigured(list.error)) {
    return (
      <>
        <PageHeader title="调价审批" description="上游价格变化生成的调价申请：逐条审阅差异与影响评估后批准或驳回" />
        <PriceSyncDisabledCard />
      </>
    );
  }

  const pendingCount = tab === 'pending' ? items.length : undefined;
  const providerOptions = (providers.data?.data ?? []).map((p) => ({ value: String(p.id), label: `${p.name}（${p.code}）` }));

  return (
    <>
      <PageHeader
        title="调价审批"
        description="上游价格变化生成的调价申请：逐条审阅差异与影响评估后批准或驳回"
        actions={
          <>
            <span className="hidden md:inline-flex items-center gap-1 text-[11px] text-gray-400 mr-1">
              <Keyboard className="w-3.5 h-3.5" />
              <kbd className="font-mono">J</kbd>/<kbd className="font-mono">K</kbd> 切换 · <kbd className="font-mono">A</kbd> 批准 ·{' '}
              <kbd className="font-mono">R</kbd> 驳回
            </span>
            <Button icon={<RotateCw className="w-3.5 h-3.5" />} onClick={() => setListTick((t) => t + 1)} loading={list.refreshing}>
              刷新
            </Button>
          </>
        }
      />

      <div className="flex flex-wrap items-center gap-2 mb-4">
        <Pills
          options={(Object.keys(TABS) as TabKey[]).map((k) => ({
            value: k,
            label: TABS[k].label,
            count: k === tab ? pendingCount : undefined,
          }))}
          value={tab}
          onChange={(v) => setParams({ tab: v === 'pending' ? null : v, id: null })}
        />
        <div className="flex-1" />
        <Select
          value={direction}
          placeholder="全部方向"
          options={DIRECTION_OPTIONS}
          onChange={(e) => setParams({ direction: e.target.value || null, id: null })}
        />
        <Select
          value={providerId}
          placeholder="全部供应商"
          options={providerOptions}
          onChange={(e) => setParams({ provider_id: e.target.value || null, id: null })}
        />
      </div>

      <DataState
        loading={list.loading}
        error={list.error}
        onRetry={list.reload}
        empty={items.length === 0}
        emptyIcon={<Scale className="w-8 h-8" />}
        emptyTitle={tab === 'history' ? '没有已处理的调价申请' : '🎉 暂无待审批的调价'}
        emptyDescription={tab === 'history' ? undefined : '上游价格变化超过自动审批阈值时会出现在这里'}
      >
        <div className="grid grid-cols-1 lg:grid-cols-[minmax(300px,380px)_1fr] gap-4 items-start">
          {/* 左：列表 */}
          <div className="bg-white border border-gray-200 rounded-xl shadow-xs overflow-hidden lg:sticky lg:top-4">
            {tab !== 'history' && (
              <div className="px-3 py-2 border-b border-gray-100 bg-gray-50 flex items-center gap-2 text-[11px] text-gray-500">
                <Checkbox
                  label="全选待审批"
                  checked={checkedItems.length > 0 && checkedItems.length === items.filter((c) => c.status === 'pending').length}
                  onChange={(v) => setChecked(v ? new Set(items.filter((c) => c.status === 'pending').map((c) => c.id)) : new Set())}
                />
                {checked.size > 0 ? (
                  <>
                    <span className="text-gray-700 font-medium">{checked.size} 项已选</span>
                    <div className="flex-1" />
                    <Select value={threshold} options={BATCH_THRESHOLDS} onChange={(e) => setThreshold(e.target.value)} />
                    <Button size="sm" variant="dark" icon={<CheckCheck className="w-3.5 h-3.5" />} onClick={() => setBatchOpen(true)}>
                      批量批准
                    </Button>
                  </>
                ) : (
                  <span>勾选小幅 pending 调价可批量批准（blocked 不可批量）</span>
                )}
              </div>
            )}
            <ul className="divide-y divide-gray-100 max-h-[calc(100vh-15rem)] overflow-y-auto">
              {items.map((c) => (
                <ListItem
                  key={c.id}
                  c={c}
                  selected={c.id === selectedId}
                  fading={c.id === fadingId}
                  showCheckbox={tab !== 'history'}
                  checked={checked.has(c.id)}
                  onCheck={(v) =>
                    setChecked((prev) => {
                      const next = new Set(prev);
                      if (v) next.add(c.id);
                      else next.delete(c.id);
                      return next;
                    })
                  }
                  onClick={() => select(c.id)}
                />
              ))}
            </ul>
          </div>

          {/* 右：详情 + 操作区 */}
          <div className="bg-white border border-gray-200 rounded-xl shadow-xs flex flex-col min-w-0">
            <div className="p-5">
              {selectedId === null ? (
                <EmptyState title="从左侧选择一条调价申请" />
              ) : (
                <DataState loading={detail.loading} error={detail.error} onRetry={detail.reload} skeleton="text">
                  {current && <ChangeRequestDetailView d={current} />}
                </DataState>
              )}
            </div>
            {actionable && current && (
              <div className="border-t border-gray-100 p-4 bg-gray-50/60 rounded-b-xl space-y-3">
                <Field label="审批理由" hint="批准时可选；驳回时必填（R 聚焦此处）" error={reasonError}>
                  <Textarea
                    ref={reasonRef}
                    rows={2}
                    value={reason}
                    invalid={!!reasonError}
                    placeholder={current.status === 'blocked' ? '例如：已在厂商官网确认本次调价' : '例如：等待厂商官方公告'}
                    onChange={(e) => {
                      setReason(e.target.value);
                      if (reasonError) setReasonError(null);
                    }}
                    onKeyDown={(e) => {
                      if (e.key === 'Escape') (e.target as HTMLTextAreaElement).blur();
                    }}
                  />
                </Field>
                <div className="flex items-center justify-end gap-2">
                  <Button icon={<X className="w-3.5 h-3.5" />} loading={acting === 'reject'} disabled={!!acting} onClick={() => void reject()}>
                    驳回 <kbd className="ml-1 font-mono text-[10px] text-gray-400">R</kbd>
                  </Button>
                  <Button
                    variant="primary"
                    icon={<Check className="w-3.5 h-3.5" />}
                    loading={acting === 'approve'}
                    disabled={!!acting}
                    onClick={approve}
                  >
                    {current.status === 'blocked' ? '确认并批准' : '批准'}
                    <kbd className="ml-1 font-mono text-[10px] text-purple-200">A</kbd>
                  </Button>
                </div>
              </div>
            )}
          </div>
        </div>
      </DataState>

      {/* blocked 项批准：输入确认（UI_DESIGN.md §5.2，服务端 confirm_blocked 兜底） */}
      <ConfirmDialog
        open={confirmBlocked}
        onClose={() => setConfirmBlocked(false)}
        onConfirm={() => doApprove(true)}
        loading={acting === 'approve'}
        level="typed"
        confirmText={current ? `#${current.id}` : ''}
        confirmLabel="确认批准"
        title="批准被拦截的调价"
      >
        {current && (
          <div className="space-y-2">
            <p>
              这条调价被系统拦截：<span className="text-rose-700">{current.blocked_reason ?? '变化幅度超过阈值'}</span>
            </p>
            <p>
              <b>{current.virtual_model_name}</b>（渠道 #{current.channel_id}）的成本价将变化 <RatioText ratio={current.max_change_ratio} />
              ，批准后立即生效。请确认你已核实这确实是厂商调价，而不是解析错误。
            </p>
          </div>
        )}
      </ConfirmDialog>

      {/* 批量批准确认 */}
      <Modal
        open={batchOpen}
        onClose={() => setBatchOpen(false)}
        busy={batchRunning}
        width="lg"
        title={`批量批准 ${checkedItems.length} 条调价`}
        description={`仅允许 pending 且变化幅度不超过 ${Number(threshold) * 100}% 的申请；超出阈值的条目会被服务端逐条拒绝，不影响其它条目。`}
        footer={
          <>
            <Button onClick={() => setBatchOpen(false)} disabled={batchRunning}>
              取消
            </Button>
            <Button variant="danger" loading={batchRunning} onClick={() => void runBatch()} disabled={checkedItems.length === 0}>
              确认批准 {checkedItems.length} 条
            </Button>
          </>
        }
      >
        <ul className="divide-y divide-gray-100 border border-gray-200 rounded-xl max-h-64 overflow-y-auto mb-3">
          {checkedItems.map((c) => (
            <li key={c.id} className="px-3 py-2 flex items-center gap-2">
              <DirectionIcon direction={c.direction} />
              <span className="font-mono text-[11px] text-gray-400">#{c.id}</span>
              <span className="truncate flex-1 text-gray-900">{c.virtual_model_name}</span>
              <RatioText ratio={c.max_change_ratio} />
              {overThreshold(c) && <span className="text-[10px] text-rose-600">超出阈值，将被拒绝</span>}
            </li>
          ))}
        </ul>
        <Field label="批量审批理由（可选）">
          <Textarea rows={2} value={batchReason} onChange={(e) => setBatchReason(e.target.value)} placeholder="例如：例行小幅调价" />
        </Field>
      </Modal>

      {/* 批量结果：逐条展示 */}
      <Modal
        open={batchResults !== null}
        onClose={() => setBatchResults(null)}
        width="lg"
        title="批量批准结果"
        description={
          batchResults
            ? `成功 ${batchResults.filter((r) => r.ok).length} 条，失败 ${batchResults.filter((r) => !r.ok).length} 条`
            : undefined
        }
        footer={<Button onClick={() => setBatchResults(null)}>关闭</Button>}
      >
        <ul className="divide-y divide-gray-100 border border-gray-200 rounded-xl max-h-80 overflow-y-auto">
          {batchResults?.map((r) => (
            <li key={r.id} className="px-3 py-2 flex items-start gap-2">
              {r.ok ? <Check className="w-3.5 h-3.5 text-emerald-600 mt-0.5" /> : <X className="w-3.5 h-3.5 text-rose-600 mt-0.5" />}
              <span className="font-mono text-[11px] text-gray-500">#{r.id}</span>
              <span className={cn('flex-1', r.ok ? 'text-gray-700' : 'text-rose-700')}>
                {r.ok ? `已生效（价格版本 #${r.applied_book_id}）` : r.error?.message ?? '失败'}
              </span>
            </li>
          ))}
        </ul>
      </Modal>
    </>
  );
}

function ListItem({
  c,
  selected,
  fading,
  showCheckbox,
  checked,
  onCheck,
  onClick,
}: {
  c: ChangeRequestSummary;
  selected: boolean;
  fading: boolean;
  showCheckbox: boolean;
  checked: boolean;
  onCheck: (v: boolean) => void;
  onClick: () => void;
}) {
  const ref = useRef<HTMLLIElement>(null);
  useEffect(() => {
    if (selected) ref.current?.scrollIntoView({ block: 'nearest' });
  }, [selected]);
  const blocked = c.status === 'blocked';
  return (
    <li
      ref={ref}
      onClick={onClick}
      className={cn(
        'px-3 py-2.5 cursor-pointer transition-all duration-200 border-l-2',
        selected ? 'bg-purple-50/60 border-l-purple-600' : 'border-l-transparent hover:bg-gray-50/70',
        fading && 'opacity-0 -translate-x-2',
      )}
    >
      <div className="flex items-center gap-2">
        {showCheckbox && <Checkbox label={`选择 #${c.id}`} checked={checked} disabled={c.status !== 'pending'} onChange={onCheck} />}
        <DirectionIcon direction={c.direction} />
        <span className="font-mono text-[11px] text-gray-400">#{c.id}</span>
        <span className="text-xs font-medium text-gray-900 truncate flex-1" title={c.virtual_model_name}>
          {c.virtual_model_name}
        </span>
        <RatioText ratio={c.max_change_ratio} className="text-xs" />
      </div>
      <div className="mt-1 flex items-center gap-2 text-[11px] text-gray-400 pl-0.5">
        <span className="truncate">
          {c.provider_code}/{c.provider_account_name} · <span className="font-mono">{c.upstream_model}</span>
        </span>
        <span className="ml-auto shrink-0">{formatRelative(c.created_at)}</span>
      </div>
      {blocked && (
        <div className="mt-1 text-[11px] text-rose-600 line-clamp-2">
          <span className="inline-block px-1.5 rounded-full bg-rose-50 border border-rose-200 mr-1">拦截</span>
          {c.blocked_reason ?? '变化幅度超过阈值'}
        </div>
      )}
      {!blocked && c.issue_count > 0 && <div className="mt-1 text-[11px] text-amber-700">{c.issue_count} 条校验告警</div>}
      {(c.status !== 'pending' && c.status !== 'blocked') && (
        <div className="mt-1 text-[11px] text-gray-400">
          {c.status === 'rejected' ? '驳回' : '处理'}：{c.decided_by_name || (c.decided_by ? `#${c.decided_by}` : '系统')}
          {c.decision_reason ? ` · ${c.decision_reason}` : ''}
        </div>
      )}
    </li>
  );
}
