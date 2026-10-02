import { useState } from 'react';
import { providerIconSrc } from '../../data/providerIcons';
import { cn } from '../../lib/cn';

// 没有品牌图标时的字母徽标配色，按 code 稳定取色
const FALLBACK_COLORS = [
  'bg-purple-100 text-purple-700',
  'bg-sky-100 text-sky-700',
  'bg-emerald-100 text-emerald-700',
  'bg-amber-100 text-amber-700',
  'bg-rose-100 text-rose-700',
  'bg-indigo-100 text-indigo-700',
  'bg-teal-100 text-teal-700',
  'bg-orange-100 text-orange-700',
];

function colorFor(code: string): string {
  let h = 0;
  for (const ch of code) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return FALLBACK_COLORS[h % FALLBACK_COLORS.length];
}

const SIZES = {
  sm: 'w-4 h-4 rounded text-[9px]',
  md: 'w-6 h-6 rounded-md text-[11px]',
  lg: 'w-9 h-9 rounded-lg text-sm',
};

// ProviderIcon：供应商品牌图标（按 code 查预设 / 厂商别名），查不到或加载失败时显示字母徽标。
export function ProviderIcon({ code, name, size = 'md', className }: { code: string; name?: string; size?: keyof typeof SIZES; className?: string }) {
  const src = providerIconSrc(code);
  const [failed, setFailed] = useState<string | null>(null);

  if (src && failed !== src) {
    return (
      <span className={cn(SIZES[size], 'shrink-0 inline-flex items-center justify-center bg-white border border-gray-100 p-[2px] overflow-hidden', className)}>
        <img src={src} alt={name ?? code} className="w-full h-full object-contain" loading="lazy" onError={() => setFailed(src)} />
      </span>
    );
  }
  const letter = (name || code || '?').trim().charAt(0).toUpperCase();
  return (
    <span className={cn(SIZES[size], 'shrink-0 inline-flex items-center justify-center font-bold', colorFor(code || name || ''), className)} aria-hidden>
      {letter}
    </span>
  );
}
