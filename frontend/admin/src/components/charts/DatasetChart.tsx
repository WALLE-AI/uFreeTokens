import { lazy, Suspense, useMemo, useState, useSyncExternalStore } from 'react';
import { BarChart3, Download, Table2 } from 'lucide-react';
import { DeltaTag } from '../ui/StatCard';
import { cn } from '../../lib/cn';
import {
  buildCartesian,
  buildPie,
  formatValue,
  NUMERIC_TYPES,
  num,
  splitByUnit,
  toCSV,
  type ChartData,
  type ChartSpec,
  type Column,
} from './chartData';
import { Legend, SvgCartesian, SvgPie } from './SvgCharts';

// DatasetChart：助手图表卡（对话中的 render_chart、报表中的图表 / 表格 / 指标卡共用）。
//   - 渲染器：内置 SVG（默认）或 Recharts（懒加载），全局切换并记住选择；
//   - 每张图都可以切到表格视图（无障碍等价物），表格可导出 CSV；
//   - 不同单位的 y 列拆成多张小图（一张图只有一个纵轴）。

const LazyRechartsCartesian = lazy(() => import('./RechartsCharts').then((m) => ({ default: m.RechartsCartesian })));
const LazyRechartsPie = lazy(() => import('./RechartsCharts').then((m) => ({ default: m.RechartsPie })));

export type ChartRenderer = 'svg' | 'recharts';
const RENDERER_KEY = 'uft_chart_renderer';
const listeners = new Set<() => void>();
let renderer: ChartRenderer = localStorage.getItem(RENDERER_KEY) === 'recharts' ? 'recharts' : 'svg';

export function setChartRenderer(r: ChartRenderer) {
  renderer = r;
  localStorage.setItem(RENDERER_KEY, r);
  listeners.forEach((l) => l());
}

export function useChartRenderer(): ChartRenderer {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => renderer,
  );
}

// 成本、错误率、延迟、退款、过期类指标上涨是坏事。
function goodWhenUp(key: string): boolean {
  return !/cost|error|latency|ttft|refund|expired|frozen/.test(key);
}

export function downloadText(filename: string, text: string, type = 'text/csv;charset=utf-8') {
  const url = URL.createObjectURL(new Blob([text], { type }));
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

function KpiTiles({ spec, data }: { spec: ChartSpec; data: ChartData }) {
  const source = data.totals ?? data.rows[0] ?? {};
  const keys = (spec.y ?? []).filter((k) => data.byKey.has(k));
  return (
    <div className="grid grid-cols-2 sm:grid-cols-[repeat(auto-fit,minmax(9rem,1fr))] gap-2">
      {keys.map((k) => {
        const col = data.byKey.get(k)!;
        const cur = num(source[k]);
        const prev = data.previous ? num(data.previous[k]) : null;
        let delta: number | null = null;
        if (cur !== null && prev !== null) {
          if (col.type === 'percent') delta = cur - prev;
          else if (prev !== 0) delta = (cur - prev) / Math.abs(prev);
        }
        return (
          <div key={k} className="rounded-lg bg-gray-50 border border-gray-200 px-3 py-2 min-w-0">
            <div className="text-[11px] text-gray-500 truncate">{col.label}</div>
            <div className="flex items-baseline gap-1.5 flex-wrap">
              <span className="text-base font-semibold text-gray-900">{formatValue(cur, col.type)}</span>
              {delta !== null && <DeltaTag delta={delta} unit={col.type === 'percent' ? 'pp' : 'percent'} goodWhenUp={goodWhenUp(k)} hint={`上期 ${formatValue(prev, col.type)}`} />}
            </div>
          </div>
        );
      })}
    </div>
  );
}

const TABLE_LIMIT = 100;

export function DatasetTable({ data, columns }: { data: ChartData; columns: Column[] }) {
  const rows = data.rows.slice(0, TABLE_LIMIT);
  return (
    <div>
      <div className="overflow-x-auto rounded-lg border border-gray-200 max-h-80 overflow-y-auto">
        <table className="w-full text-[11px]">
          <thead className="bg-gray-50 sticky top-0">
            <tr>
              {columns.map((c) => (
                <th key={c.key} className={cn('px-2 py-1.5 font-medium text-gray-500 whitespace-nowrap', NUMERIC_TYPES.has(c.type) ? 'text-right' : 'text-left')}>
                  {c.label}
                </th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100">
            {rows.map((r, i) => (
              <tr key={i}>
                {columns.map((c) => (
                  <td key={c.key} className={cn('px-2 py-1 whitespace-nowrap', NUMERIC_TYPES.has(c.type) ? 'text-right tabular-nums text-gray-900' : 'text-gray-700 max-w-[16rem] truncate')}>
                    {c.key === 'key' && r[c.key] === '__other__' ? '其他' : formatValue(r[c.key], c.type)}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {data.rows.length > TABLE_LIMIT && <div className="text-[11px] text-gray-400 mt-1">显示前 {TABLE_LIMIT} 行，共 {data.rows.length} 行（导出 CSV 含全部行）。</div>}
    </div>
  );
}

function PlotArea({ spec, data, yKeys, engine }: { spec: ChartSpec; data: ChartData; yKeys: string[]; engine: ChartRenderer }) {
  const kind = spec.type as 'line' | 'bar' | 'stacked_bar';
  const model = useMemo(() => buildCartesian(spec, data, yKeys), [spec, data, yKeys]);
  return (
    <div className="space-y-1.5">
      <Legend items={model.series} />
      {engine === 'recharts' ? (
        <Suspense fallback={<div className="h-[220px]" />}>
          <LazyRechartsCartesian kind={kind} model={model} />
        </Suspense>
      ) : (
        <SvgCartesian kind={kind} model={model} />
      )}
      {model.folded > 0 && (
        <div className="text-[11px] text-gray-400">
          {model.series.some((s) => s.key === '__folded__') ? `另有 ${model.folded} 个分组合并为「其他」。` : `另有 ${model.folded} 个分组未显示（比率类指标不能合并），见表格视图。`}
        </div>
      )}
    </div>
  );
}

export function DatasetChart({ spec, data, className }: { spec: ChartSpec; data: ChartData; className?: string }) {
  const engine = useChartRenderer();
  const plottable = spec.type !== 'table' && spec.type !== 'kpi';
  const [view, setView] = useState<'chart' | 'table'>(plottable ? 'chart' : 'table');
  const title = spec.title || data.title;

  const tableColumns = useMemo(() => {
    if (spec.type === 'table' && spec.columns?.length) return spec.columns.map((k) => data.byKey.get(k)).filter((c): c is Column => !!c);
    if (spec.type === 'kpi') return data.columns;
    if (plottable && spec.x) {
      const keys = [spec.x, ...(spec.series ? [spec.series] : []), ...(spec.y ?? [])];
      const picked = keys.map((k) => data.byKey.get(k)).filter((c): c is Column => !!c);
      if (picked.length) return picked;
    }
    return data.columns;
  }, [spec, data, plottable]);

  const unitGroups = useMemo(() => (plottable && spec.type !== 'pie' ? splitByUnit(spec.y ?? [], data) : []), [plottable, spec, data]);
  const pie = useMemo(() => (spec.type === 'pie' ? buildPie(spec, data) : null), [spec, data]);

  return (
    <div className={cn('rounded-xl border border-gray-200 bg-white p-3 space-y-2', className)}>
      <div className="flex items-center gap-2">
        <div className="text-xs font-medium text-gray-900 truncate flex-1 min-w-0" title={title}>
          {title}
        </div>
        {plottable && (
          <div className="flex items-center gap-2 print:hidden">
            <select
              aria-label="图表引擎"
              value={engine}
              onChange={(e) => setChartRenderer(e.target.value as ChartRenderer)}
              className="text-[10px] text-gray-500 bg-transparent border border-gray-200 rounded px-1 py-0.5 cursor-pointer"
            >
              <option value="svg">SVG</option>
              <option value="recharts">Recharts</option>
            </select>
            <div className="flex rounded-md border border-gray-200 overflow-hidden text-[11px]">
              {(
                [
                  ['chart', '图表', BarChart3],
                  ['table', '表格', Table2],
                ] as const
              ).map(([k, label, Icon]) => (
                <button
                  key={k}
                  type="button"
                  onClick={() => setView(k)}
                  className={cn('px-1.5 py-0.5 inline-flex items-center gap-1 cursor-pointer', view === k ? 'bg-purple-50 text-purple-700' : 'text-gray-500 hover:bg-gray-50')}
                >
                  <Icon className="w-3 h-3" />
                  {label}
                </button>
              ))}
            </div>
          </div>
        )}
        <button
          type="button"
          title="导出 CSV"
          aria-label="导出 CSV"
          onClick={() => downloadText(`dataset-${data.id}.csv`, toCSV(data, tableColumns))}
          className="p-1 rounded text-gray-400 hover:text-gray-700 hover:bg-gray-50 cursor-pointer print:hidden"
        >
          <Download className="w-3.5 h-3.5" />
        </button>
      </div>

      {spec.type === 'kpi' && <KpiTiles spec={spec} data={data} />}
      {view === 'table' && spec.type !== 'kpi' && <DatasetTable data={data} columns={tableColumns} />}
      {view === 'chart' && pie && (
        <>
          {engine === 'recharts' ? (
            <Suspense fallback={<div className="h-40" />}>
              <LazyRechartsPie slices={pie.slices} total={pie.total} yType={pie.yType} />
            </Suspense>
          ) : (
            <SvgPie slices={pie.slices} total={pie.total} yType={pie.yType} />
          )}
          {pie.dropped > 0 && <div className="text-[11px] text-gray-400">{pie.dropped} 项为零或负值，未计入占比。</div>}
        </>
      )}
      {view === 'chart' &&
        unitGroups.map((keys) => (
          <div key={keys.join(',')}>
            {unitGroups.length > 1 && <div className="text-[11px] text-gray-500 mb-1">{keys.map((k) => data.byKey.get(k)?.label ?? k).join('、')}</div>}
            <PlotArea spec={spec} data={data} yKeys={keys} engine={engine} />
          </div>
        ))}

      <div className="flex flex-wrap gap-x-2 text-[10px] text-gray-400">
        <span>数据集 #{data.id}</span>
        {data.notes.map((n) => (
          <span key={n}>· {n}</span>
        ))}
      </div>
    </div>
  );
}
