import { ApiError } from './errors';

const DEFAULT_TIMEOUT_MS = 30_000;

// GATEWAY_BASE_URL 留空表示同源：dev 下由 vite.config.ts 的 server.proxy 转发
// /v1、/console 到本地 gateway，prod 下由 Nginx 反代（见
// deploy/nginx/web.conf，迭代7 补)。只有前端和 gateway 分开部署、不同源时才
// 需要在 .env 里显式填 VITE_GATEWAY_BASE_URL。
export const GATEWAY_BASE_URL = (import.meta.env.VITE_GATEWAY_BASE_URL ?? '').replace(/\/$/, '');

export function resolveURL(path: string): string {
  return `${GATEWAY_BASE_URL}${path}`;
}

export interface RequestOptions {
  method?: string;
  body?: unknown;
  apiKey?: string | null;
  timeoutMs?: number;
  signal?: AbortSignal;
}

// request 是全项目发起普通（非流式）JSON 请求的唯一入口。流式聊天走
// chat.ts，直接用 fetch + sse.ts 解析响应体，逻辑不一样，不复用这里。
export async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const { method = 'GET', body, apiKey, timeoutMs = DEFAULT_TIMEOUT_MS, signal } = opts;

  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  if (signal) {
    if (signal.aborted) controller.abort();
    else signal.addEventListener('abort', () => controller.abort(), { once: true });
  }

  const headers: Record<string, string> = {};
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (apiKey) headers['Authorization'] = `Bearer ${apiKey}`;

  let res: Response;
  try {
    res = await fetch(resolveURL(path), {
      method,
      headers,
      body: body !== undefined ? JSON.stringify(body) : undefined,
      credentials: 'same-origin',
      signal: controller.signal,
    });
  } finally {
    clearTimeout(timer);
  }

  if (!res.ok) {
    throw await ApiError.fromResponse(res);
  }
  if (res.status === 204) {
    return undefined as T;
  }
  return (await res.json()) as T;
}
