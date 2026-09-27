import { useState } from 'react';
import { listProviderAccounts } from '../../api/catalog';
import { useAsync } from '../../hooks/useAsync';
import { SearchInput } from '../../components/ui';
import type { ProviderAccountSummary } from '../../types';

// 上游账号选择器：搜索 + 结果列表（添加渠道用）
export function ProviderAccountPicker({ value, onChange }: { value: ProviderAccountSummary | null; onChange: (a: ProviderAccountSummary | null) => void }) {
  const [q, setQ] = useState('');
  const res = useAsync((signal) => listProviderAccounts({ q: q || undefined, status: 'active', page_size: 20 }, signal), [q]);
  if (value) {
    return (
      <div className="flex items-center justify-between gap-2 border border-purple-200 bg-purple-50/40 rounded-lg px-3 py-2 text-xs">
        <span>
          <span className="text-gray-900 font-medium">{value.name}</span>
          <span className="text-gray-400 ml-2">{value.provider_code}</span>
          <span className="text-gray-400 ml-2 font-mono">×{value.cost_multiplier}</span>
        </span>
        <button type="button" onClick={() => onChange(null)} className="text-purple-600 hover:text-purple-700 cursor-pointer">
          更换
        </button>
      </div>
    );
  }
  return (
    <div className="space-y-1.5">
      <SearchInput placeholder="搜索上游账号名、base_url…" value={q} onChange={(e) => setQ(e.target.value)} />
      <div className="max-h-44 overflow-y-auto border border-gray-200 rounded-lg divide-y divide-gray-100">
        {res.loading && <div className="px-3 py-2 text-gray-400">正在加载...</div>}
        {!res.loading && (res.data?.data.length ?? 0) === 0 && <div className="px-3 py-2 text-gray-400">没有启用中的上游账号</div>}
        {res.data?.data.map((a) => (
          <button key={a.id} type="button" onClick={() => onChange(a)} className="w-full px-3 py-2 text-left hover:bg-gray-50 cursor-pointer flex items-center gap-2">
            <span className="text-gray-900">{a.name}</span>
            <span className="text-gray-400">{a.provider_code}</span>
            <span className="ml-auto text-[11px] text-gray-400 font-mono truncate max-w-48">{a.base_url}</span>
          </button>
        ))}
      </div>
    </div>
  );
}
