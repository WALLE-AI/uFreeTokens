import { describeError } from '../../api/errors';
import { useState } from 'react';
import { Link, useNavigate } from 'react-router';
import { revokeApiKey, searchApiKeys } from '../../api/accounts';
import {
  ConfirmDialog,
  DataState,
  DataTable,
  FilterBar,
  Input,
  PageHeader,
  Pagination,
  Select,
  StatusBadge,
  useToast,
  type ActiveFilter,
  type Column,
} from '../../components/ui';
import { useAsync } from '../../hooks/useAsync';
import { useQueryParams } from '../../hooks/useQueryState';
import { formatDateTime, formatRelative } from '../../lib/time';
import type { ApiKeyListItem } from '../../types';
import { keyLimitsText } from './shared';

// API 密钥全局检索：运营拿到用户发来的 sk-uft-xxxx（前缀或完整 Key）就能定位
// 是哪把 Key、属于哪个账户（UI_DESIGN.md §1.1 / 接口方案 §4.6）。

const KEY_STATUS_OPTIONS = [
  { value: 'active', label: '有效' },
  { value: 'disabled', label: '已停用' },
  { value: 'revoked', label: '已吊销' },
];

export default function ApiKeysPage() {
  const navigate = useNavigate();
  const toast = useToast();
  const [qp, setQP] = useQueryParams();
  const [accountDraft, setAccountDraft] = useState(qp.account_id ?? '');
  const [revoking, setRevoking] = useState<ApiKeyListItem | null>(null);
  const [busy, setBusy] = useState(false);
  const page = Number(qp.page || 1);
  const pageSize = Number(qp.page_size || 20);
  const accountId = /^\d+$/.test(qp.account_id ?? '') ? Number(qp.account_id) : undefined;

  const list = useAsync(
    (signal) => searchApiKeys({ q: qp.q, status: qp.status, account_id: accountId, page, page_size: pageSize }, signal),
    [qp.q, qp.status, accountId, page, pageSize],
  );

  const active: ActiveFilter[] = [];
  if (qp.q) active.push({ key: 'q', label: `搜索: ${qp.q.length > 24 ? `${qp.q.slice(0, 24)}…` : qp.q}`, onRemove: () => setQP({ q: null }) });
  if (qp.status) active.push({ key: 'status', label: `状态: ${KEY_STATUS_OPTIONS.find((o) => o.value === qp.status)?.label ?? qp.status}`, onRemove: () => setQP({ status: null }) });
  if (accountId)
    active.push({
      key: 'account',
      label: `账户 #${accountId}`,
      onRemove: () => {
        setAccountDraft('');
        setQP({ account_id: null });
      },
    });

  const doRevoke = async () => {
    if (!revoking) return;
    setBusy(true);
    try {
      await revokeApiKey(revoking.id);
      toast.success(`已吊销 ${revoking.name}`);
      setRevoking(null);
      list.reload();
    } catch (err) {
      toast.error('吊销失败', describeError(err));
    } finally {
      setBusy(false);
    }
  };

  const columns: Column<ApiKeyListItem>[] = [
    { key: 'prefix', header: '前缀', render: (k) => <span className="font-mono text-gray-900">{k.display_prefix}…</span> },
    { key: 'name', header: '名称', render: (k) => <span className="text-gray-700">{k.name}</span> },
    {
      key: 'account',
      header: '账户',
      render: (k) => (
        <Link to={`/accounts/${k.account_id}`} onClick={(e) => e.stopPropagation()} className="text-purple-600 hover:text-purple-700">
          {k.account_name} <span className="text-gray-400 font-mono">#{k.account_id}</span>
        </Link>
      ),
    },
    { key: 'status', header: '状态', render: (k) => <StatusBadge kind="api_key" value={k.status} /> },
    { key: 'limits', header: '限制', render: (k) => <span className="text-gray-600 font-mono text-[11px]">{keyLimitsText(k)}</span> },
    {
      key: 'last_used',
      header: '最近使用',
      render: (k) => (
        <span className="text-gray-500 text-[11px]" title={formatDateTime(k.last_used_at)}>
          {k.last_used_at ? formatRelative(k.last_used_at) : '从未'}
        </span>
      ),
    },
    { key: 'created', header: '创建时间', render: (k) => <span className="text-gray-500 text-[11px]">{formatDateTime(k.created_at).slice(0, 16)}</span> },
  ];

  return (
    <div>
      <PageHeader title="API 密钥" description="跨账户检索 API Key：可直接粘贴用户发来的完整 Key 或前缀定位" />

      <FilterBar
        search={qp.q ?? ''}
        onSearch={(q) => setQP({ q })}
        searchPlaceholder="Key 名称 / 前缀 / 直接粘贴完整 Key"
        controls={
          <>
            <Select value={qp.status ?? ''} onChange={(e) => setQP({ status: e.target.value || null })} placeholder="全部状态" options={KEY_STATUS_OPTIONS} />
            <div className="w-32">
              <Input
                mono
                placeholder="账户 ID"
                value={accountDraft}
                onChange={(e) => setAccountDraft(e.target.value.replace(/\D/g, ''))}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') setQP({ account_id: accountDraft || null });
                }}
                onBlur={() => setQP({ account_id: accountDraft || null })}
              />
            </div>
          </>
        }
        active={active}
        onClearAll={() => {
          setAccountDraft('');
          setQP({ q: null, status: null, account_id: null });
        }}
      />

      <DataState loading={list.loading} error={list.error} onRetry={list.reload} skeleton="table">
        {list.data && (
          <DataTable
            columns={columns}
            rows={list.data.data}
            rowKey={(k) => k.id}
            onRowClick={(k) => navigate(`/accounts/${k.account_id}#keys`)}
            rowActions={[
              { label: '查看账户', onClick: (k) => navigate(`/accounts/${k.account_id}`) },
              { label: '吊销', danger: true, hidden: (k) => k.status === 'revoked', onClick: setRevoking },
            ]}
            empty={active.length ? '没有匹配的 API Key' : '还没有 API Key'}
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

      <ConfirmDialog
        open={!!revoking}
        onClose={() => setRevoking(null)}
        onConfirm={doRevoke}
        loading={busy}
        level="typed"
        confirmText={revoking?.name}
        confirmLabel="吊销"
        title="吊销 API Key"
      >
        <p className="text-xs">
          账户 <span className="font-medium text-gray-900">{revoking?.account_name}</span> 的 Key <span className="font-mono">{revoking?.display_prefix}…</span>
          吊销后立即失效（网关返回 401），<span className="font-medium text-rose-600">此操作不可逆</span>。
        </p>
      </ConfirmDialog>
    </div>
  );
}
