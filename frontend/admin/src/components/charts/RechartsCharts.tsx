import { Bar, BarChart, CartesianGrid, Cell, Line, LineChart, Pie, PieChart, ResponsiveContainer, Tooltip as RTooltip, XAxis, YAxis } from 'recharts';
import { formatValue, type CartesianModel, type PieSlice } from './chartData';
import { Legend, TooltipRow } from './SvgCharts';

// Recharts 渲染器（可选，按需懒加载，不进首屏包）：与内置 SVG 渲染器用同一份数据模型、配色与标注规则
// （细线、柱宽 ≤24px、数据端 4px 圆角、实线细网格、≥2 个系列有图例），只是换一套绘制实现。

const GRID = '#ecebe8';
const INK_MUTED = '#8a8984';
const tick = { fontSize: 10, fill: INK_MUTED };

function toRows(model: CartesianModel) {
  return model.categories.map((c, i) => {
    const row: Record<string, unknown> = { __cat: c, __full: model.categoryFull[i] };
    for (const s of model.series) row[s.key] = s.values[i];
    return row;
  });
}

function CartesianTooltip({ active, payload, model }: { active?: boolean; payload?: Array<{ payload: Record<string, unknown> }>; model: CartesianModel }) {
  if (!active || !payload?.length) return null;
  const row = payload[0].payload;
  return (
    <div className="min-w-[8rem] max-w-[16rem] rounded-lg border border-gray-200 bg-white/95 px-2 py-1.5 text-[11px] shadow-md">
      <div className="text-gray-900 font-medium mb-0.5">{String(row.__cat)}</div>
      {model.series.map((s) => (
        <TooltipRow key={s.key} color={s.color} label={s.label} value={formatValue(row[s.key], model.yType)} />
      ))}
    </div>
  );
}

export function RechartsCartesian({ kind, model }: { kind: 'line' | 'bar' | 'stacked_bar'; model: CartesianModel }) {
  const data = toRows(model);
  const yTick = (v: number) => formatValue(v, model.yType, true);
  const common = (
    <>
      <CartesianGrid vertical={false} stroke={GRID} />
      <XAxis dataKey="__cat" tick={tick} tickLine={false} axisLine={{ stroke: '#d4d3cf' }} minTickGap={12} />
      <YAxis tick={tick} tickLine={false} axisLine={false} tickFormatter={yTick} width={56} />
      <RTooltip content={<CartesianTooltip model={model} />} cursor={kind === 'line' ? { stroke: '#d4d3cf' } : { fill: '#f5f4f1' }} />
    </>
  );
  return (
    <div className="h-[220px]">
      <ResponsiveContainer width="100%" height="100%">
        {kind === 'line' ? (
          <LineChart data={data} margin={{ top: 8, right: 12, bottom: 0, left: 0 }}>
            {common}
            {model.series.map((s) => (
              <Line key={s.key} dataKey={s.key} name={s.label} stroke={s.color} strokeWidth={2} dot={false} activeDot={{ r: 4, stroke: '#fff', strokeWidth: 2 }} isAnimationActive={false} connectNulls={false} />
            ))}
          </LineChart>
        ) : (
          <BarChart data={data} margin={{ top: 8, right: 12, bottom: 0, left: 0 }} barGap={2} barCategoryGap="28%">
            {common}
            {model.series.map((s, i) => (
              <Bar
                key={s.key}
                dataKey={s.key}
                name={s.label}
                fill={s.color}
                maxBarSize={24}
                stackId={kind === 'stacked_bar' ? 'stack' : undefined}
                radius={kind === 'stacked_bar' && i < model.series.length - 1 ? 0 : [4, 4, 0, 0]}
                stroke={kind === 'stacked_bar' ? '#fff' : undefined}
                strokeWidth={kind === 'stacked_bar' ? 1 : 0}
                isAnimationActive={false}
              />
            ))}
          </BarChart>
        )}
      </ResponsiveContainer>
    </div>
  );
}

export function RechartsPie({ slices, total, yType }: { slices: PieSlice[]; total: number; yType: string }) {
  return (
    <div className="flex flex-wrap items-center gap-4">
      <div className="w-40 h-40 shrink-0">
        <ResponsiveContainer width="100%" height="100%">
          <PieChart>
            <Pie data={slices} dataKey="value" nameKey="label" innerRadius="60%" outerRadius="98%" stroke="#fff" strokeWidth={2} isAnimationActive={false}>
              {slices.map((s) => (
                <Cell key={s.key} fill={s.color} />
              ))}
            </Pie>
            <RTooltip formatter={(v) => formatValue(v, yType)} />
          </PieChart>
        </ResponsiveContainer>
      </div>
      <ul className="flex-1 min-w-[10rem] space-y-0.5 text-[11px]">
        {slices.map((s) => (
          <li key={s.key} className="flex items-center gap-1.5 px-1">
            <span className="w-2 h-2 rounded-sm shrink-0" style={{ background: s.color }} />
            <span className="text-gray-600 truncate flex-1 min-w-0">{s.label}</span>
            <span className="text-gray-900 tabular-nums">{formatValue(s.value, yType)}</span>
            <span className="text-gray-400 tabular-nums w-12 text-right">{((s.value / total) * 100).toFixed(1)}%</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

export { Legend };
