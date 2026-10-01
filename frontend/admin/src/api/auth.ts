import { useSyncExternalStore } from 'react';
import type { AdminMe, Permission } from '../types';

// 管理员登录态（B5，见 docs/admin-api.md §1）：POST /auth/login 拿到会话令牌
// （uas_ 开头）后与 GET /me 返回的身份、权限一起存 sessionStorage——关闭标签页
// 即失效。审计日志的操作人由服务端从会话解析，前端不再发送 X-Actor-Name。
// 应急共享令牌同样通过这里保存，身份为 system（me.break_glass=true）。
const TOKEN_KEY = 'uft_admin_token';
const ME_KEY = 'uft_admin_me';

export interface AuthState {
  token: string | null;
  me: AdminMe | null;
  // actorName 是当前管理员的显示名（兼容旧组件）。
  actorName: string | null;
}

type Listener = () => void;
const listeners = new Set<Listener>();

function read(): AuthState {
  const token = sessionStorage.getItem(TOKEN_KEY);
  let me: AdminMe | null = null;
  try {
    const raw = sessionStorage.getItem(ME_KEY);
    me = raw ? (JSON.parse(raw) as AdminMe) : null;
  } catch {
    me = null;
  }
  return { token, me, actorName: me?.name ?? null };
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
    return !!snapshot.token && !!snapshot.me;
  },
  set(token: string, me: AdminMe) {
    sessionStorage.setItem(TOKEN_KEY, token);
    sessionStorage.setItem(ME_KEY, JSON.stringify(me));
    emit();
  },
  setMe(me: AdminMe) {
    sessionStorage.setItem(ME_KEY, JSON.stringify(me));
    emit();
  },
  clear() {
    sessionStorage.removeItem(TOKEN_KEY);
    sessionStorage.removeItem(ME_KEY);
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

// can 判断当前管理员是否拥有某个权限点（"*" 视为全部）。只用于界面显隐，
// 服务端仍会对每个请求做权限校验（403 permission_denied）。
export function can(me: AdminMe | null, perm: Permission | undefined): boolean {
  if (!perm) return !!me;
  if (!me) return false;
  return me.permissions.includes('*') || me.permissions.includes(perm);
}

export function useCan(perm: Permission | undefined): boolean {
  return can(useAuth().me, perm);
}
