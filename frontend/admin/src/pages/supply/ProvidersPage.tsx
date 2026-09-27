import { describeError } from '../../api/errors';
import { useState } from 'react';
import { useNavigate } from 'react-router';
import { Ban, CheckCircle2, Eye, Plus } from 'lucide-react';
import { listProviders, updateProvider } from '../../api/catalog';
import {
  Button,
  ConfirmDialog,
  DataState,
  DataTable,
  FilterBar,
  PageHeader,
  Pagination,
  Select,
  StatusBadge,
  useToast,
  type Column,
} from '../../components/ui';
import { useAsync } from '../../hooks/useAsync';
import { useQueryParams } from '../../hooks/useQueryState';
import type { ProviderSummary } from '../../types';
import { PROTOCOL_OPTIONS, ProtocolBadge } from './common';

const STATUS_OPTIONS = [
  { value: 'active', label: '启用' },
  { value: 'disabled', label: '停用' },
];

// /providers：供应商列表（UI_DESIGN.md §3.1 列表页模板）
export default function ProvidersPage() {
  const navigate = useNavigate();
  const toast = useToast();
  const [params, setParams] = useQueryParams();
  const page = Number(params.page ?? 1) || 1;
  const pageSize = Number(params.page_size ?? 20) || 20;
  const sort = params.sort ?? '';

  const list = useAsync(
    (signal) =>
      listProviders(
        { q: params.q, status: params.status, protocol: params.protocol, sort: sort || undefined, page, page_size: pageSize },
        signal,
      ),
    [params.q, params.status, params.protocol, sort, page, pageSize],
  );

  const [toggling, setToggling] = useState<ProviderSummary | null>(null);
  const [busy, setBusy] = useState(false);

  const doToggle = async () => {
    if (!toggling) return;
    const next = toggling.status === 'active' ? 'disabled' : 'active';
    setBusy(true);
    try {
      const res = await updateProvider(toggling.id, { status: next });
      if (next === 'disabled' && res.affected_active_channels > 0) {
        toast.info(`已停用 ${res.name}；仍有 ${res.affected_active_channels} 条 active 渠道在使用它的上游账号`);
      } else {
        toast.success(next === 'disabled' ? `已停用 ${res.name}` : `已启用 ${res.name}`);
      }
      setToggling(null);
      list.reload();
    } catch (err) {
      toast.error('操作失败', describeError(err));
    } finally {
      setBusy(false);
    }
  };

  const columns: Column<ProviderSummary>[] = [
    {
      key: 'code',
      header: '供应商',
      sortable: true,
      render: (p) => (
        <div className="min-w-0">
          <div className="font-medium text-gray-900 truncate">{p.name}</div>
          <div className="font-mono text-[11px] text-gray-400">{p.code}</div>
        </div>
      ),
    },
    { key: 'protocol', header: '协议', render: (p) => <ProtocolBadge protocol={p.protocol} /> },
    { key: 'account_count', header: '上游账号', numeric: true, render: (p) => p.account_count },
    {
      key: 'active_key_count',
      header: 'active 密钥',
      numeric: true,
      render: (p) => <span className={p.active_key_count === 0 ? 'text-amber-700' : undefined}>{p.active_key_count}</span>,
    },
    { key: 'channel_count', header: '渠道', numeric: true, sortable: true, render: (p) => p.channel_count },
    {
      key: 'pending_listing_count',
      header: '待上架',
      numeric: true,
      render: (p) =>
        p.pending_listing_count > 0 ? (
          <button
            type="button"
            className="text-purple-600 hover:text-purple-700 hover:underline cursor-pointer"
            onClick={(e) => {
              e.stopPropagation();
              navigate(`/pricing/listings?provider_id=${p.id}`);
            }}
          >
            {p.pending_listing_count}
          </button>
        ) : (
          <span className="text-gray-400">0</span>
        ),
    },
    { key: 'status', header: '状态', render: (p) => <StatusBadge kind="provider" value={p.status} /> },
  ];

  const active = [
    params.status && {
      key: 'status',
      label: `状态: ${STATUS_OPTIONS.find((o) => o.value === params.status)?.label ?? params.status}`,
      onRemove: () => setParams({ status: null }),
    },
    params.protocol && {
      key: 'protocol',
      label: `协议: ${PROTOCOL_OPTIONS.find((o) => o.value === params.protocol)?.label ?? params.protocol}`,
      onRemove: () => setParams({ protocol: null }),
    },
  ].filter(Boolean) as Array<{ key: string; label: string; onRemove: () => void }>;

  return (
    <div>
      <PageHeader
        title="供应商"
        description="上游供应商、上游账号与密钥。新接入一家上游请使用接入向导（建供应商 → 账号与密钥 → 选模型 → 定价 → 导入）"
        actions={
          <Button variant="primary" icon={<Plus className="w-3.5 h-3.5" />} onClick={() => navigate('/providers/new')}>
            接入新供应商
          </Button>
        }
      />

      <FilterBar
        search={params.q ?? ''}
        onSearch={(q) => setParams({ q })}
        searchPlaceholder="搜索 code / 名称…"
        controls={
          <>
            <Select
              value={params.status ?? ''}
              placeholder="全部状态"
              options={STATUS_OPTIONS}
              onChange={(e) => setParams({ status: e.target.value })}
            />
            <Select
              value={params.protocol ?? ''}
              placeholder="全部协议"
              options={PROTOCOL_OPTIONS.map((p) => ({ value: p.value, label: p.label }))}
              onChange={(e) => setParams({ protocol: e.target.value })}
            />
          </>
        }
        active={active}
        onClearAll={active.length > 0 || params.q ? () => setParams({ q: null, status: null, protocol: null }) : undefined}
      />

      <DataState
        loading={list.loading}
        error={list.error}
        onRetry={list.reload}
        empty={list.data?.total === 0 && !params.q && active.length === 0}
        emptyTitle="还没有接入任何供应商"
        emptyDescription="使用接入向导添加第一家上游：填写账号与密钥、拉取模型列表、按参考价定价后一键导入"
        emptyAction={
          <Button variant="primary" icon={<Plus className="w-3.5 h-3.5" />} onClick={() => navigate('/providers/new')}>
            接入新供应商
          </Button>
        }
      >
        {list.data && (
          <DataTable
            columns={columns}
            rows={list.data.data}
            rowKey={(p) => p.id}
            onRowClick={(p) => navigate(`/providers/${p.id}`)}
            sort={sort}
            onSortChange={(s) => setParams({ sort: s }, { keepPage: true })}
            empty="没有符合条件的供应商"
            rowActions={[
              { label: '查看详情', icon: <Eye className="w-3.5 h-3.5" />, onClick: (p) => navigate(`/providers/${p.id}`) },
              {
                label: '启用',
                icon: <CheckCircle2 className="w-3.5 h-3.5" />,
                hidden: (p) => p.status === 'active',
                onClick: (p) => setToggling(p),
              },
              {
                label: '停用',
                danger: true,
                icon: <Ban className="w-3.5 h-3.5" />,
                hidden: (p) => p.status !== 'active',
                onClick: (p) => setToggling(p),
              },
            ]}
            footer={
              <Pagination
                page={list.data.page}
                pageSize={list.data.page_size}
                total={list.data.total}
                onPageChange={(p) => setParams({ page: String(p) }, { keepPage: true })}
                onPageSizeChange={(s) => setParams({ page_size: String(s) })}
              />
            }
          />
        )}
      </DataState>

      <ConfirmDialog
        open={!!toggling}
        onClose={() => setToggling(null)}
        onConfirm={doToggle}
        loading={busy}
        level={toggling?.status === 'active' ? 'danger' : 'normal'}
        title={toggling?.status === 'active' ? `停用供应商 ${toggling?.name}？` : `启用供应商 ${toggling?.name}？`}
        confirmLabel={toggling?.status === 'active' ? '停用' : '启用'}
      >
        {toggling?.status === 'active' ? (
          <p>
            停用供应商<strong>不会</strong>级联停用其上游账号和渠道（当前 {toggling.channel_count} 条渠道）。
            如需让流量不再打到这家上游，请同时在渠道页停用相应渠道。
          </p>
        ) : (
          <p>启用后该供应商恢复可用。</p>
        )}
      </ConfirmDialog>
    </div>
  );
}
