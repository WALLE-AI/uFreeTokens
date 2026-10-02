import { useCallback, useEffect, useState } from 'react';
import { useBlocker, useNavigate, useParams, useSearchParams } from 'react-router';
import { ArrowLeft, KeyRound, Trash2 } from 'lucide-react';
import { getProvider } from '../../api/catalog';
import { useCan } from '../../api/auth';
import { Button, ConfirmDialog, DataState, EmptyState, PageHeader, ProviderIcon, Select } from '../../components/ui';
import { useAsync } from '../../hooks/useAsync';
import type { ProviderAccountSummary, ProviderDetail } from '../../types';
import { AddKeyModal } from './accounts';
import { ProtocolBadge } from './common';
import { StepImport } from './wizard/StepImport';
import { StepModels } from './wizard/StepModels';
import { StepPricing } from './wizard/StepPricing';
import { addModelsStorageKey, clearState, isAppendDirty, loadState, saveState, seedForExisting, type Step, type WizardState } from './wizard/state';
import { StepBar, STEPS, type StepProps } from './wizard/ui';

const APPEND_STEPS = STEPS.filter((s) => s.step >= 3);

// /providers/:id/models/add：为已有供应商追加模型。复用接入向导的 ③ 选择模型 → ④ 定价 → ⑤ 导入，
// 供应商与上游账号已确定，不需要再建供应商、填密钥。
export default function ProviderAddModelsPage() {
  const { id: idParam } = useParams();
  const id = Number(idParam);
  const [reloadKey, setReloadKey] = useState(0);
  const detail = useAsync((signal) => getProvider(id, signal), [id, reloadKey]);

  return (
    <DataState loading={detail.loading && !detail.data} error={detail.error} onRetry={detail.reload} skeleton="cards">
      {detail.data && <AddModelsWizard key={detail.data.id} provider={detail.data} onProviderChanged={() => setReloadKey((k) => k + 1)} />}
    </DataState>
  );
}

function AddModelsWizard({ provider: p, onProviderChanged }: { provider: ProviderDetail; onProviderChanged: () => void }) {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const canCatalog = useCan('catalog:write');
  const canPricing = useCan('pricing:write');
  const canImport = canCatalog && canPricing;
  const storageKey = addModelsStorageKey(p.id);
  const activeAccounts = p.accounts.filter((a) => a.status === 'active');

  const [state, setState] = useState<WizardState>(() => initialState(p, activeAccounts, searchParams.get('account_id'), storageKey));
  const [confirmReset, setConfirmReset] = useState(false);
  const [addingKey, setAddingKey] = useState(false);

  useEffect(() => saveState(state, storageKey), [state, storageKey]);

  const update = useCallback((fn: (s: WizardState) => WizardState) => setState(fn), []);
  const goto = useCallback((step: Step) => {
    setState((s) => ({ ...s, step }));
    document.getElementById('admin-main')?.scrollTo({ top: 0 });
  }, []);

  const dirty = isAppendDirty(state);
  const importing = Object.values(state.results).some((r) => r.state === 'running');
  const account = activeAccounts.find((a) => String(a.id) === state.account.existingId) ?? null;

  useEffect(() => {
    if (!dirty) return;
    const handler = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener('beforeunload', handler);
    return () => window.removeEventListener('beforeunload', handler);
  }, [dirty]);

  const blocker = useBlocker(({ currentLocation, nextLocation }) => dirty && currentLocation.pathname !== nextLocation.pathname);

  // 切换账号：已上架状态按账号计算，选择、定价、导入结果全部重来
  const switchAccount = (accountId: string) => {
    const a = activeAccounts.find((x) => String(x.id) === accountId) ?? null;
    setState(seedForExisting(p, a));
  };

  const restart = () => {
    clearState(storageKey);
    setState(seedForExisting(p, account));
  };

  // 新加密钥后重新拉取上游模型
  const keyAdded = () => {
    onProviderChanged();
    update((s) => ({ ...s, models: [], connection: { ok: false, count: 0, error: null, tested: false }, statusChecked: false, refChecked: false }));
  };

  const props: StepProps = { state, update, goto, secret: '', setSecret: () => {}, mode: 'append' };

  const header = (
    <PageHeader
      title={`为「${p.name}」添加模型`}
      description="选择模型 → 定价 → 导入。已上架（该账号下已有渠道）的模型不会重复导入；进度保存在当前标签页"
      badge={<ProviderIcon code={p.code} name={p.name} />}
      actions={
        <>
          {dirty && (
            <Button variant="ghost" icon={<Trash2 className="w-3.5 h-3.5" />} onClick={() => setConfirmReset(true)} disabled={importing}>
              清空重选
            </Button>
          )}
          <Button icon={<ArrowLeft className="w-3.5 h-3.5" />} onClick={() => navigate(`/providers/${p.id}`)}>
            返回供应商
          </Button>
        </>
      }
    />
  );

  if (activeAccounts.length === 0) {
    return (
      <div>
        {header}
        <EmptyState
          title="这家供应商还没有启用的上游账号"
          description="追加模型需要先有一个启用的上游账号（模型以渠道的形式挂在账号下）"
          action={<Button onClick={() => navigate(`/providers/${p.id}#accounts`)}>去新增上游账号</Button>}
        />
      </div>
    );
  }

  return (
    <div>
      {header}

      {!canImport && (
        <div className="bg-amber-50/80 border border-amber-200 rounded-xl p-3 text-xs text-amber-900 mb-4">
          当前账号没有 catalog:write + pricing:write 权限，可以浏览和试算，但无法正式导入。
        </div>
      )}

      <div className="bg-gray-50 border border-gray-200 rounded-xl p-3 mb-4 flex flex-wrap items-center gap-3 text-xs">
        <span className="text-gray-500">上游账号</span>
        <Select
          className="min-w-56"
          value={state.account.existingId}
          onChange={(e) => switchAccount(e.target.value)}
          disabled={importing}
          placeholder={account ? undefined : '选择上游账号'}
          options={activeAccounts.map((a) => ({ value: String(a.id), label: `${a.name}（×${a.cost_multiplier}）` }))}
        />
        <ProtocolBadge protocol={p.protocol} />
        {account && (
          <>
            <span className="font-mono text-[11px] text-gray-400 truncate max-w-72">{account.base_url}</span>
            <span className={account.active_key_count === 0 ? 'text-amber-700' : 'text-gray-500'}>
              密钥 {account.active_key_count}/{account.key_count}
            </span>
            {account.active_key_count === 0 && (
              <Button size="sm" icon={<KeyRound className="w-3.5 h-3.5" />} onClick={() => setAddingKey(true)}>
                添加密钥
              </Button>
            )}
          </>
        )}
      </div>

      {!account ? (
        <EmptyState title="请选择上游账号" description="新模型会以渠道的形式挂在所选账号下" />
      ) : (
        <>
          <div className="sticky top-0 z-20 -mx-4 md:-mx-8 px-4 md:px-8 py-3 mb-6 bg-white/95 backdrop-blur-md border-b border-gray-100">
            <StepBar current={state.step} onJump={goto} locked={importing || state.finished} steps={APPEND_STEPS} />
          </div>

          {state.step === 3 && <StepModels {...props} />}
          {state.step === 4 && <StepPricing {...props} />}
          {state.step === 5 && <StepImport {...props} onRestart={restart} />}

          <AddKeyModal open={addingKey} onClose={() => setAddingKey(false)} accountId={account.id} onSaved={keyAdded} />
        </>
      )}

      <ConfirmDialog
        open={confirmReset}
        onClose={() => setConfirmReset(false)}
        onConfirm={() => {
          restart();
          setConfirmReset(false);
        }}
        level="danger"
        title="清空当前选择？"
        confirmLabel="清空重选"
      >
        <p>会清空已选模型和定价设置。已经导入的模型<strong>不会</strong>被删除。</p>
      </ConfirmDialog>

      <ConfirmDialog
        open={blocker.state === 'blocked'}
        onClose={() => blocker.reset?.()}
        onConfirm={() => blocker.proceed?.()}
        title="离开添加模型？"
        confirmLabel="离开"
      >
        <p>已选模型和定价设置会保留在当前标签页，回来可以继续。</p>
        {importing && <p className="text-rose-700">导入正在进行，离开会中断剩余项。</p>}
      </ConfirmDialog>
    </div>
  );
}

// initialState：优先恢复本标签页的进度；URL 指定了其他账号（从账号行进入）时按该账号重新开始
function initialState(p: ProviderDetail, accounts: ProviderAccountSummary[], accountParam: string | null, storageKey: string): WizardState {
  const pick = accounts.find((a) => String(a.id) === accountParam) ?? (accounts.length === 1 ? accounts[0] : null);
  const seed = seedForExisting(p, pick);
  const saved = loadState(storageKey, seed);
  const savedValid = saved !== seed && accounts.some((a) => String(a.id) === saved.account.existingId);
  if (!savedValid || (accountParam && saved.account.existingId !== accountParam) || saved.finished) return seed;
  // 协议以服务端为准；步骤限定在 ③–⑤
  return { ...saved, provider: { ...saved.provider, protocol: p.protocol }, step: saved.step < 3 ? 3 : saved.step };
}
