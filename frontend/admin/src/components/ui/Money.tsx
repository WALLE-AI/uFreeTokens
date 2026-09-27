import { useEffect, useState } from 'react';
import { cn } from '../../lib/cn';
import { formatMicro, formatMicroCompact, microToYuan, yuanToMicro } from '../../lib/money';
import { Input } from './Form';

// Money：统一显示 ¥12.345678，hover 显示原始 micro 值（UI_DESIGN.md §2）
export function Money({
  micro,
  compact,
  signed,
  className,
}: {
  micro: number | null | undefined;
  compact?: boolean;
  signed?: boolean; // 显示 +/-，正数 emerald、负数 rose（流水、调账）
  className?: string;
}) {
  if (micro === null || micro === undefined) return <span className="font-mono text-gray-400">—</span>;
  const text = compact ? formatMicroCompact(micro) : formatMicro(micro);
  return (
    <span
      title={`${micro.toLocaleString('en-US')} micro`}
      className={cn('font-mono', signed && (micro > 0 ? 'text-emerald-700' : micro < 0 ? 'text-rose-700' : ''), className)}
    >
      {signed && micro > 0 ? '+' : ''}
      {text}
    </span>
  );
}

export interface MoneyInputProps {
  valueMicro: number | null;
  onChange: (micro: number | null) => void;
  allowNegative?: boolean;
  invalid?: boolean;
  placeholder?: string;
  autoFocus?: boolean;
  id?: string;
  disabled?: boolean;
}

// MoneyInput：运营输入"元"，组件内部用字符串运算转 micro，下方实时回显
// "= 100,000,000 micro"，杜绝少写 6 个 0 的事故（UI_DESIGN.md §2）。
export function MoneyInput({ valueMicro, onChange, allowNegative, invalid, placeholder = '0.00', autoFocus, id, disabled }: MoneyInputProps) {
  const [text, setText] = useState(valueMicro === null ? '' : microToYuan(valueMicro).replace(/,/g, ''));

  // 外部重置（如表单清空）时同步
  useEffect(() => {
    const cur = yuanToMicro(text);
    if (valueMicro === null && text !== '' && cur !== null) setText('');
    else if (valueMicro !== null && cur !== valueMicro) setText(microToYuan(valueMicro).replace(/,/g, ''));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [valueMicro]);

  const parsed = text.trim() === '' ? null : yuanToMicro(text);
  const formatError = text.trim() !== '' && (parsed === null || (!allowNegative && parsed < 0));

  return (
    <div>
      <div className="relative">
        <span className="absolute left-3 top-1/2 -translate-y-1/2 text-xs text-gray-400 pointer-events-none">¥</span>
        <Input
          id={id}
          mono
          inputMode="decimal"
          autoFocus={autoFocus}
          disabled={disabled}
          invalid={invalid || formatError}
          placeholder={placeholder}
          className="pl-7 pr-10"
          value={text}
          onChange={(e) => {
            const v = e.target.value;
            setText(v);
            const m = v.trim() === '' ? null : yuanToMicro(v);
            onChange(m !== null && !allowNegative && m < 0 ? null : m);
          }}
        />
        <span className="absolute right-3 top-1/2 -translate-y-1/2 text-xs text-gray-400 pointer-events-none">元</span>
      </div>
      <p className={cn('text-[11px] mt-1 font-mono', formatError ? 'text-rose-600' : 'text-gray-400')}>
        {formatError ? '格式不正确：最多 6 位小数' + (allowNegative ? '' : '，且不能为负数') : parsed !== null ? `= ${parsed.toLocaleString('en-US')} micro` : '1 元 = 1,000,000 micro'}
      </p>
    </div>
  );
}
