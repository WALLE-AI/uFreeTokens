import { request, type QueryValue } from './client';
import { ADMIN_TZ, toApiTime } from '../lib/tz';
import type { Cursor, RequestLogDetail, RequestLogItem, StatsOverview, UsageGroupBy, UsageInterval, UsageOrderBy, UsageResult } from '../types';

// 用量统计与全局调用日志（接口方案 §3、§6，后端 B4）。
// 时间参数接受 RFC3339 或 YYYY-MM-DD（按 ADMIN_TZ 换算成 RFC3339 再发送；纯日期的
// to 包含当天）。统计接口带 tz=ADMIN_TZ，按运营时区分桶。统计接口在服务端缓存 60 秒；
// 时间窗超过 48 小时时读小时汇总表（响应里 source=rollup）。

function withTime(q: { from?: string; to?: string }): Record<string, QueryValue> {
  return { ...q, from: toApiTime(q.from), to: toApiTime(q.to, true), tz: ADMIN_TZ };
}

export interface StatsFilterQuery {
  from?: string;
  to?: string;
  virtual_model?: string;
  channel_id?: number;
  provider_id?: number;
  account_id?: number;
  api_key_id?: number;
}

// 不传 from/to 时默认最近 7 天；previous 是等长的上一个时间窗（用于环比）
export function getStatsOverview(q: { from?: string; to?: string } = {}, signal?: AbortSignal) {
  return request<StatsOverview>('/stats/overview', { query: withTime(q), signal });
}

export interface UsageQuery extends StatsFilterQuery {
  interval?: UsageInterval; // hour 最长 7 天，day 最长 90 天
  group_by?: UsageGroupBy;
  top?: number; // 默认 8，最大 50；其余合并为 "__other__"
  order_by?: UsageOrderBy;
}

export function getUsage(q: UsageQuery = {}, signal?: AbortSignal) {
  return request<UsageResult>('/stats/usage', { query: withTime(q), signal });
}

export function getAccountUsage(accountId: number, q: Omit<UsageQuery, 'account_id'> = {}, signal?: AbortSignal) {
  return request<UsageResult>(`/accounts/${accountId}/usage`, { query: withTime(q), signal });
}

export interface RequestLogsQuery extends StatsFilterQuery {
  provider_key_id?: number;
  status?: string;
  error_code?: string;
  http_status?: number;
  min_latency_ms?: number;
  usage_source?: string;
  request_id?: string; // 精确定位（忽略其它过滤，只保留时间窗）
  before?: string;
  limit?: number; // 默认 50，最大 100
}

// 默认最近 24 小时；不带 account_id 时时间窗最长 7 天，带时 30 天
export function listRequestLogs(q: RequestLogsQuery = {}, signal?: AbortSignal) {
  return request<Cursor<RequestLogItem>>('/request-logs', { query: { ...q, from: toApiTime(q.from), to: toApiTime(q.to, true) }, signal });
}

// 从列表跳转时带上 created_at，可直接命中分区
export function getRequestLog(requestId: string, createdAt?: string, signal?: AbortSignal) {
  return request<RequestLogDetail>(`/request-logs/${encodeURIComponent(requestId)}`, { query: { created_at: createdAt }, signal });
}
