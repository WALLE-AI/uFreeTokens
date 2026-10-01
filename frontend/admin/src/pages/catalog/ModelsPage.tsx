import { ChipToggleGroup } from '../../components/ui/index';
import { useNavigate } from 'react-router';
import { getCatalogCounts, listVirtualModels } from '../../api/catalog';
import { DataState, DataTable, FilterBar, KpiStrip, PageHeader, Pagination, Select, StatCard, StatusBadge, type ActiveFilter, type Column } from '../../components/ui';
import { useAsync } from '../../hooks/useAsync';
import { useQueryParams } from '../../hooks/useQueryState';
import type { VirtualModelSummary } from '../../types';
import { MarginText, MODEL_STATUS_OPTIONS, MODEL_TYPE_OPTIONS, PriceBriefCell, TIER_OPTIONS, formatContext } from './shared';

// 虚拟模型列表（UI_DESIGN.md §3.1）：KPI 异常计数可点击 = 一键加上对应筛选。

const MISSING_LABELS: Record<string, string> = {
  sell_price: '缺售价',
  metadata: '缺展示元数据',
  channel: '无可用渠道',
};

export default function ModelsPage() {
  const navigate = useNavigate();
  const [qp, setQP] = useQueryParams();
  const page = Number(qp.page || 1);
  const pageSize = Number(qp.page_size || 20);
  const statuses = qp.status ? qp.status.split(',') : [];

  const list = useAsync(
    (signal) =>
      listVirtualModels(
        {
          q: qp.q,
          status: qp.status,
          type: qp.type,
          tier: qp.tier,
          missing: (qp.missing as 'sell_price' | 'metadata' | 'channel' | undefined) ?? '',
          margin: qp.margin === 'negative' ? 'negative' : '',
          sort: qp.sort,
          page,
          page_size: pageSize,
        },
        signal,
      ),
    [qp.q, qp.status, qp.type, qp.tier, qp.missing, qp.margin, qp.sort, page, pageSize],
  );

  // KPI：一次请求取全部计数（GET /catalog/counts）
  const kpi = useAsync(async (signal) => {
    const c = (await getCatalogCounts(signal)).models;
    return { noSell: c.missing_sell_price, noMeta: c.missing_metadata, noChannel: c.no_active_channel, negative: c.negative_margin };
  }, []);

  const active: ActiveFilter[] = [];
  if (qp.q) active.push({ key: 'q', label: `搜索: ${qp.q}`, onRemove: () => setQP({ q: null }) });
  if (statuses.length) {
    const label = statuses.map((s) => MODEL_STATUS_OPTIONS.find((o) => o.value === s)?.label ?? s).join('、');
    active.push({ key: 'status', label: `状态: ${label}`, onRemove: () => setQP({ status: null }) });
  }
  if (qp.type) active.push({ key: 'type', label: `类型: ${qp.type}`, onRemove: () => setQP({ type: null }) });
  if (qp.tier) active.push({ key: 'tier', label: `可见 tier: ${qp.tier}`, onRemove: () => setQP({ tier: null }) });
  if (qp.missing) active.push({ key: 'missing', label: MISSING_LABELS[qp.missing] ?? qp.missing, onRemove: () => setQP({ missing: null }) });
  if (qp.margin === 'negative') active.push({ key: 'margin', label: '负毛利', onRemove: () => setQP({ margin: null }) });

  const columns: Column<VirtualModelSummary>[] = [
    {
      key: 'name',
      header: '模型',
      sortable: true,
      render: (m) => (
        <div className="min-w-0">
          <div className="font-mono text-gray-900 truncate max-w-72" title={m.name}>
            {m.name}
          </div>
          {m.display_name && <div className="text-[11px] text-gray-400 truncate max-w-72">{m.display_name}</div>}
        </div>
      ),
    },
    { key: 'type', header: '类型', render: (m) => <span className="text-gray-600">{m.type}</span> },
    { key: 'status', header: '状态', render: (m) => <StatusBadge kind="virtual_model" value={m.status} /> },
    { key: 'context', header: '上下文', numeric: true, render: (m) => formatContext(m.context_window) },
    { key: 'sell', header: '售价 ¥/1M（入/出）', numeric: true, render: (m) => <PriceBriefCell price={m.sell_price} /> },
    { key: 'min_margin_ratio', header: '最低毛利', numeric: true, sortable: true, render: (m) => <MarginText ratio={m.min_margin_ratio} /> },
    {
      key: 'channel_count',
      header: '渠道',
      numeric: true,
      sortable: true,
      render: (m) => (
        <span className={m.active_channel_count === 0 ? 'text-rose-600' : ''}>
          {m.active_channel_count}
          <span className="text-gray-300">/{m.channel_count}</span>
        </span>
      ),
    },
    {
      key: 'meta',
      header: '元数据',
      align: 'center',
      render: (m) => (m.has_metadata ? <span className="text-emerald-600">✓</span> : <span className="text-amber-700 text-[11px]">未录入</span>),
    },
  ];

  const k = kpi.data;
  return (
    <div>
      <PageHeader title="虚拟模型" description="对外提供的模型目录：售价、可见范围、展示元数据与路由渠道" />

      <KpiStrip>
        <StatCard
          label="缺售价"
          value={k ? k.noSell : '—'}
          warning={!!k && k.noSell > 0}
          sub="无生效售价的模型无法计费"
          onClick={() => setQP({ missing: 'sell_price' })}
        />
        <StatCard
          label="缺展示元数据"
          value={k ? k.noMeta : '—'}
          warning={!!k && k.noMeta > 0}
          sub="公开目录将回退到默认文案"
          onClick={() => setQP({ missing: 'metadata' })}
        />
        <StatCard
          label="无可用渠道"
          value={k ? k.noChannel : '—'}
          warning={!!k && k.noChannel > 0}
          sub="请求会直接失败"
          onClick={() => setQP({ missing: 'channel' })}
        />
        <StatCard
          label="负毛利模型"
          value={k ? k.negative : '—'}
          warning={!!k && k.negative > 0}
          sub="至少一个渠道成本高于售价"
          onClick={() => setQP({ margin: 'negative', missing: null })}
        />
      </KpiStrip>

      <FilterBar
        search={qp.q ?? ''}
        onSearch={(q) => setQP({ q })}
        searchPlaceholder="搜索模型名、别名、展示名…"
        controls={
          <>
            <ChipToggleGroup options={MODEL_STATUS_OPTIONS} value={statuses} onChange={(v) => setQP({ status: v.join(',') || null })} />
            <Select value={qp.type ?? ''} onChange={(e) => setQP({ type: e.target.value || null })} placeholder="全部类型" options={MODEL_TYPE_OPTIONS} />
            <Select value={qp.tier ?? ''} onChange={(e) => setQP({ tier: e.target.value || null })} placeholder="全部 tier" options={TIER_OPTIONS} />
            <Select
              value={qp.missing ?? ''}
              onChange={(e) => setQP({ missing: e.target.value || null })}
              placeholder="缺失项：不限"
              options={Object.entries(MISSING_LABELS).map(([value, label]) => ({ value, label }))}
            />
          </>
        }
        active={active}
        onClearAll={() => setQP({ q: null, status: null, type: null, tier: null, missing: null, sort: null })}
      />

      <DataState loading={list.loading} error={list.error} onRetry={list.reload}>
        {list.data && (
          <DataTable
            columns={columns}
            rows={list.data.data}
            rowKey={(m) => m.id}
            onRowClick={(m) => navigate(`/models/${m.id}`)}
            sort={qp.sort}
            onSortChange={(s) => setQP({ sort: s || null })}
            empty={active.length ? '没有符合条件的模型' : '还没有虚拟模型——通过"接入新供应商"或"待上架模型"创建'}
            footer={
              <Pagination
                page={list.data.page}
                pageSize={list.data.page_size}
                total={list.data.total}
                onPageChange={(p) => setQP({ page: String(p) }, { keepPage: true })}
                onPageSizeChange={(s) => setQP({ page_size: String(s) })}
              />
            }
          />
        )}
      </DataState>
    </div>
  );
}
