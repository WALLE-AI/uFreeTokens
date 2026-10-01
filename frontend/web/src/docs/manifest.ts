import type { ComponentType } from 'react';
import pages from 'virtual:docs-manifest';
import type { Locale } from './i18n';

export type Section = 'getting-started' | 'guides' | 'integrations' | 'changelog' | 'api';

export const DOC_SECTIONS: Section[] = ['getting-started', 'guides', 'integrations', 'changelog'];

export interface ManifestPage {
  lang: Locale;
  slug: string;
  title: string;
  description: string;
  section: Section;
  order: number;
  outdated: boolean;
}

export { pages };

type MdxModule = { default: ComponentType };

// 正文按页懒加载；导航用的 frontmatter 来自 virtual:docs-manifest，不需要先加载正文。
const modules = import.meta.glob<MdxModule>('./content/**/*.mdx');
const raws = import.meta.glob<string>('./content/**/*.mdx', { query: '?raw', import: 'default' });

export function findPage(lang: Locale, slug: string): ManifestPage | undefined {
  return pages.find((p) => p.lang === lang && p.slug === slug);
}

export function loadPage(lang: Locale, slug: string): Promise<MdxModule> | null {
  const loader = modules[`./content/${lang}/${slug}.mdx`];
  return loader ? loader() : null;
}

// loadRaw 返回去掉 frontmatter 的 MDX 源文本（"复制为 Markdown"用）。
export async function loadRaw(lang: Locale, slug: string): Promise<string> {
  const loader = raws[`./content/${lang}/${slug}.mdx`];
  if (!loader) return '';
  const raw = (await loader()).replace(/\r\n/g, '\n');
  return raw.replace(/^---\n[\s\S]*?\n---\n/, '');
}

export function pagePath(lang: Locale, slug: string): string {
  return slug === 'index' ? `/docs/${lang}` : `/docs/${lang}/${slug}`;
}

// 侧栏、上一篇/下一篇使用的顺序。某语言缺失的页面用中文源页顶上（页面内会提示未翻译）。
export function navPages(lang: Locale): ManifestPage[] {
  const own = pages.filter((p) => p.lang === lang && p.section !== 'api');
  if (lang === 'zh') return own;
  const have = new Set(own.map((p) => p.slug));
  const missing = pages.filter((p) => p.lang === 'zh' && p.section !== 'api' && !have.has(p.slug));
  return [...own, ...missing].sort(
    (a, b) =>
      DOC_SECTIONS.indexOf(a.section) - DOC_SECTIONS.indexOf(b.section) || a.order - b.order || a.slug.localeCompare(b.slug),
  );
}
