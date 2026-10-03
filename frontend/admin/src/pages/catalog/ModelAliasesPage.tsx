import { useEffect, useState } from 'react';
import { Link } from 'react-router';
import { Check, RotateCw } from 'lucide-react';
import { listModelAliasNamespaces, listModelAliases, setModelAlias, type SetModelAliasBody } from '../../api/benchmarks';
import { useCan } from '../../api/auth';
import { errorMessage } from '../../api/errors';
import { searchVirtualModels, virtualModelLabel } from '../../api/pickers';
import {
  Button,
  ConfirmDialog,
  DataState,
  DataTable,
  Field,
  FilterBar,
  FormModal,
  PageHeader,
  Pagination,
  Pills,
  RemoteSelect,
  Select,
  StatusBadge,
  useToast,
  type ActiveFilter,
  type Column,
} from '../../components/ui';
import { useAsync } from '../../hooks/useAsync';
import { useQueryParams } from '../../hooks/useQueryState';
import { refreshTodoCounts } from '../../hooks/useTodoCounts';
import { cn } from '../../lib/cn';
import { formatDateTime, formatRelative } from '../../lib/time';
import type { ModelAlias, SetModelAliasResult } from '../../types';
import { AgentActionButton, AgentSuggestionBadge } from '../agent/components/AgentEmbeds';
import { useAgentSuggestions } from '../../agent/useAgentSuggestions';
import { useAgentMutated } from '../../agent/agentEvents';

// 榜单模型映射工作台（外部数据采集技术方案 §4.4）：外部评测榜单里的模型名 → 平台虚拟模型。
// 精确 / 归一化匹配自动生效（auto）；模糊匹配只给出建议（suggested），需要人工确认后才关联成绩；
// 确认后的映射对后续所有导入生效，保存时服务端立即重新关联已导入的成绩、重新投影 scores。
// URL：?status=suggested|unmatched|auto|confirmed|ignored|all &namespace= &q= &page=

const STATUS_TABS = [
  { value: 'suggested', label: '待确认建议' },
  { value: 'unmatched', label: '未匹配' },
  { value: 'auto', label: '自动匹配' },
  { value: 'confirmed', label: '人工确认' },
  { value: 'ignored', label: '平台无此模型' },
  { value: 'all', label: '全部' },
];

const METHOD_LABELS: Record<string, string> = {
  exact: '精确',
  normalized: '归一化',
  fuzzy: '模糊',
  manual: '人工',
};

const PAGE_SIZE = 50;

const aliasKey = (a: Pick<ModelAlias, 'namespace' | 'external_label'>) => `${a.namespace}\u0000${a.external_label}`;

export function relinkMessage(res: SetModelAliasResult): string {
  return `已重新关联 ${res.relinked_results} 条结果${res.reprojected_runs ? `，重新投影 ${res.reprojected_runs} 个已发布 run 的评分` : ''}`;
}

export default function ModelAliasesPage() {
  const toast = useToast();
  const canWrite = useCan('catalog:write');
  const [params, setParams] = useQueryParams();
  const status = STATUS_TABS.some((t) => t.value === params.status) ? params.status : 'suggested';
  const page = Math.max(1, Number(params.page) || 1);
  const [tick, setTick] = useState(0);

  const namespaces = useAsync((signal) => listModelAliasNamespaces(signal), []);
  const list = useAsync(
    (signal) =>
      listModelAliases(
        { status: status === 'all' ? undefined : status, namespace: params.namespace || undefined, q: params.q || undefined, page, page_size: PAGE_SIZE },
        signal,
      ),
    [status, params.namespace, params.q, page, tick],
  );
  const rows = list.data?.data ?? [];
  const suggestions = useAgentSuggestions('model_alias', rows.map((a) => `${a.namespace}:${a.external_label}`));
  useAgentMutated('model_alias', () => setTick((t) => t + 1));

  const [selected, setSelected] = useState<Set<string | number>>(new Set());
  useEffect(() => setSelected(new Set()), [status, params.namespace, params.q, page, tick]);

  const [busyKey, setBusyKey] = useState<string | null>(null);
  const [picking, setPicking] = useState<ModelAlias | null>(null);
  const [ignoring, setIgnoring] = useState<ModelAlias | null>(null);
  const [bulkOpen, setBulkOpen] = useState(false);
  const [bulkBusy, setBulkBusy] = useState(false);

  const reload = () => {
    setTick((t) => t + 1);
    refreshTodoCounts();
  };

  const save = async (a: ModelAlias, body: Omit<SetModelAliasBody, 'namespace' | 'external_label'>, okText: string) => {
    setBusyKey(aliasKey(a));
    try {
      const res = await setModelAlias({ namespace: a.namespace, external_label: a.external_label, ...body });
      toast.success(`${okText}：${relinkMessage(res)}`);
      reload();
      return true;
    } catch (err) {
      toast.error('保存映射失败', errorMessage(err));
      return false;
    } finally {
      setBusyKey(null);
    }
  };

  const bulkTargets = rows.filter((a) => selected.has(aliasKey(a)) && a.status === 'suggested' && a.virtual_model_id);

  const runBulk = async () => {
    setBulkBusy(true);
    let ok = 0;
    let relinked = 0;
    const failed: string[] = [];
    // 逐条保存（PUT 是单条接口），任何一条失败不影响其他条
    for (const a of bulkTargets) {
      try {
        const res = await setModelAlias({ namespace: a.namespace, external_label: a.external_label, status: 'confirmed', virtual_model_id: a.virtual_model_id ?? undefined });
        ok++;
        relinked += res.relinked_results;
      } catch (err) {
        failed.push(`${a.external_label}：${errorMessage(err)}`);
      }
    }
    setBulkBusy(false);
    setBulkOpen(false);
    reload();
    if (ok) toast.success(`已确认 ${ok} 条建议，已重新关联 ${relinked} 条结果`);
    if (failed.length) toast.error(`${failed.length} 条确认失败`, failed.join('；'));
  };

  const active: ActiveFilter[] = [];
  if (params.namespace) active.push({ key: 'namespace', label: `命名空间: ${params.namespace}`, onRemove: () => setParams({ namespace: null }) });
  if (params.q) active.push({ key: 'q', label: `搜索: ${params.q}`, onRemove: () => setParams({ q: null }) });

  const columns: Column<ModelAlias>[] = [
    {
      key: 'external_label',
      header: '榜单模型名',
      render: (a) => (
        <div className="min-w-0 max-w-72">
          <div className="font-mono text-gray-900 break-all">{a.external_label}</div>
          <div className="text-[10px] text-gray-400 font-mono">{a.namespace}</div>
        </div>
      ),
    },
    { key: 'variant', header: '档位', render: (a) => (a.variant ? <span className="px-1.5 py-0.5 rounded bg-gray-100 text-gray-600 font-mono text-[10px]">{a.variant}</span> : <span className="text-gray-300">—</span>) },
    { key: 'status', header: '状态', render: (a) => <StatusBadge kind="model_alias" value={a.status} /> },
    {
      key: 'agent',
      header: '智能体建议',
      render: (a) => <AgentSuggestionBadge proposal={suggestions.byId.get(`${a.namespace}:${a.external_label}`)} onDone={() => setTick((t) => t + 1)} />,
    },
    {
      key: 'virtual_model',
      header: '虚拟模型',
      render: (a) => {
        if (a.status === 'ignored') return <span className="text-gray-400 text-[11px]">平台无此模型</span>;
        if (!a.virtual_model_id) return <span className="text-gray-400 text-[11px]">未关联</span>;
        const suggested = a.status === 'suggested';
        return (
          <div className="flex items-center gap-1.5">
            {suggested && <span className="text-[10px] text-purple-600 shrink-0">建议</span>}
            <Link
              to={`/models/${a.virtual_model_id}`}
              onClick={(e) => e.stopPropagation()}
              className={cn('font-mono hover:underline', suggested ? 'text-purple-700 border-b border-dashed border-purple-300' : 'text-gray-900')}
              title={suggested ? '模糊匹配的建议，确认前不会关联成绩' : undefined}
            >
              {a.virtual_model ?? `#${a.virtual_model_id}`}
            </Link>
            {suggested && canWrite && (
              <button
                type="button"
                disabled={busyKey === aliasKey(a)}
                onClick={(e) => {
                  e.stopPropagation();
                  void save(a, { status: 'confirmed', virtual_model_id: a.virtual_model_id ?? undefined }, `已确认 ${a.external_label} → ${a.virtual_model}`);
                }}
                className="inline-flex items-center gap-0.5 px-1.5 py-0.5 rounded border border-purple-200 text-purple-700 text-[10px] hover:bg-purple-50 cursor-pointer disabled:opacity-40"
              >
                <Check className="w-3 h-3" />
                确认
              </button>
            )}
          </div>
        );
      },
    },
    { key: 'method', header: '方法', render: (a) => <span className="text-gray-600">{METHOD_LABELS[a.method] ?? a.method}</span> },
    {
      key: 'confidence',
      header: '置信度',
      numeric: true,
      render: (a) =>
        a.confidence === null ? (
          <span className="text-gray-300">—</span>
        ) : (
          <span className={cn(a.confidence < 0.8 ? 'text-amber-700' : 'text-gray-900')}>{(a.confidence * 100).toFixed(0)}%</span>
        ),
    },
    { key: 'seen_count', header: '出现次数', numeric: true, render: (a) => a.seen_count.toLocaleString('en-US') },
    {
      key: 'last_seen_at',
      header: '最近出现',
      render: (a) => (
        <span className="text-gray-500 text-[11px]" title={`首次 ${formatDateTime(a.first_seen_at)}`}>
          {formatRelative(a.last_seen_at)}
        </span>
      ),
    },
    {
      key: 'decided',
      header: '处理人',
      render: (a) =>
        a.decided_by_name ? (
          <span className="text-gray-500 text-[11px]" title={formatDateTime(a.decided_at)}>
            {a.decided_by_name}
          </span>
        ) : (
          <span className="text-gray-300">—</span>
        ),
    },
  ];

  return (
    <div>
      <PageHeader
        title="榜单模型映射"
        description="外部评测榜单里的模型名与平台虚拟模型的对应关系。模糊匹配只给出建议，确认后才会把成绩关联到模型并投影进评分；确认的映射对之后的所有导入生效。"
        actions={
          <>
            <AgentActionButton playbook="alias_matching" label="✦ 处理待确认映射" />
            <Button icon={<RotateCw className="w-3.5 h-3.5" />} loading={list.refreshing} onClick={() => setTick((t) => t + 1)}>
              刷新
            </Button>
          </>
        }
      />

      <div className="mb-3">
        <Pills
          options={STATUS_TABS.map((t) => ({ ...t, count: t.value === status ? list.data?.total : undefined }))}
          value={status}
          onChange={(v) => setParams({ status: v === 'suggested' ? null : v })}
        />
      </div>

      <FilterBar
        search={params.q ?? ''}
        onSearch={(q) => setParams({ q })}
        searchPlaceholder="搜索榜单模型名…"
        controls={
          <Select
            value={params.namespace ?? ''}
            placeholder="全部命名空间"
            options={(namespaces.data?.data ?? []).map((n) => ({ value: n, label: n }))}
            onChange={(e) => setParams({ namespace: e.target.value || null })}
          />
        }
        active={active}
        onClearAll={() => setParams({ namespace: null, q: null })}
      />

      <DataState loading={list.loading} error={list.error} onRetry={list.reload}>
        {list.data && (
          <DataTable
            columns={columns}
            rows={rows}
            rowKey={aliasKey}
            selectable={canWrite && status === 'suggested'}
            selected={selected}
            onSelectedChange={setSelected}
            bulkActions={
              <Button size="sm" variant="primary" icon={<Check className="w-3.5 h-3.5" />} disabled={bulkTargets.length === 0} onClick={() => setBulkOpen(true)}>
                批量确认建议（{bulkTargets.length}）
              </Button>
            }
            rowActions={
              canWrite
                ? [
                    {
                      label: '确认建议',
                      hidden: (a) => a.status !== 'suggested' || !a.virtual_model_id || busyKey === aliasKey(a),
                      onClick: (a) => void save(a, { status: 'confirmed', virtual_model_id: a.virtual_model_id ?? undefined }, `已确认 ${a.external_label} → ${a.virtual_model}`),
                    },
                    { label: '改为其它模型…', onClick: setPicking },
                    { label: '标记平台无此模型', hidden: (a) => a.status === 'ignored', onClick: setIgnoring },
                    {
                      label: '交回自动匹配',
                      hidden: (a) => a.status !== 'confirmed' && a.status !== 'ignored',
                      onClick: (a) => void save(a, { status: 'auto' }, `${a.external_label} 已交回自动匹配`),
                    },
                  ]
                : undefined
            }
            empty={
              status === 'suggested' && active.length === 0
                ? '🎉 没有待确认的映射建议'
                : active.length
                  ? '没有符合条件的映射'
                  : '暂无映射——评测榜单数据源导入后，榜单里出现的模型名会出现在这里'
            }
          />
        )}
        {list.data && list.data.total > PAGE_SIZE && (
          <Pagination page={page} pageSize={PAGE_SIZE} total={list.data.total} onPageChange={(p) => setParams({ page: String(p) }, { keepPage: true })} />
        )}
      </DataState>

      <PickModelModal
        alias={picking}
        onClose={() => setPicking(null)}
        onSubmit={async (a, vmId, vmName) => {
          const ok = await save(a, { status: 'confirmed', virtual_model_id: vmId }, `已将 ${a.external_label} 映射到 ${vmName}`);
          if (ok) setPicking(null);
        }}
      />

      <ConfirmDialog
        open={!!ignoring}
        onClose={() => setIgnoring(null)}
        loading={!!ignoring && busyKey === aliasKey(ignoring)}
        onConfirm={async () => {
          if (!ignoring) return;
          const ok = await save(ignoring, { status: 'ignored' }, `已标记 ${ignoring.external_label} 为平台无此模型`);
          if (ok) setIgnoring(null);
        }}
        confirmLabel="标记"
        title={`标记「${ignoring?.external_label ?? ''}」为平台无此模型？`}
      >
        <p className="text-xs">
          该榜单模型的成绩将不关联任何虚拟模型（仍以原名展示在榜单里），之后的导入也不会再自动匹配。如果以后上架了对应模型，可以"改为其它模型"或"交回自动匹配"。
        </p>
      </ConfirmDialog>

      <ConfirmDialog
        open={bulkOpen}
        onClose={() => setBulkOpen(false)}
        onConfirm={runBulk}
        loading={bulkBusy}
        confirmLabel={`确认 ${bulkTargets.length} 条`}
        title="批量确认映射建议"
      >
        <ul className="bg-gray-50 border border-gray-200 rounded-lg p-2 max-h-56 overflow-y-auto space-y-0.5">
          {bulkTargets.map((a) => (
            <li key={aliasKey(a)} className="font-mono text-[11px] text-gray-700">
              {a.external_label} <span className="text-gray-400">→</span> {a.virtual_model}
              {a.confidence !== null && <span className="text-gray-400"> · {(a.confidence * 100).toFixed(0)}%</span>}
            </li>
          ))}
        </ul>
        <p className="text-[11px] text-gray-500">确认后立即重新关联这些模型在已导入榜单中的成绩，并对已发布的 run 重新投影评分。</p>
      </ConfirmDialog>
    </div>
  );
}

function PickModelModal({
  alias,
  onClose,
  onSubmit,
}: {
  alias: ModelAlias | null;
  onClose: () => void;
  onSubmit: (a: ModelAlias, vmId: number, vmName: string) => Promise<void>;
}) {
  const [vm, setVm] = useState('');
  const [vmName, setVmName] = useState('');
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (!alias) return;
    setVm(alias.virtual_model_id && alias.status !== 'ignored' ? String(alias.virtual_model_id) : '');
    setVmName(alias.virtual_model ?? '');
  }, [alias]);

  return (
    <FormModal
      open={!!alias}
      onClose={onClose}
      title="映射到虚拟模型"
      description={alias ? `${alias.namespace} · ${alias.external_label}${alias.variant ? `（档位 ${alias.variant}）` : ''}` : undefined}
      onSubmit={async () => {
        if (!alias || !vm) return;
        setSubmitting(true);
        try {
          await onSubmit(alias, Number(vm), vmName || `#${vm}`);
        } finally {
          setSubmitting(false);
        }
      }}
      submitting={submitting}
      submitDisabled={!vm}
      submitLabel="确认映射"
    >
      <Field label="虚拟模型" required hint="保存为人工确认，之后的导入一律使用这个映射">
        <RemoteSelect
          className="w-full"
          value={vm}
          placeholder="搜索虚拟模型…"
          load={searchVirtualModels}
          resolve={virtualModelLabel}
          onChange={(v, opt) => {
            setVm(v);
            setVmName(opt?.label ?? '');
          }}
        />
      </Field>
    </FormModal>
  );
}
