import { useEffect, useState, type ReactNode } from 'react';
import { X } from 'lucide-react';
import { SearchInput } from './Form';

export interface ActiveFilter {
  key: string;
  label: string; // 例如 "供应商: DeepSeek"
  onRemove: () => void;
}

export interface FilterBarProps {
  search?: string;
  onSearch?: (q: string) => void;
  searchPlaceholder?: string;
  controls?: ReactNode; // 各页面的 Select 等筛选控件
  active?: ActiveFilter[];
  onClearAll?: () => void;
  right?: ReactNode;
}

// FilterBar：搜索 + 筛选控件 + 可移除条件 chip + "清空全部条件"
// （对齐 web App.tsx 的筛选条件样式）。搜索框本地防抖 300ms 后写回 URL。
export function FilterBar({ search, onSearch, searchPlaceholder = '搜索…', controls, active = [], onClearAll, right }: FilterBarProps) {
  const [draft, setDraft] = useState(search ?? '');
  useEffect(() => setDraft(search ?? ''), [search]);
  useEffect(() => {
    if (!onSearch || draft === (search ?? '')) return;
    const t = setTimeout(() => onSearch(draft.trim()), 300);
    return () => clearTimeout(t);
  }, [draft, search, onSearch]);

  return (
    <div className="mb-4 space-y-2">
      <div className="flex flex-wrap items-center gap-2">
        {onSearch && (
          <SearchInput
            className="w-full sm:w-64"
            placeholder={searchPlaceholder}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') onSearch(draft.trim());
              if (e.key === 'Escape') (e.target as HTMLInputElement).blur();
            }}
          />
        )}
        {controls}
        {right && <div className="ml-auto flex items-center gap-2">{right}</div>}
      </div>
      {active.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5">
          {active.map((f) => (
            <span
              key={f.key}
              className="inline-flex items-center gap-1 pl-2.5 pr-1 py-0.5 rounded-full bg-purple-50 text-purple-700 border border-purple-200 text-[11px] font-medium"
            >
              {f.label}
              <button
                type="button"
                onClick={f.onRemove}
                className="p-0.5 rounded-full hover:bg-purple-100 cursor-pointer"
                aria-label={`移除条件 ${f.label}`}
              >
                <X className="w-3 h-3" />
              </button>
            </span>
          ))}
          {onClearAll && (
            <button type="button" onClick={onClearAll} className="text-xs text-purple-600 hover:text-purple-700 ml-1 cursor-pointer">
              清空全部条件
            </button>
          )}
        </div>
      )}
    </div>
  );
}

// 胶囊筛选（tab 式状态切换，选中 bg-gray-900）
export interface PillOption {
  value: string;
  label: ReactNode;
  count?: number;
}

export function Pills({ options, value, onChange }: { options: PillOption[]; value: string; onChange: (v: string) => void }) {
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {options.map((o) => {
        const selected = o.value === value;
        return (
          <button
            key={o.value}
            type="button"
            onClick={() => onChange(o.value)}
            className={
              selected
                ? 'px-3 py-1 rounded-full text-xs border bg-gray-900 text-white border-gray-900 shadow-xs cursor-pointer'
                : 'px-3 py-1 rounded-full text-xs border border-gray-200 text-gray-600 hover:border-gray-300 bg-white cursor-pointer'
            }
          >
            {o.label}
            {o.count !== undefined && <span className={selected ? 'ml-1 text-gray-300' : 'ml-1 text-gray-400'}>{o.count}</span>}
          </button>
        );
      })}
    </div>
  );
}
