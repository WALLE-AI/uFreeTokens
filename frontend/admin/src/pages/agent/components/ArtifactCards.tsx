import { useEffect, useState } from 'react';
import { ChevronRight, FileBarChart2 } from 'lucide-react';
import { DatasetChart } from '../../../components/charts/DatasetChart';
import { toChartData, type ChartData, type ChartSpec } from '../../../components/charts/chartData';
import { useAgentOptional } from '../../../agent/AgentProvider';
import { getAgentDataset } from '../../../api/agent';
import { errorMessage } from '../../../api/errors';
import type { ToolCallView } from './ToolCallCard';

// 对话中的成果物（《运营后台全局助手执行方案》P3）：执行成功的 render_chart 渲染为图表卡、create_report 渲染为
// 报表卡。它们显示在回答区（不随过程折叠）；图表的数字从数据集读取，不来自模型输出。

export const ARTIFACT_TOOLS = new Set(['render_chart', 'create_report']);

export function isArtifact(call: ToolCallView): boolean {
  return ARTIFACT_TOOLS.has(call.tool) && call.status === 'done';
}

function parseArgs(args: unknown): Record<string, unknown> {
  if (typeof args === 'string') {
    try {
      return JSON.parse(args) as Record<string, unknown>;
    } catch {
      return {};
    }
  }
  return (args as Record<string, unknown>) ?? {};
}

export function ChartArtifact({ spec }: { spec: ChartSpec }) {
  const [data, setData] = useState<ChartData | null>(null);
  const [err, setErr] = useState<string | null>(null);
  useEffect(() => {
    let alive = true;
    getAgentDataset(spec.dataset_id)
      .then((d) => alive && setData(toChartData(d)))
      .catch((e) => alive && setErr(errorMessage(e)));
    return () => {
      alive = false;
    };
  }, [spec.dataset_id]);
  if (err) return <div className="text-[11px] text-rose-600">图表加载失败：{err}</div>;
  if (!data) return <div className="h-24 rounded-xl border border-gray-200 bg-gray-50 animate-pulse" />;
  return <DatasetChart spec={spec} data={data} />;
}

function ReportArtifact({ call }: { call: ToolCallView }) {
  const agent = useAgentOptional();
  const m = /报表 #(\d+)：(.*)$/.exec(call.summary ?? '');
  const result = (call.result ?? {}) as { report_id?: number; title?: string };
  const id = result.report_id ?? (m ? Number(m[1]) : null);
  const title = result.title ?? m?.[2] ?? String(parseArgs(call.args).title ?? '报表');
  if (!id) return null;
  return (
    <button
      type="button"
      onClick={() => agent?.openReport(id)}
      className="w-full flex items-center gap-2 rounded-xl border border-purple-200 bg-purple-50/50 px-3 py-2 text-left hover:border-purple-300 cursor-pointer"
    >
      <FileBarChart2 className="w-4 h-4 text-purple-600 shrink-0" />
      <span className="min-w-0 flex-1">
        <span className="block text-xs font-medium text-gray-900 truncate">{title}</span>
        <span className="block text-[11px] text-gray-500">报表 #{id} · 点击查看、分享或导出</span>
      </span>
      <ChevronRight className="w-4 h-4 text-gray-400 shrink-0" />
    </button>
  );
}

export function ArtifactCard({ call }: { call: ToolCallView }) {
  if (call.tool === 'create_report') return <ReportArtifact call={call} />;
  const spec = parseArgs(call.args) as unknown as ChartSpec;
  if (!spec.dataset_id) return null;
  return <ChartArtifact spec={spec} />;
}
