import React from 'react';
import { NavLink } from 'react-router';
import { DOC_SECTIONS, navPages, pagePath } from '../manifest';
import { operations, summaryOf } from '../api/openapi';
import { useLocale, useT } from '../i18n';
import { MethodBadge } from './MethodBadge';

interface SidebarProps {
  mode: 'docs' | 'api';
  onNavigate?: () => void;
}

const itemCls = ({ isActive }: { isActive: boolean }) =>
  `flex items-center gap-2 px-2.5 py-1.5 rounded-md text-[13px] transition-colors ${
    isActive ? 'bg-purple-50 text-purple-800 font-semibold' : 'text-gray-600 hover:text-gray-950 hover:bg-gray-100'
  }`;

export const Sidebar: React.FC<SidebarProps> = ({ mode, onNavigate }) => {
  const locale = useLocale();
  const t = useT();

  if (mode === 'api') {
    const ops = operations();
    const tags = [...new Set(ops.map((o) => o.op.tags?.[0] ?? ''))];
    return (
      <nav className="px-3 py-4 space-y-5">
        <div>
          <NavLink end to={`/docs/${locale}/api`} className={itemCls} onClick={onNavigate}>
            {t('section.api')}
          </NavLink>
        </div>
        {tags.map((tag) => (
          <div key={tag}>
            <p className="px-2.5 pb-1.5 text-[11px] font-semibold uppercase tracking-wider text-gray-400">{tag}</p>
            <div className="space-y-0.5">
              {ops
                .filter((o) => (o.op.tags?.[0] ?? '') === tag)
                .map((o) => (
                  <NavLink key={o.op.operationId} to={`/docs/${locale}/api/${o.op.operationId}`} className={itemCls} onClick={onNavigate}>
                    <MethodBadge method={o.method} small />
                    <span className={`truncate ${o.op['x-status'] === 'not_implemented' ? 'text-gray-400' : ''}`}>
                      {summaryOf(o.op, locale)}
                    </span>
                  </NavLink>
                ))}
            </div>
          </div>
        ))}
      </nav>
    );
  }

  const pages = navPages(locale);
  return (
    <nav className="px-3 py-4 space-y-5">
      {DOC_SECTIONS.map((section) => {
        const items = pages.filter((p) => p.section === section);
        if (items.length === 0) return null;
        return (
          <div key={section}>
            <p className="px-2.5 pb-1.5 text-[11px] font-semibold uppercase tracking-wider text-gray-400">{t(`section.${section}`)}</p>
            <div className="space-y-0.5">
              {items.map((p) => (
                <NavLink key={p.slug} end to={pagePath(locale, p.slug)} className={itemCls} onClick={onNavigate}>
                  <span className="truncate flex-1">{p.title}</span>
                  {p.lang !== locale && (
                    <span className="text-[9px] font-mono px-1 rounded bg-gray-100 text-gray-500 border border-gray-200">ZH</span>
                  )}
                </NavLink>
              ))}
            </div>
          </div>
        );
      })}
    </nav>
  );
};
