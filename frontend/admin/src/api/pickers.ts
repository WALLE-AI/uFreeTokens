import { getChannel, getProvider, getVirtualModel, listChannels, listProviderAccounts, listProviders, listVirtualModels } from './catalog';
import type { RemoteOption } from '../components/ui';
import type { ChannelSummary, Paginated, Provider, ProviderAccountSummary, ProviderSummary, VirtualModelSummary } from '../types';

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

// searchProviderCodes 同 searchProviders，但选项值是供应商 code（优惠雷达等按 code 筛选的接口用）。
export async function searchProviderCodes(q: string, signal: AbortSignal): Promise<RemoteOption<ProviderSummary>[]> {
  const res = await listProviders({ q: q || undefined, page_size: 20, sort: 'code' }, signal);
  return res.data.map((p) => ({ value: p.code, label: `${p.name}（${p.code}）`, hint: p.protocol, data: p }));
}

// searchVirtualModels 按关键字搜索虚拟模型（不含已废弃），选项值是模型 id。
export async function searchVirtualModels(q: string, signal: AbortSignal): Promise<RemoteOption<VirtualModelSummary>[]> {
  const res = await listVirtualModels({ q: q || undefined, status: 'active,hidden', page_size: 20, sort: 'name' }, signal);
  return res.data.map((m) => ({ value: String(m.id), label: m.name, hint: m.display_name ?? m.family, data: m }));
}

export async function virtualModelLabel(id: string, signal: AbortSignal): Promise<string> {
  return (await getVirtualModel(Number(id), signal)).name;
}

// searchVirtualModelNames 同 searchVirtualModels，但选项值是模型名（按名称引用模型的接口用）。
export async function searchVirtualModelNames(q: string, signal: AbortSignal): Promise<RemoteOption<VirtualModelSummary>[]> {
  return (await searchVirtualModels(q, signal)).map((o) => ({ ...o, value: o.label }));
}

// searchChannels 按虚拟模型名 / 上游模型搜索渠道，选项值是渠道 id。
export async function searchChannels(q: string, signal: AbortSignal): Promise<RemoteOption<ChannelSummary>[]> {
  const res = await listChannels({ q: q || undefined, page_size: 20 }, signal);
  return res.data.map((c) => ({
    value: String(c.id),
    label: `#${c.id} ${c.virtual_model_name}`,
    hint: `${c.provider_code} · ${c.upstream_model}${c.status === 'active' ? '' : '（停用）'}`,
    data: c,
  }));
}

export async function channelLabel(id: string, signal: AbortSignal): Promise<string> {
  const c = await getChannel(Number(id), signal);
  return `#${c.id} ${c.virtual_model_name}`;
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
