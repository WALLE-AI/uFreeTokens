import type { Dataset } from '../../api/generated';

// 助手图表的数据层（《运营后台全局助手执行方案》P3）：把数据集（agent_datasets）按图表声明整理成
// 类目 + 系列。数字只来自数据集，图表声明只说明「画哪几列」。
//
// 配色：分类色按固定顺序分配、不循环（dataviz 参考调色板，浅色背景下已通过色觉差异校验）；
// 超过 8 个系列折叠为「其他」。其中 3 个颜色对白底对比度不足 3:1，所以每张图都提供表格视图与图例。

export type ChartType = 'line' | 'bar' | 'stacked_bar' | 'pie' | 'table' | 'kpi';

export interface ChartSpec {
  dataset_id: number;
  type: ChartType;
  title?: string;
  x?: string;
  y?: string[];
  series?: string;
  columns?: string[];
}

export interface Column {
  key: string;
  label: string;
  type: string; // string / time / integer / number / money / percent / pp / ms
}

export type Row = Record<string, unknown>;

export interface ChartData {
  id: number;
  title: string;
  columns: Column[];
  byKey: Map<string, Column>;
  rows: Row[];
  totals: Row | null;
  previous: Row | null;
  notes: string[];
}

export const SERIES_COLORS = ['#2a78d6', '#eb6834', '#1baf7a', '#eda100', '#e87ba4', '#008300', '#4a3aa7', '#e34948'];
export const OTHER_COLOR = '#a3a29d';
export const MAX_SERIES = SERIES_COLORS.length;

export const NUMERIC_TYPES = new Set(['integer', 'number', 'money', 'percent', 'pp', 'ms']);

export function toChartData(d: Dataset): ChartData {
  const columns = (Array.isArray(d.columns) ? d.columns : []) as Column[];
  return {
    id: d.id,
    title: d.title,
    columns,
    byKey: new Map(columns.map((c) => [c.key, c])),
    rows: (Array.isArray(d.rows) ? d.rows : []) as Row[],
    totals: (d.totals as Row | null) ?? null,
    previous: (d.previous as Row | null) ?? null,
    notes: (Array.isArray(d.notes) ? d.notes : []) as string[],
  };
}

export function num(v: unknown): number | null {
  return typeof v === 'number' && Number.isFinite(v) ? v : null;
}

const intFmt = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 0 });
const moneyFmt = new Intl.NumberFormat('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
const numFmt = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 4 });

function compactNum(v: number): string {
  const a = Math.abs(v);
  if (a >= 1e8) return `${(v / 1e8).toFixed(a >= 1e9 ? 0 : 1)}亿`;
  if (a >= 1e4) return `${(v / 1e4).toFixed(a >= 1e5 ? 0 : 1)}万`;
  return numFmt.format(Number(v.toFixed(2)));
}

// formatValue 按列类型格式化；compact 用于坐标轴刻度。
export function formatValue(v: unknown, type: string, compact = false): string {
  if (v === null || v === undefined || v === '') return '—';
  const n = num(v);
  if (n === null) return String(v);
  switch (type) {
    case 'money':
      return compact ? `¥${compactNum(n)}` : `¥${moneyFmt.format(n)}`;
    case 'percent':
      return `${(n * 100).toFixed(compact ? 0 : 2)}%`;
    case 'pp':
      return `${n > 0 ? '+' : ''}${(n * 100).toFixed(2)}pp`;
    case 'ms':
      return compact ? `${compactNum(n)}ms` : `${intFmt.format(n)} ms`;
    case 'integer':
      return compact ? compactNum(n) : intFmt.format(n);
  }
  return compact ? compactNum(n) : numFmt.format(n);
}

// formatCategory 把类目值（时间桶、分组名）格式化为轴标签。
export function formatCategory(v: unknown, type: string | undefined): string {
  if (v === null || v === undefined || v === '') return '（无）';
  const s = String(v);
  if (s === '__other__') return '其他';
  if (type === 'time') {
    // 2026-10-01T08:00:00+08:00 → 10-01 08:00；2026-10-01 → 10-01；2026-10 保持
    const m = /^\d{4}-(\d{2}-\d{2})(?:T(\d{2}:\d{2}))?/.exec(s);
    if (m) return m[2] && m[2] !== '00:00' ? `${m[1]} ${m[2]}` : m[1];
  }
  return s;
}

export interface Series {
  key: string;
  label: string;
  color: string;
  values: (number | null)[];
}

export interface CartesianModel {
  categories: string[];
  categoryFull: string[];
  series: Series[];
  yType: string;
  folded: number; // 折叠进「其他」的系列数
}

// buildCartesian 把数据集整理成类目 × 系列：
//   - 指定 series：长表（每行 = 类目 × 分组），每个分组一个系列；超过 8 个时保留合计最大的 7 个，其余合并为「其他」；
//   - 否则：每个 y 列一个系列。
export function buildCartesian(spec: ChartSpec, data: ChartData, yKeys: string[]): CartesianModel {
  const xKey = spec.x ?? '';
  const xType = data.byKey.get(xKey)?.type;
  const yType = data.byKey.get(yKeys[0])?.type ?? 'number';
  const catIndex = new Map<string, number>();
  const categoryFull: string[] = [];
  for (const r of data.rows) {
    const k = String(r[xKey] ?? '');
    if (!catIndex.has(k)) {
      catIndex.set(k, categoryFull.length);
      categoryFull.push(k);
    }
  }
  const categories = categoryFull.map((c) => formatCategory(c, xType));
  const n = categoryFull.length;

  if (!spec.series) {
    const series = yKeys.slice(0, MAX_SERIES).map((k, i) => {
      const values: (number | null)[] = new Array(n).fill(null);
      for (const r of data.rows) values[catIndex.get(String(r[xKey] ?? ''))!] = num(r[k]);
      return { key: k, label: data.byKey.get(k)?.label ?? k, color: SERIES_COLORS[i], values };
    });
    return { categories, categoryFull, series, yType, folded: Math.max(0, yKeys.length - MAX_SERIES) };
  }

  const yKey = yKeys[0];
  const sKey = spec.series;
  const groups = new Map<string, { label: string; values: (number | null)[]; total: number; other: boolean }>();
  for (const r of data.rows) {
    const g = String(r[sKey] ?? '');
    if (!groups.has(g)) groups.set(g, { label: formatCategory(g, undefined), values: new Array(n).fill(null), total: 0, other: isOtherRow(r) });
    const v = num(r[yKey]);
    const entry = groups.get(g)!;
    entry.values[catIndex.get(String(r[xKey] ?? ''))!] = v;
    entry.total += Math.abs(v ?? 0);
  }
  // 颜色跟随分组（按合计排序后固定分配），服务端的「其他」分组排在最后。
  const ordered = [...groups.entries()].sort((a, b) => Number(a[1].other) - Number(b[1].other) || b[1].total - a[1].total);
  const additive = yType !== 'percent' && yType !== 'pp' && yType !== 'ms';
  let kept = ordered;
  let folded = 0;
  let other: Series | null = null;
  if (ordered.length > MAX_SERIES) {
    kept = ordered.slice(0, MAX_SERIES - 1);
    const rest = ordered.slice(MAX_SERIES - 1);
    folded = rest.length;
    if (additive) {
      const values: (number | null)[] = new Array(n).fill(null);
      for (const [, g] of rest) g.values.forEach((v, i) => (v === null ? null : (values[i] = (values[i] ?? 0) + v)));
      other = { key: '__folded__', label: `其他（${rest.length} 项）`, color: OTHER_COLOR, values };
    }
  }
  const series: Series[] = kept.map(([k, g], i) => ({ key: k, label: g.label, color: g.other ? OTHER_COLOR : SERIES_COLORS[i], values: g.values }));
  if (other) series.push(other);
  return { categories, categoryFull, series, yType, folded };
}

export interface PieSlice {
  key: string;
  label: string;
  value: number;
  color: string;
}

// isOtherRow：服务端把 Top N 之外的分组合并成 key=__other__ 的一行（如「其他（341 项）」）。
export function isOtherRow(r: Row): boolean {
  return r.key === '__other__';
}

// buildPie 取 x 为名称、y[0] 为数值；只画正数。服务端的「其他」行与超出 8 个的扇区合并为一个灰色「其他」，排在最后。
export function buildPie(spec: ChartSpec, data: ChartData): { slices: PieSlice[]; total: number; yType: string; dropped: number } {
  const yKey = spec.y?.[0] ?? '';
  const xType = data.byKey.get(spec.x ?? '')?.type;
  const yType = data.byKey.get(yKey)?.type ?? 'number';
  const items = data.rows.map((r) => ({ key: String(r[spec.x ?? ''] ?? ''), label: formatCategory(r[spec.x ?? ''], xType), value: num(r[yKey]) ?? 0, other: isOtherRow(r) }));
  const positive = items.filter((i) => i.value > 0);
  const dropped = items.length - positive.length;
  const named = positive.filter((i) => !i.other).sort((a, b) => b.value - a.value);
  const others = positive.filter((i) => i.other);
  const limit = others.length > 0 ? MAX_SERIES - 1 : named.length > MAX_SERIES ? MAX_SERIES - 1 : MAX_SERIES;
  const kept = named.slice(0, limit);
  const rest = [...named.slice(limit), ...others];
  const slices: PieSlice[] = kept.map((s, i) => ({ key: s.key, label: s.label, value: s.value, color: SERIES_COLORS[i] }));
  if (rest.length > 0) {
    slices.push({ key: '__folded__', label: rest.length === 1 ? rest[0].label : '其他', value: rest.reduce((s, i) => s + i.value, 0), color: OTHER_COLOR });
  }
  return { slices, total: slices.reduce((s, i) => s + i.value, 0), yType, dropped };
}

// splitByUnit 把不同单位的 y 列分组（一张图只有一个纵轴：金额与比率不画在同一张图上）。
export function splitByUnit(yKeys: string[], data: ChartData): string[][] {
  const groups = new Map<string, string[]>();
  for (const k of yKeys) {
    const t = data.byKey.get(k)?.type ?? 'number';
    const unit = t === 'pp' ? 'percent' : t;
    if (!groups.has(unit)) groups.set(unit, []);
    groups.get(unit)!.push(k);
  }
  return [...groups.values()];
}

// niceTicks 返回包含 [min, max] 的「整齐」刻度（约 count 个）。
export function niceTicks(min: number, max: number, count = 4): number[] {
  if (min === max) {
    if (max === 0) return [0, 1];
    min = Math.min(0, min);
    max = Math.max(0, max);
  }
  const span = max - min;
  const raw = span / count;
  const pow = 10 ** Math.floor(Math.log10(raw));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * pow).find((s) => span / s <= count) ?? 10 * pow;
  const lo = Math.floor(min / step) * step;
  const hi = Math.ceil(max / step) * step;
  const out: number[] = [];
  for (let v = lo; v <= hi + step / 2; v += step) out.push(Number(v.toPrecision(12)));
  return out;
}

// toCSV 把数据集（或其中几列）转成带 BOM 的 CSV 文本（表格视图「导出 CSV」）。
export function toCSV(data: ChartData, columns: Column[]): string {
  const esc = (s: string) => (/[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s);
  const lines = [columns.map((c) => esc(c.label)).join(',')];
  for (const r of data.rows) {
    lines.push(
      columns
        .map((c) => {
          const v = r[c.key];
          if (v === null || v === undefined) return '';
          if ((c.type === 'percent' || c.type === 'pp') && typeof v === 'number') return `${(v * 100).toFixed(2)}%`;
          return esc(String(v));
        })
        .join(','),
    );
  }
  return `﻿${lines.join('\n')}`;
}
