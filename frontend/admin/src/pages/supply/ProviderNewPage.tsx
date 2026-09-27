import { useCallback, useEffect, useState } from 'react';
import { useBlocker } from 'react-router';
import { Trash2 } from 'lucide-react';
import { Button, ConfirmDialog, PageHeader } from '../../components/ui';
import { StepAccount, StepProvider } from './wizard/StepProviderAccount';
import { StepImport } from './wizard/StepImport';
import { StepModels } from './wizard/StepModels';
import { StepPricing } from './wizard/StepPricing';
import { clearState, INITIAL_STATE, isDirty, loadState, saveState, type Step, type WizardState } from './wizard/state';
import { StepBar, type StepProps } from './wizard/ui';

// /providers/new：接入新供应商五步向导（UI_DESIGN.md §5.1），是 test_web/admin.html
// "建供应商 → 账号+密钥 → 拉模型 → 定价导入"的正式版。
export default function ProviderNewPage() {
  const [state, setState] = useState<WizardState>(loadState);
  // 上游密钥明文只放内存，不进 sessionStorage
  const [secret, setSecret] = useState('');
  const [confirmReset, setConfirmReset] = useState(false);

  useEffect(() => saveState(state), [state]);

  const update = useCallback((fn: (s: WizardState) => WizardState) => setState(fn), []);
  const goto = useCallback((step: Step) => {
    setState((s) => ({ ...s, step }));
    document.getElementById('admin-main')?.scrollTo({ top: 0 });
  }, []);

  const dirty = isDirty(state);

  // 刷新 / 关闭标签页：向导状态已存 sessionStorage 可恢复，但已填写的密钥会丢失，仍然提示
  useEffect(() => {
    if (!dirty) return;
    const handler = (e: BeforeUnloadEvent) => {
      e.preventDefault();
    };
    window.addEventListener('beforeunload', handler);
    return () => window.removeEventListener('beforeunload', handler);
  }, [dirty]);

  // 站内跳转离开向导：提示一次（状态保留在本标签页，回来可以继续）
  const blocker = useBlocker(({ currentLocation, nextLocation }) => dirty && currentLocation.pathname !== nextLocation.pathname);

  const restart = () => {
    clearState();
    setSecret('');
    setState(INITIAL_STATE);
  };

  const props: StepProps = { state, update, goto, secret, setSecret };
  const importing = Object.values(state.results).some((r) => r.state === 'running');

  return (
    <div>
      <PageHeader
        title="接入新供应商"
        description="建供应商 → 上游账号与密钥 → 选择模型 → 定价 → 导入。进度保存在当前标签页，刷新后可继续（密钥需要重新输入）"
        actions={
          dirty && (
            <Button variant="ghost" icon={<Trash2 className="w-3.5 h-3.5" />} onClick={() => setConfirmReset(true)} disabled={importing}>
              放弃并重新开始
            </Button>
          )
        }
      />

      <div className="sticky top-0 z-20 -mx-4 md:-mx-8 px-4 md:px-8 py-3 mb-6 bg-white/95 backdrop-blur-md border-b border-gray-100">
        <StepBar current={state.step} onJump={goto} locked={importing || state.finished} />
      </div>

      {state.step === 1 && <StepProvider {...props} />}
      {state.step === 2 && <StepAccount {...props} />}
      {state.step === 3 && <StepModels {...props} />}
      {state.step === 4 && <StepPricing {...props} />}
      {state.step === 5 && <StepImport {...props} onRestart={restart} />}

      <ConfirmDialog
        open={confirmReset}
        onClose={() => setConfirmReset(false)}
        onConfirm={() => {
          restart();
          setConfirmReset(false);
        }}
        level="danger"
        title="放弃当前向导？"
        confirmLabel="放弃并重新开始"
      >
        <p>会清空本页填写的内容。已经创建的供应商、上游账号、密钥和已导入的模型<strong>不会</strong>被删除。</p>
      </ConfirmDialog>

      <ConfirmDialog
        open={blocker.state === 'blocked'}
        onClose={() => blocker.reset?.()}
        onConfirm={() => blocker.proceed?.()}
        title="离开接入向导？"
        confirmLabel="离开"
      >
        <p>向导进度会保留在当前标签页，回到"接入新供应商"可以继续{secret ? '；但尚未提交的密钥会丢失' : ''}。</p>
        {importing && <p className="text-rose-700">导入正在进行，离开会中断剩余项。</p>}
        <div className="pt-1">
          <button type="button" className="text-xs text-purple-600 hover:underline cursor-pointer" onClick={() => blocker.reset?.()}>
            留在本页
          </button>
          <span className="text-gray-300 mx-2">·</span>
          <button
            type="button"
            className="text-xs text-gray-500 hover:underline cursor-pointer"
            onClick={() => {
              restart();
              blocker.proceed?.();
            }}
          >
            放弃向导并离开
          </button>
        </div>
      </ConfirmDialog>
    </div>
  );
}
