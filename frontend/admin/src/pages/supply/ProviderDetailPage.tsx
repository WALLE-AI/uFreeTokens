import { describeError } from '../../api/errors';
import { useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router';
import { ArrowLeft, Ban, CheckCircle2, KeyRound, PackagePlus, Pencil, Plug, Plus } from 'lucide-react';
import { useCan } from '../../api/auth';
import { getProvider, listChannels, updateProvider } from '../../api/catalog';
import { AuditTimeline } from '../../components/audit/AuditTimeline';
import {
  AnchorNav,
  Button,
  ConfirmDialog,
  DataState,
  DataTable,
  Field,
  FormModal,
  Input,
  ProviderIcon,
  SectionTitle,
  StatusBadge,
  useToast,
  type Column,
} from '../../components/ui';
import { useAsync } from '../../hooks/useAsync';
import type { ChannelSummary, ProviderAccountSummary } from '../../types';
import { MarginText, PriceBriefCell } from '../catalog/shared';
import { AccountDrawer, AccountFormModal, UpstreamModelsModal } from './accounts';
import { ProtocolBadge, Stat } from './common';
import { CreateSourceModal, PriceSourcesTable } from './sources';

// /providers/:id：供应商详情（UI_DESIGN.md §3.2 详情页模板）
export default function ProviderDetailPage() {
  const { id: idParam } = useParams();
  const id = Number(idParam);
  const navigate = useNavigate();
  const toast = useToast();
  const [reloadKey, setReloadKey] = useState(0);
  const detail = useAsync((signal) => getProvider(id, signal), [id, reloadKey]);
  const changed = () => setReloadKey((k) => k + 1);

  const [renaming, setRenaming] = useState(false);
  const [newName, setNewName] = useState('');
  const [toggling, setToggling] = useState(false);
  const [busy, setBusy] = useState(false);
  const [formErr, setFormErr] = useState<string | null>(null);

  const [accountForm, setAccountForm] = useState<{ open: boolean; account: ProviderAccountSummary | null }>({ open: false, account: null });
  const [drawerAccount, setDrawerAccount] = useState<number | null>(null);
  const [testing, setTesting] = useState<number | null>(null);
  const [creatingSource, setCreatingSource] = useState(false);

  const p = detail.data;
  // 供应商的模型 = 其上游账号下的渠道
  const channels = useAsync((signal) => listChannels({ provider_id: id, page_size: 20 }, signal), [id, reloadKey]);
  const canAddModels = useCan('catalog:read');
  const activeAccountCount = p?.accounts.filter((a) => a.status === 'active').length ?? 0;
  const addModelsBlocked = !canAddModels
    ? '没有 catalog:read 权限'
    : p?.status !== 'active'
      ? '供应商已停用'
      : activeAccountCount === 0
        ? '还没有启用的上游账号'
        : null;
  const addModels = (accountId?: number) => navigate(`/providers/${id}/models/add${accountId ? `?account_id=${accountId}` : ''}`);

  const rename = async () => {
    setBusy(true);
    setFormErr(null);
    try {
      await updateProvider(id, { name: newName.trim() });
      toast.success('名称已更新');
      setRenaming(false);
      changed();
    } catch (err) {
      setFormErr(describeError(err));
    } finally {
      setBusy(false);
    }
  };

  const toggleStatus = async () => {
    if (!p) return;
    const next = p.status === 'active' ? 'disabled' : 'active';
    setBusy(true);
    try {
      const res = await updateProvider(id, { status: next });
      if (next === 'disabled' && res.affected_active_channels > 0) {
        toast.info(`已停用；仍有 ${res.affected_active_channels} 条 active 渠道在使用这家供应商的上游账号`);
      } else {
        toast.success(next === 'disabled' ? '已停用' : '已启用');
      }
      setToggling(false);
      changed();
    } catch (err) {
      toast.error('操作失败', describeError(err));
    } finally {
      setBusy(false);
    }
  };

  const accountColumns: Column<ProviderAccountSummary>[] = [
    {
      key: 'name',
      header: '账号',
      render: (a) => (
        <div className="min-w-0">
          <div className="font-medium text-gray-900">{a.name}</div>
          <div className="font-mono text-[11px] text-gray-400 truncate max-w-72">{a.base_url}</div>
        </div>
      ),
    },
    { key: 'cost_multiplier', header: '成本倍率', numeric: true, render: (a) => `×${a.cost_multiplier}` },
    {
      key: 'keys',
      header: '密钥（active / 全部）',
      numeric: true,
      render: (a) => (
        <span className={a.active_key_count === 0 ? 'text-amber-700' : undefined}>
          {a.active_key_count} / {a.key_count}
        </span>
      ),
    },
    {
      key: 'channel_count',
      header: '渠道',
      numeric: true,
      render: (a) =>
        a.channel_count > 0 ? (
          <Link
            to={`/channels?provider_account_id=${a.id}`}
            className="text-purple-600 hover:underline"
            onClick={(e) => e.stopPropagation()}
          >
            {a.channel_count}
          </Link>
        ) : (
          0
        ),
    },
    { key: 'status', header: '状态', render: (a) => <StatusBadge kind="provider_account" value={a.status} /> },
  ];

  const channelColumns: Column<ChannelSummary>[] = [
    {
      key: 'vm',
      header: '虚拟模型',
      render: (c) => (
        <Link to={`/models/${c.virtual_model_id}`} onClick={(e) => e.stopPropagation()} className="font-mono text-gray-900 hover:text-purple-600 truncate inline-block max-w-56" title={c.virtual_model_name}>
          {c.virtual_model_name}
        </Link>
      ),
    },
    { key: 'upstream', header: '上游模型', render: (c) => <span className="font-mono text-gray-600 truncate inline-block max-w-48" title={c.upstream_model}>{c.upstream_model}</span> },
    { key: 'account', header: '上游账号', render: (c) => <span className="text-gray-600 whitespace-nowrap">{c.provider_account_name}</span> },
    { key: 'cost', header: '成本（原币种）', numeric: true, render: (c) => <PriceBriefCell price={c.cost_price} /> },
    { key: 'sell', header: '售价 ¥', numeric: true, render: (c) => <PriceBriefCell price={c.sell_price} /> },
    { key: 'margin', header: '毛利率', numeric: true, render: (c) => <MarginText ratio={c.margin_ratio} /> },
    { key: 'status', header: '状态', render: (c) => <StatusBadge kind="channel" value={c.status} /> },
  ];

  return (
    <DataState loading={detail.loading} error={detail.error} onRetry={detail.reload} skeleton="cards">
      {p && (
        <div>
          {/* 粘性操作栏 */}
          <div className="sticky top-0 z-20 -mx-4 md:-mx-8 px-4 md:px-8 py-3 mb-5 bg-white/95 backdrop-blur-md border-b border-gray-100 flex flex-wrap items-center gap-3">
            <Button variant="ghost" size="sm" icon={<ArrowLeft className="w-3.5 h-3.5" />} onClick={() => navigate('/providers')}>
              供应商
            </Button>
            <div className="flex items-center gap-2 min-w-0">
              <ProviderIcon code={p.code} name={p.name} />
              <h1 className="text-xl font-bold text-gray-900 truncate">{p.name}</h1>
              <span className="font-mono text-xs text-gray-400">{p.code}</span>
              <ProtocolBadge protocol={p.protocol} />
              <StatusBadge kind="provider" value={p.status} />
            </div>
            <div className="ml-auto flex items-center gap-2">
              <span title={addModelsBlocked ?? undefined}>
                <Button variant="primary" icon={<PackagePlus className="w-3.5 h-3.5" />} onClick={() => addModels()} disabled={!!addModelsBlocked}>
                  添加模型
                </Button>
              </span>
              <Button
                icon={<Pencil className="w-3.5 h-3.5" />}
                onClick={() => {
                  setNewName(p.name);
                  setFormErr(null);
                  setRenaming(true);
                }}
              >
                修改名称
              </Button>
              {p.status === 'active' ? (
                <Button icon={<Ban className="w-3.5 h-3.5" />} onClick={() => setToggling(true)}>
                  停用
                </Button>
              ) : (
                <Button icon={<CheckCircle2 className="w-3.5 h-3.5" />} onClick={() => setToggling(true)}>
                  启用
                </Button>
              )}
            </div>
          </div>

          {/* Hero 统计块 */}
          <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 mb-6">
            <Stat label="上游账号" value={p.account_count} />
            <Stat label="active 密钥" value={<span className={p.active_key_count === 0 ? 'text-amber-700' : undefined}>{p.active_key_count}</span>} />
            <Stat label="渠道" value={p.channel_count} />
            <Stat
              label="待上架模型"
              value={
                p.pending_listing_count > 0 ? (
                  <Link to={`/pricing/listings?provider_id=${p.id}`} className="text-purple-600 hover:underline">
                    {p.pending_listing_count}
                  </Link>
                ) : (
                  0
                )
              }
            />
          </div>

          <div className="flex gap-8">
            <AnchorNav
              items={[
                { id: 'accounts', label: '上游账号' },
                { id: 'models', label: '模型 / 渠道' },
                { id: 'sources', label: '价格源' },
                { id: 'audit', label: '操作记录' },
              ]}
            />
            <div className="flex-1 min-w-0 space-y-10">
              <section>
                <SectionTitle
                  id="accounts"
                  actions={
                    <Button size="sm" icon={<Plus className="w-3.5 h-3.5" />} onClick={() => setAccountForm({ open: true, account: null })}>
                      新增上游账号
                    </Button>
                  }
                >
                  上游账号
                </SectionTitle>
                <DataTable
                  columns={accountColumns}
                  rows={p.accounts}
                  rowKey={(a) => a.id}
                  onRowClick={(a) => setDrawerAccount(a.id)}
                  empty="还没有上游账号"
                  rowActions={[
                    { label: '管理密钥', icon: <KeyRound className="w-3.5 h-3.5" />, onClick: (a) => setDrawerAccount(a.id) },
                    { label: '编辑账号', icon: <Pencil className="w-3.5 h-3.5" />, onClick: (a) => setAccountForm({ open: true, account: a }) },
                    { label: '测试连接', icon: <Plug className="w-3.5 h-3.5" />, onClick: (a) => setTesting(a.id) },
                    ...(addModelsBlocked === null
                      ? [{ label: '添加模型', icon: <PackagePlus className="w-3.5 h-3.5" />, onClick: (a: ProviderAccountSummary) => addModels(a.id) }]
                      : []),
                  ]}
                />
              </section>

              <section>
                <SectionTitle
                  id="models"
                  actions={
                    <div className="flex items-center gap-2">
                      {p.channel_count > 0 && (
                        <Link to={`/channels?provider_id=${p.id}`} className="text-xs text-purple-600 hover:underline">
                          查看全部 {p.channel_count} 条
                        </Link>
                      )}
                      <Button size="sm" icon={<PackagePlus className="w-3.5 h-3.5" />} onClick={() => addModels()} disabled={!!addModelsBlocked} title={addModelsBlocked ?? undefined}>
                        添加模型
                      </Button>
                    </div>
                  }
                >
                  模型 / 渠道
                </SectionTitle>
                <DataState loading={channels.loading} error={channels.error} onRetry={channels.reload} skeleton="table">
                  <DataTable
                    columns={channelColumns}
                    rows={channels.data?.data ?? []}
                    rowKey={(c) => c.id}
                    onRowClick={(c) => navigate(`/channels/${c.id}`)}
                    empty="还没有模型，点击「添加模型」从上游列表导入"
                  />
                </DataState>
              </section>

              <section>
                <SectionTitle
                  id="sources"
                  actions={
                    <Button size="sm" icon={<Plus className="w-3.5 h-3.5" />} onClick={() => setCreatingSource(true)}>
                      新增价格源
                    </Button>
                  }
                >
                  价格源
                </SectionTitle>
                <PriceSourcesTable sources={p.price_sources} onChanged={changed} />
              </section>

              <section>
                <SectionTitle id="audit">操作记录</SectionTitle>
                <AuditTimeline targetType="provider" targetId={p.id} reloadKey={reloadKey} />
              </section>
            </div>
          </div>

          <FormModal
            open={renaming}
            onClose={() => setRenaming(false)}
            title="修改供应商名称"
            onSubmit={rename}
            submitting={busy}
            submitDisabled={!newName.trim() || newName.trim() === p.name}
            submitLabel="保存"
            error={formErr}
          >
            <Field label="名称" required hint="code 是对内标识，创建后不可修改">
              <Input value={newName} onChange={(e) => setNewName(e.target.value)} autoFocus />
            </Field>
          </FormModal>

          <ConfirmDialog
            open={toggling}
            onClose={() => setToggling(false)}
            onConfirm={toggleStatus}
            loading={busy}
            level={p.status === 'active' ? 'danger' : 'normal'}
            title={p.status === 'active' ? `停用供应商 ${p.name}？` : `启用供应商 ${p.name}？`}
            confirmLabel={p.status === 'active' ? '停用' : '启用'}
          >
            {p.status === 'active' ? (
              <p>
                停用供应商<strong>不会</strong>级联停用其 {p.account_count} 个上游账号和 {p.channel_count} 条渠道。
                如需让流量不再打到这家上游，请在渠道页停用相应渠道。
              </p>
            ) : (
              <p>启用后该供应商恢复可用。</p>
            )}
          </ConfirmDialog>

          <AccountFormModal
            open={accountForm.open}
            onClose={() => setAccountForm({ open: false, account: null })}
            providerId={p.id}
            account={accountForm.account}
            onSaved={changed}
          />
          <AccountDrawer
            accountId={drawerAccount}
            onClose={() => setDrawerAccount(null)}
            onChanged={changed}
            onEdit={(a) => setAccountForm({ open: true, account: a })}
            onTest={(aid) => setTesting(aid)}
          />
          <UpstreamModelsModal open={testing !== null} onClose={() => setTesting(null)} accountId={testing} />
          <CreateSourceModal open={creatingSource} onClose={() => setCreatingSource(false)} providerId={p.id} onSaved={changed} />
        </div>
      )}
    </DataState>
  );
}
