import { getProvider, listProviderAccounts, listProviders } from './catalog';
import type { RemoteOption } from '../components/ui';
import type { Paginated, Provider, ProviderAccountSummary, ProviderSummary } from '../types';

// 下拉选择器用的远程搜索与"取全部"工具（方案 §2.5 F2：不再一次拉 100 条后静默截断）。

// searchProviders 按关键字搜索供应商（服务端分页的第一页，20 条）。
export async function searchProviders(q: string, signal: AbortSignal): Promise<RemoteOption<ProviderSummary>[]> {
  const res = await listProviders({ q: q || undefined, page_size: 20, sort: 'code' }, signal);
  return res.data.map((p) => ({ value: String(p.id), label: `${p.name}（${p.code}）`, hint: p.protocol, data: p }));
}

export async function providerLabel(id: string, signal: AbortSignal): Promise<string> {
  const p: Provider = await getProvider(Number(id), signal);
  return `${p.name}（${p.code}）`;
}

// fetchAllPages 逐页取完一个分页列表（最多 maxItems 条）。用于"某个供应商下的全部
// 上游账号"这类有界、但可能超过一页的列表；truncated 表示超过上限没取完。
export async function fetchAllPages<T>(
  loader: (page: number, pageSize: number) => Promise<Paginated<T>>,
  maxItems = 1000,
): Promise<{ data: T[]; total: number; truncated: boolean }> {
  const pageSize = 100;
  const out: T[] = [];
  let total = 0;
  for (let page = 1; out.length < maxItems; page++) {
    const res = await loader(page, pageSize);
    total = res.total;
    out.push(...res.data);
    if (res.data.length < pageSize || out.length >= total) break;
  }
  return { data: out.slice(0, maxItems), total, truncated: total > maxItems };
}

export function listAllProviderAccounts(providerId: number, signal?: AbortSignal) {
  return fetchAllPages<ProviderAccountSummary>((page, page_size) => listProviderAccounts({ provider_id: providerId, page, page_size }, signal));
}
