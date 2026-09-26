import { useEffect, useState } from 'react';

const STORAGE_KEY = 'uft.apiKey';

type Listener = (apiKey: string | null) => void;

// authStore 目前只管理 BYOK 模式下浏览器本地保存的 API Key（'mode' 字段为
// 迭代4 的 console 会话模式预留：那时会有 'byok'|'session' 两种取值，/v1
// 调用始终走 byok 的 Key，console 会话只用于控制台自身的登录态，见
// docs/frontend-web 与 Go 后端集成迭代执行方案.md 的"已确认的决策"）。
// 只存在 localStorage 里，从不发到除 gateway /v1 以外的任何地方。
class AuthStore {
  private listeners = new Set<Listener>();

  get mode(): 'byok' {
    return 'byok';
  }

  getApiKey(): string | null {
    try {
      return window.localStorage.getItem(STORAGE_KEY);
    } catch {
      return null;
    }
  }

  setApiKey(key: string): void {
    try {
      window.localStorage.setItem(STORAGE_KEY, key);
    } catch {
      // localStorage 不可用（隐私模式等）时静默忽略，不阻塞使用；只是刷新页面后
      // 需要重新连接。
    }
    this.emit(key);
  }

  clear(): void {
    try {
      window.localStorage.removeItem(STORAGE_KEY);
    } catch {
      // ignore
    }
    this.emit(null);
  }

  subscribe(listener: Listener): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  private emit(key: string | null): void {
    this.listeners.forEach((l) => l(key));
  }
}

export const authStore = new AuthStore();

// useApiKey 让组件响应式地跟随 authStore 的变化（连接/断开），不用自己订阅。
export function useApiKey(): string | null {
  const [key, setKey] = useState<string | null>(() => authStore.getApiKey());
  useEffect(() => authStore.subscribe(setKey), []);
  return key;
}
