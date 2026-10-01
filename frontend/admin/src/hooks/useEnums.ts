import { request } from '../api/client';
import type { MetaEnums } from '../types';
import { useAsync } from './useAsync';

// 枚举字典（GET /meta/enums）：进程内只拉一次，所有页面共用，替代前端硬编码的
// tier / capability / meter 等取值（docs/admin-api.md §2）。拉取失败时返回 fallback。
let cached: Promise<MetaEnums> | null = null;

function loadEnums(): Promise<MetaEnums> {
  if (!cached) {
    cached = request<MetaEnums>('/meta/enums').catch((err) => {
      cached = null; // 失败不缓存，下次重试
      throw err;
    });
  }
  return cached;
}

export function useEnums(): MetaEnums | undefined {
  return useAsync(() => loadEnums(), []).data;
}
