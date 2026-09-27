import { useSyncExternalStore } from 'react';

// Phase 0 鉴权（ARCHITECTURE.md §3）：运营在登录页输入被下发的
// UFT_ADMIN_TOKEN 和自己的姓名，存 sessionStorage——关闭标签页即失效，
// 比 localStorage 更安全。姓名作为所有写请求的 X-Actor-Name 头进入审计
// 日志（接口方案 §0.6）。RBAC 上线后只需要替换这个文件的存取方式。
const TOKEN_KEY = 'uft_admin_token';
const ACTOR_KEY = 'uft_admin_actor';

export interface AuthState {
  token: string | null;
  actorName: string | null;
}

type Listener = () => void;
const listeners = new Set<Listener>();

function read(): AuthState {
  return {
    token: sessionStorage.getItem(TOKEN_KEY),
    actorName: sessionStorage.getItem(ACTOR_KEY),
  };
}

// useSyncExternalStore 要求 getSnapshot 在状态不变时返回同一个引用。
let snapshot: AuthState = read();

function emit() {
  snapshot = read();
  listeners.forEach((l) => l());
}

export const authStore = {
  get(): AuthState {
    return snapshot;
  },
  isAuthenticated(): boolean {
    return !!snapshot.token;
  },
  set(token: string, actorName: string) {
    sessionStorage.setItem(TOKEN_KEY, token);
    sessionStorage.setItem(ACTOR_KEY, actorName);
    emit();
  },
  clear() {
    sessionStorage.removeItem(TOKEN_KEY);
    sessionStorage.removeItem(ACTOR_KEY);
    emit();
  },
  subscribe(listener: Listener): () => void {
    listeners.add(listener);
    return () => listeners.delete(listener);
  },
};

export function useAuth(): AuthState {
  return useSyncExternalStore(authStore.subscribe, authStore.get, authStore.get);
}
