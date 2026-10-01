import React, { useEffect, useMemo, useState } from 'react';
import { Link, NavLink, Navigate, Route, Routes, useLocation, useNavigate, useParams } from 'react-router';
import { BookOpen, Code2, Languages, Menu, Search, X } from 'lucide-react';
import { LocaleContext, isLocale, rememberLocale, resolveLocale, useT, type Locale } from './i18n';
import { DocsChromeContext } from './chrome';
import { Sidebar } from './components/Sidebar';
import { Toc, type TocItem } from './components/Toc';
import { SearchDialog } from './components/SearchDialog';
import { DocPage } from './DocPage';
import { OperationPage } from './api/OperationPage';

// 顶栏（App Header）高 48px，文档工具栏高 44px，两者之和是侧栏/TOC 的吸顶偏移。
const TOP = 'top-[92px]';
const FULL_HEIGHT = 'h-[calc(100vh-92px)]';

// /docs/:lang/* —— :lang 不是合法语言时（例如旧链接 /docs/quickstart）补上语言前缀再跳转。
export const DocsLayout: React.FC = () => {
  const { lang } = useParams();
  const location = useLocation();
  if (!isLocale(lang)) {
    const rest = location.pathname.replace(/^\/docs\/?/, '');
    return <Navigate to={`/docs/${resolveLocale()}/${rest}${location.search}${location.hash}`} replace />;
  }
  return (
    <LocaleContext.Provider value={lang}>
      <DocsShell locale={lang} />
    </LocaleContext.Provider>
  );
};

const DocsShell: React.FC<{ locale: Locale }> = ({ locale }) => {
  const t = useT();
  const location = useLocation();
  const navigate = useNavigate();
  const [toc, setToc] = useState<TocItem[]>([]);
  const [drawer, setDrawer] = useState(false);
  const [searchOpen, setSearchOpen] = useState(false);
  const chrome = useMemo(() => ({ setToc }), []);
  const mode: 'docs' | 'api' = /^\/docs\/[a-z]+\/api(\/|$)/.test(location.pathname) ? 'api' : 'docs';
  const isOperation = /^\/docs\/[a-z]+\/api\/[^/]+/.test(location.pathname);

  useEffect(() => {
    rememberLocale(locale);
    const html = document.documentElement;
    const prev = html.lang;
    html.lang = locale === 'zh' ? 'zh-CN' : 'en';
    return () => {
      html.lang = prev;
    };
  }, [locale]);

  useEffect(() => setDrawer(false), [location.pathname]);

  // ⌘K / Ctrl+K 在文档页打开文档搜索；用捕获阶段拦截，避免同时触发全站的命令面板。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const typing = e.target instanceof HTMLElement && /^(INPUT|TEXTAREA|SELECT)$/.test(e.target.tagName);
      if (((e.metaKey || e.ctrlKey) && e.key === 'k') || (e.key === '/' && !typing)) {
        e.preventDefault();
        e.stopPropagation();
        setSearchOpen(true);
      }
    };
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  }, []);

  // 切换语言：保留当前页面路径和锚点，只换语言段。
  const switchLocale = (next: Locale) => {
    rememberLocale(next);
    navigate(location.pathname.replace(/^\/docs\/[a-z]+/, `/docs/${next}`) + location.search + location.hash);
  };

  const tabCls = ({ isActive }: { isActive: boolean }) =>
    `flex items-center gap-1.5 px-3 py-1.5 rounded-md font-medium transition-colors whitespace-nowrap ${
      isActive ? 'bg-purple-50 text-purple-700' : 'text-gray-600 hover:text-gray-900 hover:bg-gray-100'
    }`;

  return (
    <DocsChromeContext.Provider value={chrome}>
      <div className="flex-1 bg-white">
        {/* 文档工具栏 */}
        <div className="sticky top-12 z-30 h-11 border-b border-gray-200 bg-white/95 backdrop-blur-xs px-3 sm:px-4 flex items-center gap-2 text-xs">
          <button
            type="button"
            onClick={() => setDrawer(true)}
            className="lg:hidden p-1.5 rounded-md text-gray-600 hover:bg-gray-100 cursor-pointer"
            aria-label={t('menu')}
          >
            <Menu className="w-4 h-4" />
          </button>
          <nav className="flex items-center gap-1 overflow-x-auto">
            <NavLink to={`/docs/${locale}`} end className={() => tabCls({ isActive: mode === 'docs' })}>
              <BookOpen className="w-3.5 h-3.5" />
              {t('docs')}
            </NavLink>
            <NavLink to={`/docs/${locale}/api`} className={() => tabCls({ isActive: mode === 'api' })}>
              <Code2 className="w-3.5 h-3.5" />
              {t('apiReference')}
            </NavLink>
          </nav>
          <div className="flex-1" />
          <button
            type="button"
            onClick={() => setSearchOpen(true)}
            className="flex items-center gap-2 w-9 sm:w-56 h-7 px-2.5 rounded-md border border-gray-200 bg-gray-50 text-gray-400 hover:border-gray-300 cursor-pointer"
          >
            <Search className="w-3.5 h-3.5 shrink-0" />
            <span className="hidden sm:inline flex-1 text-left">{t('search')}</span>
            <kbd className="hidden sm:inline text-[10px] font-mono border border-gray-200 rounded px-1 bg-white">⌘K</kbd>
          </button>
          <label className="flex items-center gap-1 text-gray-600">
            <Languages className="w-3.5 h-3.5" />
            <select
              value={locale}
              onChange={(e) => switchLocale(e.target.value as Locale)}
              className="bg-transparent outline-none cursor-pointer font-medium"
              aria-label={t('language')}
            >
              <option value="zh">中文</option>
              <option value="en">English</option>
            </select>
          </label>
        </div>

        <div className="max-w-[1440px] mx-auto flex">
          {/* 侧栏：lg 及以上常驻，以下为抽屉 */}
          <aside className={`hidden lg:block w-64 shrink-0 sticky ${TOP} ${FULL_HEIGHT} overflow-y-auto border-r border-gray-100`}>
            <Sidebar mode={mode} />
          </aside>
          {drawer && (
            <div className="lg:hidden fixed inset-0 z-50 flex" onClick={() => setDrawer(false)}>
              <div className="absolute inset-0 bg-gray-900/30" />
              <aside className="relative w-72 max-w-[85vw] h-full bg-white overflow-y-auto shadow-xl" onClick={(e) => e.stopPropagation()}>
                <div className="flex items-center justify-between px-4 h-11 border-b border-gray-100">
                  <Link to={`/docs/${locale}`} className="text-sm font-bold text-gray-900">
                    {t(mode === 'api' ? 'apiReference' : 'docs')}
                  </Link>
                  <button type="button" onClick={() => setDrawer(false)} className="p-1 text-gray-500 cursor-pointer" aria-label="close">
                    <X className="w-4 h-4" />
                  </button>
                </div>
                <Sidebar mode={mode} onNavigate={() => setDrawer(false)} />
              </aside>
            </div>
          )}

          <main className="flex-1 min-w-0 px-4 sm:px-8 lg:px-12 py-8 pb-24">
            {/* 文章页限制行宽；接口页自己是两栏布局，放宽。 */}
            <div className={isOperation ? 'max-w-6xl mx-auto' : 'max-w-3xl mx-auto'}>
            <Routes>
              <Route index element={<DocPage slug="index" />} />
              <Route path="api" element={<DocPage slug="api" />} />
              <Route path="api/:operationId" element={<OperationPage />} />
              <Route path="*" element={<DocPage />} />
            </Routes>
            </div>
          </main>

          {!isOperation && (
            <aside className={`hidden xl:block w-56 shrink-0 sticky ${TOP} ${FULL_HEIGHT} overflow-y-auto py-8 pr-4`}>
              <Toc items={toc} />
            </aside>
          )}
        </div>
      </div>
      <SearchDialog open={searchOpen} onClose={() => setSearchOpen(false)} />
    </DocsChromeContext.Provider>
  );
};

// /docs 的入口：没带语言时按"上次选择 > 浏览器语言"决定。
export default function DocsRoutes() {
  return (
    <Routes>
      <Route index element={<Navigate to={`/docs/${resolveLocale()}`} replace />} />
      <Route path=":lang/*" element={<DocsLayout />} />
    </Routes>
  );
}
