import { useState } from 'react';
import { useNavigate } from 'react-router';
import { Plus } from 'lucide-react';
import { listAccounts } from '../../api/accounts';
import { Button, DataState, DataTable, FilterBar, Money, PageHeader, Pagination, Select, StatusBadge, type ActiveFilter, type Column } from '../../components/ui';
import { useAsync } from '../../hooks/useAsync';
import { useQueryParams } from '../../hooks/useQueryState';
import { formatDateTime, formatRelative } from '../../lib/time';
import type { AccountSummary } from '../../types';
import { CreateAccountModal } from './modals';
import { ACCOUNT_STATUS_OPTIONS, ACCOUNT_TYPE_LABELS, TIER_OPTIONS } from './shared';
import { Can } from '../../components/ui/Can';

// 账户列表（UI_DESIGN.md §5.5 / §3.1）：q 支持账户 ID、owner 邮箱、名称三种检索。

export default function AccountsPage() {
  const navigate = useNavigate();
  const [qp, setQP] = useQueryParams();
  const [creating, setCreating] = useState(false);
  const page = Number(qp.page || 1);
  const pageSize = Number(qp.page_size || 20);

  const list = useAsync(
    (signal) =>
      listAccounts({ q: qp.q, status: qp.status, tier: qp.tier, type: qp.type, sort: qp.sort, page, page_size: pageSize }, signal),
    [qp.q, qp.status, qp.tier, qp.type, qp.sort, page, pageSize],
  );

  const active: ActiveFilter[] = [];
  if (qp.q) active.push({ key: 'q', label: `搜索: ${qp.q}`, onRemove: () => setQP({ q: null }) });
  if (qp.status)
    active.push({ key: 'status', label: `状态: ${ACCOUNT_STATUS_OPTIONS.find((o) => o.value === qp.status)?.label ?? qp.status}`, onRemove: () => setQP({ status: null }) });
  if (qp.tier) active.push({ key: 'tier', label: `Tier: ${qp.tier}`, onRemove: () => setQP({ tier: null }) });
  if (qp.type) active.push({ key: 'type', label: `类型: ${ACCOUNT_TYPE_LABELS[qp.type] ?? qp.type}`, onRemove: () => setQP({ type: null }) });

  const columns: Column<AccountSummary>[] = [
    { key: 'id', header: 'ID', numeric: true, sortable: true, width: 'w-16', render: (a) => <span className="text-gray-400">#{a.id}</span> },
    {
      key: 'name',
      header: '名称',
      sortable: true,
      render: (a) => (
        <div className="min-w-0">
          <div className="text-gray-900 truncate max-w-64">{a.name}</div>
          {a.owner_email && <div className="text-[11px] text-gray-400 font-mono truncate max-w-64">{a.owner_email}</div>}
        </div>
      ),
    },
    { key: 'type', header: '类型', render: (a) => <span className="text-gray-600">{ACCOUNT_TYPE_LABELS[a.type] ?? a.type}</span> },
    { key: 'tier', header: 'Tier', render: (a) => <span className="font-mono text-gray-600">{a.tier}</span> },
    { key: 'status', header: '状态', render: (a) => <StatusBadge kind="account" value={a.status} /> },
    { key: 'cash_balance', header: '现金', numeric: true, sortable: true, render: (a) => <Money micro={a.cash_balance_micro} className="text-gray-900" /> },
    { key: 'bonus', header: '赠送', numeric: true, render: (a) => <Money micro={a.bonus_balance_micro} className={a.bonus_balance_micro ? 'text-gray-900' : 'text-gray-300'} /> },
    { key: 'frozen', header: '冻结', numeric: true, render: (a) => <Money micro={a.frozen_micro} className={a.frozen_micro ? 'text-amber-700' : 'text-gray-300'} /> },
    { key: 'keys', header: 'Key', numeric: true, render: (a) => a.active_key_count },
    {
      key: 'last_active_at',
      header: '最近活跃',
      sortable: true,
      render: (a) => (
        <span className="text-gray-500 text-[11px]" title={formatDateTime(a.last_active_at)}>
          {a.last_active_at ? formatRelative(a.last_active_at) : '从未'}
        </span>
      ),
    },
    {
      key: 'created_at',
      header: '创建时间',
      sortable: true,
      render: (a) => <span className="text-gray-500 text-[11px]">{formatDateTime(a.created_at).slice(0, 10)}</span>,
    },
  ];

  return (
    <div>
      <PageHeader
        title="账户"
        description="计费主体（个人或组织）：余额、API Key、资金流水与用量"
        actions={
          <Can perm="account:write"><Button variant="primary" icon={<Plus className="w-3.5 h-3.5" />} onClick={() => setCreating(true)}>
            新建账户
          </Button></Can>
        }
      />

      <FilterBar
        search={qp.q ?? ''}
        onSearch={(q) => setQP({ q })}
        searchPlaceholder="账户 ID / 邮箱 / 名称"
        controls={
          <>
            <Select value={qp.status ?? ''} onChange={(e) => setQP({ status: e.target.value || null })} placeholder="全部状态" options={ACCOUNT_STATUS_OPTIONS} />
            <Select value={qp.tier ?? ''} onChange={(e) => setQP({ tier: e.target.value || null })} placeholder="全部 tier" options={TIER_OPTIONS} />
            <Select
              value={qp.type ?? ''}
              onChange={(e) => setQP({ type: e.target.value || null })}
              placeholder="全部类型"
              options={Object.entries(ACCOUNT_TYPE_LABELS).map(([value, label]) => ({ value, label }))}
            />
          </>
        }
        active={active}
        onClearAll={() => setQP({ q: null, status: null, tier: null, type: null })}
      />

      <DataState loading={list.loading} error={list.error} onRetry={list.reload} skeleton="table">
        {list.data && (
          <DataTable
            columns={columns}
            rows={list.data.data}
            rowKey={(a) => a.id}
            onRowClick={(a) => navigate(`/accounts/${a.id}`)}
            sort={qp.sort || '-created_at'}
            onSortChange={(s) => setQP({ sort: s && s !== '-created_at' ? s : null })}
            empty={active.length ? '没有符合条件的账户' : '还没有账户'}
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

      <CreateAccountModal open={creating} onClose={() => setCreating(false)} onCreated={(a) => navigate(`/accounts/${a.id}`)} />
    </div>
  );
}
