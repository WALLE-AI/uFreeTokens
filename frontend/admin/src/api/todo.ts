import { request } from './client';
import type { TodoCounts } from '../types';

// GET /todo-counts（接口方案 §7，后端 B2 提供）。侧栏徽标 / 工作台待办条使用，
// 每 60 秒轮询一次；接口尚未上线（404）时静默降级为不显示徽标。
export function getTodoCounts(signal?: AbortSignal): Promise<TodoCounts> {
  return request<TodoCounts>('/todo-counts', { signal });
}
