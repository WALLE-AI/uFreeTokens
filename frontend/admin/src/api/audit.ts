import { request } from './client';
import type { AuditLogEntry, Cursor } from '../types';

export interface ListAuditLogsParams {
  target_type?: string;
  target_id?: string;
  actor_id?: number;
  actor_name?: string;
  action?: string; // 前缀匹配，如 "price_change."
  from?: string; // RFC3339 或 YYYY-MM-DD
  to?: string; // 同上；纯日期表示包含当天
  before?: string; // 上一页的 next_cursor
  limit?: number; // 默认 100，最大 500
}

// GET /audit-logs：按 (created_at, id) 倒序的游标分页（接口方案 §8）
export function listAuditLogs(params: ListAuditLogsParams = {}, signal?: AbortSignal) {
  return request<Cursor<AuditLogEntry>>('/audit-logs', { query: { ...params }, signal });
}
