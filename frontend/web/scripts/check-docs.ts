// 文档一致性检查（npm run docs:check，已串进 npm run lint）。
//
// 错误（退出码 1）：
//   - frontmatter 缺字段 / section 非法
//   - MDX 里出现的 /v1/... 路径不在 docs/gateway-openapi.json 里（文档不许写不存在的接口）
//   - 站内链接 /docs/<lang>/<slug>#<anchor> 指向不存在的页面、接口或锚点
//   - en/ 下出现 zh/ 没有的页面（中文是源语言）
// 警告（不阻断）：
//   - en/ 缺少某篇译文
//   - 译文的 translatedFrom 与中文源文当前 hash 不一致（中文改过，译文可能过期）
//
// 翻译/校对完英文后运行 `npm run docs:check -- --write-hashes`，把每篇英文的
// translatedFrom 更新为对应中文源文的当前 hash。
import { readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import {
  LOCALES,
  SOURCE_LOCALE,
  WEB_ROOT,
  contentHash,
  listOperations,
  loadOpenAPI,
  loadPages,
  type DocPage,
  type LoadIssue,
} from '../tools/docsContent.ts';

const errors: string[] = [];
const warnings: string[] = [];

const issues: LoadIssue[] = [];
const pages = loadPages(issues);
for (const i of issues) errors.push(`${i.file}: ${i.message}`);

const spec = loadOpenAPI();
if (!spec) errors.push('找不到 docs/gateway-openapi.json（先运行 UPDATE_GATEWAY_API_DOC=1 go test ./internal/app -run TestGatewayOpenAPI_UpToDate）');
const specPaths = new Set(Object.keys(spec?.paths ?? {}));
const operationIds = new Set(listOperations(spec).map((o) => o.operationId));
const errorCodes = new Set<string>((spec?.components?.['x-error-codes'] ?? []).map((c: { code: string }) => c.code));

const byKey = new Map(pages.map((p) => [`${p.lang}/${p.slug}`, p]));
const zhHash = new Map(pages.filter((p) => p.lang === SOURCE_LOCALE).map((p) => [p.slug, contentHash(p.body)]));

function lineOf(body: string, index: number): number {
  return body.slice(0, index).split('\n').length;
}

function checkApiPaths(p: DocPage) {
  for (const m of p.body.matchAll(/\/v1\/[A-Za-z0-9_./{}-]*[A-Za-z0-9_}]/g)) {
    const used = m[0];
    if (!specPaths.has(used)) errors.push(`${p.file}:${lineOf(p.body, m.index!)}: 接口路径 ${used} 不在 gateway-openapi.json 中`);
  }
}

function checkLinks(p: DocPage) {
  for (const m of p.body.matchAll(/\]\((\/docs\/[^)\s#]*)(#[^)\s]*)?\)|href="(\/docs\/[^"#]*)(#[^"]*)?"/g)) {
    const target = (m[1] ?? m[3]).replace(/\/$/, '');
    const anchor = (m[2] ?? m[4] ?? '').slice(1);
    const where = `${p.file}:${lineOf(p.body, m.index!)}`;
    const [, , lang, ...rest] = target.split('/');
    if (!LOCALES.includes(lang as (typeof LOCALES)[number])) {
      errors.push(`${where}: 链接 ${target} 缺少语言前缀（应为 /docs/zh/... 或 /docs/en/...）`);
      continue;
    }
    if (lang !== p.lang) warnings.push(`${where}: ${p.lang} 页面链接到了 ${lang} 页面 ${target}`);
    const slug = rest.join('/') || 'index';
    if (rest[0] === 'api' && rest.length === 2) {
      if (!operationIds.has(rest[1])) errors.push(`${where}: 接口 ${rest[1]} 不存在`);
      continue;
    }
    const page = byKey.get(`${lang}/${slug}`) ?? byKey.get(`${SOURCE_LOCALE}/${slug}`);
    if (!page) {
      errors.push(`${where}: 链接 ${target} 指向不存在的页面`);
      continue;
    }
    if (!anchor) continue;
    const anchorCode = /^code-(.+)$/.exec(anchor);
    if (slug === 'errors' && anchorCode && errorCodes.has(anchorCode[1])) continue;
    if (!page.headings.some((h) => h.id === decodeURIComponent(anchor))) {
      errors.push(`${where}: 锚点 #${anchor} 在 ${page.file} 中不存在（可用：${page.headings.map((h) => h.id).join(', ')}）`);
    }
  }
}

for (const p of pages) {
  checkApiPaths(p);
  checkLinks(p);
}

for (const lang of LOCALES.filter((l) => l !== SOURCE_LOCALE)) {
  for (const p of pages.filter((x) => x.lang === lang)) {
    if (!zhHash.has(p.slug)) errors.push(`${p.file}: ${SOURCE_LOCALE}/ 下没有对应的源文 ${p.slug}.mdx`);
    else if (!p.translatedFrom) warnings.push(`${p.file}: 缺少 translatedFrom（翻译完成后运行 --write-hashes）`);
    else if (p.translatedFrom !== zhHash.get(p.slug)) warnings.push(`${p.file}: 中文源文已更新，译文可能过期`);
  }
  for (const slug of zhHash.keys()) {
    if (!byKey.has(`${lang}/${slug}`)) warnings.push(`content/${lang}/${slug}.mdx: 缺少译文`);
  }
}

if (process.argv.includes('--write-hashes')) {
  for (const p of pages.filter((x) => x.lang !== SOURCE_LOCALE && zhHash.has(x.slug))) {
    const file = path.join(WEB_ROOT, p.file);
    const raw = readFileSync(file, 'utf8').replace(/\r\n/g, '\n');
    const hash = zhHash.get(p.slug)!;
    const next = /^translatedFrom:.*$/m.test(raw.split('\n---\n')[0])
      ? raw.replace(/^translatedFrom:.*$/m, `translatedFrom: ${hash}`)
      : raw.replace(/^(---\n[\s\S]*?)\n---\n/, `$1\ntranslatedFrom: ${hash}\n---\n`);
    if (next !== raw) writeFileSync(file, next);
  }
  console.log('已更新译文的 translatedFrom。');
  process.exit(0);
}

for (const w of warnings) console.warn(`warn  ${w}`);
for (const e of errors) console.error(`error ${e}`);
console.log(`docs:check — ${pages.length} 个页面，${errors.length} 个错误，${warnings.length} 个警告`);
process.exit(errors.length ? 1 : 0);
