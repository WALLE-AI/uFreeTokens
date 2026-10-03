import { useSyncExternalStore } from 'react';
import { getAgentMeta, type AgentMetaResponse, type ContextRef, type PageContext } from '../api/agent';

// 智能体的全局状态（不依赖 React 树位置，导航、页面嵌入组件与 Dock 共用）：
//   - meta：GET /agent/meta；enabled=false 时前端不渲染任何智能体入口（实施方案 §2-4）；
//   - pageContext：页面通过 useAgentContext 注册的“当前对象”，Dock 顶部显示为上下文芯片；
//   - currentPage / pageState：用户所在的页面（AdminLayout 按路由维护）与页面登记的状态（useAgentPageState），
//     每条消息随请求发送，全局助手据此理解「这个」「当前筛选」。

type Listener = () => void;

function createStore<T>(initial: T) {
  let value = initial;
  const listeners = new Set<Listener>();
  return {
    get: () => value,
    set(next: T) {
      value = next;
      listeners.forEach((l) => l());
    },
    subscribe(l: Listener) {
      listeners.add(l);
      return () => listeners.delete(l);
    },
  };
}

export const agentMetaStore = createStore<AgentMetaResponse | null>(null);

let loading: Promise<void> | null = null;

// loadAgentMeta 拉取一次元信息（失败或无权限时视为未启用）。
export function loadAgentMeta(force = false): Promise<void> {
  if (loading && !force) return loading;
  loading = getAgentMeta()
    .then((m) => agentMetaStore.set(m))
    .catch(() => agentMetaStore.set(null));
  return loading;
}

export function resetAgentMeta() {
  loading = null;
  agentMetaStore.set(null);
}

export function useAgentMeta(): AgentMetaResponse | null {
  return useSyncExternalStore(agentMetaStore.subscribe, agentMetaStore.get, agentMetaStore.get);
}

export function useAgentEnabled(): boolean {
  return !!useAgentMeta()?.enabled;
}

// ---------- 页面上下文 ----------

export const pageContextStore = createStore<ContextRef[]>([]);

export function usePageContext(): ContextRef[] {
  return useSyncExternalStore(pageContextStore.subscribe, pageContextStore.get, pageContextStore.get);
}

export function ctxKey(c: ContextRef) {
  return `${c.type}:${c.id}`;
}

// ---------- 当前页面 ----------

export const currentPageStore = createStore<PageContext | null>(null);
export const pageStateStore = createStore<Record<string, string>>({});

export function useCurrentPage(): PageContext | null {
  return useSyncExternalStore(currentPageStore.subscribe, currentPageStore.get, currentPageStore.get);
}

// snapshotPage 返回发送消息时要带上的页面上下文（路由 + 页面登记的状态）。
export function snapshotPage(): PageContext | null {
  const page = currentPageStore.get();
  if (!page) return null;
  const state = pageStateStore.get();
  return Object.keys(state).length > 0 ? { ...page, state } : page;
}
