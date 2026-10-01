import { zonedParts } from './tz';

// 时间统一按运营时区（ADMIN_TZ）显示，与统计分桶口径一致，不随浏览器时区变化。
export function formatDateTime(iso: string | null | undefined): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const p = zonedParts(d);
  return `${p.year}-${p.month}-${p.day} ${p.hour}:${p.minute}:${p.second}`;
}

export function formatRelative(iso: string | null | undefined): string {
  if (!iso) return '—';
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return iso;
  const diff = Math.round((Date.now() - t) / 1000);
  if (diff < 60) return '刚刚';
  if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`;
  if (diff < 86400) return `${Math.floor(diff / 3600)} 小时前`;
  if (diff < 86400 * 30) return `${Math.floor(diff / 86400)} 天前`;
  return formatDateTime(iso).slice(0, 10);
}

// formatFromNow 同时支持过去与将来："3 小时后" / "2 天前"。用于下次运行、优惠截止等可能在将来的时间。
export function formatFromNow(iso: string | null | undefined): string {
  if (!iso) return '—';
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return iso;
  const diff = Math.round((t - Date.now()) / 1000);
  if (diff <= 0) return formatRelative(iso);
  if (diff < 60) return '即将';
  if (diff < 3600) return `${Math.floor(diff / 60)} 分钟后`;
  if (diff < 86400) return `${Math.floor(diff / 3600)} 小时后`;
  if (diff < 86400 * 30) return `${Math.floor(diff / 86400)} 天后`;
  return formatDateTime(iso).slice(0, 10);
}
