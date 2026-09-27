import { useEffect, useState } from 'react';
import { ConsoleUser, getMe } from './console';

const STORAGE_KEY = 'uft.apiKey';

type Listener = (apiKey: string | null) => void;

// 两套完全独立的鉴权状态（技术方案的"已确认的决策"：控制台鉴权与 API Key
// 鉴权完全分离）：
//   - authStore（下面）：BYOK 模式下浏览器本地保存的 API Key，/v1/* 调用用它。
//   - consoleAuthStore（本文件下半部分）：控制台登录态，只活在 httpOnly
//     Cookie 里，前端拿不到 token 本身，只缓存服务端返回的 profile（me）。
// 两者互不依赖：可以只连 Key 不登录控制台（纯 BYOK 玩家），也可以登录了
// 控制台但还没在这台设备连 Key（刚注册，只能在个人中心建 Key，建完后才能用
// Playground）。
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

type ConsoleListener = (me: ConsoleUser | null) => void;

// ConsoleAuthStore 缓存 GET /console/me 的结果，纯内存（不落 localStorage：
// 会话本身由 httpOnly Cookie 维持，前端缓存只是省得每次渲染都发一次
// /console/me）。页面刷新后内存状态会丢失，但 Cookie 还在，
// useConsoleUser 会在还没 initialize 过的时候自动重新拉一次 /console/me
// 校验登录态是否仍然有效。
class ConsoleAuthStore {
  private me: ConsoleUser | null = null;
  private initialized = false;
  private listeners = new Set<ConsoleListener>();

  getMe(): ConsoleUser | null {
    return this.me;
  }

  isInitialized(): boolean {
    return this.initialized;
  }

  setMe(me: ConsoleUser | null): void {
    this.me = me;
    this.initialized = true;
    this.listeners.forEach((l) => l(me));
  }

  subscribe(listener: ConsoleListener): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }
}

export const consoleAuthStore = new ConsoleAuthStore();

// useConsoleUser 返回当前控制台登录态；首次挂载时如果还没 initialize 过，
// 会自动打一次 GET /console/me 探测是否已经有一个有效的会话 Cookie
// （典型场景：用户之前登录过，刷新了页面）。401 视为"未登录"，不当错误抛出。
export function useConsoleUser(): { me: ConsoleUser | null; loading: boolean } {
  const [me, setMeState] = useState<ConsoleUser | null>(() => consoleAuthStore.getMe());
  const [loading, setLoading] = useState(!consoleAuthStore.isInitialized());

  useEffect(() => {
    const unsubscribe = consoleAuthStore.subscribe((next) => {
      setMeState(next);
      setLoading(false);
    });
    if (!consoleAuthStore.isInitialized()) {
      getMe()
        .then((profile) => consoleAuthStore.setMe(profile))
        .catch(() => consoleAuthStore.setMe(null));
    }
    return unsubscribe;
  }, []);

  return { me, loading };
}
