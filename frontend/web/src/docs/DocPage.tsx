import React, { useEffect, useRef, useState, type ComponentType } from 'react';
import { Link, useLocation, useParams } from 'react-router';
import { MDXProvider } from '@mdx-js/react';
import { ChevronLeft, ChevronRight, FileText } from 'lucide-react';
import { findPage, loadPage, loadRaw, navPages, pagePath, type ManifestPage } from './manifest';
import { mdxToMarkdown } from './markdown';
import { apiOrigin } from './origin';
import { useLocale, useT } from './i18n';
import { mdxComponents, Callout } from './components/mdx';
import { useDocsChrome } from './chrome';
import { collectToc } from './components/Toc';
import { NotFound } from './NotFound';

// DocPage 渲染一篇 MDX。当前语言没有这篇时回退到中文原文并提示"未翻译"。
export const DocPage: React.FC<{ slug?: string }> = ({ slug: fixedSlug }) => {
  const locale = useLocale();
  const t = useT();
  const params = useParams();
  const location = useLocation();
  const slug = (fixedSlug ?? params['*'] ?? 'index').replace(/\/$/, '') || 'index';
  const own = findPage(locale, slug);
  const meta: ManifestPage | undefined = own ?? findPage('zh', slug);
  const fallback = !own && !!meta;

  const [Content, setContent] = useState<ComponentType | null>(null);
  const [failed, setFailed] = useState(false);
  const articleRef = useRef<HTMLDivElement>(null);
  const { setToc } = useDocsChrome();

  useEffect(() => {
    if (!meta) return;
    let alive = true;
    setContent(null);
    setFailed(false);
    loadPage(meta.lang, meta.slug)
      ?.then((m) => alive && setContent(() => m.default))
      .catch(() => alive && setFailed(true));
    return () => {
      alive = false;
    };
  }, [meta?.lang, meta?.slug]);

  useEffect(() => {
    if (!meta) return;
    document.title = `${meta.title} · uFreeTokens ${locale === 'zh' ? '开发文档' : 'Docs'}`;
    document.querySelector('meta[name="description"]')?.setAttribute('content', meta.description);
  }, [meta, locale]);

  // 正文渲染完成后：收集 TOC，并滚动到 URL 里的锚点（懒加载导致浏览器自己定位不到）。
  useEffect(() => {
    if (!Content) return;
    setToc(collectToc(articleRef.current));
    const id = decodeURIComponent(location.hash.slice(1));
    if (id) document.getElementById(id)?.scrollIntoView();
    else window.scrollTo(0, 0);
    return () => setToc([]);
  }, [Content, location.hash]);

  if (!meta) return <NotFound />;

  const order = navPages(locale);
  const idx = order.findIndex((p) => p.slug === slug);
  const prev = idx > 0 ? order[idx - 1] : undefined;
  const next = idx >= 0 && idx < order.length - 1 ? order[idx + 1] : undefined;
  const sectionLabel = meta.section === 'api' ? t('apiReference') : t(`section.${meta.section}`);

  const copyMarkdown = async () => {
    const raw = await loadRaw(meta.lang, meta.slug);
    return mdxToMarkdown(meta.title, meta.description, raw, apiOrigin());
  };

  return (
    <article className="min-w-0">
      <header className="mb-8">
        <div className="flex items-center justify-between gap-3 text-xs text-gray-500 mb-2">
          <span className="font-medium text-purple-700">{sectionLabel}</span>
          <MarkdownCopyButton load={copyMarkdown} />
        </div>
        <h1 className="text-[28px] leading-tight font-extrabold text-gray-950 tracking-tight">{meta.title}</h1>
        {meta.description && <p className="mt-2 text-base text-gray-600 leading-relaxed">{meta.description}</p>}
      </header>

      {fallback && (
        <div className="mb-6">
          <Callout type="info" title={t('notTranslated')}>
            <p>{t('notTranslatedBody')}</p>
          </Callout>
        </div>
      )}
      {meta.outdated && (
        <div className="mb-6">
          <Callout type="warning" title={t('outdatedTranslation')}>
            <p>
              {t('outdatedTranslationBody')}{' '}
              <Link to={pagePath('zh', slug)} className="underline font-medium">
                {t('readSource')}
              </Link>
            </p>
          </Callout>
        </div>
      )}

      <div ref={articleRef} className="docs-prose" lang={meta.lang === 'zh' ? 'zh-CN' : 'en'}>
        {failed && <p className="text-rose-700">{t('loadFailed')}</p>}
        {!Content && !failed && <p className="text-gray-400 text-sm">{t('loading')}</p>}
        {Content && (
          <MDXProvider components={mdxComponents}>
            <Content />
          </MDXProvider>
        )}
      </div>

      {(prev || next) && (
        <nav className="mt-14 pt-6 border-t border-gray-200 grid grid-cols-2 gap-3 text-sm">
          {prev ? (
            <Link to={pagePath(locale, prev.slug)} className="group rounded-lg border border-gray-200 p-3 hover:border-purple-300">
              <span className="flex items-center gap-1 text-xs text-gray-500">
                <ChevronLeft className="w-3.5 h-3.5" />
                {t('previous')}
              </span>
              <span className="block mt-0.5 font-semibold text-gray-900 group-hover:text-purple-700 truncate">{prev.title}</span>
            </Link>
          ) : (
            <span />
          )}
          {next && (
            <Link to={pagePath(locale, next.slug)} className="group rounded-lg border border-gray-200 p-3 text-right hover:border-purple-300">
              <span className="flex items-center justify-end gap-1 text-xs text-gray-500">
                {t('next')}
                <ChevronRight className="w-3.5 h-3.5" />
              </span>
              <span className="block mt-0.5 font-semibold text-gray-900 group-hover:text-purple-700 truncate">{next.title}</span>
            </Link>
          )}
        </nav>
      )}
    </article>
  );
};

// "复制为 Markdown"：源文本要异步加载，先取到再写剪贴板。
const MarkdownCopyButton: React.FC<{ load: () => Promise<string> }> = ({ load }) => {
  const t = useT();
  const [state, setState] = useState<'idle' | 'copied'>('idle');
  return (
    <button
      type="button"
      onClick={async () => {
        const text = await load();
        await navigator.clipboard?.writeText(text);
        setState('copied');
        setTimeout(() => setState('idle'), 1500);
      }}
      className="inline-flex items-center gap-1.5 px-2 py-1 rounded-md border border-gray-200 text-gray-600 hover:text-gray-900 hover:bg-gray-50 cursor-pointer"
    >
      <FileText className="w-3.5 h-3.5" />
      {state === 'copied' ? t('copied') : t('copyMarkdown')}
    </button>
  );
};
