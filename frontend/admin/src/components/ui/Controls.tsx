import type { ReactNode } from 'react';
import { cn } from '../../lib/cn';

// 表单控件：多选胶囊、单选卡、开关、复选框。§11.5 没有 switch / checkbox 的配方，
// 这里统一用主色 purple-600 实现，各页面不再各写一份。

// 多选胶囊（visible_tiers、allowed_tiers、状态多选等），选中态同 web 的筛选胶囊
export function ChipToggleGroup<V extends string>({
  options,
  value,
  onChange,
}: {
  options: Array<{ value: V; label: string }>;
  value: V[];
  onChange: (v: V[]) => void;
}) {
  return (
    <div className="flex flex-wrap gap-1.5">
      {options.map((o) => {
        const on = value.includes(o.value);
        return (
          <button
            key={o.value}
            type="button"
            onClick={() => onChange(on ? value.filter((x) => x !== o.value) : [...value, o.value])}
            className={cn(
              'px-3 py-1 rounded-full text-xs border cursor-pointer',
              on ? 'bg-gray-900 text-white border-gray-900 shadow-xs' : 'border-gray-200 text-gray-600 hover:border-gray-300 bg-white',
            )}
          >
            {o.label}
          </button>
        );
      })}
    </div>
  );
}

// 单选卡（账户类型、调账方向、赠送来源、协议……）；tone=danger 的选项选中时用 rose
export function RadioCards<V extends string>({
  options,
  value,
  onChange,
  cols = 2,
}: {
  options: Array<{ value: V; label: ReactNode; hint?: ReactNode; tone?: 'default' | 'danger' }>;
  value: V;
  onChange: (v: V) => void;
  cols?: 2 | 3 | 4;
}) {
  return (
    <div className={cn('grid gap-2', cols === 2 ? 'grid-cols-2' : cols === 3 ? 'grid-cols-3' : 'grid-cols-2 sm:grid-cols-4')}>
      {options.map((o) => {
        const on = o.value === value;
        const danger = o.tone === 'danger';
        return (
          <button
            key={o.value}
            type="button"
            onClick={() => onChange(o.value)}
            className={cn(
              'text-left rounded-lg border px-3 py-2 cursor-pointer transition-colors',
              on
                ? danger
                  ? 'border-rose-300 bg-rose-50/60 text-rose-700'
                  : 'border-purple-300 bg-purple-50/60 text-purple-700'
                : 'border-gray-200 bg-white text-gray-700 hover:border-gray-300',
            )}
          >
            <div className="flex items-center gap-2 text-xs font-medium">
              <span className={cn('w-3 h-3 rounded-full border flex items-center justify-center', on ? (danger ? 'border-rose-500' : 'border-purple-600') : 'border-gray-300')}>
                {on && <span className={cn('w-1.5 h-1.5 rounded-full', danger ? 'bg-rose-500' : 'bg-purple-600')} />}
              </span>
              {o.label}
            </div>
            {o.hint && <div className="text-[11px] text-gray-400 mt-0.5 pl-5">{o.hint}</div>}
          </button>
        );
      })}
    </div>
  );
}

// 开关。label 为可见文字；只放在表格里时用 ariaLabel 提供无障碍名称。
// 点击不冒泡，避免触发所在表格行的点击（打开详情）。
export function Switch({
  checked,
  onChange,
  label,
  ariaLabel,
  disabled,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  label?: ReactNode;
  ariaLabel?: string;
  disabled?: boolean;
}) {
  const toggle = (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={ariaLabel}
      title={ariaLabel}
      disabled={disabled}
      onClick={(e) => {
        e.stopPropagation();
        onChange(!checked);
      }}
      className={cn(
        'relative inline-flex h-4 w-7 shrink-0 rounded-full transition-colors cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed',
        checked ? 'bg-purple-600' : 'bg-gray-200',
      )}
    >
      <span className={cn('absolute top-0.5 h-3 w-3 rounded-full bg-white shadow-xs transition-transform', checked ? 'translate-x-3.5' : 'translate-x-0.5')} />
    </button>
  );
  if (!label) return toggle;
  return (
    <label className="inline-flex items-center gap-2 text-xs text-gray-600 cursor-pointer select-none">
      {toggle}
      {label}
    </label>
  );
}

// 复选框（紫色勾选）。点击不冒泡，避免触发所在表格行的点击。
export function Checkbox({
  checked,
  onChange,
  disabled,
  label,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  disabled?: boolean;
  label?: string;
}) {
  return (
    <input
      type="checkbox"
      aria-label={label}
      checked={checked}
      disabled={disabled}
      onClick={(e) => e.stopPropagation()}
      onChange={(e) => onChange(e.target.checked)}
      className="w-3.5 h-3.5 rounded border-gray-300 accent-purple-600 cursor-pointer disabled:cursor-not-allowed disabled:opacity-40"
    />
  );
}
