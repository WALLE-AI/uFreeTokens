// 金额换算全部走字符串/整数运算，不经过浮点：运营输入 "100.10" 元必须精确
// 得到 100_100_000 micro，而不是 100.1 * 1e6 = 100099999.99999999。
export const MICRO_PER_YUAN = 1_000_000;

// yuanToMicro 把用户输入的元（最多 6 位小数，可带负号）转为 micro 整数；
// 格式不合法返回 null。
export function yuanToMicro(input: string): number | null {
  const s = input.trim().replace(/,/g, '');
  const m = /^(-)?(\d+)(?:\.(\d{0,6}))?$/.exec(s);
  if (!m) return null;
  const [, neg, intPart, frac = ''] = m;
  const micro = BigInt(intPart) * 1_000_000n + BigInt(frac.padEnd(6, '0') || '0');
  if (micro > BigInt(Number.MAX_SAFE_INTEGER)) return null;
  const n = Number(micro);
  return neg ? -n : n;
}

// checkSafeMicro：后端金额是 int64 微元，JSON 解析成 JS number 后超过 2^53（约 90 亿元）
// 会丢精度。现实金额远小于这个量级，所以不改传输格式（方案 §3 B9 第 6 条"先只在超过 2^53
// 时告警"），但一旦出现就在控制台明确报出来，并在显示值后加"≈"提醒不精确。
const warnedMicro = new Set<number>();
function checkSafeMicro(micro: number): boolean {
  if (Number.isSafeInteger(micro)) return true;
  if (!warnedMicro.has(micro)) {
    warnedMicro.add(micro);
    console.error(`金额 ${micro} micro 超出 JS 安全整数范围（2^53），显示值可能不精确`);
  }
  return false;
}

// microToYuan 返回精确的十进制字符串，去掉多余的尾随 0，但至少保留 2 位小数。
// 超出安全整数范围时前面加 "≈"（见 checkSafeMicro）。
export function microToYuan(micro: number): string {
  const approx = checkSafeMicro(micro) ? '' : '≈';
  const neg = micro < 0;
  const abs = BigInt(Math.abs(Math.trunc(micro)));
  const intPart = abs / 1_000_000n;
  let frac = (abs % 1_000_000n).toString().padStart(6, '0').replace(/0+$/, '');
  if (frac.length < 2) frac = frac.padEnd(2, '0');
  const grouped = intPart.toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',');
  return `${approx}${neg ? '-' : ''}${grouped}.${frac}`;
}

export function formatMicro(micro: number): string {
  return `¥${microToYuan(micro)}`;
}

// 紧凑显示（KPI 卡、排行）：¥12.3k / ¥1.2M。LLM 计费里单次请求常常不到 1 分钱，
// 小于 ¥1 的非零金额保留 2 位有效数字（¥0.0031），避免把真实收入显示成 ¥0.00。
export function formatMicroCompact(micro: number): string {
  checkSafeMicro(micro);
  const yuan = micro / MICRO_PER_YUAN;
  const abs = Math.abs(yuan);
  if (abs >= 1_000_000) return `¥${(yuan / 1_000_000).toFixed(2)}M`;
  if (abs >= 10_000) return `¥${(yuan / 1_000).toFixed(1)}k`;
  if (abs > 0 && abs < 1) return `¥${Number(yuan.toPrecision(2)).toString()}`;
  return `¥${yuan.toFixed(2)}`;
}

export function formatInt(n: number): string {
  return n.toLocaleString('en-US');
}

export function formatCompact(n: number): string {
  const abs = Math.abs(n);
  if (abs >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`;
  if (abs >= 10_000) return `${(n / 1_000).toFixed(1)}k`;
  return formatInt(n);
}

// 比率是十进制字符串（"0.3610"），显示为 36.10%
export function formatRatio(ratio: string | number | null | undefined, digits = 1): string {
  if (ratio === null || ratio === undefined || ratio === '') return '—';
  const n = typeof ratio === 'number' ? ratio : Number(ratio);
  if (!Number.isFinite(n)) return '—';
  return `${(n * 100).toFixed(digits)}%`;
}
