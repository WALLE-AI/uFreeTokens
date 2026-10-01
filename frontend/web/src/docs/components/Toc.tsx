import React, { useEffect, useState } from 'react';
import { useT } from '../i18n';

export interface TocItem {
  id: string;
  text: string;
  depth: number;
}

// collectToc 从已渲染的正文里读 h2/h3（id 由 rehype-slug 在构建期生成）。
export function collectToc(root: HTMLElement | null): TocItem[] {
  if (!root) return [];
  return Array.from(root.querySelectorAll<HTMLElement>('h2[id], h3[id]')).map((h) => ({
    id: h.id,
    text: h.textContent ?? '',
    depth: h.tagName === 'H2' ? 2 : 3,
  }));
}

export const Toc: React.FC<{ items: TocItem[] }> = ({ items }) => {
  const t = useT();
  const [active, setActive] = useState('');

  useEffect(() => {
    if (items.length === 0) return;
    // 视口上方 30% 区域内最后一个越过顶部的标题算"当前"。
    const observer = new IntersectionObserver(
      (entries) => {
        const visible = entries.filter((e) => e.isIntersecting).sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top);
        if (visible[0]) setActive(visible[0].target.id);
      },
      { rootMargin: '-96px 0px -70% 0px' },
    );
    items.forEach((i) => {
      const el = document.getElementById(i.id);
      if (el) observer.observe(el);
    });
    return () => observer.disconnect();
  }, [items]);

  if (items.length === 0) return null;
  return (
    <div className="text-xs">
      <p className="font-semibold text-gray-900 mb-2">{t('onThisPage')}</p>
      <ul className="space-y-1.5 border-l border-gray-200">
        {items.map((i) => (
          <li key={i.id}>
            <a
              href={`#${i.id}`}
              className={`block -ml-px border-l pl-3 leading-snug transition-colors ${i.depth === 3 ? 'pl-6' : ''} ${
                active === i.id ? 'border-purple-600 text-purple-700 font-medium' : 'border-transparent text-gray-500 hover:text-gray-900'
              }`}
            >
              {i.text}
            </a>
          </li>
        ))}
      </ul>
    </div>
  );
};
