import { describeError } from '../../api/errors';
import { ActionMenu, HeroStat, InfoGrid, MenuItem, Section, StickyActionBar } from '../../components/ui/index';
import { useEffect, useState } from 'react';
import { useLocation, useParams } from 'react-router';
import { getAccount, updateAccount } from '../../api/accounts';
import { AuditTimeline } from '../../components/audit/AuditTimeline';
import { AnchorNav, Button, ConfirmDialog, DataState, Money, SecretReveal, StatusBadge, useToast } from '../../components/ui';
import { useAsync } from '../../hooks/useAsync';
import { formatDateTime, formatRelative } from '../../lib/time';
import type { AccountStatus, ApiKeyCreated } from '../../types';
import { AdjustWalletModal, CreateApiKeyModal, EditAccountModal, GrantCreditModal } from './modals';
import { ApiKeysSection, GrantsSection, LedgerSection, MembersTable, UsageSection } from './sections';
import { ACCOUNT_TYPE_LABELS } from './shared';

// 账户详情（UI_DESIGN.md §3.2 详情模板 + §5.5 资金操作）。

const ANCHORS = [
  { id: 'overview', label: '概览' },
  { id: 'usage', label: '用量趋势' },
  { id: 'keys', label: 'API 密钥' },
  { id: 'ledger', label: '资金流水' },
  { id: 'grants', label: '赠送余额' },
  { id: 'audit', label: '操作记录' },
];

const STATUS_ACTIONS: Record<AccountStatus, { label: string; title: string; level: 'normal' | 'danger' | 'typed'; body: string }> = {
  active: {
    label: '恢复账户',
    title: '恢复账户',
    level: 'normal',
    body: '恢复后该账户的 API Key 可以重新调用网关。',
  },
  suspended: {
    label: '暂停账户',
    title: '暂停账户',
    level: 'danger',
    body: '暂停后该账户所有 API Key 的请求都会被网关拒绝（403 account_suspended），余额与配置保留，可随时恢复。',
  },
  closed: {
    label: '关闭账户',
    title: '关闭账户',
    level: 'typed',
    body: '关闭后该账户所有 API Key 的请求都会被网关拒绝（403）。关闭表示终止服务，请确认余额已处理（退款或清零）。',
  },
};

export default function AccountDetailPage() {
  const id = Number(useParams().id);
  const location = useLocation();
  const toast = useToast();
  const res = useAsync((signal) => getAccount(id, signal), [id]);

  // 写操作后递增：刷新密钥、流水、赠送、操作记录
  const [version, setVersion] = useState(0);
  const bump = () => setVersion((v) => v + 1);
  const [highlightRef, setHighlightRef] = useState<string | null>(null);

  const [adjusting, setAdjusting] = useState(false);
  const [granting, setGranting] = useState(false);
  const [editing, setEditing] = useState(false);
  const [creatingKey, setCreatingKey] = useState(false);
  const [createdKey, setCreatedKey] = useState<ApiKeyCreated | null>(null);
  const [statusTarget, setStatusTarget] = useState<AccountStatus | null>(null);
  const [statusBusy, setStatusBusy] = useState(false);

  // 从 /api-keys 跳转过来时带 #keys：数据加载完后滚动到对应段落
  useEffect(() => {
    if (!res.data || !location.hash) return;
    const el = document.getElementById(location.hash.slice(1));
    if (el) setTimeout(() => el.scrollIntoView({ behavior: 'smooth', block: 'start' }), 50);
  }, [res.data, location.hash]);

  const changeStatus = async () => {
    if (!statusTarget) return;
    setStatusBusy(true);
    try {
      await updateAccount(id, { status: statusTarget });
      toast.success(`账户已${STATUS_ACTIONS[statusTarget].label.replace('账户', '')}`);
      setStatusTarget(null);
      res.reload();
      bump();
    } catch (err) {
      toast.error('操作失败', describeError(err));
    } finally {
      setStatusBusy(false);
    }
  };

  return (
    <DataState loading={res.loading} error={res.error} onRetry={res.reload} skeleton="cards">
      {res.data &&
        (() => {
          const { account: a, wallet: w, members, active_grants_summary: gs } = res.data;
          const menu: MenuItem[] = [
            { label: '编辑账户', onClick: () => setEditing(true) },
            ...(['active', 'suspended', 'closed'] as AccountStatus[])
              .filter((s) => s !== a.status)
              .map((s) => ({ label: STATUS_ACTIONS[s].label, danger: s !== 'active', onClick: () => setStatusTarget(s) })),
          ];
          return (
            <div>
              <StickyActionBar
                backTo="/accounts"
                backLabel="账户"
                title={
                  <span className="font-sans">
                    {a.name} <span className="text-gray-400 font-mono font-normal">#{a.id}</span>
                  </span>
                }
                badges={<StatusBadge kind="account" value={a.status} />}
                actions={
                  <>
                    <Button onClick={() => setGranting(true)}>发放赠送</Button>
                    <Button variant="primary" onClick={() => setAdjusting(true)}>
                      人工调账
                    </Button>
                    <ActionMenu items={menu} />
                  </>
                }
              />

              {a.status !== 'active' && (
                <div className="bg-amber-50/80 border border-amber-200 rounded-xl p-4 text-xs text-amber-900 mb-6">
                  该账户{a.status === 'suspended' ? '已暂停' : '已关闭'}：所有 API Key 的请求都会被网关拒绝（403）。
                </div>
              )}

              <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 mb-8">
                <div className="bg-purple-50/60 border border-purple-100 rounded-xl p-4">
                  <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold">现金余额</div>
                  <div className="text-2xl font-bold text-gray-900 mt-1">
                    <Money micro={w.cash_balance_micro} />
                  </div>
                  <div className="text-[11px] text-gray-500 mt-0.5">可用于所有模型</div>
                </div>
                <HeroStat
                  label="赠送余额"
                  value={<Money micro={w.bonus_balance_micro} />}
                  sub={
                    gs.count > 0
                      ? `${gs.count} 笔有效${gs.nearest_expires_at ? ` · 最近 ${formatDateTime(gs.nearest_expires_at).slice(0, 10)} 到期` : ''}`
                      : '无有效赠款'
                  }
                />
                <HeroStat label="冻结中" value={<Money micro={w.frozen_micro} />} sub="进行中请求的预扣" />
                <HeroStat label="授信额度" value={<Money micro={a.credit_limit_micro} />} sub={a.credit_limit_micro ? '余额不足时可透支' : '未授信'} />
              </div>

              <div className="flex gap-8">
                <AnchorNav items={ANCHORS} />
                <div className="flex-1 min-w-0 space-y-10">
                  <Section id="overview" title="概览" actions={<Button size="sm" onClick={() => setEditing(true)}>编辑</Button>}>
                    <div className="bg-white border border-gray-200 rounded-xl p-5 shadow-xs mb-4">
                      <InfoGrid
                        items={[
                          { label: '账户 ID', value: <span className="font-mono">#{a.id}</span> },
                          { label: '类型', value: ACCOUNT_TYPE_LABELS[a.type] ?? a.type },
                          { label: 'Tier', value: <span className="font-mono">{a.tier}</span> },
                          { label: '状态', value: <StatusBadge kind="account" value={a.status} /> },
                          { label: '创建时间', value: `${formatDateTime(a.created_at)}（${formatRelative(a.created_at)}）` },
                          { label: '授信额度', value: <Money micro={a.credit_limit_micro} /> },
                        ]}
                      />
                    </div>
                    <div className="text-xs font-medium text-gray-900 mb-2">成员</div>
                    <MembersTable members={members} />
                  </Section>

                  <Section id="usage" title="用量趋势">
                    <UsageSection accountId={a.id} />
                  </Section>

                  <Section
                    id="keys"
                    title="API 密钥"
                    actions={
                      <Button size="sm" onClick={() => setCreatingKey(true)} disabled={a.status !== 'active'}>
                        代开 API Key
                      </Button>
                    }
                  >
                    <ApiKeysSection accountId={a.id} reloadKey={version} onChanged={bump} />
                  </Section>

                  <Section id="ledger" title="资金流水">
                    <LedgerSection accountId={a.id} reloadKey={version} highlightRef={highlightRef} />
                  </Section>

                  <Section id="grants" title="赠送余额">
                    <GrantsSection accountId={a.id} reloadKey={version} />
                  </Section>

                  <Section id="audit" title="操作记录">
                    <AuditTimeline targetType="account" targetId={a.id} reloadKey={version} />
                  </Section>
                </div>
              </div>

              <AdjustWalletModal
                open={adjusting}
                onClose={() => setAdjusting(false)}
                account={a}
                cashMicro={w.cash_balance_micro}
                onBalanceChanged={res.reload}
                onDone={(r) => {
                  setAdjusting(false);
                  setHighlightRef(r.ref_id);
                  res.reload();
                  bump();
                  document.getElementById('ledger')?.scrollIntoView({ behavior: 'smooth', block: 'start' });
                }}
              />
              <GrantCreditModal
                open={granting}
                onClose={() => setGranting(false)}
                account={a}
                onDone={() => {
                  setGranting(false);
                  res.reload();
                  bump();
                }}
              />
              <EditAccountModal
                open={editing}
                onClose={() => setEditing(false)}
                account={a}
                onSaved={() => {
                  res.reload();
                  bump();
                }}
              />
              <CreateApiKeyModal
                open={creatingKey}
                onClose={() => setCreatingKey(false)}
                account={a}
                onCreated={(k) => {
                  setCreatingKey(false);
                  setCreatedKey(k);
                  bump();
                }}
              />
              <SecretReveal
                open={!!createdKey}
                onClose={() => setCreatedKey(null)}
                title={`API Key「${createdKey?.name ?? ''}」已创建`}
                secret={createdKey?.raw_key ?? ''}
              />
              {statusTarget && (
                <ConfirmDialog
                  open
                  onClose={() => setStatusTarget(null)}
                  onConfirm={changeStatus}
                  loading={statusBusy}
                  level={STATUS_ACTIONS[statusTarget].level}
                  confirmText={STATUS_ACTIONS[statusTarget].level === 'typed' ? a.name : undefined}
                  confirmLabel={STATUS_ACTIONS[statusTarget].label}
                  title={`${STATUS_ACTIONS[statusTarget].title} · ${a.name}`}
                >
                  <p className="text-xs">{STATUS_ACTIONS[statusTarget].body}</p>
                  {statusTarget === 'closed' && w.cash_balance_micro > 0 && (
                    <p className="text-[11px] text-amber-700">
                      该账户仍有现金余额 <Money micro={w.cash_balance_micro} />。
                    </p>
                  )}
                </ConfirmDialog>
              )}
            </div>
          );
        })()}
    </DataState>
  );
}
