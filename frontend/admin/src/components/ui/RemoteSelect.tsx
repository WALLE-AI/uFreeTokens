import { useCallback, useEffect, useRef, useState } from 'react';
import { ChevronDown, Loader2, Search, X } from 'lucide-react';
import { isAbortError } from '../../api/client';
import { useDismiss } from '../../hooks/useDismiss';
import { cn } from '../../lib/cn';

export interface RemoteOption<T = unknown> {
  value: string;
  label: string;
  hint?: string;
  icon?: React.ReactNode; // 选项前的小图标（例如供应商品牌图标）
  data?: T; // 调用方需要的原始对象（例如供应商的 protocol）
}

export interface RemoteSelectProps<T> {
  value: string;
  onChange: (value: string, option: RemoteOption<T> | null) => void;
  // load 按关键字远程搜索候选项（服务端分页的第一页），替代"一次拉 100 条"的下拉框：
  // 选项再多也能搜到，不会被静默截断（方案 §2.5 F2）。
  load: (q: string, signal: AbortSignal) => Promise<RemoteOption<T>[]>;
  // resolve 在只有 value（例如来自 URL 参数）时取回显示名。
  resolve?: (value: string, signal: AbortSignal) => Promise<string>;
  placeholder?: string;
  clearable?: boolean; // 允许清空（筛选条件用），清空后显示 placeholder
  disabled?: boolean;
  className?: string;
}

// RemoteSelect：带远程搜索的下拉选择（组合框）。样式与 Select 保持一致。
export function RemoteSelect<T>({ value, onChange, load, resolve, placeholder = '请选择', clearable, disabled, className }: RemoteSelectProps<T>) {
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState('');
  const [options, setOptions] = useState<RemoteOption<T>[]>([]);
  const [loading, setLoading] = useState(false);
  const [label, setLabel] = useState('');
  const ref = useRef<HTMLDivElement>(null);
  const close = useCallback(() => setOpen(false), []);
  useDismiss(ref, open, close);
  const loadRef = useRef(load);
  loadRef.current = load;
  const resolveRef = useRef(resolve);
  resolveRef.current = resolve;

  // 选中值变化（包括从 URL 恢复）时补齐显示名
  useEffect(() => {
    if (!value) {
      setLabel('');
      return;
    }
    const known = options.find((o) => o.value === value);
    if (known) {
      setLabel(known.label);
      return;
    }
    if (!resolveRef.current) return;
    const ctrl = new AbortController();
    resolveRef.current(value, ctrl.signal).then(setLabel, () => setLabel(`#${value}`));
    return () => ctrl.abort();
    // options 变化不需要重新 resolve
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value]);

  // 打开时按关键字搜索（250ms 防抖）
  useEffect(() => {
    if (!open) return;
    const ctrl = new AbortController();
    setLoading(true);
    const timer = setTimeout(() => {
      loadRef
        .current(q.trim(), ctrl.signal)
        .then((opts) => !ctrl.signal.aborted && setOptions(opts))
        .catch((err) => !isAbortError(err) && setOptions([]))
        .finally(() => !ctrl.signal.aborted && setLoading(false));
    }, 250);
    return () => {
      clearTimeout(timer);
      ctrl.abort();
    };
  }, [open, q]);

  const pick = (o: RemoteOption<T> | null) => {
    setLabel(o?.label ?? '');
    onChange(o?.value ?? '', o);
    setOpen(false);
    setQ('');
  };

  return (
    <div ref={ref} className={cn('relative inline-block min-w-40', className)}>
      <button
        type="button"
        disabled={disabled}
        onClick={() => setOpen((o) => !o)}
        className="w-full flex items-center gap-1.5 bg-white border border-gray-200 rounded-lg px-3 py-1.5 text-xs font-medium shadow-xs hover:border-gray-300 focus:outline-none focus:border-purple-400 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
      >
        <span className={cn('flex-1 truncate text-left', value ? 'text-gray-700' : 'text-gray-400')}>{value ? label || `#${value}` : placeholder}</span>
        {clearable && value ? (
          <X
            className="w-3.5 h-3.5 text-gray-400 hover:text-gray-700"
            onClick={(e) => {
              e.stopPropagation();
              pick(null);
            }}
          />
        ) : (
          <ChevronDown className="w-3.5 h-3.5 text-gray-400" />
        )}
      </button>
      {open && (
        <div className="absolute z-50 mt-1 w-72 max-w-[80vw] bg-white border border-gray-200 rounded-lg shadow-lg p-1.5">
          <div className="relative mb-1">
            <Search className="w-3.5 h-3.5 text-gray-400 absolute left-2.5 top-1/2 -translate-y-1/2 pointer-events-none" />
            <input
              autoFocus
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder="输入关键字搜索"
              className="w-full bg-gray-50 border border-gray-200 rounded-md pl-7 pr-2 py-1.5 text-xs focus:outline-none focus:border-purple-400"
            />
          </div>
          <ul className="max-h-60 overflow-y-auto text-xs">
            {clearable && (
              <li>
                <button type="button" onClick={() => pick(null)} className="w-full text-left px-2.5 py-1.5 rounded text-gray-400 hover:bg-gray-50 cursor-pointer">
                  {placeholder}
                </button>
              </li>
            )}
            {loading && (
              <li className="px-2.5 py-2 text-gray-400 flex items-center gap-1.5">
                <Loader2 className="w-3 h-3 animate-spin" /> 搜索中…
              </li>
            )}
            {!loading && options.length === 0 && <li className="px-2.5 py-2 text-gray-400">没有匹配项</li>}
            {!loading &&
              options.map((o) => (
                <li key={o.value}>
                  <button
                    type="button"
                    onClick={() => pick(o)}
                    className={cn('w-full text-left px-2.5 py-1.5 rounded hover:bg-gray-50 cursor-pointer flex items-center gap-2', o.value === value && 'bg-purple-50 text-purple-700')}
                  >
                    {o.icon}
                    <span className="truncate">{o.label}</span>
                    {o.hint && <span className="ml-auto text-[11px] text-gray-400 truncate">{o.hint}</span>}
                  </button>
                </li>
              ))}
          </ul>
          {!loading && options.length >= 20 && <div className="px-2.5 pt-1 text-[11px] text-gray-400">只显示前 20 个，继续输入以缩小范围</div>}
        </div>
      )}
    </div>
  );
}
