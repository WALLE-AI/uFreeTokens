// 文档站点的 Vite 插件：把 src/docs/content 下的 MDX 和 docs/gateway-openapi.json
// 变成前端能直接 import 的几个虚拟模块，并在 build 时产出 llms.txt。
//
//   virtual:docs-manifest      导航用的页面元数据（不含正文，体积很小）
//   virtual:docs-search/<lang> 搜索索引的原始文档（按语言拆分，按需加载）
//   virtual:docs-openapi       网关 OpenAPI；description 预先渲染成 HTML
//
// OpenAPI 文件在仓库根目录的 docs/ 下，通过虚拟模块读进来，而不是放宽
// server.fs.allow——dev server 监听 0.0.0.0，不想把整个仓库暴露出去。
import type { Plugin, ResolvedConfig, ViteDevServer } from 'vite';
import { micromark } from 'micromark';
import { gfm, gfmHtml } from 'micromark-extension-gfm';
import {
  CONTENT_DIR,
  LOCALES,
  OPENAPI_FILE,
  SOURCE_LOCALE,
  contentHash,
  listOperations,
  loadOpenAPI,
  loadPages,
  type DocPage,
  type Locale,
} from './docsContent.ts';
import { mdxToMarkdown } from '../src/docs/markdown.ts';

const MANIFEST_ID = 'virtual:docs-manifest';
const SEARCH_PREFIX = 'virtual:docs-search/';
const OPENAPI_ID = 'virtual:docs-openapi';
const RESOLVED_PREFIX = '\0';

const DEFAULT_ORIGIN = 'https://ufreetokens.com';

function renderMarkdown(md: string): string {
  return micromark(md, { extensions: [gfm()], htmlExtensions: [gfmHtml()] });
}

// 递归给所有字符串 description 补上 x-desc-html: {en, zh}。
function withRenderedDescriptions(node: unknown): unknown {
  if (Array.isArray(node)) return node.map(withRenderedDescriptions);
  if (!node || typeof node !== 'object') return node;
  const obj = node as Record<string, unknown>;
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(obj)) out[k] = withRenderedDescriptions(v);
  if (typeof obj.description === 'string') {
    const zh = (obj['x-i18n'] as { zh?: { description?: string } } | undefined)?.zh?.description ?? obj.description;
    out['x-desc-html'] = { en: renderMarkdown(obj.description), zh: renderMarkdown(zh) };
  }
  return out;
}

function manifestOf(pages: DocPage[]) {
  const sourceHash = new Map(pages.filter((p) => p.lang === SOURCE_LOCALE).map((p) => [p.slug, contentHash(p.body)]));
  return pages.map((p) => ({
    lang: p.lang,
    slug: p.slug,
    title: p.title,
    description: p.description,
    section: p.section,
    order: p.order,
    // 译文对应的源文已经改过（hash 不一致）时，页面上提示"译文可能过期"。
    outdated: p.lang !== SOURCE_LOCALE && !!p.translatedFrom && p.translatedFrom !== sourceHash.get(p.slug),
  }));
}

function searchDocsOf(pages: DocPage[], lang: Locale, spec: unknown) {
  const docs: Array<{ id: string; path: string; page: string; heading: string; text: string }> = [];
  for (const p of pages.filter((x) => x.lang === lang)) {
    const base = p.slug === 'index' ? `/docs/${lang}` : `/docs/${lang}/${p.slug}`;
    for (const s of p.sections) {
      docs.push({
        id: `${p.slug}#${s.id}`,
        path: s.id ? `${base}#${s.id}` : base,
        page: p.title,
        heading: s.heading,
        text: s.text.slice(0, 2000),
      });
    }
  }
  for (const op of listOperations(spec)) {
    docs.push({
      id: `api/${op.operationId}`,
      path: `/docs/${lang}/api/${op.operationId}`,
      page: lang === 'zh' ? 'API 参考' : 'API Reference',
      heading: `${op.method} ${op.path} · ${op.summary[lang]}`,
      text: op.description[lang].slice(0, 2000),
    });
  }
  return docs;
}

function llmsFiles(pages: DocPage[], spec: unknown, origin: string) {
  const files: Record<string, string> = {};
  const ops = listOperations(spec).filter((o) => !o.notImplemented);
  for (const lang of LOCALES) {
    const own = pages.filter((p) => p.lang === lang);
    const full = own
      .map((p) => mdxToMarkdown(p.title, p.description, p.body, origin))
      .join('\n---\n\n');
    files[lang === 'en' ? 'llms-full.txt' : `llms-full.${lang}.txt`] = full;
  }
  const en = pages.filter((p) => p.lang === 'en');
  const link = (p: DocPage) => `${origin}/docs/en${p.slug === 'index' ? '' : `/${p.slug}`}`;
  files['llms.txt'] = [
    '# uFreeTokens',
    '',
    '> OpenAI-compatible LLM API gateway. Base URL: ' + `${origin}/v1` + ', auth: `Authorization: Bearer <api-key>`. Prices are in CNY; money fields are int64 micro-yuan (1,000,000 = 1 CNY).',
    '',
    '## Docs',
    '',
    ...en.filter((p) => p.section !== 'api').map((p) => `- [${p.title}](${link(p)}): ${p.description}`),
    '',
    '## API Reference',
    '',
    `- [OpenAPI spec](${origin}/gateway-openapi.json)`,
    ...ops.map((o) => `- [${o.method} ${o.path}](${origin}/docs/en/api/${o.operationId}): ${o.summary.en}`),
    '',
    '## Optional',
    '',
    `- [Full docs as one file](${origin}/llms-full.txt)`,
    `- [完整中文文档](${origin}/llms-full.zh.txt)`,
    '',
  ].join('\n');
  return files;
}

export function docsPlugin(): Plugin {
  let config: ResolvedConfig;
  const origin = () => (config?.env?.VITE_DOCS_PUBLIC_ORIGIN || DEFAULT_ORIGIN).replace(/\/$/, '');

  const invalidate = (server: ViteDevServer) => {
    for (const mod of server.moduleGraph.idToModuleMap.values()) {
      if (mod.id?.startsWith(RESOLVED_PREFIX + 'virtual:docs-')) server.moduleGraph.invalidateModule(mod);
    }
    server.ws.send({ type: 'full-reload' });
  };

  return {
    name: 'ufreetokens-docs',
    configResolved(c) {
      config = c;
    },
    resolveId(id) {
      if (id === MANIFEST_ID || id === OPENAPI_ID || id.startsWith(SEARCH_PREFIX)) return RESOLVED_PREFIX + id;
      return null;
    },
    load(id) {
      if (!id.startsWith(RESOLVED_PREFIX + 'virtual:docs-')) return null;
      const vid = id.slice(RESOLVED_PREFIX.length);
      this.addWatchFile(OPENAPI_FILE);
      if (vid === OPENAPI_ID) {
        return `export default ${JSON.stringify(withRenderedDescriptions(loadOpenAPI() ?? {}))};`;
      }
      const pages = loadPages();
      if (vid === MANIFEST_ID) return `export default ${JSON.stringify(manifestOf(pages))};`;
      const lang = vid.slice(SEARCH_PREFIX.length) as Locale;
      return `export default ${JSON.stringify(searchDocsOf(pages, lang, loadOpenAPI()))};`;
    },
    configureServer(server) {
      server.watcher.add([CONTENT_DIR, OPENAPI_FILE]);
      const onFs = (file: string) => {
        const f = file.replace(/\\/g, '/');
        if (f.startsWith(CONTENT_DIR.replace(/\\/g, '/')) || f === OPENAPI_FILE.replace(/\\/g, '/')) invalidate(server);
      };
      server.watcher.on('add', onFs);
      server.watcher.on('unlink', onFs);
      server.watcher.on('change', onFs);
    },
    generateBundle() {
      const spec = loadOpenAPI();
      for (const [fileName, source] of Object.entries(llmsFiles(loadPages(), spec, origin()))) {
        this.emitFile({ type: 'asset', fileName, source });
      }
      if (spec) this.emitFile({ type: 'asset', fileName: 'gateway-openapi.json', source: JSON.stringify(spec, null, 2) });
    },
  };
}
