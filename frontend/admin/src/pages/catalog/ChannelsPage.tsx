import { Link, useNavigate } from 'react-router';
import { listChannels, listProviders } from '../../api/catalog';
import { DataState, DataTable, FilterBar, KpiStrip, PageHeader, Pagination, Select, StatCard, StatusBadge, type ActiveFilter, type Column } from '../../components/ui';
import { useAsync } from '../../hooks/useAsync';
import { useQueryParams } from '../../hooks/useQueryState';
import type { ChannelSummary } from '../../types';
import { MarginText, PriceBriefCell, formatPrice } from './shared';

// 渠道列表（UI_DESIGN.md §3.1 列表页模板）：虚拟模型 → 上游账号的路由。

export default function ChannelsPage() {
  const navigate = useNavigate();
  const [qp, setQP] = useQueryParams();
  const page = Number(qp.page || 1);
  const pageSize = Number(qp.page_size || 20);
  const providerId = qp.provider_id ? Number(qp.provider_id) : undefined;

  const list = useAsync(
    (signal) =>
      listChannels(
        {
          q: qp.q,
          status: qp.status,
          provider_id: providerId,
          margin: qp.margin === 'negative' ? 'negative' : '',
          missing_cost: qp.missing_cost === 'true' || undefined,
          dedicated: qp.dedicated === 'true' || undefined,
          sort: qp.sort,
          page,
          page_size: pageSize,
        },
        signal,
      ),
    [qp.q, qp.status, providerId, qp.margin, qp.missing_cost, qp.dedicated, qp.sort, page, pageSize],
  );

  const kpi = useAsync(async (signal) => {
    const [act, neg, noCost, ded] = await Promise.all([
      listChannels({ status: 'active', page_size: 1 }, signal),
      listChannels({ margin: 'negative', page_size: 1 }, signal),
      listChannels({ missing_cost: true, page_size: 1 }, signal),
      listChannels({ dedicated: true, page_size: 1 }, signal),
    ]);
    return { active: act.total, negative: neg.total, missingCost: noCost.total, dedicated: ded.total };
  }, []);

  const providers = useAsync((signal) => listProviders({ page_size: 100 }, signal), []);
  const providerOptions = (providers.data?.data ?? []).map((p) => ({ value: String(p.id), label: `${p.name}（${p.code}）` }));

  const active: ActiveFilter[] = [];
  if (qp.q) active.push({ key: 'q', label: `搜索: ${qp.q}`, onRemove: () => setQP({ q: null }) });
  if (qp.status) active.push({ key: 'status', label: `状态: ${qp.status === 'active' ? '启用' : '停用'}`, onRemove: () => setQP({ status: null }) });
  if (providerId) {
    const p = providers.data?.data.find((x) => x.id === providerId);
    active.push({ key: 'provider', label: `供应商: ${p ? p.name : `#${providerId}`}`, onRemove: () => setQP({ provider_id: null }) });
  }
  if (qp.margin === 'negative') active.push({ key: 'margin', label: '负毛利', onRemove: () => setQP({ margin: null }) });
  if (qp.missing_cost === 'true') active.push({ key: 'missing_cost', label: '未设成本价', onRemove: () => setQP({ missing_cost: null }) });
  if (qp.dedicated === 'true') active.push({ key: 'dedicated', label: '专属渠道', onRemove: () => setQP({ dedicated: null }) });

  const columns: Column<ChannelSummary>[] = [
    { key: 'id', header: 'ID', sortable: true, numeric: true, render: (c) => <span className="text-gray-400">#{c.id}</span> },
    {
      key: 'vm',
      header: '虚拟模型',
      render: (c) => (
        <Link to={`/models/${c.virtual_model_id}`} onClick={(e) => e.stopPropagation()} className="font-mono text-gray-900 hover:text-purple-600 truncate inline-block max-w-56" title={c.virtual_model_name}>
          {c.virtual_model_name}
        </Link>
      ),
    },
    {
      key: 'account',
      header: '供应商 / 上游账号',
      render: (c) => (
        <span className="whitespace-nowrap">
          <span className="text-gray-900">{c.provider_code}</span>
          <span className="text-gray-300"> / </span>
          <span className="text-gray-600">{c.provider_account_name}</span>
        </span>
      ),
    },
    { key: 'upstream', header: '上游模型', render: (c) => <span className="font-mono text-gray-600 truncate inline-block max-w-48" title={c.upstream_model}>{c.upstream_model}</span> },
    { key: 'priority', header: '优先级', numeric: true, sortable: true, render: (c) => c.priority },
    { key: 'weight', header: '权重', numeric: true, sortable: true, render: (c) => c.weight },
    {
      key: 'cost',
      header: '成本（原币种）',
      numeric: true,
      render: (c) => <PriceBriefCell price={c.cost_price} />,
    },
    {
      key: 'cost_cny',
      header: '成本 ¥（含倍率）',
      numeric: true,
      render: (c) =>
        !c.cost_price_cny ? (
          <span className="text-gray-400">—</span>
        ) : c.cost_price_cny.fx_missing ? (
          <span className="text-amber-700 text-[11px] font-sans">缺 {c.cost_price?.currency} 汇率</span>
        ) : (
          <span>
            ¥{formatPrice(c.cost_price_cny.input)} <span className="text-gray-300">/</span> ¥{formatPrice(c.cost_price_cny.output)}
          </span>
        ),
    },
    { key: 'sell', header: '售价 ¥', numeric: true, render: (c) => <PriceBriefCell price={c.sell_price} /> },
    { key: 'margin_ratio', header: '毛利率', numeric: true, sortable: true, render: (c) => <MarginText ratio={c.margin_ratio} /> },
    {
      key: 'status',
      header: '状态',
      render: (c) => (
        <span className="inline-flex items-center gap-1">
          <StatusBadge kind="channel" value={c.status} />
          {c.allowed_account_ids && c.allowed_account_ids.length > 0 && (
            <span className="px-1.5 py-0.5 rounded-full text-[10px] border bg-blue-50 text-blue-700 border-blue-200" title={`仅账户 ${c.allowed_account_ids.join(', ')} 可用`}>
              专属
            </span>
          )}
        </span>
      ),
    },
    {
      key: 'pending',
      header: '待审调价',
      render: (c) =>
        c.pending_change_request_id ? (
          <Link
            to={`/pricing/changes?id=${c.pending_change_request_id}`}
            onClick={(e) => e.stopPropagation()}
            className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] border bg-purple-50 text-purple-700 border-purple-200 hover:bg-purple-100"
          >
            #{c.pending_change_request_id}
          </Link>
        ) : (
          <span className="text-gray-300">—</span>
        ),
    },
  ];

  const k = kpi.data;
  return (
    <div>
      <PageHeader title="渠道" description="虚拟模型 → 上游账号的路由，决定请求实际打到哪里；毛利率 = 1 − 成本 CNY ÷ 售价，取输入/输出中较低者" />

      <KpiStrip>
        <StatCard primary label="活跃渠道" value={k ? k.active : '—'} onClick={() => setQP({ status: 'active' })} />
        <StatCard
          label="负毛利渠道"
          value={k ? k.negative : '—'}
          warning={!!k && k.negative > 0}
          sub="成本高于当前售价"
          onClick={() => setQP({ margin: 'negative' })}
        />
        <StatCard
          label="未设成本价"
          value={k ? k.missingCost : '—'}
          warning={!!k && k.missingCost > 0}
          sub="无法计算毛利"
          onClick={() => setQP({ missing_cost: 'true' })}
        />
        <StatCard label="专属渠道" value={k ? k.dedicated : '—'} sub="限定账户白名单" onClick={() => setQP({ dedicated: 'true' })} />
      </KpiStrip>

      <FilterBar
        search={qp.q ?? ''}
        onSearch={(q) => setQP({ q })}
        searchPlaceholder="搜索虚拟模型名、上游模型…"
        controls={
          <>
            <Select
              value={qp.status ?? ''}
              onChange={(e) => setQP({ status: e.target.value || null })}
              placeholder="全部状态"
              options={[
                { value: 'active', label: '启用' },
                { value: 'disabled', label: '停用' },
              ]}
            />
            <Select value={qp.provider_id ?? ''} onChange={(e) => setQP({ provider_id: e.target.value || null })} placeholder="全部供应商" options={providerOptions} />
            <Select
              value={qp.margin === 'negative' ? 'negative' : qp.missing_cost === 'true' ? 'missing_cost' : qp.dedicated === 'true' ? 'dedicated' : ''}
              onChange={(e) => {
                const v = e.target.value;
                setQP({
                  margin: v === 'negative' ? 'negative' : null,
                  missing_cost: v === 'missing_cost' ? 'true' : null,
                  dedicated: v === 'dedicated' ? 'true' : null,
                });
              }}
              placeholder="异常项：不限"
              options={[
                { value: 'negative', label: '负毛利' },
                { value: 'missing_cost', label: '未设成本价' },
                { value: 'dedicated', label: '专属渠道' },
              ]}
            />
          </>
        }
        active={active}
        onClearAll={() => setQP({ q: null, status: null, provider_id: null, margin: null, missing_cost: null, dedicated: null, sort: null })}
      />

      <DataState loading={list.loading} error={list.error} onRetry={list.reload}>
        {list.data && (
          <DataTable
            columns={columns}
            rows={list.data.data}
            rowKey={(c) => c.id}
            onRowClick={(c) => navigate(`/channels/${c.id}`)}
            sort={qp.sort}
            onSortChange={(s) => setQP({ sort: s || null })}
            empty={active.length ? '没有符合条件的渠道' : '还没有渠道——在虚拟模型详情页添加渠道'}
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
