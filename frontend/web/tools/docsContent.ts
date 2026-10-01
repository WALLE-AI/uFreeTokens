// 文档内容的构建期处理：读取 src/docs/content/{zh,en}/**/*.mdx，解析
// frontmatter、标题锚点、纯文本分节。vite 插件（tools/docsPlugin.ts）用它生成导航
// manifest / 搜索索引 / llms.txt，scripts/check-docs.ts 用它做 CI 校验——两边
// 共用这一份解析逻辑，保证"校验通过"和"页面实际渲染"看到的是同一份结构。
//
// 运行环境：既被 vite.config.ts 加载，也被 `node scripts/check-docs.ts`
// （Node 原生 TS 类型擦除）直接执行，所以只能用可擦除的 TS 语法（不能用
// enum / 参数属性），相对导入必须带 .ts 后缀。
import { createHash } from 'node:crypto';
import { readFileSync, readdirSync, statSync, existsSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import matter from 'gray-matter';
import GithubSlugger from 'github-slugger';

const here = path.dirname(fileURLToPath(import.meta.url));

export const WEB_ROOT = path.resolve(here, '..');
export const CONTENT_DIR = path.join(WEB_ROOT, 'src', 'docs', 'content');
export const OPENAPI_FILE = path.resolve(WEB_ROOT, '..', '..', 'docs', 'gateway-openapi.json');

export const LOCALES = ['zh', 'en'] as const;
export type Locale = (typeof LOCALES)[number];
export const SOURCE_LOCALE: Locale = 'zh';

export const SECTIONS = ['getting-started', 'guides', 'integrations', 'changelog', 'api'] as const;
export type Section = (typeof SECTIONS)[number];

export interface Heading {
  depth: 2 | 3;
  text: string;
  id: string;
}

export interface DocSection {
  // 所在小节的锚点；页面开头（第一个 ## 之前）为空串。
  id: string;
  heading: string;
  text: string;
}

export interface DocPage {
  lang: Locale;
  slug: string;
  file: string;
  title: string;
  description: string;
  section: Section;
  order: number;
  translatedFrom?: string;
  // 去掉 frontmatter 后的 MDX 正文（原样，含 {{BASE_URL}} 占位符）。
  body: string;
  headings: Heading[];
  sections: DocSection[];
}

function walk(dir: string): string[] {
  if (!existsSync(dir)) return [];
  const out: string[] = [];
  for (const name of readdirSync(dir)) {
    const full = path.join(dir, name);
    if (statSync(full).isDirectory()) out.push(...walk(full));
    else if (name.endsWith('.mdx')) out.push(full);
  }
  return out;
}

// stripInline 把一行 Markdown 的行内语法去掉，得到 rehype-slug 看到的纯文本
// （它对标题的 hast 文本节点做 slug，行内代码的文字也算在内）。
export function stripInline(s: string): string {
  return s
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/`([^`]*)`/g, '$1')
    .replace(/(\*\*|__)(.*?)\1/g, '$2')
    .replace(/(\*|_)(.*?)\1/g, '$2')
    .trim();
}

// toPlain 把一段 MDX 正文转成搜索/llms 用的纯文本：去掉代码块、JSX 标签行、
// 表格分隔线和行内标记。
function toPlain(md: string): string {
  return md
    .replace(/```[\s\S]*?```/g, ' ')
    .replace(/<\/?[A-Z][^>]*>/g, ' ')
    .replace(/^\|?\s*:?-{3,}.*$/gm, ' ')
    .split('\n')
    .map((l) => stripInline(l.replace(/^#{1,6}\s+/, '').replace(/^\s*[-*>]\s+/, '').replace(/\|/g, ' ')))
    .join(' ')
    .replace(/\s+/g, ' ')
    .trim();
}

export function parseBody(body: string): { headings: Heading[]; sections: DocSection[] } {
  const slugger = new GithubSlugger();
  const headings: Heading[] = [];
  const sections: DocSection[] = [];
  let current: DocSection = { id: '', heading: '', text: '' };
  let buf: string[] = [];
  let inFence = false;

  const flush = () => {
    current.text = toPlain(buf.join('\n'));
    if (current.text || current.id) sections.push(current);
    buf = [];
  };

  for (const line of body.split('\n')) {
    if (/^\s*```/.test(line)) inFence = !inFence;
    const m = !inFence && /^(#{2,3})\s+(.+?)\s*$/.exec(line);
    if (m) {
      const text = stripInline(m[2]);
      const id = slugger.slug(text);
      headings.push({ depth: m[1].length as 2 | 3, text, id });
      flush();
      current = { id, heading: text, text: '' };
      continue;
    }
    buf.push(line);
  }
  flush();
  return { headings, sections };
}

export function contentHash(body: string): string {
  return createHash('sha1').update(body.replace(/\r\n/g, '\n').trim()).digest('hex').slice(0, 12);
}

export interface LoadIssue {
  file: string;
  message: string;
}

export function loadPages(issues: LoadIssue[] = []): DocPage[] {
  const pages: DocPage[] = [];
  for (const lang of LOCALES) {
    const root = path.join(CONTENT_DIR, lang);
    for (const file of walk(root)) {
      const raw = readFileSync(file, 'utf8').replace(/\r\n/g, '\n');
      const { data, content } = matter(raw);
      const slug = path.relative(root, file).replace(/\\/g, '/').replace(/\.mdx$/, '');
      const rel = path.relative(WEB_ROOT, file).replace(/\\/g, '/');
      if (typeof data.title !== 'string' || !data.title) issues.push({ file: rel, message: 'frontmatter 缺少 title' });
      if (typeof data.description !== 'string') issues.push({ file: rel, message: 'frontmatter 缺少 description' });
      if (!SECTIONS.includes(data.section)) issues.push({ file: rel, message: `frontmatter section 非法：${data.section}` });
      const { headings, sections } = parseBody(content);
      pages.push({
        lang,
        slug,
        file: rel,
        title: String(data.title ?? slug),
        description: String(data.description ?? ''),
        section: SECTIONS.includes(data.section) ? data.section : 'guides',
        order: Number(data.order ?? 999),
        translatedFrom: data.translatedFrom ? String(data.translatedFrom) : undefined,
        body: content,
        headings,
        sections,
      });
    }
  }
  pages.sort((a, b) => SECTIONS.indexOf(a.section) - SECTIONS.indexOf(b.section) || a.order - b.order || a.slug.localeCompare(b.slug));
  return pages;
}

// ---------- OpenAPI ----------

export interface I18nText {
  en: string;
  zh: string;
}

export interface OperationInfo {
  operationId: string;
  method: string;
  path: string;
  tag: string;
  summary: I18nText;
  description: I18nText;
  notImplemented: boolean;
}

export function loadOpenAPI(): any | null {
  if (!existsSync(OPENAPI_FILE)) return null;
  return JSON.parse(readFileSync(OPENAPI_FILE, 'utf8'));
}

export function listOperations(spec: any): OperationInfo[] {
  const ops: OperationInfo[] = [];
  if (!spec?.paths) return ops;
  for (const [p, item] of Object.entries<Record<string, any>>(spec.paths)) {
    for (const method of ['get', 'post', 'put', 'patch', 'delete']) {
      const op = item[method];
      if (!op) continue;
      ops.push({
        operationId: op.operationId,
        method: method.toUpperCase(),
        path: p,
        tag: op.tags?.[0] ?? '',
        summary: { en: op.summary ?? '', zh: op['x-i18n']?.zh?.summary ?? op.summary ?? '' },
        description: { en: op.description ?? '', zh: op['x-i18n']?.zh?.description ?? op.description ?? '' },
        notImplemented: op['x-status'] === 'not_implemented',
      });
    }
  }
  return ops;
}
