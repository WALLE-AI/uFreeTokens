import { useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router';
import { ExternalLink, Plus } from 'lucide-react';
import { deleteBenchmarkRun, getBenchmark, getBenchmarkRun, publishBenchmarkRun, updateBenchmark } from '../../api/benchmarks';
import { describeError } from '../../api/errors';
import { AuditTimeline } from '../../components/audit/AuditTimeline';
import {
  ActionMenu,
  AnchorNav,
  Button,
  ConfirmDialog,
  DataState,
  DataTable,
  EmptyState,
  HeroStat,
  InfoGrid,
  Section,
  StatusBadge,
  StickyActionBar,
  useToast,
  type Column,
  type MenuItem,
} from '../../components/ui';
import { Can } from '../../components/ui/Can';
import { useCan } from '../../api/auth';
import { useAsync } from '../../hooks/useAsync';
import { useQueryParams } from '../../hooks/useQueryState';
import { cn } from '../../lib/cn';
import { formatDateTime, formatRelative } from '../../lib/time';
import type { BenchmarkDetail, BenchmarkResult, BenchmarkRun, BenchmarkStatus } from '../../types';
import { BenchmarkFormModal } from './BenchmarkFormModal';
import {
  BENCHMARK_ORIGIN_LABELS,
  categoryLabel,
  formatCostMicro,
  formatDurationMs,
  formatErrorRate,
  formatScore,
  runState,
  scoreKeyLabel,
} from './benchmarkShared';

// 基准测试详情：基准定义 + run 列表（新的在前）+ 选中 run 的成绩表。
// 选中的 run 存在 URL ?run=，默认选当前已发布的 run，没有则选最新的一个。

const ANCHORS = [
  { id: 'basic', label: '基本信息' },
  { id: 'runs', label: '评测 run' },
  { id: 'results', label: '成绩' },
  { id: 'audit', label: '操作记录' },
];

const STATUS_ACTIONS: Record<BenchmarkStatus, { label: string; body: string; danger: boolean }> = {
  published: {
    label: '发布基准',
    body: '基准将出现在公开基准测试页（GET /v1/benchmarks），展示其最新一次已发布 run 的成绩。',
    danger: false,
  },
  draft: {
    label: '改回草稿',
    body: '基准将从公开基准测试页下线，已录入的 run 与成绩保留。',
    danger: true,
  },
  archived: {
    label: '归档',
    body: '基准将从公开基准测试页下线并标记为已归档，已录入的 run 与成绩保留，可随时恢复。',
    danger: true,
  },
};

export default function BenchmarkDetailPage() {
  const id = Number(useParams().id);
  const navigate = useNavigate();
  const toast = useToast();
  const canWrite = useCan('catalog:write');
  const [qp, setQP] = useQueryParams();
  const detail = useAsync((signal) => getBenchmark(id, signal), [id]);
  const [auditKey, setAuditKey] = useState(0);

  const b = detail.data;
  const runs = b?.runs ?? [];
  const selectedId = qp.run ? Number(qp.run) : (runs.find((r) => r.published) ?? runs[0])?.id;
  const run = useAsync((signal) => (selectedId ? getBenchmarkRun(selectedId, signal) : Promise.resolve(null)), [selectedId]);

  const [editing, setEditing] = useState(false);
  const [statusTarget, setStatusTarget] = useState<BenchmarkStatus | null>(null);
  const [publishTarget, setPublishTarget] = useState<BenchmarkRun | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<BenchmarkRun | null>(null);
  const [busy, setBusy] = useState(false);

  const refresh = () => {
    detail.reload();
    run.reload();
    setAuditKey((k) => k + 1);
  };

  const changeStatus = async () => {
    if (!statusTarget) return;
    setBusy(true);
    try {
      await updateBenchmark(id, { status: statusTarget });
      toast.success(`已${STATUS_ACTIONS[statusTarget].label}`);
      setStatusTarget(null);
      refresh();
    } catch (err) {
      toast.error('修改状态失败', describeError(err));
    } finally {
      setBusy(false);
    }
  };

  const publishRun = async () => {
    if (!publishTarget) return;
    setBusy(true);
    try {
      await publishBenchmarkRun(publishTarget.id);
      toast.success(`run #${publishTarget.id} 已发布`);
      setPublishTarget(null);
      setQP({ run: String(publishTarget.id) }, { keepPage: true });
      refresh();
    } catch (err) {
      toast.error('发布失败', describeError(err));
    } finally {
      setBusy(false);
    }
  };

  const deleteRun = async () => {
    if (!deleteTarget) return;
    setBusy(true);
    try {
      await deleteBenchmarkRun(deleteTarget.id);
      toast.success(`run #${deleteTarget.id} 已删除`);
      setDeleteTarget(null);
      if (selectedId === deleteTarget.id) setQP({ run: null }, { keepPage: true });
      refresh();
    } catch (err) {
      toast.error('删除失败', describeError(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <DataState loading={detail.loading} error={detail.error} onRetry={detail.reload} skeleton="cards">
      {b && (
        <div>
          <StickyActionBar
            backTo="/benchmarks"
            backLabel="基准测试"
            title={
              <span className="font-sans">
                {b.name} <span className="text-gray-400 font-mono font-normal">{b.slug}</span>
              </span>
            }
            badges={
              <>
                <StatusBadge kind="benchmark" value={b.status} />
                {!b.public_display && <PrivateTag />}
                {b.data_source_id && <ImportTag />}
                {detail.refreshing && <span className="text-[11px] text-gray-400">刷新中…</span>}
              </>
            }
            actions={
              <Can perm="catalog:write">
                <Button onClick={() => setEditing(true)}>编辑</Button>
                <Button variant="primary" icon={<Plus className="w-3.5 h-3.5" />} onClick={() => navigate(`/benchmarks/${b.id}/runs/new`)}>
                  录入 run
                </Button>
                <ActionMenu
                  items={(['published', 'draft', 'archived'] as BenchmarkStatus[])
                    .filter((s) => s !== b.status)
                    .map(
                      (s): MenuItem => ({
                        label: s === 'draft' && b.status === 'archived' ? '恢复为草稿' : STATUS_ACTIONS[s].label,
                        danger: STATUS_ACTIONS[s].danger,
                        onClick: () => setStatusTarget(s),
                      }),
                    )}
                />
              </Can>
            }
          />

          {!b.public_display && (
            <div className="bg-amber-50/80 border border-amber-200 rounded-xl p-4 text-xs text-amber-900 mb-6">
              该基准的数据源许可不允许对外展示：成绩只在后台可见，不会出现在公开基准测试页，也不会投影进模型评分。
            </div>
          )}

          {b.status === 'published' && !runs.some((r) => r.published) && (
            <div className="bg-amber-50/80 border border-amber-200 rounded-xl p-4 text-xs text-amber-900 mb-6">
              基准已发布，但还没有已发布的 run：公开页上该基准没有成绩可展示。请录入并发布一个 run。
            </div>
          )}

          <BenchmarkHero b={b} />

          <div className="flex gap-8">
            <AnchorNav items={ANCHORS} />
            <div className="flex-1 min-w-0 space-y-10">
              <Section id="basic" title="基本信息">
                <div className="bg-white border border-gray-200 rounded-xl p-5 shadow-xs">
                  <InfoGrid
                    items={[
                      { label: 'ID', value: <span className="font-mono">#{b.id}</span> },
                      { label: 'Slug', value: <span className="font-mono">{b.slug}</span> },
                      { label: '类别', value: categoryLabel(b.category) },
                      { label: '排序', value: <span className="font-mono">{b.sort_order}</span> },
                      {
                        label: '指标',
                        value: (
                          <span className="font-mono">
                            {b.metric_name} · {b.metric_unit} <span className="font-sans text-gray-500">（{b.higher_is_better ? '越高越好' : '越低越好'}）</span>
                          </span>
                        ),
                      },
                      {
                        label: '来源',
                        value: b.source_url ? (
                          <a href={b.source_url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-purple-600 hover:text-purple-700">
                            {b.source_name || b.source_url}
                            <ExternalLink className="w-3 h-3" />
                          </a>
                        ) : (
                          b.source_name || <span className="text-gray-400">自建评测</span>
                        ),
                      },
                      {
                        label: '数据源',
                        value: b.data_source_id ? (
                          <Link to="/pricing/sources?domain=benchmark" className="text-purple-600 hover:text-purple-700">
                            {b.data_source_name || `#${b.data_source_id}`}
                          </Link>
                        ) : (
                          <span className="text-gray-400">手工维护</span>
                        ),
                      },
                      { label: '外部键', value: b.external_key ? <span className="font-mono break-all">{b.external_key}</span> : <span className="text-gray-400">—</span> },
                      {
                        label: '模型名映射',
                        value: b.alias_namespace ? (
                          <Link
                            to={`/catalog/model-aliases?namespace=${encodeURIComponent(b.alias_namespace)}&status=all`}
                            className="font-mono text-purple-600 hover:text-purple-700"
                          >
                            {b.alias_namespace}
                          </Link>
                        ) : (
                          <span className="text-gray-400">—</span>
                        ),
                      },
                      { label: '评分投影', value: b.score_key ? scoreKeyLabel(b.score_key) : <span className="text-gray-400">不投影</span> },
                      { label: '公开展示', value: b.public_display ? <span className="text-emerald-700">允许</span> : <PrivateTag /> },
                      { label: '创建时间', value: formatDateTime(b.created_at) },
                      { label: '更新时间', value: `${formatDateTime(b.updated_at)}（${formatRelative(b.updated_at)}）` },
                    ]}
                  />
                  {b.description && <p className="text-xs text-gray-600 mt-4 pt-4 border-t border-gray-100 whitespace-pre-wrap">{b.description}</p>}
                </div>
              </Section>

              <Section id="runs" title={`评测 run（${runs.length}）`}>
                {runs.length === 0 ? (
                  <EmptyState
                    title="还没有录入任何 run"
                    description="一次 run = 某个时间点对一批模型的评测成绩。支持手工录入、粘贴电子表格或导入 CSV。"
                    action={
                      canWrite ? (
                        <Button variant="primary" size="sm" onClick={() => navigate(`/benchmarks/${b.id}/runs/new`)}>
                          录入 run
                        </Button>
                      ) : undefined
                    }
                  />
                ) : (
                  <RunsTable
                    runs={runs}
                    selectedId={selectedId}
                    canWrite={canWrite}
                    onSelect={(r) => setQP({ run: String(r.id) }, { keepPage: true })}
                    onPublish={setPublishTarget}
                    onDelete={setDeleteTarget}
                  />
                )}
              </Section>

              <Section id="results" title={selectedId ? `成绩 · run #${selectedId}` : '成绩'}>
                {!selectedId ? (
                  <EmptyState title="选择一个 run 查看成绩" />
                ) : (
                  <DataState loading={run.loading} error={run.error} onRetry={run.reload}>
                    {run.data && (
                      <>
                        {run.data.notes && <p className="text-xs text-gray-600 mb-3 whitespace-pre-wrap">备注：{run.data.notes}</p>}
                        <ResultsTable results={run.data.results ?? []} unit={b.metric_unit} currency={run.data.cost_currency} />
                      </>
                    )}
                  </DataState>
                )}
              </Section>

              <Section id="audit" title="操作记录">
                <AuditTimeline targetType="benchmark" targetId={b.id} reloadKey={auditKey} />
              </Section>
            </div>
          </div>

          <BenchmarkFormModal open={editing} onClose={() => setEditing(false)} benchmark={b} onSaved={refresh} />

          {statusTarget && (
            <ConfirmDialog
              open
              onClose={() => setStatusTarget(null)}
              onConfirm={changeStatus}
              loading={busy}
              level={STATUS_ACTIONS[statusTarget].danger ? 'danger' : 'normal'}
              confirmLabel={STATUS_ACTIONS[statusTarget].label}
              title={`${STATUS_ACTIONS[statusTarget].label} · ${b.name}`}
            >
              <p className="text-xs">{STATUS_ACTIONS[statusTarget].body}</p>
              {statusTarget === 'published' && !runs.some((r) => r.published) && (
                <p className="text-[11px] text-amber-700">该基准还没有已发布的 run，公开页上暂时不会有成绩。</p>
              )}
            </ConfirmDialog>
          )}

          {publishTarget && (
            <ConfirmDialog
              open
              onClose={() => setPublishTarget(null)}
              onConfirm={publishRun}
              loading={busy}
              confirmLabel="发布 run"
              title={`发布 run #${publishTarget.id}`}
            >
              <p className="text-xs">
                发布后，公开页上「{b.name}」的成绩将替换为该 run（{formatDateTime(publishTarget.run_at)}，{publishTarget.result_count} 个模型）。
              </p>
              {runs.some((r) => r.published) && (
                <p className="text-[11px] text-gray-500">当前已发布的 run #{runs.find((r) => r.published)?.id} 会自动退为历史，不会被删除。</p>
              )}
              {b.status !== 'published' && <p className="text-[11px] text-amber-700">注意：基准本身仍是「{b.status === 'draft' ? '草稿' : '已归档'}」，需要发布基准后才会出现在公开页。</p>}
            </ConfirmDialog>
          )}

          {deleteTarget && (
            <ConfirmDialog
              open
              onClose={() => setDeleteTarget(null)}
              onConfirm={deleteRun}
              loading={busy}
              level="danger"
              confirmLabel="删除"
              title={`删除草稿 run #${deleteTarget.id}`}
            >
              <p className="text-xs">将删除该 run 及其 {deleteTarget.result_count} 条成绩，不可恢复。只有从未发布过的 run 可以删除。</p>
            </ConfirmDialog>
          )}
        </div>
      )}
    </DataState>
  );
}

function BenchmarkHero({ b }: { b: BenchmarkDetail }) {
  const runs = b.runs ?? [];
  const published = runs.find((r) => r.published);
  const drafts = runs.filter((r) => runState(r) === 'draft').length;
  return (
    <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 mb-8">
      <HeroStat label="类别" value={<span className="font-sans text-base">{categoryLabel(b.category)}</span>} />
      <HeroStat label="指标" value={<span className="text-base">{b.metric_name}</span>} sub={`${b.metric_unit} · ${b.higher_is_better ? '越高越好' : '越低越好'}`} />
      <HeroStat
        label="当前发布"
        value={published ? <span className="text-base">#{published.id}</span> : <span className="text-amber-700 text-sm font-sans">未发布</span>}
        sub={published ? `${formatDateTime(published.run_at).slice(0, 10)} · ${published.result_count} 个模型` : '公开页无成绩'}
      />
      <HeroStat label="run 数" value={runs.length} sub={drafts ? `其中 ${drafts} 个草稿` : '无草稿'} />
    </div>
  );
}

function RunsTable({
  runs,
  selectedId,
  canWrite,
  onSelect,
  onPublish,
  onDelete,
}: {
  runs: BenchmarkRun[];
  selectedId: number | undefined;
  canWrite: boolean;
  onSelect: (r: BenchmarkRun) => void;
  onPublish: (r: BenchmarkRun) => void;
  onDelete: (r: BenchmarkRun) => void;
}) {
  const columns: Column<BenchmarkRun>[] = [
    {
      key: 'id',
      header: 'run',
      width: 'w-16',
      render: (r) => <span className={cn('font-mono', r.id === selectedId ? 'text-purple-700 font-semibold' : 'text-gray-400')}>#{r.id}</span>,
    },
    { key: 'run_at', header: '评测时间', render: (r) => <span className="font-mono text-gray-700">{formatDateTime(r.run_at).slice(0, 16)}</span> },
    { key: 'state', header: '状态', render: (r) => <StatusBadge kind="benchmark_run" value={runState(r)} /> },
    {
      key: 'origin',
      header: '来源',
      render: (r) =>
        r.origin === 'import' ? (
          <span className="inline-flex items-center px-1.5 py-0.5 rounded bg-blue-50 text-blue-700 border border-blue-200 text-[10px] font-medium" title="由评测榜单数据源自动导入">
            {BENCHMARK_ORIGIN_LABELS.import}
          </span>
        ) : (
          <span className="text-gray-600">{BENCHMARK_ORIGIN_LABELS[r.origin] ?? r.origin}</span>
        ),
    },
    { key: 'result_count', header: '模型数', numeric: true, render: (r) => r.result_count },
    { key: 'currency', header: '成本币种', render: (r) => <span className="font-mono text-gray-500">{r.cost_currency}</span> },
    {
      key: 'notes',
      header: '备注',
      render: (r) => (
        <span className="text-gray-500 text-[11px] truncate max-w-56 inline-block align-bottom" title={r.notes}>
          {r.notes || '—'}
        </span>
      ),
    },
    {
      key: 'created_at',
      header: '录入时间',
      render: (r) => (
        <span className="text-gray-500 text-[11px]" title={formatDateTime(r.created_at)}>
          {formatRelative(r.created_at)}
        </span>
      ),
    },
  ];
  return (
    <DataTable
      columns={columns}
      rows={runs}
      rowKey={(r) => r.id}
      onRowClick={onSelect}
      highlightKey={selectedId ?? null}
      rowActions={
        canWrite
          ? [
              { label: '发布此 run', hidden: (r) => r.published, onClick: onPublish },
              { label: '删除', danger: true, hidden: (r) => runState(r) !== 'draft', onClick: onDelete },
            ]
          : undefined
      }
    />
  );
}

function ResultsTable({ results, unit, currency }: { results: BenchmarkResult[]; unit: string; currency: string }) {
  const columns: Column<BenchmarkResult & { rank: number }>[] = [
    { key: 'rank', header: '#', numeric: true, width: 'w-10', render: (r) => <span className="text-gray-400">{r.rank}</span> },
    { key: 'model_label', header: '模型', render: (r) => <span className="text-gray-900">{r.model_label}</span> },
    {
      key: 'virtual_model',
      header: '关联虚拟模型',
      render: (r) =>
        r.virtual_model_id ? (
          <Link to={`/models/${r.virtual_model_id}`} onClick={(e) => e.stopPropagation()} className="font-mono text-purple-600 hover:text-purple-700">
            {r.virtual_model ?? `#${r.virtual_model_id}`}
          </Link>
        ) : (
          <span className="text-gray-400 text-[11px]">未关联</span>
        ),
    },
    { key: 'score', header: '成绩', numeric: true, render: (r) => <span className="text-gray-900 font-semibold">{formatScore(r.score, unit)}</span> },
    {
      key: 'cost',
      header: `单题成本（${currency}）`,
      numeric: true,
      render: (r) => <span title={r.cost_per_task_micro !== null ? `${r.cost_per_task_micro.toLocaleString('en-US')} micro` : undefined}>{formatCostMicro(r.cost_per_task_micro, currency)}</span>,
    },
    { key: 'avg_duration_ms', header: '平均耗时', numeric: true, render: (r) => formatDurationMs(r.avg_duration_ms) },
    { key: 'error_rate', header: '错误率', numeric: true, render: (r) => formatErrorRate(r.error_rate) },
    { key: 'sample_count', header: '样本数', numeric: true, render: (r) => (r.sample_count ?? '—').toLocaleString() },
  ];
  return <DataTable columns={columns} rows={results.map((r, i) => ({ ...r, rank: i + 1 }))} rowKey={(r) => r.model_label} empty="该 run 没有成绩" />;
}

// 来源许可不允许对外展示的基准：只在后台可见
function PrivateTag() {
  return (
    <span className="inline-flex items-center px-1.5 py-0.5 rounded bg-amber-50 text-amber-700 border border-amber-200 text-[10px] font-medium whitespace-nowrap">
      仅后台可见
    </span>
  );
}

function ImportTag() {
  return (
    <span className="inline-flex items-center px-1.5 py-0.5 rounded bg-blue-50 text-blue-700 border border-blue-200 text-[10px] font-medium whitespace-nowrap">
      外部导入
    </span>
  );
}
