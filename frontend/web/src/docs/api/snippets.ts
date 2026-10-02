import { BASE_URL_PLACEHOLDER } from '../markdown';
import type { OperationEntry } from './openapi';
import { binaryResponseType, fileFields, isMultipart, jsonContent } from './openapi';

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

// 示例里上传用的本地文件名（语音识别）。
const UPLOAD_FILE = 'speech.mp3';

// outputFile 是二进制响应（语音合成）保存到本地的文件名，扩展名取 response_format。
function outputFile(body: unknown, contentType: string): string {
  const fmt = body && typeof body === 'object' ? (body as { response_format?: unknown }).response_format : undefined;
  if (typeof fmt === 'string' && fmt) return `output.${fmt}`;
  const sub = contentType.split('/')[1] ?? 'bin';
  return `output.${sub === 'mpeg' ? 'mp3' : sub}`;
}

// multipartSnippet：multipart/form-data 请求（语音识别）。body 是文本表单字段，
// 文件字段（schema format=binary）统一用本地文件 UPLOAD_FILE。
function multipartSnippet(lang: SnippetLang, entry: OperationEntry, url: string, key: boolean, body: unknown): string {
  const fields = Object.entries((body ?? {}) as Record<string, unknown>).map(([k, v]) => [k, String(v)] as const);
  const files = fileFields(entry);

  if (lang === 'cURL') {
    const lines = [`curl ${url}`];
    if (key) lines.push(`  -H "Authorization: Bearer $UFREETOKENS_API_KEY"`);
    for (const f of files) lines.push(`  -F "${f}=@${UPLOAD_FILE}"`);
    for (const [k, v] of fields) lines.push(`  -F ${JSON.stringify(`${k}=${v}`)}`);
    return lines.join(' \\\n');
  }

  if (lang === 'Python') {
    const out = ['import os', 'import requests', '', `with open("${UPLOAD_FILE}", "rb") as f:`, '    resp = requests.post(', `        "${url}",`];
    if (key) out.push(`        headers={"Authorization": f"Bearer {os.environ['UFREETOKENS_API_KEY']}"},`);
    out.push(`        files={${files.map((f) => `"${f}": f`).join(', ')}},`);
    out.push(`        data=${pyLiteral(Object.fromEntries(fields), 2)},`, '    )', 'resp.raise_for_status()', 'print(resp.json())');
    return out.join('\n');
  }

  const out = ["import { readFile } from 'node:fs/promises';", '', 'const form = new FormData();'];
  for (const f of files) out.push(`form.append('${f}', new Blob([await readFile('${UPLOAD_FILE}')]), '${UPLOAD_FILE}');`);
  for (const [k, v] of fields) out.push(`form.append('${k}', ${JSON.stringify(v)});`);
  out.push(`const resp = await fetch('${url}', {`, `  method: '${entry.method}',`);
  if (key) out.push('  headers: { Authorization: `Bearer ${process.env.UFREETOKENS_API_KEY}` },');
  out.push('  body: form,', '});');
  out.push("if (!resp.ok) throw new Error(`HTTP ${resp.status}: ${await resp.text()}`);", 'console.log(await resp.json());');
  return out.join('\n');
}

export function buildSnippet(lang: SnippetLang, entry: OperationEntry, body: unknown = exampleBody(entry)): string {
  const url = `${BASE_URL_PLACEHOLDER}${entry.path}${queryExample(entry)}`;
  const key = needsKey(entry);
  if (isMultipart(entry)) return multipartSnippet(lang, entry, url, key, body);

  const hasBody = body !== undefined;
  const json = JSON.stringify(body, null, 2);
  // 二进制响应（语音合成）：示例把响应写进本地文件，而不是当 JSON 打印。
  const binary = binaryResponseType(entry);
  const outFile = binary ? outputFile(body, binary) : '';

  if (lang === 'cURL') {
    const lines = [`curl${isStream(body) ? ' -N' : ''}${entry.method !== 'GET' && !hasBody ? ` -X ${entry.method}` : ''} ${url}`];
    if (key) lines.push(`  -H "Authorization: Bearer $UFREETOKENS_API_KEY"`);
    if (hasBody) {
      lines.push(`  -H "Content-Type: application/json"`);
      lines.push(`  -d '${json.replace(/'/g, `'\\''`)}'`);
    }
    if (binary) lines.push(`  --output ${outFile}`);
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
    if (binary) {
      out.push(`with open("${outFile}", "wb") as f:`);
      if (isStream(body)) out.push('    for chunk in resp.iter_content(chunk_size=None):', '        f.write(chunk)');
      else out.push('    f.write(resp.content)');
    } else if (isStream(body)) {
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
  const out = binary ? ["import { writeFile } from 'node:fs/promises';", ''] : [];
  out.push(`const resp = await fetch('${url}', {`, ...opts, '});');
  out.push("if (!resp.ok) throw new Error(`HTTP ${resp.status}: ${await resp.text()}`);");
  if (binary) {
    out.push(`await writeFile('${outFile}', Buffer.from(await resp.arrayBuffer()));`);
  } else if (isStream(body)) {
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
