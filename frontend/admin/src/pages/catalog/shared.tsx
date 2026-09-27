import type { ReactNode } from 'react';
import { cn } from '../../lib/cn';
import { formatRatio } from '../../lib/money';
import type { ModelStatus, ModelType, PriceBrief, Tier } from '../../types';

// 目录与定价页面共用的小组件与选项（仅 src/pages/catalog 内部使用）。

export const TIER_OPTIONS: Array<{ value: Tier; label: string }> = [
  { value: 'free', label: 'free' },
  { value: 'pro', label: 'pro' },
  { value: 'enterprise', label: 'enterprise' },
];

export const MODEL_STATUS_OPTIONS: Array<{ value: ModelStatus; label: string }> = [
  { value: 'active', label: '已上架' },
  { value: 'hidden', label: '未公开' },
  { value: 'deprecated', label: '已废弃' },
];

export const MODEL_TYPE_OPTIONS: Array<{ value: ModelType; label: string }> = [
  { value: 'chat', label: 'chat' },
  { value: 'embedding', label: 'embedding' },
  { value: 'image', label: 'image' },
  { value: 'audio', label: 'audio' },
  { value: 'rerank', label: 'rerank' },
];

// 毛利率显示：< 0 rose、< 10% amber、其余正常；null 显示 —
export function MarginText({ ratio, className }: { ratio: string | null | undefined; className?: string }) {
  if (ratio === null || ratio === undefined) {
    return (
      <span className={cn('font-mono text-gray-400', className)} title="缺少成本价、售价或汇率，无法计算">
        —
      </span>
    );
  }
  const n = Number(ratio);
  return (
    <span className={cn('font-mono', n < 0 ? 'text-rose-600 font-medium' : n < 0.1 ? 'text-amber-700' : 'text-gray-900', className)}>
      {formatRatio(ratio)}
    </span>
  );
}

// 单价显示：保留原始小数，去掉多余 0
export function formatPrice(p: string | number | null | undefined): string {
  if (p === null || p === undefined || p === '') return '—';
  const n = typeof p === 'number' ? p : Number(p);
  if (!Number.isFinite(n)) return String(p);
  return n.toLocaleString('en-US', { maximumFractionDigits: 6 });
}

export function currencySymbol(c: string | null | undefined): string {
  if (!c || c === 'CNY') return '¥';
  if (c === 'USD') return '$';
  return `${c} `;
}

// 基础 input/output 单价（每百万 token），列表单元格用
export function PriceBriefCell({ price, empty = '未设置' }: { price: PriceBrief | null | undefined; empty?: ReactNode }) {
  if (!price) return <span className="text-amber-700 text-[11px] font-sans">{empty}</span>;
  const sym = currencySymbol(price.currency);
  return (
    <span className="font-mono whitespace-nowrap" title="输入 / 输出，每百万 token">
      {sym}
      {formatPrice(price.input)} <span className="text-gray-300">/</span> {sym}
      {formatPrice(price.output)}
    </span>
  );
}

export function formatContext(n: number | null | undefined): string {
  if (!n) return '—';
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(n % 1_000_000 === 0 ? 0 : 1)}M`;
  if (n >= 1000) return `${Math.round(n / 1000)}K`;
  return String(n);
}

// 逗号 / 回车添加的标签输入（tags、capabilities、aliases）
export function TagInput({ value, onChange, placeholder }: { value: string[]; onChange: (v: string[]) => void; placeholder?: string }) {
  const add = (raw: string) => {
    const parts = raw
      .split(/[,，\n]/)
      .map((s) => s.trim())
      .filter(Boolean);
    if (parts.length === 0) return;
    onChange(Array.from(new Set([...value, ...parts])));
  };
  return (
    <div className="w-full bg-white border border-gray-200 rounded-lg px-2 py-1.5 flex flex-wrap items-center gap-1 focus-within:border-purple-500">
      {value.map((t) => (
        <span key={t} className="inline-flex items-center gap-1 pl-2 pr-1 py-0.5 rounded-full bg-gray-100 text-gray-700 text-[11px]">
          {t}
          <button type="button" onClick={() => onChange(value.filter((x) => x !== t))} className="px-1 text-gray-400 hover:text-gray-700 cursor-pointer" aria-label={`移除 ${t}`}>
            ×
          </button>
        </span>
      ))}
      <input
        className="flex-1 min-w-24 text-xs px-1 py-0.5 focus:outline-none bg-transparent"
        placeholder={value.length === 0 ? placeholder : ''}
        onKeyDown={(e) => {
          const el = e.currentTarget;
          if (e.key === 'Enter' || e.key === ',') {
            e.preventDefault();
            add(el.value);
            el.value = '';
          } else if (e.key === 'Backspace' && el.value === '' && value.length > 0) {
            onChange(value.slice(0, -1));
          }
        }}
        onBlur={(e) => {
          add(e.currentTarget.value);
          e.currentTarget.value = '';
        }}
      />
    </div>
  );
}

export function parseIdList(s: string): number[] | null {
  const parts = s
    .split(/[,，\s]+/)
    .map((x) => x.trim())
    .filter(Boolean);
  const out: number[] = [];
  for (const p of parts) {
    if (!/^\d+$/.test(p)) return null;
    out.push(Number(p));
  }
  return out;
}
