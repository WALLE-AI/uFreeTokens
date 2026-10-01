import { useState } from 'react';
import { useNavigate } from 'react-router';
import { Plus } from 'lucide-react';
import { listBenchmarks } from '../../api/benchmarks';
import { Button, DataState, DataTable, FilterBar, PageHeader, Select, StatusBadge, type ActiveFilter, type Column } from '../../components/ui';
import { Can } from '../../components/ui/Can';
import { useAsync } from '../../hooks/useAsync';
import { useQueryParams } from '../../hooks/useQueryState';
import { formatDateTime, formatRelative } from '../../lib/time';
import type { BenchmarkSummary } from '../../types';
import { BenchmarkFormModal } from './BenchmarkFormModal';
import { BENCHMARK_CATEGORY_OPTIONS, BENCHMARK_STATUS_OPTIONS, categoryLabel } from './benchmarkShared';

// 基准测试列表（技术方案 §3.5）：基准定义 + 当前已发布 run 摘要。列表不分页（基准数量有限），
// 类别 / 状态走服务端筛选，搜索在本地按名称、slug 过滤。

export default function BenchmarksPage() {
  const navigate = useNavigate();
  const [qp, setQP] = useQueryParams();
  const [creating, setCreating] = useState(false);

  const list = useAsync((signal) => listBenchmarks({ category: qp.category, status: qp.status }, signal), [qp.category, qp.status]);

  const q = (qp.q ?? '').toLowerCase();
  const rows = (list.data?.data ?? []).filter(
    (b) => !q || b.name.toLowerCase().includes(q) || b.slug.includes(q) || (b.external_key ?? '').toLowerCase().includes(q),
  );

  const active: ActiveFilter[] = [];
  if (qp.q) active.push({ key: 'q', label: `搜索: ${qp.q}`, onRemove: () => setQP({ q: null }) });
  if (qp.category) active.push({ key: 'category', label: `类别: ${categoryLabel(qp.category)}`, onRemove: () => setQP({ category: null }) });
  if (qp.status)
    active.push({
      key: 'status',
      label: `状态: ${BENCHMARK_STATUS_OPTIONS.find((o) => o.value === qp.status)?.label ?? qp.status}`,
      onRemove: () => setQP({ status: null }),
    });

  const columns: Column<BenchmarkSummary>[] = [
    {
      key: 'name',
      header: '基准',
      render: (b) => (
        <div className="min-w-0">
          <div className="flex items-center gap-1.5">
            <span className="text-gray-900 truncate max-w-72">{b.name}</span>
            {!b.public_display && (
              <span className="shrink-0 px-1.5 py-0.5 rounded bg-amber-50 text-amber-700 border border-amber-200 text-[10px] font-medium">仅后台可见</span>
            )}
          </div>
          <div className="text-[11px] text-gray-400 font-mono truncate max-w-72">
            {b.slug}
            {b.data_source_id && <span className="font-sans text-blue-600"> · 导入自 {b.data_source_name || `数据源 #${b.data_source_id}`}</span>}
          </div>
        </div>
      ),
    },
    { key: 'category', header: '类别', render: (b) => <span className="text-gray-600">{categoryLabel(b.category)}</span> },
    {
      key: 'metric',
      header: '指标',
      render: (b) => (
        <span className="font-mono text-gray-600">
          {b.metric_name}
          <span className="text-gray-400"> · {b.metric_unit}</span>
          <span className="text-gray-400 font-sans ml-1" title={b.higher_is_better ? '分数越高越好' : '分数越低越好'}>
            {b.higher_is_better ? '↑' : '↓'}
          </span>
        </span>
      ),
    },
    { key: 'status', header: '状态', render: (b) => <StatusBadge kind="benchmark" value={b.status} /> },
    {
      key: 'score_key',
      header: '评分投影',
      render: (b) => (b.score_key ? <span className="font-mono text-[11px] text-gray-600">{b.score_key}</span> : <span className="text-gray-300">—</span>),
    },
    {
      key: 'published_run',
      header: '已发布 run',
      render: (b) =>
        b.published_run_id ? (
          <span className="text-gray-700 text-[11px]" title={formatDateTime(b.published_run_at)}>
            <span className="font-mono text-gray-400">#{b.published_run_id}</span> · {formatDateTime(b.published_run_at).slice(0, 10)} · {b.published_result_count} 个模型
          </span>
        ) : (
          <span className="text-amber-700 text-[11px]">未发布</span>
        ),
    },
    { key: 'run_count', header: 'run 数', numeric: true, render: (b) => b.run_count },
    { key: 'sort_order', header: '排序', numeric: true, render: (b) => <span className="text-gray-500">{b.sort_order}</span> },
    {
      key: 'updated_at',
      header: '更新时间',
      render: (b) => (
        <span className="text-gray-500 text-[11px]" title={formatDateTime(b.updated_at)}>
          {formatRelative(b.updated_at)}
        </span>
      ),
    },
  ];

  return (
    <div>
      <PageHeader
        title="基准测试"
        description="公开基准测试页的数据：基准定义、评测 run 的录入与发布（每个基准只展示最新一次已发布的 run）"
        actions={
          <Can perm="catalog:write">
            <Button variant="primary" icon={<Plus className="w-3.5 h-3.5" />} onClick={() => setCreating(true)}>
              新建基准
            </Button>
          </Can>
        }
      />

      <FilterBar
        search={qp.q ?? ''}
        onSearch={(v) => setQP({ q: v })}
        searchPlaceholder="搜索名称、slug 或外部键…"
        controls={
          <>
            <Select value={qp.category ?? ''} onChange={(e) => setQP({ category: e.target.value || null })} placeholder="全部类别" options={BENCHMARK_CATEGORY_OPTIONS} />
            <Select value={qp.status ?? ''} onChange={(e) => setQP({ status: e.target.value || null })} placeholder="全部状态" options={BENCHMARK_STATUS_OPTIONS} />
          </>
        }
        active={active}
        onClearAll={() => setQP({ q: null, category: null, status: null })}
      />

      <DataState loading={list.loading} error={list.error} onRetry={list.reload}>
        {list.data && (
          <DataTable
            columns={columns}
            rows={rows}
            rowKey={(b) => b.id}
            onRowClick={(b) => navigate(`/benchmarks/${b.id}`)}
            empty={active.length ? '没有符合条件的基准' : '还没有基准测试——点击右上角"新建基准"'}
          />
        )}
      </DataState>

      <BenchmarkFormModal open={creating} onClose={() => setCreating(false)} onSaved={(b) => navigate(`/benchmarks/${b.id}`)} />
    </div>
  );
}
