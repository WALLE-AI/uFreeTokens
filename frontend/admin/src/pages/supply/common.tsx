import { ApiError, describeError } from '../../api/errors';
import type { Protocol } from '../../types';
import { cn } from '../../lib/cn';

// 供给侧页面共用的小工具（供应商列表 / 详情 / 接入向导 / 价格源）。

export const PROTOCOL_OPTIONS: Array<{ value: Protocol; label: string; hint: string }> = [
  { value: 'openai', label: 'OpenAI 兼容', hint: 'DeepSeek、SiliconFlow、百炼、火山方舟等，支持拉取上游模型列表' },
  { value: 'anthropic', label: 'Anthropic', hint: 'Claude 原生协议' },
  { value: 'gemini', label: 'Gemini', hint: 'Google Gemini 原生协议' },
];

export function ProtocolBadge({ protocol }: { protocol: string }) {
  const label = PROTOCOL_OPTIONS.find((p) => p.value === protocol)?.label ?? protocol;
  return (
    <span className="inline-flex items-center px-1.5 py-0.5 rounded bg-gray-100 text-gray-600 text-[10px] font-mono uppercase tracking-wide">
      {label}
    </span>
  );
}

// upstreamErrorHint：拉取上游模型列表失败时，按状态码给出可操作的提示
export function upstreamErrorHint(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.status === 409) return '该上游账号没有可用的 active 密钥，请先添加或启用一把密钥';
    if (err.status === 502) return `上游不可用：${err.detail || err.message}（检查 base_url 是否正确、密钥是否有效）`;
    if (err.status === 400 && /protocol/i.test(err.detail)) return '只有 OpenAI 兼容协议的供应商支持自动拉取模型列表';
  }
  return describeError(err, '拉取上游模型失败');
}

export function Stat({ label, value, className }: { label: string; value: React.ReactNode; className?: string }) {
  return (
    <div className={cn('bg-gray-50 border border-gray-200 rounded-xl p-3.5', className)}>
      <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold">{label}</div>
      <div className="text-lg font-bold font-mono text-gray-900 mt-0.5">{value}</div>
    </div>
  );
}

// 十进制字符串 → 数字；空串 / 非法返回 null
export function num(v: string | null | undefined): number | null {
  if (v === null || v === undefined || v.trim() === '') return null;
  const n = Number(v);
  return Number.isFinite(n) ? n : null;
}

// 价格展示：去掉多余尾随 0，最多 6 位小数
export function fmtPrice(n: number | null | undefined, digits = 4): string {
  if (n === null || n === undefined || !Number.isFinite(n)) return '—';
  return String(Number(n.toFixed(digits)));
}
