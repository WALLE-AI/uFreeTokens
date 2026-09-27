import { useCallback, useRef, useState, type ReactNode } from 'react';
import { Link } from 'react-router';
import { ArrowLeft, MoreVertical } from 'lucide-react';
import { useDismiss } from '../../hooks/useDismiss';
import { cn } from '../../lib/cn';

// 详情页模板的公共部件（UI_DESIGN.md §3.2）：粘性操作栏、⋮ 菜单、Hero 统计块、
// 带锚点的段落、键值信息网格。模型 / 渠道 / 账户详情页共用。

// 粘性面包屑 + 操作栏。滚动容器是布局的 <main>（外层有 py-6），用 -mt-6 贴到顶部。
export function StickyActionBar({
  backTo,
  backLabel,
  title,
  badges,
  actions,
}: {
  backTo: string;
  backLabel: string;
  title: ReactNode;
  badges?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <div className="sticky top-0 z-30 -mt-6 -mx-4 md:-mx-8 px-4 md:px-8 py-3 mb-6 bg-white/95 backdrop-blur-md border-b border-gray-100 flex flex-wrap items-center gap-3">
      <Link to={backTo} className="inline-flex items-center gap-1 text-xs text-gray-500 hover:text-gray-900">
        <ArrowLeft className="w-3.5 h-3.5" />
        {backLabel}
      </Link>
      <span className="text-gray-300">/</span>
      <div className="min-w-0 flex items-center gap-2">
        <h1 className="text-sm font-semibold text-gray-900 font-mono truncate max-w-md">{title}</h1>
        {badges}
      </div>
      {actions && <div className="ml-auto flex items-center gap-2">{actions}</div>}
    </div>
  );
}

export interface MenuItem {
  label: string;
  onClick: () => void;
  danger?: boolean;
  disabled?: boolean;
  hint?: string;
}

// ⋮ 下拉菜单（点外 / Esc 关闭；危险项与普通项之间自动加分隔线）
export function ActionMenu({ items }: { items: MenuItem[] }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const close = useCallback(() => setOpen(false), []);
  useDismiss(ref, open, close);
  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="p-1.5 rounded-lg border border-gray-200 text-gray-500 hover:text-gray-800 hover:bg-gray-50 cursor-pointer"
        aria-label="更多操作"
      >
        <MoreVertical className="w-3.5 h-3.5" />
      </button>
      {open && (
        <div className="absolute right-0 top-9 bg-white border border-gray-200 rounded-lg shadow-lg py-1 z-40 text-xs w-48 animate-in fade-in zoom-in-95 duration-100">
          {items.map((it, i) => (
            <button
              key={it.label}
              type="button"
              disabled={it.disabled}
              title={it.hint}
              onClick={() => {
                setOpen(false);
                it.onClick();
              }}
              className={cn(
                'w-full px-3 py-1.5 text-left flex items-center gap-2 cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed',
                it.danger ? 'hover:bg-rose-50 text-rose-600' : 'hover:bg-gray-50 text-gray-700',
                it.danger && i > 0 && !items[i - 1].danger && 'border-t border-gray-100',
              )}
            >
              {it.label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

// Hero 统计块（web ModelDetailPage 的 grid-cols-2 sm:grid-cols-4 标签 + 值）
export function HeroStat({ label, value, sub }: { label: string; value: ReactNode; sub?: ReactNode }) {
  return (
    <div className="bg-white border border-gray-200 rounded-xl p-4 shadow-xs">
      <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold">{label}</div>
      <div className="text-lg font-bold text-gray-900 mt-1 font-mono">{value}</div>
      {sub && <div className="text-[11px] text-gray-500 mt-0.5">{sub}</div>}
    </div>
  );
}

// 详情页段落外壳：锚点 id（配合 AnchorNav）+ 标题 + 右侧操作
export function Section({ id, title, actions, children }: { id: string; title: ReactNode; actions?: ReactNode; children: ReactNode }) {
  return (
    <section id={id} className="scroll-mt-28">
      <div className="flex items-center justify-between mb-3">
        <h2 className="text-sm font-semibold text-gray-900">{title}</h2>
        {actions && <div className="flex items-center gap-2">{actions}</div>}
      </div>
      {children}
    </section>
  );
}

// 键值信息网格
export function InfoGrid({ items }: { items: Array<{ label: string; value: ReactNode }> }) {
  return (
    <dl className="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-3 text-xs">
      {items.map((it) => (
        <div key={it.label} className="flex gap-3">
          <dt className="w-24 shrink-0 text-gray-400">{it.label}</dt>
          <dd className="text-gray-900 min-w-0 break-words">{it.value}</dd>
        </div>
      ))}
    </dl>
  );
}
