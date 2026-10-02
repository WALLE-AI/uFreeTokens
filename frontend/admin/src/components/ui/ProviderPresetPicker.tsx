import { useCallback, useMemo, useRef, useState } from 'react';
import { ChevronDown, PencilLine, Search } from 'lucide-react';
import { PRESET_GROUPS, PROVIDER_PRESETS, findPreset, type ProviderPreset } from '../../data/providerPresets';
import { useDismiss } from '../../hooks/useDismiss';
import { cn } from '../../lib/cn';
import { ProviderIcon } from './ProviderIcon';

const PROTOCOL_SHORT: Record<string, string> = { openai: 'OpenAI', anthropic: 'Anthropic', gemini: 'Gemini' };

// ProviderPresetPicker：从内置的供应商预设里挑一个（带图标、分组、搜索）。
// value 为预设 id；空串表示"自定义"（手动填写 code / 名称 / 协议）。
export function ProviderPresetPicker({
  value,
  onChange,
  disabled,
  className,
}: {
  value: string;
  onChange: (preset: ProviderPreset | null) => void;
  disabled?: boolean;
  className?: string;
}) {
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState('');
  const ref = useRef<HTMLDivElement>(null);
  const close = useCallback(() => setOpen(false), []);
  useDismiss(ref, open, close);
  const current = findPreset(value);

  const groups = useMemo(() => {
    const kw = q.trim().toLowerCase();
    const hit = (p: ProviderPreset) => !kw || p.id.includes(kw) || p.name.toLowerCase().includes(kw) || p.baseUrl.toLowerCase().includes(kw);
    return PRESET_GROUPS.map((g) => ({ ...g, items: PROVIDER_PRESETS.filter((p) => p.group === g.value && hit(p)) })).filter((g) => g.items.length > 0);
  }, [q]);

  const pick = (p: ProviderPreset | null) => {
    onChange(p);
    setOpen(false);
    setQ('');
  };

  return (
    <div ref={ref} className={cn('relative', className)}>
      <button
        type="button"
        disabled={disabled}
        onClick={() => setOpen((o) => !o)}
        className="w-full flex items-center gap-2 bg-white border border-gray-200 rounded-lg px-3 py-2 text-xs font-medium shadow-xs hover:border-gray-300 focus:outline-none focus:border-purple-400 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
      >
        {current ? (
          <>
            <ProviderIcon code={current.id} name={current.name} size="sm" />
            <span className="text-gray-900">{current.name}</span>
            <span className="font-mono text-[11px] text-gray-400 truncate">{current.baseUrl}</span>
          </>
        ) : (
          <>
            <PencilLine className="w-4 h-4 text-gray-400" />
            <span className="text-gray-500">自定义（手动填写）</span>
          </>
        )}
        <ChevronDown className="w-3.5 h-3.5 text-gray-400 ml-auto shrink-0" />
      </button>
      {open && (
        <div className="absolute z-50 mt-1 w-full min-w-80 bg-white border border-gray-200 rounded-lg shadow-lg p-1.5">
          <div className="relative mb-1">
            <Search className="w-3.5 h-3.5 text-gray-400 absolute left-2.5 top-1/2 -translate-y-1/2 pointer-events-none" />
            <input
              autoFocus
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder={`搜索 ${PROVIDER_PRESETS.length} 个预设供应商（名称 / code / 域名）`}
              className="w-full bg-gray-50 border border-gray-200 rounded-md pl-7 pr-2 py-1.5 text-xs focus:outline-none focus:border-purple-400"
            />
          </div>
          <div className="max-h-96 overflow-y-auto text-xs">
            <button
              type="button"
              onClick={() => pick(null)}
              className={cn('w-full text-left px-2.5 py-1.5 rounded hover:bg-gray-50 cursor-pointer flex items-center gap-2', !current && 'bg-purple-50 text-purple-700')}
            >
              <PencilLine className="w-4 h-4 text-gray-400" />
              自定义（手动填写 code / 名称 / 协议）
            </button>
            {groups.length === 0 && <div className="px-2.5 py-2 text-gray-400">没有匹配的预设，可选择"自定义"</div>}
            {groups.map((g) => (
              <div key={g.value} className="mt-1.5">
                <div className="px-2.5 py-1 text-[10px] font-semibold uppercase tracking-wider text-gray-400">
                  {g.label} <span className="font-mono">{g.items.length}</span>
                </div>
                {g.items.map((p) => (
                  <button
                    key={p.id}
                    type="button"
                    disabled={p.supported === false}
                    title={p.note}
                    onClick={() => pick(p)}
                    className={cn(
                      'w-full text-left px-2.5 py-1.5 rounded flex items-center gap-2 cursor-pointer hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-50 disabled:hover:bg-transparent',
                      p.id === value && 'bg-purple-50 text-purple-700',
                    )}
                  >
                    <ProviderIcon code={p.id} name={p.name} size="sm" />
                    <span className="truncate">{p.name}</span>
                    <span className="font-mono text-[11px] text-gray-400">{p.id}</span>
                    {p.keyless && <span className="text-[10px] px-1 rounded bg-emerald-50 text-emerald-700">免密钥</span>}
                    <span className="ml-auto text-[11px] text-gray-400 truncate">{p.supported === false ? p.note : PROTOCOL_SHORT[p.protocol]}</span>
                  </button>
                ))}
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
