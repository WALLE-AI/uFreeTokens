import { describeError } from '../../../api/errors';
import { useState } from 'react';
import { CheckCircle2, Eye, EyeOff, Lock, Plug, XCircle } from 'lucide-react';
import { addProviderKey, createProvider, createProviderAccount, getProvider, listUpstreamModels } from '../../../api/catalog';
import { Button, ConfirmDialog, DataState, Field, IconButton, Input, RemoteSelect, SegmentedToggle, Select } from '../../../components/ui';
import { useAsync } from '../../../hooks/useAsync';
import { cn } from '../../../lib/cn';
import type { Protocol, ProviderSummary } from '../../../types';
import { PROTOCOL_OPTIONS, ProtocolBadge, upstreamErrorHint } from '../common';
import { resolvedAccountId, resolvedProviderId, type WizardState } from './state';
import { StepFooter, type StepProps } from './ui';
import { listAllProviderAccounts, providerLabel, searchProviders } from '../../../api/pickers';

// ① 基本信息：选择已有供应商或新建
export function StepProvider({ state, update, goto }: StepProps) {
  // 选中的供应商（RemoteSelect 远程搜索选择；刷新后按 id 重新取详情）
  const [picked, setPicked] = useState<ProviderSummary | null>(null);
  const providerDetail = useAsync(
    (signal) => (state.provider.existingId && !picked ? getProvider(Number(state.provider.existingId), signal) : Promise.resolve(null)),
    [state.provider.existingId],
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const p = state.provider;
  const locked = p.createdId !== null;
  const codeValid = /^[a-z0-9][a-z0-9_-]{1,39}$/.test(p.code);

  const setProvider = (patch: Partial<WizardState['provider']>) =>
    update((s) => ({
      ...s,
      provider: { ...s.provider, ...patch },
      // 换了供应商，后面依赖供应商的数据全部作废
      account: { ...s.account, existingId: '', createdId: null, keyLast4: null },
      connection: { ok: false, count: 0, error: null, tested: false },
      models: [],
      statusChecked: false,
      refChecked: false,
      selected: [],
    }));

  const next = async () => {
    setError(null);
    if (p.mode === 'existing') {
      const found = picked ?? providerDetail.data;
      update((s) => ({ ...s, provider: { ...s.provider, protocol: found?.protocol ?? s.provider.protocol } }));
      goto(2);
      return;
    }
    if (locked) {
      goto(2);
      return;
    }
    setBusy(true);
    try {
      const created = await createProvider({ code: p.code.trim(), name: p.name.trim(), protocol: p.protocol });
      update((s) => ({ ...s, provider: { ...s.provider, createdId: created.id }, account: { ...s.account, mode: 'new' } }));
      goto(2);
    } catch (err) {
      setError(describeError(err, '创建供应商失败'));
    } finally {
      setBusy(false);
    }
  };

  const canNext = p.mode === 'existing' ? !!p.existingId : locked || (codeValid && !!p.name.trim());
  const selected = picked ?? providerDetail.data ?? null;

  return (
    <div className="max-w-2xl space-y-5">
      <SegmentedToggle
        options={[
          { value: 'existing', label: '选择已有供应商' },
          { value: 'new', label: '新建供应商' },
        ]}
        value={p.mode}
        onChange={(v) => !locked && setProvider({ mode: v })}
      />

      {p.mode === 'existing' ? (
        <div>
          <Field label="供应商" required>
            <RemoteSelect<ProviderSummary>
              className="w-full"
              value={p.existingId}
              placeholder="搜索并选择供应商…"
              load={searchProviders}
              resolve={providerLabel}
              onChange={(v, o) => {
                setPicked(o?.data ?? null);
                setProvider({ existingId: v });
              }}
            />
          </Field>
          {selected && (
            <div className="mt-3 bg-gray-50 border border-gray-200 rounded-xl p-4 text-xs flex flex-wrap gap-x-6 gap-y-1 text-gray-600">
              <span>
                协议 <ProtocolBadge protocol={selected.protocol} />
              </span>
              <span>上游账号 <span className="font-mono text-gray-900">{selected.account_count}</span></span>
              <span>渠道 <span className="font-mono text-gray-900">{selected.channel_count}</span></span>
              {selected.status === 'disabled' && <span className="text-amber-700">该供应商已停用</span>}
            </div>
          )}
        </div>
      ) : (
        <div className="space-y-4">
          {locked && (
            <div className="bg-emerald-50 border border-emerald-200 text-emerald-700 rounded-xl p-3 text-xs flex items-center gap-2">
              <Lock className="w-3.5 h-3.5" /> 已创建供应商 #{p.createdId}（{p.code}），以下信息已锁定
            </div>
          )}
          <div className="grid grid-cols-2 gap-3">
            <Field label="Code" required hint="小写字母、数字、- 或 _，创建后不可修改" error={p.code && !codeValid ? '格式不正确' : undefined}>
              <Input mono value={p.code} disabled={locked} invalid={!!p.code && !codeValid} onChange={(e) => setProvider({ code: e.target.value.toLowerCase() })} placeholder="deepseek" autoFocus />
            </Field>
            <Field label="名称" required>
              <Input value={p.name} disabled={locked} onChange={(e) => setProvider({ name: e.target.value })} placeholder="DeepSeek" />
            </Field>
          </div>
          <Field label="协议" required>
            <div className="grid grid-cols-1 sm:grid-cols-3 gap-2">
              {PROTOCOL_OPTIONS.map((o) => (
                <button
                  key={o.value}
                  type="button"
                  disabled={locked}
                  onClick={() => setProvider({ protocol: o.value as Protocol })}
                  className={cn(
                    'text-left p-3 rounded-xl border transition-colors cursor-pointer disabled:cursor-not-allowed disabled:opacity-60',
                    p.protocol === o.value ? 'border-purple-300 bg-purple-50/60' : 'border-gray-200 hover:border-gray-300 bg-white',
                  )}
                >
                  <div className={cn('text-xs font-medium', p.protocol === o.value ? 'text-purple-700' : 'text-gray-900')}>{o.label}</div>
                  <div className="text-[11px] text-gray-400 mt-0.5">{o.hint}</div>
                </button>
              ))}
            </div>
          </Field>
        </div>
      )}

      {error && <div className="bg-rose-50 border border-rose-200 text-rose-700 rounded-xl p-3 text-xs">{error}</div>}
      <StepFooter onNext={next} nextDisabled={!canNext} nextLoading={busy} nextLabel={p.mode === 'new' && !locked ? '创建并继续' : '下一步'} />
    </div>
  );
}

// ② 上游账号与密钥 + 测试连接
export function StepAccount({ state, update, goto, secret, setSecret }: StepProps) {
  const providerId = resolvedProviderId(state);
  const accounts = useAsync(
    (signal) => (providerId ? listAllProviderAccounts(providerId, signal) : Promise.resolve(null)),
    [providerId],
  );
  const [showSecret, setShowSecret] = useState(false);
  const [busy, setBusy] = useState<'test' | 'next' | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [confirmSkip, setConfirmSkip] = useState(false);
  const a = state.account;
  const isOpenAI = state.provider.protocol === 'openai';
  const locked = a.createdId !== null;
  const hasAccounts = (accounts.data?.data.length ?? 0) > 0;
  const mode = hasAccounts ? a.mode : 'new';

  const setAccount = (patch: Partial<WizardState['account']>, resetConnection = true) =>
    update((s) => ({
      ...s,
      account: { ...s.account, ...patch },
      ...(resetConnection
        ? { connection: { ok: false, count: 0, error: null, tested: false }, models: [], statusChecked: false, refChecked: false, selected: [] }
        : {}),
    }));

  const multInvalid = !(Number(a.multiplier) > 0);

  // ensureAccountAndKey：按需创建账号、添加密钥；已创建/已添加的不会重复提交
  const ensureAccountAndKey = async (): Promise<number> => {
    let accountId = mode === 'new' ? a.createdId : a.existingId ? Number(a.existingId) : null;
    if (mode === 'new' && accountId === null) {
      if (!secret.trim()) throw new Error('新建上游账号需要至少一把密钥');
      const created = await createProviderAccount({
        provider_id: providerId!,
        name: a.name.trim(),
        base_url: a.baseURL.trim(),
        cost_multiplier: a.multiplier.trim(),
      });
      accountId = created.id;
      update((s) => ({ ...s, account: { ...s.account, mode: 'new', createdId: created.id } }));
    }
    if (accountId === null) throw new Error('请选择上游账号');
    if (secret.trim()) {
      const k = await addProviderKey(accountId, { secret: secret.trim(), weight: Number(a.weight) || 100 });
      setSecret('');
      update((s) => ({ ...s, account: { ...s.account, keyLast4: k.last4 } }));
    }
    return accountId;
  };

  const test = async () => {
    setError(null);
    setBusy('test');
    let accountId: number;
    try {
      accountId = await ensureAccountAndKey();
    } catch (err) {
      setError(describeError(err, '创建账号或密钥失败'));
      setBusy(null);
      return;
    }
    try {
      const res = await listUpstreamModels(accountId);
      update((s) => ({
        ...s,
        connection: { ok: true, count: res.data.length, error: null, tested: true },
        models: res.data,
        statusChecked: false,
        refChecked: false,
      }));
    } catch (err) {
      update((s) => ({ ...s, connection: { ok: false, count: 0, error: upstreamErrorHint(err), tested: true } }));
    } finally {
      setBusy(null);
    }
  };

  const next = async (skipCheck = false) => {
    setError(null);
    if (isOpenAI && !state.connection.ok && !skipCheck) {
      setConfirmSkip(true);
      return;
    }
    setBusy('next');
    try {
      await ensureAccountAndKey();
      goto(3);
    } catch (err) {
      setError(describeError(err, '创建账号或密钥失败'));
    } finally {
      setBusy(null);
    }
  };

  const selectedExisting = accounts.data?.data.find((x) => String(x.id) === a.existingId);
  const canProceed =
    mode === 'new'
      ? locked || (!!a.name.trim() && !!a.baseURL.trim() && !multInvalid && !!secret.trim())
      : !!a.existingId;

  return (
    <div className="max-w-2xl space-y-5">
      <DataState loading={accounts.loading} error={accounts.error} onRetry={accounts.reload} skeleton="text">
        {hasAccounts && (
          <SegmentedToggle
            options={[
              { value: 'existing', label: `使用已有账号（${accounts.data?.data.length}）` },
              { value: 'new', label: '新建上游账号' },
            ]}
            value={mode}
            onChange={(v) => !locked && setAccount({ mode: v })}
          />
        )}

        {mode === 'existing' ? (
          <div className="space-y-4 mt-4">
            <Field label="上游账号" required>
              <Select
                className="w-full"
                value={a.existingId}
                placeholder="请选择…"
                options={(accounts.data?.data ?? []).map((x) => ({
                  value: String(x.id),
                  label: `${x.name} · ${x.base_url} · active 密钥 ${x.active_key_count}`,
                }))}
                onChange={(e) => {
                  const found = accounts.data?.data.find((x) => String(x.id) === e.target.value);
                  setAccount({ existingId: e.target.value, existingMultiplier: found?.cost_multiplier ?? '1', keyLast4: null });
                }}
              />
            </Field>
            {selectedExisting && selectedExisting.active_key_count === 0 && (
              <div className="bg-amber-50/80 border border-amber-200 rounded-xl p-3 text-xs text-amber-900">该账号没有 active 密钥，请在下面追加一把。</div>
            )}
          </div>
        ) : (
          <div className="space-y-4 mt-4">
            {locked && (
              <div className="bg-emerald-50 border border-emerald-200 text-emerald-700 rounded-xl p-3 text-xs flex items-center gap-2">
                <Lock className="w-3.5 h-3.5" /> 已创建上游账号 #{a.createdId}（{a.name}）
              </div>
            )}
            <div className="grid grid-cols-2 gap-3">
              <Field label="账号名称" required>
                <Input value={a.name} disabled={locked} onChange={(e) => setAccount({ name: e.target.value })} placeholder={`${state.provider.code || 'provider'}-main`} />
              </Field>
              <Field label="成本倍率" required hint="上游计费倍率，1 = 原价" error={multInvalid ? '必须大于 0' : undefined}>
                <Input mono value={a.multiplier} disabled={locked} invalid={multInvalid} onChange={(e) => setAccount({ multiplier: e.target.value }, false)} />
              </Field>
            </div>
            <Field label="Base URL" required hint="OpenAI 兼容协议填到 /v1，例如 https://api.deepseek.com/v1">
              <Input mono value={a.baseURL} disabled={locked} onChange={(e) => setAccount({ baseURL: e.target.value })} placeholder="https://…" />
            </Field>
          </div>
        )}
      </DataState>

      <div className="grid grid-cols-[1fr_7rem] gap-3">
        <Field
          label={mode === 'existing' || locked ? '追加一把密钥（可选）' : '密钥'}
          required={mode === 'new' && !locked}
          hint={a.keyLast4 ? `✓ 已添加密钥 …${a.keyLast4}` : '只在本页面内存中保留，刷新后需要重新输入；落库时加密'}
        >
          <div className="relative">
            <Input
              mono
              type={showSecret ? 'text' : 'password'}
              autoComplete="off"
              value={secret}
              onChange={(e) => setSecret(e.target.value)}
              placeholder="sk-…"
              className="pr-9"
            />
            <IconButton label={showSecret ? '隐藏' : '显示'} className="absolute right-2 top-1/2 -translate-y-1/2" onClick={() => setShowSecret((v) => !v)}>
              {showSecret ? <EyeOff className="w-3.5 h-3.5" /> : <Eye className="w-3.5 h-3.5" />}
            </IconButton>
          </div>
        </Field>
        <Field label="权重">
          <Input mono type="number" min={1} value={a.weight} onChange={(e) => setAccount({ weight: e.target.value }, false)} />
        </Field>
      </div>

      <div className="flex items-center gap-3">
        <Button icon={<Plug className="w-3.5 h-3.5" />} loading={busy === 'test'} disabled={!canProceed || busy !== null || !isOpenAI} onClick={test}>
          测试连接
        </Button>
        {!isOpenAI && <span className="text-[11px] text-gray-400">{state.provider.protocol} 协议没有标准的模型列表接口，下一步可手动填写模型 ID</span>}
        {state.connection.tested &&
          (state.connection.ok ? (
            <span className="text-xs text-emerald-700 flex items-center gap-1">
              <CheckCircle2 className="w-3.5 h-3.5" /> 连通，发现 {state.connection.count} 个模型
            </span>
          ) : (
            <span className="text-xs text-rose-700 flex items-start gap-1">
              <XCircle className="w-3.5 h-3.5 mt-0.5 shrink-0" /> {state.connection.error}
            </span>
          ))}
      </div>

      {error && <div className="bg-rose-50 border border-rose-200 text-rose-700 rounded-xl p-3 text-xs">{error}</div>}

      <StepFooter onBack={() => goto(1)} onNext={() => void next()} nextDisabled={!canProceed && resolvedAccountId(state) === null} nextLoading={busy === 'next'} />

      <ConfirmDialog
        open={confirmSkip}
        onClose={() => setConfirmSkip(false)}
        onConfirm={() => {
          setConfirmSkip(false);
          void next(true);
        }}
        title="尚未测试连接"
        confirmLabel="仍然继续"
      >
        <p>还没有确认上游能连通。继续的话，下一步需要手动填写要导入的模型 ID。建议先点"测试连接"。</p>
      </ConfirmDialog>
    </div>
  );
}
