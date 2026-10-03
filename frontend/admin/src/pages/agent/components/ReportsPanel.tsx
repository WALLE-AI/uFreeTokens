import { useCallback, useEffect, useMemo, useState } from 'react';
import { createPortal } from 'react-dom';
import { ArrowLeft, Download, FileBarChart2, Printer, Share2, Timer, Trash2 } from 'lucide-react';
import { Button, ConfirmDialog, DataState, IconButton, SearchInput, useToast } from '../../../components/ui';
import { DatasetChart } from '../../../components/charts/DatasetChart';
import { toChartData, type ChartData, type ChartSpec } from '../../../components/charts/chartData';
import { useAgent } from '../../../agent/AgentProvider';
import {
  deleteAgentReport,
  downloadAgentFile,
  getAgentReport,
  listAgentReports,
  updateAgentReport,
  type AgentReport,
  type AgentReportDetail,
} from '../../../api/agent';
import { useCan } from '../../../api/auth';
import { errorMessage } from '../../../api/errors';
import { cn } from '../../../lib/cn';
import { formatDateTime, formatRelative } from '../../../lib/time';
import { Markdown } from './Markdown';

// Dock「报表」页签（《运营后台全局助手执行方案》P3）：列表（我可见的 / 我的 / 全部）+ 报表详情。
// 详情可分享、删除、导出 Excel / Markdown，打印为 PDF（把报表渲染到 body 下的打印根节点再调用浏览器打印）。

interface Section {
  type: 'markdown' | 'chart';
  text?: string;
  chart?: ChartSpec;
}

function ReportList() {
  const { openReport } = useAgent();
  const canAudit = useCan('audit:read');
  const [scope, setScope] = useState<'' | 'mine' | 'all'>('');
  const [q, setQ] = useState('');
  const [list, setList] = useState<AgentReport[] | null>(null);
  const [err, setErr] = useState<unknown>(null);
  const load = useCallback(() => {
    listAgentReports({ scope: scope || undefined, q: q || undefined, limit: 50 })
      .then((p) => {
        setList(p.data ?? []);
        setErr(null);
      })
      .catch(setErr);
  }, [scope, q]);
  useEffect(() => {
    load();
  }, [load]);
  const scopes: Array<['' | 'mine' | 'all', string]> = [
    ['', '我可见的'],
    ['mine', '我的'],
    ...(canAudit ? ([['all', '全部']] as Array<['all', string]>) : []),
  ];
  return (
    <div className="flex-1 min-h-0 flex flex-col">
      <div className="p-3 space-y-2 border-b border-gray-100">
        <SearchInput placeholder="搜索报表标题" value={q} onChange={(e) => setQ(e.target.value)} />
        <div className="flex gap-1 text-[11px]">
          {scopes.map(([k, label]) => (
            <button key={k || 'visible'} type="button" onClick={() => setScope(k)} className={cn('px-2 py-0.5 rounded-full cursor-pointer', scope === k ? 'bg-purple-100 text-purple-700' : 'text-gray-500 hover:bg-gray-50')}>
              {label}
            </button>
          ))}
        </div>
      </div>
      <div className="flex-1 overflow-y-auto">
        <DataState loading={!list && !err} error={err} onRetry={load} empty={list?.length === 0} emptyTitle="还没有报表" skeleton="text">
          <ul className="py-1">
            {(list ?? []).map((r) => (
              <li key={r.id}>
                <button type="button" onClick={() => openReport(r.id)} className="w-full text-left px-3 py-2 hover:bg-gray-50 cursor-pointer flex items-start gap-2">
                  <FileBarChart2 className="w-3.5 h-3.5 text-purple-500 mt-0.5 shrink-0" />
                  <span className="min-w-0 flex-1">
                    <span className="flex items-center gap-1 text-xs text-gray-900">
                      {r.job_id && <Timer className="w-3 h-3 text-gray-400 shrink-0" />}
                      <span className="truncate">{r.title}</span>
                      {r.visibility === 'shared' && <span className="shrink-0 text-[10px] px-1 rounded bg-sky-50 text-sky-700">共享</span>}
                    </span>
                    {r.summary && <span className="block text-[11px] text-gray-500 truncate">{r.summary}</span>}
                    <span className="block text-[11px] text-gray-400">
                      报表 #{r.id} · {r.owner_name} · {formatRelative(r.created_at)}
                    </span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        </DataState>
      </div>
    </div>
  );
}

function ReportBody({ detail, datasets }: { detail: AgentReportDetail; datasets: Map<number, ChartData> }) {
  const r = detail.report;
  const sections = (Array.isArray(r.sections) ? r.sections : []) as Section[];
  return (
    <div className="space-y-3">
      <div>
        <h2 className="text-sm font-semibold text-gray-900">{r.title}</h2>
        <div className="text-[11px] text-gray-400 mt-0.5">
          报表 #{r.id} · {r.owner_name} · {formatDateTime(r.created_at)}
          {r.visibility === 'shared' ? ' · 共享' : ' · 仅自己可见'}
        </div>
      </div>
      {r.summary && <div className="rounded-lg bg-purple-50/50 border border-purple-100 px-3 py-2 text-xs text-gray-700">{r.summary}</div>}
      {sections.map((s, i) => {
        if (s.type === 'markdown') return <Markdown key={i} text={s.text ?? ''} />;
        const spec = s.chart;
        if (!spec) return null;
        const data = datasets.get(spec.dataset_id);
        if (!data) return <div key={i} className="text-[11px] text-gray-400">（数据集 #{spec.dataset_id} 已不存在）</div>;
        return <DatasetChart key={i} spec={spec} data={data} className="break-inside-avoid" />;
      })}
    </div>
  );
}

function ReportView({ id }: { id: number }) {
  const { openReport } = useAgent();
  const toast = useToast();
  const [detail, setDetail] = useState<AgentReportDetail | null>(null);
  const [err, setErr] = useState<unknown>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [printing, setPrinting] = useState(false);
  const load = useCallback(() => {
    getAgentReport(id)
      .then((d) => {
        setDetail(d);
        setErr(null);
      })
      .catch(setErr);
  }, [id]);
  useEffect(() => {
    load();
  }, [load]);
  const datasets = useMemo(() => new Map((detail?.datasets ?? []).map((d) => [d.id, toChartData(d)])), [detail]);

  useEffect(() => {
    if (!printing) return;
    const done = () => setPrinting(false);
    window.addEventListener('afterprint', done, { once: true });
    // 等打印根节点渲染完（图表需要一帧测量宽度）再打印。
    const t = setTimeout(() => window.print(), 300);
    return () => {
      clearTimeout(t);
      window.removeEventListener('afterprint', done);
    };
  }, [printing]);

  const download = (format: 'xlsx' | 'md') =>
    void downloadAgentFile(`/agent/reports/${id}/export?format=${format}`, `report-${id}.${format}`).catch((e) => toast.error('导出失败', errorMessage(e)));

  const r = detail?.report;
  return (
    <div className="flex-1 min-h-0 flex flex-col">
      <div className="px-3 py-2 border-b border-gray-100 flex items-center gap-1.5 flex-wrap">
        <Button size="sm" variant="ghost" icon={<ArrowLeft className="w-3 h-3" />} onClick={() => openReport(null)}>
          报表列表
        </Button>
        <span className="flex-1" />
        {r && detail?.can_edit && (
          <Button
            size="sm"
            icon={<Share2 className="w-3 h-3" />}
            onClick={() =>
              void updateAgentReport(id, { visibility: r.visibility === 'shared' ? 'private' : 'shared' })
                .then(() => {
                  toast.success(r.visibility === 'shared' ? '已取消共享' : '已共享给所有运营');
                  load();
                })
                .catch((e) => toast.error('操作失败', errorMessage(e)))
            }
          >
            {r.visibility === 'shared' ? '取消共享' : '共享'}
          </Button>
        )}
        <Button size="sm" icon={<Download className="w-3 h-3" />} onClick={() => download('xlsx')} disabled={!r}>
          Excel
        </Button>
        <Button size="sm" icon={<Download className="w-3 h-3" />} onClick={() => download('md')} disabled={!r}>
          Markdown
        </Button>
        <Button size="sm" icon={<Printer className="w-3 h-3" />} onClick={() => setPrinting(true)} disabled={!r}>
          PDF
        </Button>
        {detail?.can_edit && (
          <IconButton label="删除报表" onClick={() => setConfirmDelete(true)}>
            <Trash2 className="w-3.5 h-3.5" />
          </IconButton>
        )}
      </div>
      <div className="flex-1 overflow-y-auto p-3">
        <DataState loading={!detail && !err} error={err} onRetry={load} skeleton="text">
          {detail && <ReportBody detail={detail} datasets={datasets} />}
        </DataState>
      </div>
      <ConfirmDialog
        open={confirmDelete}
        onClose={() => setConfirmDelete(false)}
        level="danger"
        title="删除报表"
        confirmLabel="删除"
        onConfirm={async () => {
          try {
            await deleteAgentReport(id);
            toast.success('报表已删除');
            setConfirmDelete(false);
            openReport(null);
          } catch (e) {
            toast.error('删除失败', errorMessage(e));
          }
        }}
      >
        删除后不可恢复（引用的数据集随会话保留）。
      </ConfirmDialog>
      {printing &&
        detail &&
        createPortal(
          <div className="print-root bg-white p-8 max-w-[960px]">
            <ReportBody detail={detail} datasets={datasets} />
          </div>,
          document.body,
        )}
    </div>
  );
}

export function ReportsPanel() {
  const { reportId } = useAgent();
  return reportId ? <ReportView key={reportId} id={reportId} /> : <ReportList />;
}
