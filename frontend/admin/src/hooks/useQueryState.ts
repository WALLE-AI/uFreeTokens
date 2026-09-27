import { useCallback } from 'react';
import { useSearchParams } from 'react-router';

// 列表页的筛选、排序、分页一律存在 URL query 里（UI_DESIGN.md §8），刷新和
// 分享链接都不丢。useQueryState 读写单个参数；useQueryParams 批量读写。

export function useQueryState(key: string, defaultValue = ''): [string, (v: string | null) => void] {
  const [params, setParams] = useSearchParams();
  const value = params.get(key) ?? defaultValue;
  const setValue = useCallback(
    (v: string | null) => {
      setParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          if (v === null || v === '' || v === defaultValue) next.delete(key);
          else next.set(key, v);
          // 任何筛选变化都回到第 1 页
          if (key !== 'page') next.delete('page');
          return next;
        },
        { replace: true },
      );
    },
    [key, defaultValue, setParams],
  );
  return [value, setValue];
}

export function useQueryParams(): [
  Record<string, string>,
  (patch: Record<string, string | null>, opts?: { keepPage?: boolean }) => void,
] {
  const [params, setParams] = useSearchParams();
  const values = Object.fromEntries(params.entries());
  const update = useCallback(
    (patch: Record<string, string | null>, opts?: { keepPage?: boolean }) => {
      setParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          for (const [k, v] of Object.entries(patch)) {
            if (v === null || v === '') next.delete(k);
            else next.set(k, v);
          }
          if (!opts?.keepPage && !('page' in patch)) next.delete('page');
          return next;
        },
        { replace: true },
      );
    },
    [setParams],
  );
  return [values, update];
}
