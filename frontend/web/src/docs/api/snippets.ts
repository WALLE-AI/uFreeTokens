import { BASE_URL_PLACEHOLDER } from '../markdown';
import type { OperationEntry } from './openapi';
import { jsonContent } from './openapi';

// 根据 OpenAPI operation + 示例请求体生成三种语言的调用代码。三种语言从同一份
// 数据生成，不会各写各的、慢慢不一致。Key 一律写成环境变量，绝不注入真实 Key。

export type SnippetLang = 'cURL' | 'Python' | 'Node.js';
export const SNIPPET_LANGS: SnippetLang[] = ['cURL', 'Python', 'Node.js'];

export function exampleBody(entry: OperationEntry): unknown {
  return jsonContent(entry.op.requestBody)?.example;
}

function queryExample(entry: OperationEntry): string {
  const qs = (entry.op.parameters ?? [])
    .filter((p) => p.in === 'query' && p.example !== undefined)
    .map((p) => `${encodeURIComponent(p.name)}=${encodeURIComponent(String(p.example))}`);
  return qs.length ? `?${qs.join('&')}` : '';
}

function needsKey(entry: OperationEntry): boolean {
  return entry.op['x-auth'] !== 'none' && (entry.op.security?.length ?? 1) > 0;
}

// pyLiteral 把 JSON 值写成 Python 字面量（true/false/null → True/False/None）。
function pyLiteral(v: unknown, indent = 0): string {
  const pad = '    '.repeat(indent + 1);
  const end = '    '.repeat(indent);
  if (v === null || v === undefined) return 'None';
  if (v === true) return 'True';
  if (v === false) return 'False';
  if (typeof v === 'number') return String(v);
  if (typeof v === 'string') return JSON.stringify(v);
  if (Array.isArray(v)) {
    if (v.length === 0) return '[]';
    return `[\n${v.map((x) => pad + pyLiteral(x, indent + 1)).join(',\n')},\n${end}]`;
  }
  const entries = Object.entries(v as Record<string, unknown>);
  if (entries.length === 0) return '{}';
  return `{\n${entries.map(([k, x]) => `${pad}${JSON.stringify(k)}: ${pyLiteral(x, indent + 1)}`).join(',\n')},\n${end}}`;
}

function isStream(body: unknown): boolean {
  return !!body && typeof body === 'object' && (body as { stream?: unknown }).stream === true;
}

export function buildSnippet(lang: SnippetLang, entry: OperationEntry, body: unknown = exampleBody(entry)): string {
  const url = `${BASE_URL_PLACEHOLDER}${entry.path}${queryExample(entry)}`;
  const key = needsKey(entry);
  const hasBody = body !== undefined;
  const json = JSON.stringify(body, null, 2);

  if (lang === 'cURL') {
    const lines = [`curl${isStream(body) ? ' -N' : ''}${entry.method !== 'GET' && !hasBody ? ` -X ${entry.method}` : ''} ${url}`];
    if (key) lines.push(`  -H "Authorization: Bearer $UFREETOKENS_API_KEY"`);
    if (hasBody) {
      lines.push(`  -H "Content-Type: application/json"`);
      lines.push(`  -d '${json.replace(/'/g, `'\\''`)}'`);
    }
    return lines.join(' \\\n');
  }

  if (lang === 'Python') {
    const out = ['import os', 'import requests', ''];
    const args = [`    "${url}",`];
    if (key) args.push(`    headers={"Authorization": f"Bearer {os.environ['UFREETOKENS_API_KEY']}"},`);
    if (hasBody) args.push(`    json=${pyLiteral(body, 1)},`);
    if (isStream(body)) args.push('    stream=True,');
    out.push(`resp = requests.${entry.method.toLowerCase()}(`, ...args, ')');
    out.push('resp.raise_for_status()');
    if (isStream(body)) {
      out.push('for line in resp.iter_lines():', '    if line:', '        print(line.decode())');
    } else {
      out.push('print(resp.json())');
    }
    return out.join('\n');
  }

  const headers: string[] = [];
  if (key) headers.push('    Authorization: `Bearer ${process.env.UFREETOKENS_API_KEY}`,');
  if (hasBody) headers.push("    'Content-Type': 'application/json',");
  const opts = [`  method: '${entry.method}',`];
  if (headers.length) opts.push('  headers: {', ...headers, '  },');
  if (hasBody) opts.push(`  body: JSON.stringify(${json.replace(/\n/g, '\n  ')}),`);
  const out = [`const resp = await fetch('${url}', {`, ...opts, '});'];
  out.push("if (!resp.ok) throw new Error(`HTTP ${resp.status}: ${await resp.text()}`);");
  if (isStream(body)) {
    out.push(
      'const reader = resp.body.getReader();',
      'const decoder = new TextDecoder();',
      'for (;;) {',
      '  const { done, value } = await reader.read();',
      '  if (done) break;',
      '  process.stdout.write(decoder.decode(value, { stream: true }));',
      '}',
    );
  } else {
    out.push('console.log(await resp.json());');
  }
  return out.join('\n');
}
