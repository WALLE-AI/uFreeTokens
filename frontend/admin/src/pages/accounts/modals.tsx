import { RadioCards } from '../../components/ui/index';
import { useEffect, useMemo, useState } from 'react';
import { ArrowRight, ChevronDown, ChevronRight } from 'lucide-react';
import { adjustWallet, createAccount, createApiKey, grantCredit, updateAccount } from '../../api/accounts';
import { listVirtualModels } from '../../api/catalog';
import { ApiError, describeError } from '../../api/errors';
import { newIdempotencyKey } from '../../api/client';
import { Button, Checkbox, ConfirmDialog, Field, FormModal, Input, Money, MoneyInput, SearchInput, Select, Textarea, useToast } from '../../components/ui';
import { useAsync } from '../../hooks/useAsync';
import { cn } from '../../lib/cn';
import type { Account, ApiKeyCreated, GrantSource, Tier, WalletAdjustReceipt } from '../../types';
import { GRANT_SOURCE_OPTIONS, LARGE_ADJUST_MICRO, TIER_OPTIONS, genRefId } from './shared';
import { toApiTime } from '../../lib/tz';

// ---------- 新建账户 ----------

export function CreateAccountModal({ open, onClose, onCreated }: { open: boolean; onClose: () => void; onCreated: (a: Account) => void }) {
  const toast = useToast();
  const [type, setType] = useState<'personal' | 'organization'>('personal');
  const [name, setName] = useState('');
  const [tier, setTier] = useState<Tier>('free');
  const [credit, setCredit] = useState<number | null>(0);
  const [excludePublic, setExcludePublic] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (open) {
      setType('personal');
      setName('');
      setTier('free');
      setCredit(0);
      setExcludePublic(false);
      setError(null);
    }
  }, [open]);

  const submit = async () => {
    setSubmitting(true);
    setError(null);
    try {
      const a = await createAccount({ type, name: name.trim(), tier, credit_limit_micro: credit ?? 0, exclude_from_public_stats: excludePublic });
      toast.success(`账户 #${a.id} 已创建`);
      onCreated(a);
    } catch (err) {
      setError(describeError(err, '创建失败'));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <FormModal
      open={open}
      onClose={onClose}
      title="新建账户"
      description="账户是计费主体，创建时同时初始化一个空钱包"
      onSubmit={submit}
      submitLabel="创建"
      submitting={submitting}
      submitDisabled={!name.trim() || credit === null}
      error={error}
    >
      <Field label="类型" required>
        <RadioCards
          options={[
            { value: 'personal', label: '个人', hint: '单个开发者' },
            { value: 'organization', label: '组织', hint: '团队 / 企业，可配授信' },
          ]}
          value={type}
          onChange={setType}
        />
      </Field>
      <Field label="名称" required>
        <Input autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="例如 张三的团队" />
      </Field>
      <div className="grid grid-cols-2 gap-4">
        <Field label="Tier" hint="决定可见模型与渠道">
          <Select value={tier} onChange={(e) => setTier(e.target.value as Tier)} options={TIER_OPTIONS} />
        </Field>
        <Field label="授信额度" hint="余额不足时可透支的额度，0 = 不授信">
          <MoneyInput valueMicro={credit} onChange={setCredit} />
        </Field>
      </div>
      <label className="flex items-start gap-2 cursor-pointer select-none">
        <span className="pt-0.5">
          <Checkbox checked={excludePublic} onChange={setExcludePublic} label="不计入公开榜单" />
        </span>
        <span>
          <span className="text-xs font-medium text-gray-700">不计入公开榜单</span>
          <span className="block text-[11px] text-gray-400 mt-0.5">内部测试 / 压测 / 评测账户勾选此项，其流量不会出现在公开排行榜中；之后可在账户详情页修改。</span>
        </span>
      </label>
    </FormModal>
  );
}

// ---------- 编辑账户 ----------

export function EditAccountModal({ open, onClose, account, onSaved }: { open: boolean; onClose: () => void; account: Account; onSaved: () => void }) {
  const toast = useToast();
  const [name, setName] = useState(account.name);
  const [tier, setTier] = useState<Tier>(account.tier);
  const [credit, setCredit] = useState<number | null>(account.credit_limit_micro);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (open) {
      setName(account.name);
      setTier(account.tier);
      setCredit(account.credit_limit_micro);
      setError(null);
    }
  }, [open, account]);

  const submit = async () => {
    const body: Parameters<typeof updateAccount>[1] = {};
    if (name.trim() !== account.name) body.name = name.trim();
    if (tier !== account.tier) body.tier = tier;
    if (credit !== null && credit !== account.credit_limit_micro) body.credit_limit_micro = credit;
    if (Object.keys(body).length === 0) {
      onClose();
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      await updateAccount(account.id, body);
      toast.success('账户信息已更新');
      onSaved();
      onClose();
    } catch (err) {
      setError(describeError(err, '保存失败'));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <FormModal
      open={open}
      onClose={onClose}
      title="编辑账户"
      onSubmit={submit}
      submitLabel="保存"
      submitting={submitting}
      submitDisabled={!name.trim() || credit === null}
      error={error}
    >
      <Field label="名称" required>
        <Input value={name} onChange={(e) => setName(e.target.value)} />
      </Field>
      <div className="grid grid-cols-2 gap-4">
        <Field label="Tier" hint={tier !== account.tier ? '修改 tier 会改变该账户可见的模型与渠道' : '决定可见模型与渠道'}>
          <Select value={tier} onChange={(e) => setTier(e.target.value as Tier)} options={TIER_OPTIONS} />
        </Field>
        <Field label="授信额度" hint="余额不足时可透支的额度">
          <MoneyInput valueMicro={credit} onChange={setCredit} />
        </Field>
      </div>
    </FormModal>
  );
}

// ---------- 人工调账（§5.5，风险最高的操作） ----------

export function AdjustWalletModal({
  open,
  onClose,
  account,
  cashMicro,
  onDone,
  onBalanceChanged,
}: {
  open: boolean;
  onClose: () => void;
  account: Account;
  cashMicro: number; // 页面上当前显示的现金余额，作为 expected_cash_balance_micro 提交
  onDone: (r: WalletAdjustReceipt) => void;
  onBalanceChanged: () => void; // 409 balance_changed：刷新账户数据，保留表单
}) {
  const toast = useToast();
  const [kind, setKind] = useState<'credit' | 'debit'>('credit');
  const [amount, setAmount] = useState<number | null>(null);
  const [refId, setRefId] = useState('');
  const [reason, setReason] = useState('');
  const [refErr, setRefErr] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (open) {
      setKind('credit');
      setAmount(null);
      setRefId('');
      setReason('');
      setRefErr(null);
      setError(null);
      setConfirming(false);
    }
  }, [open]);

  const signed = amount === null ? null : kind === 'debit' ? -amount : amount;
  const after = signed === null ? null : cashMicro + signed;
  const negative = after !== null && after < 0;
  const reasonTooLong = [...reason].length > 200;
  const large = amount !== null && amount >= LARGE_ADJUST_MICRO;
  const canSubmit = amount !== null && amount > 0 && !!refId.trim() && !!reason.trim() && !reasonTooLong && !negative;

  const doAdjust = async () => {
    if (signed === null) return;
    setSubmitting(true);
    setError(null);
    try {
      const r = await adjustWallet(account.id, {
        amount_micro: signed,
        ref_id: refId.trim(),
        reason: reason.trim(),
        expected_cash_balance_micro: cashMicro,
      });
      toast.success(`调账成功：现金余额 ¥${(r.cash_after_micro / 1_000_000).toFixed(2)}`);
      setConfirming(false);
      onDone(r);
    } catch (err) {
      setConfirming(false);
      if (err instanceof ApiError && err.status === 409 && err.code === 'balance_changed') {
        setError('余额已发生变化（期间有消费、充值或他人调账），已刷新为最新余额。请核对"操作后余额"后重新提交。');
        onBalanceChanged();
      } else if (err instanceof ApiError && err.status === 409) {
        setRefErr('该单号已用于本账户的调账，请更换单号（防重复提交）');
      } else {
        setError(describeError(err, '调账失败'));
      }
    } finally {
      setSubmitting(false);
    }
  };

  const preview =
    after !== null ? (
      <div className={cn('rounded-xl border px-4 py-3 flex items-center gap-3 text-xs', negative ? 'bg-rose-50 border-rose-200' : 'bg-gray-50 border-gray-200')}>
        <span className="text-gray-500">现金余额</span>
        <Money micro={cashMicro} className="text-gray-900" />
        <ArrowRight className="w-3.5 h-3.5 text-gray-400" />
        <Money micro={after} className={cn('font-semibold', negative ? 'text-rose-700' : 'text-gray-900')} />
        <span className="ml-auto">
          <Money micro={signed} signed />
        </span>
      </div>
    ) : null;

  return (
    <>
      <FormModal
        open={open && !confirming}
        onClose={onClose}
        title={
          <span>
            人工调账 <span className="text-gray-400 font-normal">· 账户 #{account.id} {account.name}</span>
          </span>
        }
        onSubmit={() => setConfirming(true)}
        submitLabel="确认调账"
        submitVariant={kind === 'debit' ? 'danger' : 'primary'}
        submitDisabled={!canSubmit}
        error={error}
      >
        <Field label="类型" required>
          <RadioCards
            options={[
              { value: 'credit', label: '充值 / 补偿', hint: '增加现金余额' },
              { value: 'debit', label: '扣减', hint: '减少现金余额，不能扣成负数', tone: 'danger' },
            ]}
            value={kind}
            onChange={setKind}
          />
        </Field>
        <Field label="金额" required>
          <MoneyInput valueMicro={amount} onChange={setAmount} autoFocus />
        </Field>
        <Field label="关联单号 ref_id" required hint="填工单号；同一账户同一单号只能调一次账" error={refErr ?? undefined}>
          <div className="flex gap-2">
            <Input
              mono
              value={refId}
              invalid={!!refErr}
              onChange={(e) => {
                setRefId(e.target.value);
                setRefErr(null);
              }}
              placeholder="TICKET-5521"
            />
            <Button
              type="button"
              onClick={() => {
                setRefId(genRefId());
                setRefErr(null);
              }}
            >
              生成
            </Button>
          </div>
        </Field>
        <Field label="原因" required error={reasonTooLong ? '原因最多 200 字' : undefined} hint={`${[...reason].length}/200，写入审计日志`}>
          <Textarea rows={2} value={reason} invalid={reasonTooLong} onChange={(e) => setReason(e.target.value)} placeholder="例如：客户投诉补偿，工单 5521" />
        </Field>
        {preview}
        {negative && <p className="text-[11px] text-rose-600">扣减后现金余额为负，不允许提交</p>}
      </FormModal>

      <ConfirmDialog
        open={open && confirming}
        onClose={() => setConfirming(false)}
        onConfirm={doAdjust}
        loading={submitting}
        level={large ? 'typed' : kind === 'debit' ? 'danger' : 'normal'}
        confirmText={large ? account.name : undefined}
        confirmLabel={kind === 'debit' ? '确认扣减' : '确认充值'}
        title={kind === 'debit' ? '确认扣减现金余额' : '确认增加现金余额'}
      >
        <p className="text-xs">
          账户 <span className="font-medium text-gray-900">#{account.id} {account.name}</span>，单号 <span className="font-mono">{refId.trim()}</span>
        </p>
        {preview}
        <p className="text-[11px] text-gray-500">原因：{reason.trim()}</p>
        {large && <p className="text-[11px] text-amber-700">金额 ≥ ¥1,000，属于大额调账，需要输入账户名确认。</p>}
      </ConfirmDialog>
    </>
  );
}

// ---------- 模型多选（赠送余额的 model_scope、API Key 的 allowed_models） ----------

export function ModelMultiSelect({ value, onChange, emptyHint }: { value: string[]; onChange: (v: string[]) => void; emptyHint: string }) {
  const [q, setQ] = useState('');
  const res = useAsync((signal) => listVirtualModels({ q: q || undefined, status: 'active', page_size: 20, sort: 'name' }, signal), [q]);
  const toggle = (name: string) => onChange(value.includes(name) ? value.filter((x) => x !== name) : [...value, name]);
  return (
    <div className="space-y-1.5">
      <div className="flex flex-wrap gap-1 min-h-6">
        {value.length === 0 && <span className="text-[11px] text-gray-400">{emptyHint}</span>}
        {value.map((n) => (
          <span key={n} className="inline-flex items-center gap-1 pl-2 pr-1 py-0.5 rounded-full bg-purple-50 text-purple-700 border border-purple-200 text-[11px] font-mono">
            {n}
            <button type="button" onClick={() => toggle(n)} className="px-1 text-purple-400 hover:text-purple-700 cursor-pointer" aria-label={`移除 ${n}`}>
              ×
            </button>
          </span>
        ))}
      </div>
      <SearchInput placeholder="搜索模型…" value={q} onChange={(e) => setQ(e.target.value)} />
      <div className="max-h-36 overflow-y-auto border border-gray-200 rounded-lg divide-y divide-gray-100">
        {res.loading && <div className="px-3 py-2 text-gray-400">正在加载...</div>}
        {!res.loading && (res.data?.data.length ?? 0) === 0 && <div className="px-3 py-2 text-gray-400">没有匹配的模型</div>}
        {res.data?.data.map((m) => {
          const on = value.includes(m.name);
          return (
            <button key={m.id} type="button" onClick={() => toggle(m.name)} className={cn('w-full px-3 py-1.5 text-left flex items-center gap-2 cursor-pointer', on ? 'bg-purple-50/50' : 'hover:bg-gray-50')}>
              <span className={cn('w-3 h-3 rounded border flex items-center justify-center text-[9px]', on ? 'bg-purple-600 border-purple-600 text-white' : 'border-gray-300')}>{on ? '✓' : ''}</span>
              <span className="font-mono text-gray-900 truncate">{m.name}</span>
              {m.display_name && <span className="text-gray-400 truncate">{m.display_name}</span>}
            </button>
          );
        })}
      </div>
    </div>
  );
}

// ---------- 发放赠送余额 ----------

type Expiry = '7' | '30' | '90' | 'custom' | 'never';

function expiresAtFrom(expiry: Expiry, customDate: string): string | undefined | null {
  if (expiry === 'never') return undefined;
  if (expiry === 'custom') {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(customDate)) return null;
    return new Date(`${customDate}T23:59:59`).toISOString();
  }
  return new Date(Date.now() + Number(expiry) * 86400_000).toISOString();
}

export function GrantCreditModal({ open, onClose, account, onDone }: { open: boolean; onClose: () => void; account: Account; onDone: () => void }) {
  const toast = useToast();
  const [source, setSource] = useState<GrantSource>('compensation');
  const [amount, setAmount] = useState<number | null>(null);
  const [expiry, setExpiry] = useState<Expiry>('30');
  const [customDate, setCustomDate] = useState('');
  const [scope, setScope] = useState<string[]>([]);
  const [refId, setRefId] = useState('');
  const [reason, setReason] = useState('');
  const [confirming, setConfirming] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // 每次打开弹窗生成一个幂等键，作为未填单号时的 ref_id
  const autoRefId = useMemo(() => (open ? newIdempotencyKey() : ''), [open]);

  useEffect(() => {
    if (open) {
      setSource('compensation');
      setAmount(null);
      setExpiry('30');
      setCustomDate('');
      setScope([]);
      setRefId('');
      setReason('');
      setError(null);
      setConfirming(false);
    }
  }, [open]);

  const expiresAt = expiresAtFrom(expiry, customDate);
  const reasonTooLong = [...reason].length > 200;
  const canSubmit = amount !== null && amount > 0 && !!reason.trim() && !reasonTooLong && expiresAt !== null;

  const doGrant = async () => {
    if (amount === null || expiresAt === null) return;
    setSubmitting(true);
    setError(null);
    try {
      await grantCredit(account.id, {
        source,
        amount_micro: amount,
        reason: reason.trim(),
        expires_at: expiresAt,
        model_scope: scope.length ? scope : undefined,
        // 没填单号时用本次弹窗生成的幂等键：同一弹窗里重试不会重复发放
        ref_id: refId.trim() || autoRefId,
      });
      toast.success('赠送余额已发放');
      setConfirming(false);
      onDone();
    } catch (err) {
      setConfirming(false);
      setError(describeError(err, '发放失败'));
    } finally {
      setSubmitting(false);
    }
  };

  const sourceLabel = GRANT_SOURCE_OPTIONS.find((o) => o.value === source)?.label;
  const expiryText = expiry === 'never' ? '永久有效' : expiresAt ? `有效期至 ${new Date(expiresAt).toLocaleDateString('zh-CN')}` : '—';

  return (
    <>
      <FormModal
        open={open && !confirming}
        onClose={onClose}
        width="lg"
        title={
          <span>
            发放赠送余额 <span className="text-gray-400 font-normal">· 账户 #{account.id} {account.name}</span>
          </span>
        }
        description="赠送余额优先于现金扣费，可限定模型范围与有效期"
        onSubmit={() => setConfirming(true)}
        submitLabel="发放"
        submitDisabled={!canSubmit}
        error={error}
      >
        <Field label="来源" required>
          <RadioCards options={GRANT_SOURCE_OPTIONS.map((o) => ({ value: o.value, label: o.label, hint: o.hint }))} value={source} onChange={setSource} cols={4} />
        </Field>
        <Field label="金额" required>
          <MoneyInput valueMicro={amount} onChange={setAmount} />
        </Field>
        <Field label="有效期" required error={expiresAt === null ? '请选择有效日期' : undefined}>
          <div className="flex flex-wrap items-center gap-2">
            {(
              [
                ['7', '7 天'],
                ['30', '30 天'],
                ['90', '90 天'],
                ['custom', '自定义'],
                ['never', '永久'],
              ] as Array<[Expiry, string]>
            ).map(([v, l]) => (
              <button
                key={v}
                type="button"
                onClick={() => setExpiry(v)}
                className={cn(
                  'px-3 py-1 rounded-full text-xs border cursor-pointer',
                  expiry === v ? 'bg-gray-900 text-white border-gray-900 shadow-xs' : 'border-gray-200 text-gray-600 hover:border-gray-300 bg-white',
                )}
              >
                {l}
              </button>
            ))}
            {expiry === 'custom' && (
              <div className="w-40">
                <Input type="date" value={customDate} onChange={(e) => setCustomDate(e.target.value)} />
              </div>
            )}
          </div>
        </Field>
        <Field label="模型范围" hint="不选 = 全部模型可用">
          <ModelMultiSelect value={scope} onChange={setScope} emptyHint="全部模型" />
        </Field>
        <div className="grid grid-cols-2 gap-4">
          <Field label="关联单号" hint="留空自动生成；同一账户同一来源的单号只能发放一次（防重复发放）">
            <Input mono value={refId} onChange={(e) => setRefId(e.target.value)} placeholder="PROMO-2026-10" />
          </Field>
          <Field label="原因" required error={reasonTooLong ? '原因最多 200 字' : undefined}>
            <Input value={reason} invalid={reasonTooLong} onChange={(e) => setReason(e.target.value)} placeholder="例如：故障补偿" />
          </Field>
        </div>
      </FormModal>

      <ConfirmDialog
        open={open && confirming}
        onClose={() => setConfirming(false)}
        onConfirm={doGrant}
        loading={submitting}
        level={amount !== null && amount >= LARGE_ADJUST_MICRO ? 'typed' : 'normal'}
        confirmText={amount !== null && amount >= LARGE_ADJUST_MICRO ? account.name : undefined}
        confirmLabel="确认发放"
        title="确认发放赠送余额"
      >
        <p className="text-xs">
          向账户 <span className="font-medium text-gray-900">#{account.id} {account.name}</span> 发放 <Money micro={amount} className="font-semibold text-gray-900" />（{sourceLabel}）
        </p>
        <p className="text-[11px] text-gray-500">
          {expiryText} · {scope.length ? `限 ${scope.length} 个模型` : '全部模型'} · 原因：{reason.trim()}
        </p>
      </ConfirmDialog>
    </>
  );
}

// ---------- 代开 API Key ----------

function parseLimit(s: string): number | undefined | null {
  if (!s.trim()) return undefined;
  return /^\d+$/.test(s.trim()) && Number(s) > 0 ? Number(s) : null;
}

export function CreateApiKeyModal({ open, onClose, account, onCreated }: { open: boolean; onClose: () => void; account: Account; onCreated: (k: ApiKeyCreated) => void }) {
  const [name, setName] = useState('');
  const [models, setModels] = useState<string[]>([]);
  const [advanced, setAdvanced] = useState(false);
  const [rpm, setRpm] = useState('');
  const [tpm, setTpm] = useState('');
  const [conc, setConc] = useState('');
  const [budget, setBudget] = useState<number | null>(null);
  const [budgetPeriod, setBudgetPeriod] = useState<'none' | 'daily' | 'monthly'>('none');
  const [expiresOn, setExpiresOn] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (open) {
      setName('');
      setModels([]);
      setAdvanced(false);
      setRpm('');
      setTpm('');
      setConc('');
      setBudget(null);
      setBudgetPeriod('none');
      setExpiresOn('');
      setError(null);
    }
  }, [open]);

  const rpmV = parseLimit(rpm);
  const tpmV = parseLimit(tpm);
  const concV = parseLimit(conc);
  // 过期日按运营时区当天结束（次日零点）失效
  const expiresAt = expiresOn ? toApiTime(expiresOn, true) : undefined;
  const expiryErr = !!expiresAt && Date.parse(expiresAt) <= Date.now();
  const budgetErr = budget !== null && budget > 0 && budgetPeriod === 'none';
  const limitErr = rpmV === null || tpmV === null || concV === null || expiryErr || budgetErr;

  const submit = async () => {
    setSubmitting(true);
    setError(null);
    try {
      const k = await createApiKey(account.id, {
        name: name.trim(),
        allowed_models: models.length ? models : undefined,
        rpm_limit: rpmV ?? undefined,
        tpm_limit: tpmV ?? undefined,
        concurrency_limit: concV ?? undefined,
        budget_limit_micro: budget && budget > 0 ? budget : undefined,
        budget_period: budget && budget > 0 ? budgetPeriod : undefined,
        expires_at: expiresAt,
      });
      onCreated(k);
    } catch (err) {
      setError(describeError(err, '创建失败'));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <FormModal
      open={open}
      onClose={onClose}
      width="lg"
      title={
        <span>
          代开 API Key <span className="text-gray-400 font-normal">· 账户 #{account.id} {account.name}</span>
        </span>
      }
      description="明文只在创建成功后显示一次"
      onSubmit={submit}
      submitLabel="创建"
      submitting={submitting}
      submitDisabled={!name.trim() || limitErr}
      error={error}
    >
      <Field label="名称" required>
        <Input autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="例如 production" />
      </Field>
      <Field label="允许的模型" hint="不选 = 该账户 tier 可见的全部模型">
        <ModelMultiSelect value={models} onChange={setModels} emptyHint="不限制" />
      </Field>
      <div>
        <button type="button" onClick={() => setAdvanced((v) => !v)} className="inline-flex items-center gap-1 text-xs text-gray-600 hover:text-gray-900 cursor-pointer">
          {advanced ? <ChevronDown className="w-3.5 h-3.5" /> : <ChevronRight className="w-3.5 h-3.5" />}
          高级限制（默认不限制）
        </button>
        {advanced && (
          <div className="grid grid-cols-3 gap-3 mt-3">
            <Field label="RPM" error={rpmV === null ? '需为正整数' : undefined}>
              <Input mono value={rpm} invalid={rpmV === null} onChange={(e) => setRpm(e.target.value)} placeholder="不限" />
            </Field>
            <Field label="TPM" error={tpmV === null ? '需为正整数' : undefined}>
              <Input mono value={tpm} invalid={tpmV === null} onChange={(e) => setTpm(e.target.value)} placeholder="不限" />
            </Field>
            <Field label="并发" error={concV === null ? '需为正整数' : undefined}>
              <Input mono value={conc} invalid={concV === null} onChange={(e) => setConc(e.target.value)} placeholder="不限" />
            </Field>
            <Field label="预算" hint="留空 = 不限">
              <MoneyInput valueMicro={budget} onChange={setBudget} />
            </Field>
            <Field label="预算周期" error={budgetErr ? '设置预算时需选择周期' : undefined}>
              <Select
                className="w-full"
                value={budgetPeriod}
                onChange={(e) => setBudgetPeriod(e.target.value as 'none' | 'daily' | 'monthly')}
                options={[
                  { value: 'none', label: '不限' },
                  { value: 'daily', label: '每天' },
                  { value: 'monthly', label: '每月' },
                ]}
              />
            </Field>
            <Field label="过期日期" hint="留空 = 永不过期" error={expiryErr ? '需晚于今天' : undefined}>
              <Input type="date" value={expiresOn} invalid={expiryErr} onChange={(e) => setExpiresOn(e.target.value)} />
            </Field>
          </div>
        )}
      </div>
    </FormModal>
  );
}
