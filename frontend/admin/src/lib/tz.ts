// 运营后台统一使用的时区（统计分桶、"今天"的边界、时间显示），默认北京时间。
// 与后端统计接口的 ?tz= 参数一致：后端按这个时区分桶，前端按同一时区补齐空桶、
// 显示时间，避免"前端本地时间 vs 后端 UTC 分桶"的错位（接口方案 §2.3 A7）。
export const ADMIN_TZ: string = import.meta.env.VITE_ADMIN_TZ || 'Asia/Shanghai';

const partsFormatter = new Intl.DateTimeFormat('en-CA', {
  timeZone: ADMIN_TZ,
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hourCycle: 'h23',
});

const offsetFormatter = new Intl.DateTimeFormat('en-US', { timeZone: ADMIN_TZ, timeZoneName: 'longOffset' });

export interface ZonedParts {
  year: string;
  month: string;
  day: string;
  hour: string;
  minute: string;
  second: string;
}

// zonedParts 返回某一时刻在 ADMIN_TZ 下的年月日时分秒（均为补零字符串）。
export function zonedParts(t: number | Date): ZonedParts {
  const out: Record<string, string> = {};
  for (const p of partsFormatter.formatToParts(t)) out[p.type] = p.value;
  return out as unknown as ZonedParts;
}

// zoneOffset 返回某一时刻 ADMIN_TZ 的 UTC 偏移，形如 "+08:00"；UTC 返回 "Z"。
export function zoneOffset(t: number | Date): string {
  const name = offsetFormatter.formatToParts(t).find((p) => p.type === 'timeZoneName')?.value ?? 'GMT';
  const m = /GMT([+-]\d{2}:\d{2})/.exec(name);
  return m ? m[1] : 'Z';
}

// zonedDate 返回 ADMIN_TZ 下的日期 YYYY-MM-DD。
export function zonedDate(t: number | Date): string {
  const p = zonedParts(t);
  return `${p.year}-${p.month}-${p.day}`;
}

// startOfZonedDay 返回 ADMIN_TZ 下某个日期（YYYY-MM-DD）零点对应的时间戳（毫秒）。
export function startOfZonedDay(date: string): number {
  // 先按 UTC 零点取一个近似时刻，再用该时刻的偏移校正（固定偏移时区精确，DST 当天误差 ≤1 小时）
  const approx = Date.parse(`${date}T00:00:00Z`);
  const off = zoneOffset(approx);
  return Date.parse(`${date}T00:00:00${off}`);
}

// toApiTime 把 YYYY-MM-DD 转成 ADMIN_TZ 下当天零点（end=true 时为次日零点）的 RFC3339，
// 其他格式原样返回。这样时间窗的解释不依赖后端的默认时区。
export function toApiTime(v: string | undefined, end = false): string | undefined {
  if (!v || !/^\d{4}-\d{2}-\d{2}$/.test(v)) return v;
  const t = startOfZonedDay(v) + (end ? 86400_000 : 0);
  return new Date(t).toISOString();
}

// isoToZonedInput / zonedInputToISO：<input type="datetime-local"> 的值按 ADMIN_TZ 解释（而不是浏览器时区）。
export function isoToZonedInput(iso: string | null | undefined): string {
  if (!iso) return '';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const p = zonedParts(t);
  return `${p.year}-${p.month}-${p.day}T${p.hour}:${p.minute}`;
}

// 空串返回 undefined；格式不对返回 null
export function zonedInputToISO(v: string): string | null | undefined {
  if (!v.trim()) return undefined;
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(v)) return null;
  const approx = Date.parse(`${v}:00Z`);
  const t = Date.parse(`${v}:00${zoneOffset(approx)}`);
  return Number.isNaN(t) ? null : new Date(t).toISOString();
}
