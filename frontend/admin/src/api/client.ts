import { authStore } from './auth';
import { ApiError } from './errors';

const DEFAULT_TIMEOUT_MS = 30_000;

// cmd/admin 的接口挂在根路径，和 SPA 路由同名冲突，所以统一加 /admin-api
// 前缀，由 Vite proxy（dev）/ Nginx（prod）转发时去掉。见 vite.config.ts。
export const ADMIN_API_BASE = (import.meta.env.VITE_ADMIN_API_BASE ?? '/admin-api').replace(/\/$/, '');

export type QueryValue = string | number | boolean | null | undefined | Array<string | number>;

export interface RequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';
  query?: Record<string, QueryValue>;
  body?: unknown;
  signal?: AbortSignal;
  timeoutMs?: number;
  // 登录页校验令牌时用：还没写进 authStore 的候选令牌，且 401 时不要跳转。
  token?: string;
}

export function buildURL(path: string, query?: Record<string, QueryValue>): string {
  const qs = new URLSearchParams();
  if (query) {
    for (const [k, v] of Object.entries(query)) {
      if (v === undefined || v === null || v === '') continue;
      qs.set(k, Array.isArray(v) ? v.join(',') : String(v));
    }
  }
  const s = qs.toString();
  return `${ADMIN_API_BASE}${path}${s ? `?${s}` : ''}`;
}

function redirectToLogin() {
  authStore.clear();
  const { pathname, search } = window.location;
  if (pathname.startsWith('/login')) return;
  const next = encodeURIComponent(pathname + search);
  window.location.assign(`/login?next=${next}`);
}

// request 是 frontend/admin 发起网络请求的唯一入口：注入管理员令牌与操作人，
// 统一解析错误；401 说明令牌失效，清空登录态并回到登录页（登录后回跳原页面）。
export async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const { method = 'GET', query, body, signal, timeoutMs = DEFAULT_TIMEOUT_MS, token: explicitToken } = opts;
  const { token: storedToken, actorName } = authStore.get();
  const token = explicitToken ?? storedToken;

  const controller = new AbortController();
  let timedOut = false;
  const timer = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, timeoutMs);
  if (signal) {
    if (signal.aborted) controller.abort();
    else signal.addEventListener('abort', () => controller.abort(), { once: true });
  }

  const headers: Record<string, string> = {};
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  if (token) headers['Authorization'] = `Bearer ${token}`;
  if (actorName) headers['X-Actor-Name'] = encodeURIComponent(actorName);

  let res: Response;
  try {
    res = await fetch(buildURL(path, query), {
      method,
      headers,
      body: body !== undefined ? JSON.stringify(body) : undefined,
      credentials: 'same-origin',
      signal: controller.signal,
    });
  } catch (err) {
    if (signal?.aborted) throw err; // 调用方主动取消，原样抛出 AbortError
    throw ApiError.network(timedOut);
  } finally {
    clearTimeout(timer);
  }

  if (!res.ok) {
    const apiErr = await ApiError.fromResponse(res);
    if (res.status === 401 && !explicitToken) redirectToLogin();
    throw apiErr;
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

export function isAbortError(err: unknown): boolean {
  return err instanceof DOMException && err.name === 'AbortError';
}
