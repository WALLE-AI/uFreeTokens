import spec from 'virtual:docs-openapi';
import type { Locale } from '../i18n';
import { pick } from '../i18n';

// docs/gateway-openapi.json 的前端类型（只声明文档页面用得到的部分）。
// 约定见 internal/app/gateway_openapi.go：英文写在标准字段里，中文写在
// x-i18n.zh；tools/docsPlugin.ts 额外把每个 description 预渲染成 x-desc-html。

export interface I18nText {
  en: string;
  zh: string;
}

interface Described {
  description?: string;
  'x-i18n'?: { zh?: { description?: string; summary?: string } };
  'x-desc-html'?: I18nText;
}

export interface Schema extends Described {
  $ref?: string;
  type?: string | string[];
  format?: string;
  properties?: Record<string, Schema>;
  required?: string[];
  items?: Schema;
  additionalProperties?: boolean | Schema;
  enum?: unknown[];
  default?: unknown;
  oneOf?: Schema[];
  anyOf?: Schema[];
  allOf?: Schema[];
  nullable?: boolean;
  example?: unknown;
}

export interface MediaType {
  schema?: Schema;
  example?: unknown;
}

export interface Parameter extends Described {
  name: string;
  in: 'query' | 'path' | 'header';
  required?: boolean;
  schema?: Schema;
  example?: unknown;
}

export interface Response extends Described {
  $ref?: string;
  content?: Record<string, MediaType>;
  headers?: Record<string, { $ref?: string } & Described>;
  'x-error-codes'?: string[];
}

export interface Operation extends Described {
  operationId: string;
  summary?: string;
  tags?: string[];
  security?: Array<Record<string, string[]>>;
  parameters?: Parameter[];
  requestBody?: { required?: boolean; content?: Record<string, MediaType> } & Described;
  responses?: Record<string, Response>;
  'x-status'?: 'not_implemented';
  'x-auth'?: 'none' | 'api_key';
  'x-billable'?: boolean;
  'x-rate-limit'?: I18nText;
}

export interface ErrorCode {
  code: string;
  status: number;
  retryable: boolean;
  description: I18nText;
}

export interface OpenAPISpec {
  openapi?: string;
  paths?: Record<string, Record<string, Operation>>;
  components?: {
    schemas?: Record<string, Schema>;
    responses?: Record<string, Response>;
    headers?: Record<string, Described & { schema?: Schema }>;
    'x-error-codes'?: ErrorCode[];
  };
}

export interface OperationEntry {
  method: string;
  path: string;
  op: Operation;
}

export const TAG_ORDER = ['Catalog', 'Models', 'Usage', 'Chat', 'Embeddings', 'Anthropic', 'Not implemented'];

export function operations(): OperationEntry[] {
  const out: OperationEntry[] = [];
  for (const [path, item] of Object.entries(spec.paths ?? {})) {
    for (const method of ['get', 'post', 'put', 'patch', 'delete']) {
      const op = item[method];
      if (op) out.push({ method: method.toUpperCase(), path, op });
    }
  }
  const tagIndex = (e: OperationEntry) => {
    const i = TAG_ORDER.indexOf(e.op.tags?.[0] ?? '');
    return i === -1 ? TAG_ORDER.length : i;
  };
  return out.sort((a, b) => tagIndex(a) - tagIndex(b) || a.path.localeCompare(b.path));
}

export function findOperation(operationId: string): OperationEntry | undefined {
  return operations().find((e) => e.op.operationId === operationId);
}

export function errorCodes(): ErrorCode[] {
  return spec.components?.['x-error-codes'] ?? [];
}

// resolve 展开 #/components/... 形式的 $ref（只支持本文件内引用，够用）。
export function resolve<T extends { $ref?: string }>(node: T | undefined): T | undefined {
  let cur: T | undefined = node;
  for (let i = 0; cur?.$ref && i < 10; i++) {
    const parts = cur.$ref.replace(/^#\//, '').split('/');
    let target: unknown = spec;
    for (const p of parts) target = (target as Record<string, unknown> | undefined)?.[p];
    cur = target as T | undefined;
  }
  return cur;
}

export function refName(node: { $ref?: string } | undefined): string | undefined {
  return node?.$ref?.split('/').pop();
}

export function summaryOf(op: Operation, locale: Locale): string {
  return locale === 'zh' ? op['x-i18n']?.zh?.summary || op.summary || '' : op.summary || '';
}

// descHtml 返回某个节点 description 的预渲染 HTML（内容来自仓库内的 OpenAPI 文件，可信）。
export function descHtml(node: Described | undefined, locale: Locale): string {
  return pick(node?.['x-desc-html'], locale);
}

export function jsonContent(r: { content?: Record<string, MediaType> } | undefined): MediaType | undefined {
  if (!r?.content) return undefined;
  return r.content['application/json'] ?? Object.values(r.content)[0];
}
