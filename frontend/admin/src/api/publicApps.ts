import { request } from './client';
import type { ListData, PublicAppRule, PublicAppRuleAction, PublicAppsResponse } from '../types';

// 公开"热门应用"榜治理（技术方案 §8.2）：读 catalog:read，写 catalog:write。
// 规则在公开接口下一次缓存刷新（≤5 分钟）时生效，不需要重新物化。

// 最近 days 天（1..90，默认 7）的原始自报应用，未做隐私阈值过滤；同时返回公开榜单的隐私阈值
export function listPublicApps(days = 7, signal?: AbortSignal) {
  return request<PublicAppsResponse>('/public-apps', { query: { days }, signal });
}

export function listPublicAppRules(signal?: AbortSignal) {
  return request<ListData<PublicAppRule>>('/public-app-rules', { signal });
}

// merge 必须带 merge_into（另一个 app_key）；rename 必须带 display_name（≤64 字）；
// 同一 app_key 已有规则时 409，需先删除再建。
export function createPublicAppRule(body: {
  app_key: string;
  action: PublicAppRuleAction;
  merge_into?: string | null;
  display_name?: string | null;
  note: string;
}) {
  return request<PublicAppRule>('/public-app-rules', { method: 'POST', body });
}

export function deletePublicAppRule(id: number) {
  return request<void>(`/public-app-rules/${id}`, { method: 'DELETE' });
}
