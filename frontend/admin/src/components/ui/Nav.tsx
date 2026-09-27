import { useEffect, useState, type ReactNode } from 'react';
import { cn } from '../../lib/cn';

export interface SegmentOption<V extends string> {
  value: V;
  label: ReactNode;
}

// SegmentedToggle：RankingsPage 的线性/对数切换样式（bg-gray-100 p-1，选中 bg-white shadow-xs）
export function SegmentedToggle<V extends string>({
  options,
  value,
  onChange,
}: {
  options: SegmentOption<V>[];
  value: V;
  onChange: (v: V) => void;
}) {
  return (
    <div className="inline-flex bg-gray-100 p-1 rounded-lg">
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          onClick={() => onChange(o.value)}
          className={cn(
            'px-2.5 py-1 rounded-md text-xs cursor-pointer transition-colors',
            o.value === value ? 'bg-white text-gray-900 shadow-xs font-medium' : 'text-gray-500 hover:text-gray-700',
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

export interface AnchorItem {
  id: string;
  label: string;
}

// AnchorNav：ModelDetailPage 的段落导航（w-48 sticky top-28，选中 border-l-2 border-purple-600）。
// 滚动容器是布局里的 <main>，所以监听它而不是 window。
export function AnchorNav({ items, scrollContainerId = 'admin-main' }: { items: AnchorItem[]; scrollContainerId?: string }) {
  const [active, setActive] = useState(items[0]?.id);

  useEffect(() => {
    const root = document.getElementById(scrollContainerId);
    if (!root) return;
    const onScroll = () => {
      let current = items[0]?.id;
      for (const it of items) {
        const el = document.getElementById(it.id);
        if (el && el.getBoundingClientRect().top - root.getBoundingClientRect().top < 140) current = it.id;
      }
      setActive(current);
    };
    root.addEventListener('scroll', onScroll, { passive: true });
    onScroll();
    return () => root.removeEventListener('scroll', onScroll);
  }, [items, scrollContainerId]);

  return (
    <nav className="hidden lg:block w-48 shrink-0 sticky top-28 self-start">
      <ul className="space-y-0.5 text-xs">
        {items.map((it) => (
          <li key={it.id}>
            <button
              type="button"
              onClick={() => document.getElementById(it.id)?.scrollIntoView({ behavior: 'smooth', block: 'start' })}
              className={cn(
                'w-full text-left pl-3 py-1.5 border-l-2 cursor-pointer transition-colors',
                active === it.id ? 'border-purple-600 text-purple-700 font-medium' : 'border-transparent text-gray-500 hover:text-gray-800',
              )}
            >
              {it.label}
            </button>
          </li>
        ))}
      </ul>
    </nav>
  );
}
