import { Check } from 'lucide-react';
import { Button } from '../../../components/ui';
import { cn } from '../../../lib/cn';
import type { Step, WizardState } from './state';

export interface StepProps {
  state: WizardState;
  update: (fn: (s: WizardState) => WizardState) => void;
  goto: (step: Step) => void;
  secret: string;
  setSecret: (v: string) => void;
  // onboard = 接入新供应商五步向导；append = 已有供应商追加模型（只有 ③④⑤）
  mode?: 'onboard' | 'append';
}

export const STEPS: Array<{ step: Step; label: string }> = [
  { step: 1, label: '基本信息' },
  { step: 2, label: '上游账号与密钥' },
  { step: 3, label: '选择模型' },
  { step: 4, label: '定价' },
  { step: 5, label: '确认导入' },
];

// 顶部步骤条：已完成的步骤可点击回退，后面的步骤不能跳过去
export function StepBar({
  current,
  onJump,
  locked,
  steps = STEPS,
}: {
  current: Step;
  onJump: (s: Step) => void;
  locked?: boolean;
  steps?: Array<{ step: Step; label: string }>;
}) {
  return (
    <ol className="flex items-center gap-2 overflow-x-auto">
      {steps.map(({ step, label }, i) => {
        const done = step < current;
        const active = step === current;
        return (
          <li key={step} className="flex items-center gap-2 shrink-0">
            {i > 0 && <span className={cn('w-8 h-px', done || active ? 'bg-purple-300' : 'bg-gray-200')} />}
            <button
              type="button"
              disabled={!done || locked}
              onClick={() => onJump(step)}
              className={cn(
                'flex items-center gap-2 text-xs rounded-lg px-2 py-1 transition-colors',
                done && !locked ? 'cursor-pointer hover:bg-gray-50' : 'cursor-default',
                active ? 'text-purple-700 font-medium' : done ? 'text-gray-700' : 'text-gray-400',
              )}
            >
              <span
                className={cn(
                  'w-5 h-5 rounded-full flex items-center justify-center text-[10px] font-semibold font-mono',
                  active ? 'bg-purple-600 text-white' : done ? 'bg-purple-100 text-purple-700' : 'bg-gray-100 text-gray-400',
                )}
              >
                {done ? <Check className="w-3 h-3" /> : i + 1}
              </span>
              {label}
            </button>
          </li>
        );
      })}
    </ol>
  );
}

export function StepFooter({
  onBack,
  onNext,
  nextLabel = '下一步',
  nextDisabled,
  nextLoading,
  extra,
}: {
  onBack?: () => void;
  onNext?: () => void;
  nextLabel?: string;
  nextDisabled?: boolean;
  nextLoading?: boolean;
  extra?: React.ReactNode;
}) {
  return (
    <div className="flex items-center gap-2 pt-4 border-t border-gray-100">
      {onBack && <Button onClick={onBack}>上一步</Button>}
      {extra}
      {onNext && (
        <Button variant="primary" className="ml-auto" onClick={onNext} disabled={nextDisabled} loading={nextLoading}>
          {nextLabel}
        </Button>
      )}
    </div>
  );
}
