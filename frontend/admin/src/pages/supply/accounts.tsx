import { describeError } from '../../api/errors';
import { useEffect, useState } from 'react';
import { Ban, CheckCircle2, Eye, EyeOff, KeyRound, Pencil, Plug, Plus, Trash2 } from 'lucide-react';
import {
  addProviderKey,
  createProviderAccount,
  getProviderAccount,
  listUpstreamModels,
  revokeProviderKey,
  updateProviderAccount,
  updateProviderKey,
} from '../../api/catalog';
import { AuditTimeline } from '../../components/audit/AuditTimeline';
import {
  Button,
  ConfirmDialog,
  DataState,
  DetailDrawer,
  Field,
  FormModal,
  IconButton,
  Input,
  Modal,
  SectionTitle,
  Select,
  StatusBadge,
  useToast,
} from '../../components/ui';
import { useAsync } from '../../hooks/useAsync';
import { formatDateTime } from '../../lib/time';
import type { ActiveStatus, ProviderAccountSummary, ProviderKey, UpstreamModel } from '../../types';
import { upstreamErrorHint } from './common';
import { Can } from '../../components/ui/Can';

// ---------- 新建 / 编辑上游账号 ----------

export function AccountFormModal({
  open,
  onClose,
  providerId,
  account,
  onSaved,
}: {
  open: boolean;
  onClose: () => void;
  providerId: number;
  account?: ProviderAccountSummary | null; // 传入 = 编辑
  onSaved: () => void;
}) {
  const toast = useToast();
  const editing = !!account;
  const [name, setName] = useState('');
  const [baseURL, setBaseURL] = useState('');
  const [region, setRegion] = useState('');
  const [multiplier, setMultiplier] = useState('1');
  const [status, setStatus] = useState<ActiveStatus>('active');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    setName(account?.name ?? '');
    setBaseURL(account?.base_url ?? '');
    setRegion(account?.region ?? '');
    setMultiplier(account?.cost_multiplier ?? '1');
    setStatus(account?.status ?? 'active');
    setError(null);
  }, [open, account]);

  const multNum = Number(multiplier);
  const multInvalid = !(multNum > 0);
  const multChanged = editing && account && Number(account.cost_multiplier) !== multNum;

  const submit = async () => {
    setSubmitting(true);
    setError(null);
    try {
      if (editing && account) {
        await updateProviderAccount(account.id, {
          name: name.trim(),
          base_url: baseURL.trim(),
          region: region.trim(),
          cost_multiplier: multiplier.trim(),
          status,
        });
        toast.success('上游账号已更新');
      } else {
        await createProviderAccount({ provider_id: providerId, name: name.trim(), base_url: baseURL.trim(), cost_multiplier: multiplier.trim() });
        toast.success('上游账号已创建，记得添加密钥');
      }
      onSaved();
      onClose();
    } catch (err) {
      setError(describeError(err));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <FormModal
      open={open}
      onClose={onClose}
      title={editing ? `编辑上游账号 ${account?.name}` : '新增上游账号'}
      onSubmit={submit}
      submitting={submitting}
      submitDisabled={!name.trim() || !baseURL.trim() || multInvalid}
      submitLabel={editing ? '保存' : '创建'}
      error={error}
      width="lg"
    >
      <Field label="名称" required>
        <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="例如 ds-main" autoFocus />
      </Field>
      <Field label="Base URL" required hint="OpenAI 兼容协议填到 /v1，例如 https://api.deepseek.com/v1">
        <Input mono value={baseURL} onChange={(e) => setBaseURL(e.target.value)} placeholder="https://…" />
      </Field>
      {editing && (
        <Field label="区域" hint="可选，仅作标识">
          <Input value={region} onChange={(e) => setRegion(e.target.value)} placeholder="例如 cn-hangzhou" />
        </Field>
      )}
      <Field
        label="成本倍率 cost_multiplier"
        required
        hint="上游计费倍率：1 = 原价，0.8 = 八折合同价"
        error={multInvalid ? '必须是大于 0 的数字' : undefined}
      >
        <Input mono value={multiplier} onChange={(e) => setMultiplier(e.target.value)} invalid={multInvalid} />
      </Field>
      {multChanged && (
        <div className="bg-amber-50/80 border border-amber-200 rounded-xl p-3 text-xs text-amber-900">
          修改倍率会影响该账号下所有渠道（{account?.channel_count} 条）的成本折算与毛利率。
        </div>
      )}
      {editing && (
        <Field label="状态">
          <Select
            value={status}
            onChange={(e) => setStatus(e.target.value as ActiveStatus)}
            options={[
              { value: 'active', label: '启用' },
              { value: 'disabled', label: '停用' },
            ]}
          />
        </Field>
      )}
    </FormModal>
  );
}

// ---------- 添加密钥 ----------

export function AddKeyModal({
  open,
  onClose,
  accountId,
  onSaved,
}: {
  open: boolean;
  onClose: () => void;
  accountId: number;
  onSaved: () => void;
}) {
  const toast = useToast();
  const [secret, setSecret] = useState('');
  const [show, setShow] = useState(false);
  const [weight, setWeight] = useState('100');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (open) {
      setSecret('');
      setShow(false);
      setWeight('100');
      setError(null);
    }
  }, [open]);

  const submit = async () => {
    setSubmitting(true);
    setError(null);
    try {
      const k = await addProviderKey(accountId, { secret: secret.trim(), weight: Number(weight) || 100 });
      toast.success(`已添加密钥 …${k.last4}`);
      onSaved();
      onClose();
    } catch (err) {
      setError(describeError(err));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <FormModal
      open={open}
      onClose={onClose}
      title="添加上游密钥"
      description="密钥用信封加密落库，之后只显示末 4 位"
      onSubmit={submit}
      submitting={submitting}
      submitDisabled={!secret.trim()}
      submitLabel="添加"
      error={error}
    >
      <Field label="密钥" required>
        <div className="relative">
          <Input
            mono
            type={show ? 'text' : 'password'}
            autoComplete="off"
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
            placeholder="sk-…"
            autoFocus
            className="pr-9"
          />
          <IconButton label={show ? '隐藏' : '显示'} className="absolute right-2 top-1/2 -translate-y-1/2" onClick={() => setShow((v) => !v)}>
            {show ? <EyeOff className="w-3.5 h-3.5" /> : <Eye className="w-3.5 h-3.5" />}
          </IconButton>
        </div>
      </Field>
      <Field label="权重" hint="同一账号下多把密钥按权重分流，默认 100">
        <Input mono type="number" min={1} value={weight} onChange={(e) => setWeight(e.target.value)} />
      </Field>
    </FormModal>
  );
}

// ---------- 测试连接 / 查看上游模型 ----------

export function UpstreamModelsModal({ open, onClose, accountId }: { open: boolean; onClose: () => void; accountId: number | null }) {
  const [state, setState] = useState<{ loading: boolean; models?: UpstreamModel[]; error?: string }>({ loading: false });
  const [filter, setFilter] = useState('');

  useEffect(() => {
    if (!open || accountId === null) return;
    const controller = new AbortController();
    setState({ loading: true });
    setFilter('');
    listUpstreamModels(accountId, controller.signal)
      .then((res) => setState({ loading: false, models: res.data }))
      .catch((err) => {
        if (!controller.signal.aborted) setState({ loading: false, error: upstreamErrorHint(err) });
      });
    return () => controller.abort();
  }, [open, accountId]);

  const shown = (state.models ?? []).filter((m) => m.id.toLowerCase().includes(filter.toLowerCase()));

  return (
    <Modal open={open} onClose={onClose} title="测试连接 · 上游模型列表" width="lg" footer={<Button onClick={onClose}>关闭</Button>}>
      {state.loading && <div className="text-gray-400 py-6 text-center">正在调用上游 /models…</div>}
      {state.error && <div className="bg-rose-50 border border-rose-200 text-rose-700 rounded-xl p-3">✗ {state.error}</div>}
      {state.models && (
        <div className="space-y-3">
          <div className="bg-emerald-50 border border-emerald-200 text-emerald-700 rounded-xl p-3">✓ 连通，发现 {state.models.length} 个模型</div>
          <Input placeholder="过滤…" value={filter} onChange={(e) => setFilter(e.target.value)} />
          <ul className="max-h-72 overflow-y-auto divide-y divide-gray-100 border border-gray-200 rounded-xl">
            {shown.map((m) => (
              <li key={m.id} className="px-3 py-1.5 flex items-center justify-between">
                <span className="font-mono text-gray-900">{m.id}</span>
                <span className="text-[11px] text-gray-400">{m.owned_by}</span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </Modal>
  );
}

// ---------- 账号抽屉：密钥管理 ----------

export function AccountDrawer({
  accountId,
  onClose,
  onChanged,
  onEdit,
  onTest,
}: {
  accountId: number | null;
  onClose: () => void;
  onChanged: () => void;
  onEdit: (a: ProviderAccountSummary) => void;
  onTest: (id: number) => void;
}) {
  const toast = useToast();
  const [reloadKey, setReloadKey] = useState(0);
  const detail = useAsync(
    (signal) => (accountId === null ? Promise.resolve(null) : getProviderAccount(accountId, signal)),
    [accountId, reloadKey],
  );
  const [adding, setAdding] = useState(false);
  const [revoking, setRevoking] = useState<ProviderKey | null>(null);
  const [disabling, setDisabling] = useState<ProviderKey | null>(null);
  const [limits, setLimits] = useState<ProviderKey | null>(null);
  const [busy, setBusy] = useState(false);

  const changed = () => {
    setReloadKey((k) => k + 1);
    onChanged();
  };

  const patchKey = async (k: ProviderKey, body: Parameters<typeof updateProviderKey>[1], ok: string) => {
    setBusy(true);
    try {
      await updateProviderKey(k.id, body);
      toast.success(ok);
      changed();
      return true;
    } catch (err) {
      toast.error('修改密钥失败', describeError(err));
      return false;
    } finally {
      setBusy(false);
    }
  };

  const a = detail.data;
  return (
    <DetailDrawer
      open={accountId !== null}
      onClose={onClose}
      title={a ? a.name : '上游账号'}
      subtitle={a && <span className="font-mono">{a.base_url}</span>}
      footer={
        a && (
          <div className="flex items-center gap-2">
            <Button icon={<Pencil className="w-3.5 h-3.5" />} onClick={() => onEdit(a)}>
              编辑账号
            </Button>
            <Button icon={<Plug className="w-3.5 h-3.5" />} onClick={() => onTest(a.id)}>
              测试连接
            </Button>
            <Can perm="provider_key:write"><Button variant="primary" className="ml-auto" icon={<Plus className="w-3.5 h-3.5" />} onClick={() => setAdding(true)}>
              添加密钥
            </Button></Can>
          </div>
        )
      }
    >
      <DataState loading={detail.loading} error={detail.error} onRetry={detail.reload} skeleton="text">
        {a && (
          <div className="space-y-6">
            <div className="grid grid-cols-3 gap-3 text-xs">
              <Info label="状态" value={<StatusBadge kind="provider_account" value={a.status} />} />
              <Info label="成本倍率" value={<span className="font-mono">×{a.cost_multiplier}</span>} />
              <Info label="渠道" value={<span className="font-mono">{a.channel_count}</span>} />
            </div>

            <div>
              <SectionTitle>密钥（{a.keys.length}）</SectionTitle>
              {a.keys.length === 0 ? (
                <div className="bg-amber-50/80 border border-amber-200 rounded-xl p-3 text-xs text-amber-900">
                  该账号还没有密钥，路由无法使用它。点击底部"添加密钥"。
                </div>
              ) : (
                <div className="border border-gray-200 rounded-xl divide-y divide-gray-100">
                  {a.keys.map((k) => (
                    <KeyRow
                      key={k.id}
                      k={k}
                      busy={busy}
                      onWeight={(w) => patchKey(k, { weight: w }, `密钥 …${k.last4} 权重已改为 ${w}`)}
                      onEnable={() => patchKey(k, { status: 'active' }, `已启用密钥 …${k.last4}`)}
                      onDisable={() => setDisabling(k)}
                      onLimits={() => setLimits(k)}
                      onRevoke={() => setRevoking(k)}
                    />
                  ))}
                </div>
              )}
            </div>

            <div>
              <SectionTitle>操作记录</SectionTitle>
              <AuditTimeline targetType="provider_account" targetId={a.id} reloadKey={reloadKey} limit={10} />
            </div>
          </div>
        )}
      </DataState>

      {a && <AddKeyModal open={adding} onClose={() => setAdding(false)} accountId={a.id} onSaved={changed} />}

      <DisableKeyModal
        k={disabling}
        onClose={() => setDisabling(null)}
        onSubmit={async (reason) => {
          if (disabling && (await patchKey(disabling, { status: 'disabled', disabled_reason: reason }, `已停用密钥 …${disabling.last4}`))) {
            setDisabling(null);
          }
        }}
        busy={busy}
      />
      <LimitsModal
        k={limits}
        onClose={() => setLimits(null)}
        busy={busy}
        onSubmit={async (body) => {
          if (limits && (await patchKey(limits, body, '限流设置已更新'))) setLimits(null);
        }}
      />

      <ConfirmDialog
        open={!!revoking}
        onClose={() => setRevoking(null)}
        level="typed"
        confirmText={revoking?.last4}
        confirmLabel="吊销"
        loading={busy}
        title={`吊销密钥 …${revoking?.last4}？`}
        onConfirm={async () => {
          if (!revoking) return;
          setBusy(true);
          try {
            await revokeProviderKey(revoking.id);
            toast.success(`已吊销密钥 …${revoking.last4}`);
            setRevoking(null);
            changed();
          } catch (err) {
            toast.error('吊销失败', describeError(err));
          } finally {
            setBusy(false);
          }
        }}
      >
        <p>吊销<strong>不可逆</strong>：之后这把密钥不能再启用或修改。约 10 秒后（目录快照刷新）路由不再选中它。</p>
        <p className="text-gray-500">如果只是临时不想用，请改为"停用"。</p>
      </ConfirmDialog>
    </DetailDrawer>
  );
}

function Info({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="bg-gray-50 border border-gray-200 rounded-xl p-3">
      <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold mb-1">{label}</div>
      {value}
    </div>
  );
}

function KeyRow({
  k,
  busy,
  onWeight,
  onEnable,
  onDisable,
  onLimits,
  onRevoke,
}: {
  k: ProviderKey;
  busy: boolean;
  onWeight: (w: number) => Promise<boolean>;
  onEnable: () => void;
  onDisable: () => void;
  onLimits: () => void;
  onRevoke: () => void;
}) {
  const [weight, setWeight] = useState(String(k.weight));
  useEffect(() => setWeight(String(k.weight)), [k.weight]);
  const revoked = k.status === 'revoked';
  const commit = () => {
    const w = Number(weight);
    if (!(w > 0) || w === k.weight) {
      setWeight(String(k.weight));
      return;
    }
    void onWeight(w);
  };
  const limitText = [k.rpm_limit && `${k.rpm_limit} RPM`, k.tpm_limit && `${k.tpm_limit} TPM`, k.concurrency_limit && `并发 ${k.concurrency_limit}`]
    .filter(Boolean)
    .join(' · ');
  return (
    <div className="px-3 py-2.5 flex items-center gap-3 text-xs">
      <KeyRound className="w-3.5 h-3.5 text-gray-400 shrink-0" />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="font-mono text-gray-900">…{k.last4}</span>
          <StatusBadge kind="provider_key" value={k.status} />
        </div>
        <div className="text-[11px] text-gray-400 mt-0.5">
          {k.disabled_reason ? <span className="text-amber-700">{k.disabled_reason} · </span> : null}
          {limitText || '不限流'} · 添加于 {formatDateTime(k.created_at).slice(0, 10)}
        </div>
      </div>
      <label className="flex items-center gap-1 text-[11px] text-gray-400">
        权重
        <input
          className="w-14 border border-gray-200 rounded-md px-1.5 py-0.5 font-mono text-xs text-right text-gray-900 focus:outline-none focus:border-purple-500 disabled:bg-gray-50"
          value={weight}
          disabled={revoked || busy}
          onChange={(e) => setWeight(e.target.value.replace(/[^\d]/g, ''))}
          onBlur={commit}
          onKeyDown={(e) => e.key === 'Enter' && (e.target as HTMLInputElement).blur()}
        />
      </label>
      {!revoked && (
        <div className="flex items-center">
          {k.status === 'active' ? (
            <IconButton label="停用" onClick={onDisable} disabled={busy}>
              <Ban className="w-3.5 h-3.5" />
            </IconButton>
          ) : (
            <IconButton label={k.status === 'exhausted' ? '恢复为启用' : '启用'} onClick={onEnable} disabled={busy}>
              <CheckCircle2 className="w-3.5 h-3.5" />
            </IconButton>
          )}
          <IconButton label="限流设置" onClick={onLimits} disabled={busy}>
            <Pencil className="w-3.5 h-3.5" />
          </IconButton>
          <IconButton label="吊销" danger onClick={onRevoke} disabled={busy}>
            <Trash2 className="w-3.5 h-3.5" />
          </IconButton>
        </div>
      )}
    </div>
  );
}

function DisableKeyModal({
  k,
  onClose,
  onSubmit,
  busy,
}: {
  k: ProviderKey | null;
  onClose: () => void;
  onSubmit: (reason: string) => void;
  busy: boolean;
}) {
  const [reason, setReason] = useState('');
  useEffect(() => setReason(''), [k]);
  return (
    <FormModal
      open={!!k}
      onClose={onClose}
      title={`停用密钥 …${k?.last4}`}
      description="停用后路由不再使用这把密钥，之后可以随时重新启用"
      onSubmit={() => onSubmit(reason.trim())}
      submitting={busy}
      submitLabel="停用"
      submitVariant="danger"
    >
      <Field label="停用原因" hint="可选，会显示在密钥列表里，方便同事了解情况">
        <Input value={reason} onChange={(e) => setReason(e.target.value)} placeholder="例如 额度即将用完 / 上游报 401" autoFocus />
      </Field>
    </FormModal>
  );
}

function LimitsModal({
  k,
  onClose,
  onSubmit,
  busy,
}: {
  k: ProviderKey | null;
  onClose: () => void;
  onSubmit: (body: { rpm_limit: number; tpm_limit: number; concurrency_limit: number }) => void;
  busy: boolean;
}) {
  const [rpm, setRpm] = useState('');
  const [tpm, setTpm] = useState('');
  const [conc, setConc] = useState('');
  useEffect(() => {
    setRpm(k?.rpm_limit ? String(k.rpm_limit) : '');
    setTpm(k?.tpm_limit ? String(k.tpm_limit) : '');
    setConc(k?.concurrency_limit ? String(k.concurrency_limit) : '');
  }, [k]);
  const n = (v: string) => Number(v) || 0; // 0 = 清除限制
  return (
    <FormModal
      open={!!k}
      onClose={onClose}
      title={`限流设置 · 密钥 …${k?.last4}`}
      description="留空表示不限制"
      onSubmit={() => onSubmit({ rpm_limit: n(rpm), tpm_limit: n(tpm), concurrency_limit: n(conc) })}
      submitting={busy}
      submitLabel="保存"
    >
      <div className="grid grid-cols-3 gap-3">
        <Field label="RPM">
          <Input mono type="number" min={0} value={rpm} onChange={(e) => setRpm(e.target.value)} placeholder="不限" />
        </Field>
        <Field label="TPM">
          <Input mono type="number" min={0} value={tpm} onChange={(e) => setTpm(e.target.value)} placeholder="不限" />
        </Field>
        <Field label="并发">
          <Input mono type="number" min={0} value={conc} onChange={(e) => setConc(e.target.value)} placeholder="不限" />
        </Field>
      </div>
    </FormModal>
  );
}
