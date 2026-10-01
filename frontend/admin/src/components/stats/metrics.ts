import { formatCompact, formatMicroCompact, formatRatio } from '../../lib/money';
import type { Metrics, UsageInterval, UsageOrderBy } from '../../types';
import { startOfZonedDay, zoneOffset, zonedDate, zonedParts } from '../../lib/tz';

// 统计页面共用的指标定义与时间范围工具（接口方案 §3.1 口径：requests 含失败，
// tokens / 延迟只算成功请求）。

export type MetricKey = 'requests' | 'tokens' | 'revenue' | 'cost' | 'gross_profit' | 'error_rate' | 'p95';

export interface MetricDef {
  key: MetricKey;
  label: string;
  value: (m: Metrics) => number | null;
  format: (v: number) => string;
  // 可叠加：能在堆叠柱里按分组相加。错误率、P95 不可叠加
  additive: boolean;
  // 排名接口的 order_by
  orderBy: UsageOrderBy;
  // 越大越好？（决定环比的颜色）
  goodWhenUp: boolean;
}

export const METRICS: Record<MetricKey, MetricDef> = {
  requests: { key: 'requests', label: '请求数', value: (m) => m.requests, format: formatCompact, additive: true, orderBy: 'requests', goodWhenUp: true },
  tokens: {
    key: 'tokens',
    label: 'Tokens',
    value: (m) => m.input_tokens + m.output_tokens,
    format: formatCompact,
    additive: true,
    orderBy: 'input_tokens',
    goodWhenUp: true,
  },
  revenue: { key: 'revenue', label: '收入', value: (m) => m.revenue_micro, format: formatMicroCompact, additive: true, orderBy: 'revenue_micro', goodWhenUp: true },
  cost: { key: 'cost', label: '成本', value: (m) => m.cost_micro, format: formatMicroCompact, additive: true, orderBy: 'cost_micro', goodWhenUp: false },
  gross_profit: {
    key: 'gross_profit',
    label: '毛利',
    value: (m) => m.gross_profit_micro,
    format: formatMicroCompact,
    additive: true,
    orderBy: 'gross_profit',
    goodWhenUp: true,
  },
  error_rate: {
    key: 'error_rate',
    label: '错误率',
    value: (m) => (m.error_rate === null ? null : Number(m.error_rate)),
    format: (v) => formatRatio(v, 2),
    additive: false,
    orderBy: 'errors',
    goodWhenUp: false,
  },
  p95: {
    key: 'p95',
    label: 'P95 延迟',
    value: (m) => m.p95_latency_ms,
    format: formatMs,
    additive: false,
    orderBy: 'requests',
    goodWhenUp: false,
  },
};

export function formatMs(v: number | null | undefined): string {
  if (v === null || v === undefined) return '—';
  return v >= 1000 ? `${(v / 1000).toFixed(v >= 10_000 ? 0 : 1)}s` : `${Math.round(v)}ms`;
}

// ---------- 时间范围 ----------

export type RangeKey = 'today' | '1h' | '24h' | '7d' | '30d' | 'custom';

// rangeBounds 返回 [from, to) 的 ISO 字符串。"现在"取整到分钟：统计接口按完整 URL
// 缓存 60 秒，取整后同一分钟内的重复请求能命中缓存。
export function rangeBounds(key: RangeKey, custom?: { from?: string; to?: string }): { from: string; to: string } {
  const now = new Date();
  now.setUTCSeconds(0, 0);
  const to = new Date(now.getTime() + 60_000);
  const ago = (ms: number) => new Date(to.getTime() - ms).toISOString();
  switch (key) {
    case 'today': {
      // "今天"按运营时区（ADMIN_TZ）的零点计算，与后端 ?tz= 分桶一致
      return { from: new Date(startOfZonedDay(zonedDate(now))).toISOString(), to: to.toISOString() };
    }
    case '1h':
      return { from: ago(3600_000), to: to.toISOString() };
    case '24h':
      return { from: ago(86400_000), to: to.toISOString() };
    case '30d':
      return { from: ago(30 * 86400_000), to: to.toISOString() };
    case 'custom':
      return { from: custom?.from || ago(7 * 86400_000), to: custom?.to || to.toISOString() };
    case '7d':
    default:
      return { from: ago(7 * 86400_000), to: to.toISOString() };
  }
}

// 自定义日期（YYYY-MM-DD）按 ADMIN_TZ 解释，to 包含当天：换算成毫秒跨度用于前端校验
export function spanMs(from: string, to: string): number {
  const f = parseBound(from, false);
  const t = parseBound(to, true);
  return t - f;
}

function parseBound(v: string, end: boolean): number {
  if (/^\d{4}-\d{2}-\d{2}$/.test(v)) {
    const t = startOfZonedDay(v);
    return end ? t + 86400_000 : t;
  }
  return Date.parse(v);
}

// fillBuckets 生成 [from, to) 内的全部时间桶（与后端 ?tz=ADMIN_TZ 的 bucket 格式一致：
// 日 → YYYY-MM-DD，小时 → YYYY-MM-DDTHH:00:00+08:00），用来补齐没有数据的天/小时，
// 让柱状图的时间轴连续。
export function fillBuckets(interval: UsageInterval, from: string, to: string): string[] {
  if (interval === 'none') return [];
  const start = parseBound(from, false);
  const end = parseBound(to, true);
  if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) return [];
  const out: string[] = [];
  if (interval === 'hour') {
    for (let t = Math.floor(start / 3600_000) * 3600_000, i = 0; t < end && i < 24 * 8; t += 3600_000, i++) {
      const p = zonedParts(t);
      out.push(`${p.year}-${p.month}-${p.day}T${p.hour}:00:00${zoneOffset(t)}`);
    }
    return out;
  }
  let day = zonedDate(start);
  for (let i = 0; startOfZonedDay(day) < end && i < 92; i++) {
    out.push(day);
    day = zonedDate(startOfZonedDay(day) + 36 * 3600_000); // +36h 再取日期：跨过 DST 也能落到下一天
  }
  return out;
}

// 桶的简短显示：日 → 09-26；小时 → 09-26 14:00（ADMIN_TZ）
export function bucketLabel(bucket: string): string {
  if (bucket.length === 10) return bucket.slice(5);
  return `${bucket.slice(5, 10)} ${bucket.slice(11, 16)}`;
}

// 环比：比率类（错误率、毛利率）用差值（百分点），其余用相对变化
export function relativeDelta(cur: number | null, prev: number | null): number | null {
  if (cur === null || prev === null) return null;
  if (prev === 0) return cur === 0 ? 0 : null;
  return (cur - prev) / Math.abs(prev);
}

export function pointDelta(cur: string | null, prev: string | null): number | null {
  if (cur === null || prev === null) return null;
  return Number(cur) - Number(prev);
}
