import { useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import { formatValue, niceTicks, type CartesianModel, type PieSlice } from './chartData';

// 内置 SVG 渲染器（零依赖，默认）：折线、分组柱、堆叠柱、环形图。
// 遵循 dataviz 规范：2px 折线；柱宽 ≤24px、数据端 4px 圆角、基线端方角；相邻柱与堆叠段之间 2px 底色间隙；
// 网格与坐标轴为实线细线；折线的悬停是十字准线 + 提示框，柱图按类目整列悬停；键盘 ←/→ 与悬停等效。
// ≥2 个系列时总是有图例，≤4 条折线另在末端直接标注。

const SURFACE = '#ffffff';
const GRID = '#ecebe8';
const AXIS = '#d4d3cf';
const INK_MUTED = '#8a8984';
const PLOT_H = 190;
const X_BAND = 22;

export function useWidth<T extends HTMLElement>(): [React.RefObject<T | null>, number] {
  const ref = useRef<T>(null);
  const [w, setW] = useState(0);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const ro = new ResizeObserver(([e]) => setW(Math.floor(e.contentRect.width)));
    ro.observe(el);
    setW(Math.floor(el.getBoundingClientRect().width));
    return () => ro.disconnect();
  }, []);
  return [ref, w];
}

export function Legend({ items }: { items: Array<{ key: string; label: string; color: string }> }) {
  if (items.length < 2) return null;
  return (
    <div className="flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-gray-600">
      {items.map((s) => (
        <span key={s.key} className="inline-flex items-center gap-1 min-w-0">
          <span className="w-2 h-2 rounded-sm shrink-0" style={{ background: s.color }} />
          <span className="truncate max-w-[12rem]">{s.label}</span>
        </span>
      ))}
    </div>
  );
}

export function Tooltip({ x, width, children }: { x: number; width: number; children: ReactNode }) {
  // 提示框放在悬停位置右侧，靠近右边界时翻到左侧。
  const flip = x > width - 180;
  return (
    <div
      className="pointer-events-none absolute top-1 z-10 min-w-[8rem] max-w-[16rem] rounded-lg border border-gray-200 bg-white/95 px-2 py-1.5 text-[11px] shadow-md"
      style={flip ? { right: Math.max(0, width - x + 8) } : { left: x + 8 }}
    >
      {children}
    </div>
  );
}

export function TooltipRow({ color, label, value }: { color?: string; label: string; value: string }) {
  return (
    <div className="flex items-center gap-1.5 leading-5">
      {color && <span className="w-2 h-2 rounded-sm shrink-0" style={{ background: color }} />}
      <span className="text-gray-600 truncate flex-1 min-w-0">{label}</span>
      <span className="text-gray-900 font-medium tabular-nums">{value}</span>
    </div>
  );
}

// barPath 画一根柱子：数据端圆角，基线端方角（负值时圆角在下）。
function barPath(x: number, w: number, yBase: number, yEnd: number, roundEnd: boolean): string {
  const h = Math.abs(yEnd - yBase);
  if (h < 0.5) return '';
  const r = roundEnd ? Math.min(4, w / 2, h) : 0;
  const up = yEnd < yBase;
  const top = up ? yEnd : yBase;
  const bottom = up ? yBase : yEnd;
  if (up) {
    return `M${x},${bottom}V${top + r}Q${x},${top} ${x + r},${top}H${x + w - r}Q${x + w},${top} ${x + w},${top + r}V${bottom}Z`;
  }
  return `M${x},${top}V${bottom - r}Q${x},${bottom} ${x + r},${bottom}H${x + w - r}Q${x + w},${bottom} ${x + w},${bottom - r}V${top}Z`;
}

function textWidth(s: string): number {
  // 10px 字体的粗略宽度：中文约 10px，其余约 6px。
  let w = 0;
  for (const ch of s) w += /[⺀-鿿＀-￯]/.test(ch) ? 10 : 6;
  return w;
}

function truncate(s: string, maxPx: number): string {
  if (textWidth(s) <= maxPx) return s;
  let out = '';
  for (const ch of s) {
    if (textWidth(out + ch + '…') > maxPx) break;
    out += ch;
  }
  return `${out}…`;
}

export function SvgCartesian({ kind, model }: { kind: 'line' | 'bar' | 'stacked_bar'; model: CartesianModel }) {
  const [ref, width] = useWidth<HTMLDivElement>();
  const [hover, setHover] = useState<number | null>(null);
  const { categories, categoryFull, series, yType } = model;
  const n = categories.length;

  const layout = useMemo(() => {
    let lo = 0;
    let hi = 0;
    if (kind === 'stacked_bar') {
      for (let i = 0; i < n; i++) {
        let pos = 0;
        let neg = 0;
        for (const s of series) {
          const v = s.values[i] ?? 0;
          if (v >= 0) pos += v;
          else neg += v;
        }
        hi = Math.max(hi, pos);
        lo = Math.min(lo, neg);
      }
    } else {
      const all = series.flatMap((s) => s.values.filter((v): v is number => v !== null));
      if (all.length) {
        hi = Math.max(...all);
        lo = Math.min(...all);
      }
      if (kind === 'bar') {
        hi = Math.max(hi, 0);
        lo = Math.min(lo, 0);
      } else if (lo > 0 && (hi - lo) / hi < 0.5) {
        // 折线：数据远离 0 时不强制从 0 开始，但留出余量
        lo = lo - (hi - lo) * 0.2;
      } else {
        lo = Math.min(lo, 0);
      }
    }
    const ticks = niceTicks(lo, hi, 4);
    const tickLabels = ticks.map((t) => formatValue(t, yType, true));
    const left = Math.max(...tickLabels.map(textWidth)) + 10;
    const endLabels = kind === 'line' && series.length >= 2 && series.length <= 4;
    const right = endLabels ? Math.min(90, Math.max(...series.map((s) => textWidth(s.label))) + 12) : 12;
    return { ticks, tickLabels, left, right, min: ticks[0], max: ticks[ticks.length - 1], endLabels };
  }, [kind, n, series, yType]);

  if (n === 0 || series.length === 0) return <div ref={ref} className="text-[11px] text-gray-400 py-6 text-center">没有可画的数据</div>;

  const plotW = Math.max(40, width - layout.left - layout.right);
  const yOf = (v: number) => 8 + PLOT_H - ((v - layout.min) / (layout.max - layout.min || 1)) * PLOT_H;
  // 末端标注：两个末端相距不足 12px 时不标注（不堆叠标签，图例已能区分系列）。
  const endYs = series.map((s) => {
    const last = s.values.map((v) => v !== null).lastIndexOf(true);
    return last >= 0 ? yOf(s.values[last]!) : null;
  });
  const endLabels =
    layout.endLabels && endYs.every((a, i) => a === null || endYs.every((b, j) => j === i || b === null || Math.abs(a - b) >= 12));
  const band = plotW / n;
  const y = yOf;
  const xMid = (i: number) => layout.left + band * (i + 0.5);
  const baseY = y(Math.max(layout.min, Math.min(0, layout.max)));

  const maxLabels = Math.max(2, Math.floor(plotW / 64));
  const step = Math.ceil(n / maxLabels);
  const labelPx = band * step - 6;

  const k = series.length;
  const groupW = band * 0.72;
  const barW = kind === 'bar' ? Math.max(2, Math.min(24, (groupW - 2 * (k - 1)) / k)) : Math.max(2, Math.min(24, groupW));
  const totalW = kind === 'bar' ? barW * k + 2 * (k - 1) : barW;

  const onMove = (clientX: number, rect: DOMRect) => {
    const i = Math.floor((clientX - rect.left - layout.left) / band);
    setHover(i >= 0 && i < n ? i : null);
  };
  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'ArrowRight') setHover((h) => Math.min(n - 1, (h ?? -1) + 1));
    else if (e.key === 'ArrowLeft') setHover((h) => Math.max(0, (h ?? n) - 1));
    else if (e.key === 'Escape') setHover(null);
    else return;
    e.preventDefault();
  };

  const height = 8 + PLOT_H + X_BAND;
  return (
    <div ref={ref} className="relative select-none">
      {width > 0 && (
        <svg
          width={width}
          height={height}
          role="img"
          tabIndex={0}
          className="block outline-none focus-visible:ring-2 focus-visible:ring-purple-300 rounded"
          onMouseMove={(e) => onMove(e.clientX, e.currentTarget.getBoundingClientRect())}
          onMouseLeave={() => setHover(null)}
          onKeyDown={onKey}
          onBlur={() => setHover(null)}
        >
          {/* 网格与刻度 */}
          {layout.ticks.map((t, i) => (
            <g key={t}>
              <line x1={layout.left} x2={layout.left + plotW} y1={y(t)} y2={y(t)} stroke={t === 0 ? AXIS : GRID} strokeWidth={1} shapeRendering="crispEdges" />
              <text x={layout.left - 6} y={y(t)} dy="0.32em" textAnchor="end" fontSize={10} fill={INK_MUTED} className="tabular-nums">
                {layout.tickLabels[i]}
              </text>
            </g>
          ))}
          {/* 悬停类目的底色（柱图） */}
          {hover !== null && kind !== 'line' && <rect x={layout.left + band * hover} y={8} width={band} height={PLOT_H} fill="#f5f4f1" />}

          {kind === 'line' &&
            series.map((s) => {
              let d = '';
              let pen = false;
              s.values.forEach((v, i) => {
                if (v === null) {
                  pen = false;
                  return;
                }
                d += `${pen ? 'L' : 'M'}${xMid(i)},${y(v)}`;
                pen = true;
              });
              let last = -1;
              s.values.forEach((v, i) => v !== null && (last = i));
              return (
                <g key={s.key}>
                  <path d={d} fill="none" stroke={s.color} strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />
                  {/* 只有一个点时画点，否则线不可见 */}
                  {s.values.filter((v) => v !== null).length === 1 && last >= 0 && (
                    <circle cx={xMid(last)} cy={y(s.values[last]!)} r={4} fill={s.color} stroke={SURFACE} strokeWidth={2} />
                  )}
                  {endLabels && last >= 0 && (
                    <text x={xMid(last) + 6} y={y(s.values[last]!)} dy="0.32em" fontSize={10} fill="#52514e">
                      {truncate(s.label, layout.right - 8)}
                    </text>
                  )}
                </g>
              );
            })}

          {kind === 'bar' &&
            categories.map((_, i) =>
              series.map((s, j) => {
                const v = s.values[i];
                if (v === null) return null;
                const x = xMid(i) - totalW / 2 + j * (barW + 2);
                return <path key={`${s.key}-${i}`} d={barPath(x, barW, baseY, y(v), true)} fill={s.color} />;
              }),
            )}

          {kind === 'stacked_bar' &&
            categories.map((_, i) => {
              let pos = 0;
              let neg = 0;
              const segs = series.map((s) => {
                const v = s.values[i] ?? 0;
                const from = v >= 0 ? pos : neg;
                const to = from + v;
                if (v >= 0) pos = to;
                else neg = to;
                return { s, v, from, to };
              });
              const lastPos = segs.map((g) => g.v > 0).lastIndexOf(true);
              const lastNeg = segs.map((g) => g.v < 0).lastIndexOf(true);
              const x = xMid(i) - barW / 2;
              return segs.map((g, j) => {
                if (g.v === 0) return null;
                const isEnd = j === (g.v > 0 ? lastPos : lastNeg);
                // 段与段之间留 2px 底色间隙：每段在远离基线的一端让出 2px（最外段除外）
                const gap = isEnd ? 0 : g.v > 0 ? 2 : -2;
                const yFrom = y(g.from);
                let yTo = y(g.to) + gap;
                if ((g.v > 0 && yTo > yFrom) || (g.v < 0 && yTo < yFrom)) yTo = yFrom;
                return <path key={`${g.s.key}-${i}`} d={barPath(x, barW, yFrom, yTo, isEnd)} fill={g.s.color} />;
              });
            })}

          {/* 十字准线与悬停点（折线） */}
          {hover !== null && kind === 'line' && (
            <g>
              <line x1={xMid(hover)} x2={xMid(hover)} y1={8} y2={8 + PLOT_H} stroke={AXIS} strokeWidth={1} shapeRendering="crispEdges" />
              {series.map((s) =>
                s.values[hover] === null ? null : <circle key={s.key} cx={xMid(hover)} cy={y(s.values[hover]!)} r={4} fill={s.color} stroke={SURFACE} strokeWidth={2} />,
              )}
            </g>
          )}

          {/* x 轴标签 */}
          {categories.map((c, i) =>
            i % step === 0 ? (
              <text key={i} x={xMid(i)} y={8 + PLOT_H + 15} textAnchor="middle" fontSize={10} fill={INK_MUTED}>
                {truncate(c, Math.max(24, labelPx))}
              </text>
            ) : null,
          )}
        </svg>
      )}
      {hover !== null && width > 0 && (
        <Tooltip x={xMid(hover)} width={width}>
          <div className="text-gray-900 font-medium mb-0.5 break-words">{tooltipTitle(categoryFull[hover], categories[hover])}</div>
          {series.map((s) => (
            <TooltipRow key={s.key} color={s.color} label={s.label} value={formatValue(s.values[hover], yType)} />
          ))}
        </Tooltip>
      )}
    </div>
  );
}

// tooltipTitle：时间桶显示完整时间（2026-10-01 08:00），其余显示类目名。
function tooltipTitle(full: string, label: string): string {
  if (/^\d{4}-\d{2}/.test(full)) return full.replace('T', ' ').replace(/:00(?:[+-]\d{2}:\d{2}|Z)$/, '');
  return label;
}

function arc(cx: number, cy: number, r0: number, r1: number, a0: number, a1: number): string {
  const p = (r: number, a: number) => `${cx + r * Math.sin(a)},${cy - r * Math.cos(a)}`;
  const large = a1 - a0 > Math.PI ? 1 : 0;
  if (a1 - a0 >= Math.PI * 2 - 1e-6) {
    // 只有一个扇区：两段半圆
    return `M${p(r1, 0)}A${r1},${r1} 0 1 1 ${p(r1, Math.PI)}A${r1},${r1} 0 1 1 ${p(r1, 0)}M${p(r0, 0)}A${r0},${r0} 0 1 0 ${p(r0, Math.PI)}A${r0},${r0} 0 1 0 ${p(r0, 0)}Z`;
  }
  return `M${p(r1, a0)}A${r1},${r1} 0 ${large} 1 ${p(r1, a1)}L${p(r0, a1)}A${r0},${r0} 0 ${large} 0 ${p(r0, a0)}Z`;
}

export function SvgPie({ slices, total, yType }: { slices: PieSlice[]; total: number; yType: string }) {
  const [hover, setHover] = useState<number | null>(null);
  if (total <= 0 || slices.length === 0) return <div className="text-[11px] text-gray-400 py-6 text-center">没有可画的数据</div>;
  const size = 160;
  const c = size / 2;
  let a = 0;
  const arcs = slices.map((s) => {
    const a0 = a;
    a += (s.value / total) * Math.PI * 2;
    return { s, a0, a1: a };
  });
  const pct = (v: number) => `${((v / total) * 100).toFixed(1)}%`;
  return (
    <div className="flex flex-wrap items-center gap-4">
      <svg width={size} height={size} role="img" className="shrink-0" onMouseLeave={() => setHover(null)}>
        {arcs.map(({ s, a0, a1 }, i) => (
          <path
            key={s.key}
            d={arc(c, c, c * 0.6, c - 2, a0, a1)}
            fill={s.color}
            stroke={SURFACE}
            strokeWidth={2}
            opacity={hover === null || hover === i ? 1 : 0.35}
            onMouseEnter={() => setHover(i)}
          />
        ))}
        <text x={c} y={c - 4} textAnchor="middle" fontSize={10} fill={INK_MUTED}>
          {hover === null ? '合计' : truncate(slices[hover].label, 80)}
        </text>
        <text x={c} y={c + 12} textAnchor="middle" fontSize={12} fontWeight={600} fill="#0b0b0b">
          {formatValue(hover === null ? total : slices[hover].value, yType, true)}
        </text>
      </svg>
      {/* 图例兼直接标注：名称、数值、占比 */}
      <ul className="flex-1 min-w-[10rem] space-y-0.5 text-[11px]">
        {slices.map((s, i) => (
          <li
            key={s.key}
            className={`flex items-center gap-1.5 rounded px-1 ${hover === i ? 'bg-gray-50' : ''}`}
            onMouseEnter={() => setHover(i)}
            onMouseLeave={() => setHover(null)}
          >
            <span className="w-2 h-2 rounded-sm shrink-0" style={{ background: s.color }} />
            <span className="text-gray-600 truncate flex-1 min-w-0">{s.label}</span>
            <span className="text-gray-900 tabular-nums">{formatValue(s.value, yType)}</span>
            <span className="text-gray-400 tabular-nums w-12 text-right">{pct(s.value)}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}
