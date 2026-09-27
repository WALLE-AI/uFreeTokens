import { useCallback, useEffect, useRef, useState } from 'react';
import { isAbortError } from '../api/client';

export interface AsyncState<T> {
  data: T | undefined;
  error: unknown;
  loading: boolean; // 首次加载（无数据）
  refreshing: boolean; // 已有数据时的刷新：保留旧数据，不闪白屏（UI_DESIGN.md §6）
  reload: () => void;
}

// useAsync 是各页面拉取数据的统一写法：deps 变化自动重拉，卸载/重拉时
// 取消上一次请求，避免旧响应覆盖新结果。
export function useAsync<T>(fn: (signal: AbortSignal) => Promise<T>, deps: unknown[]): AsyncState<T> {
  const [data, setData] = useState<T | undefined>(undefined);
  const [error, setError] = useState<unknown>(null);
  const [pending, setPending] = useState(true);
  const [tick, setTick] = useState(0);
  const fnRef = useRef(fn);
  fnRef.current = fn;

  useEffect(() => {
    const controller = new AbortController();
    setPending(true);
    setError(null);
    fnRef
      .current(controller.signal)
      .then((d) => {
        if (!controller.signal.aborted) setData(d);
      })
      .catch((err) => {
        if (!controller.signal.aborted && !isAbortError(err)) setError(err);
      })
      .finally(() => {
        if (!controller.signal.aborted) setPending(false);
      });
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, tick]);

  const reload = useCallback(() => setTick((t) => t + 1), []);
  return { data, error, loading: pending && data === undefined, refreshing: pending && data !== undefined, reload };
}
