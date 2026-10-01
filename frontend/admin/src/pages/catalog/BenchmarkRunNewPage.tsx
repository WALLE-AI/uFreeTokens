import { useEffect, useMemo, useRef, useState, type ChangeEvent } from 'react';
import { useNavigate, useParams } from 'react-router';
import { CheckCircle2, CircleDashed, ClipboardPaste, Download, FileUp, HelpCircle, Loader2, Plus, Trash2, XCircle } from 'lucide-react';
import { useCan } from '../../api/auth';
import { createBenchmarkRun, getBenchmark, type CreateBenchmarkRunBody } from '../../api/benchmarks';
import { findVirtualModelByName } from '../../api/catalog';
import { ApiError, describeError } from '../../api/errors';
import { Button, Card, DataState, EmptyState, Field, IconButton, Input, Modal, RadioCards, Select, StickyActionBar, Switch, Textarea, useToast } from '../../components/ui';
import { useAsync } from '../../hooks/useAsync';
import { cn } from '../../lib/cn';
import { parseDelimited } from '../../lib/csv';
import { yuanToMicro } from '../../lib/money';
import { zoneOffset, zonedParts, ADMIN_TZ } from '../../lib/tz';
import type { BenchmarkCostCurrency, BenchmarkResultInput } from '../../types';
import { COST_CURRENCY_OPTIONS, currencySign, formatScore, microToDecimalText } from './benchmarkShared';

// 录入一次基准测试 run（技术方案 §3.5）：run 信息 + 成绩表格。成绩可以手工逐行填，
// 也可以从电子表格粘贴（TSV）或导入 CSV 文件，在浏览器里解析成行；每行按
// virtual_model（留空则按 model_label）调 GET /virtual-models/lookup 自动匹配虚拟模型，
// 匹配上的行提交 virtual_model_id。整批提交，后端全部成功或全部失败。

// 表头列名（导入时大小写不敏感）。cost_per_task 是 cost_per_task_micro 的小数写法，二选一。
const IMPORT_COLUMNS = ['model_label', 'virtual_model', 'score', 'cost_per_task_micro', 'cost_per_task', 'avg_duration_ms', 'error_rate', 'sample_count', 'extra'] as const;
type ImportColumn = (typeof IMPORT_COLUMNS)[number];

const TEMPLATE_CSV =
  'model_label,virtual_model,score,cost_per_task_micro,avg_duration_ms,error_rate,sample_count\n' +
  'DeepSeek V4,deepseek/deepseek-v4,78.5,12300,4200,0.01,198\n' +
  'GPT-5 (high),,84.1,,,,\n';

interface Row {
  key: number;
  line?: number; // 导入来源的行号（表头为第 1 行）
  model_label: string;
  virtual_model: string; // 显式指定的虚拟模型名；留空 = 按 model_label 匹配
  score: string;
  cost: string; // 单题成本，cost_currency 的小数（提交时换算成微单位）
  avg_duration_ms: string;
  error_rate: string;
  sample_count: string;
  extra: string; // JSON 对象文本，仅导入时可带
  parseErrors: string[]; // 导入时的解析错误；编辑该行后清除
}

type MatchState = { state: 'pending' } | { state: 'found'; id: number; name: string } | { state: 'missing' } | { state: 'error'; message: string };

let rowSeq = 0;
function emptyRow(): Row {
  return { key: ++rowSeq, model_label: '', virtual_model: '', score: '', cost: '', avg_duration_ms: '', error_rate: '', sample_count: '', extra: '', parseErrors: [] };
}

const isBlankRow = (r: Row) =>
  !r.model_label.trim() && !r.virtual_model.trim() && !r.score.trim() && !r.cost.trim() && !r.avg_duration_ms.trim() && !r.error_rate.trim() && !r.sample_count.trim() && !r.extra.trim();

const lookupName = (r: Row) => r.virtual_model.trim() || r.model_label.trim();

// ---------- 时间：datetime-local 按运营时区（ADMIN_TZ）解释 ----------

function nowLocalInput(): string {
  const p = zonedParts(Date.now());
  return `${p.year}-${p.month}-${p.day}T${p.hour}:${p.minute}`;
}

function localInputToRFC3339(v: string): string | null {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(v)) return null;
  const approx = Date.parse(`${v}:00Z`);
  const t = Date.parse(`${v}:00${zoneOffset(approx)}`);
  return Number.isNaN(t) ? null : new Date(t).toISOString();
}

// ---------- 单元格解析 ----------

function parseNonNegInt(v: string): number | null | 'bad' {
  const s = v.trim().replace(/,/g, '');
  if (s === '') return null;
  return /^\d+$/.test(s) && Number.isSafeInteger(Number(s)) ? Number(s) : 'bad';
}

// 错误率：0..1 的小数，或带 % 的百分数（"1.5%" = 0.015）
function parseErrorRate(v: string): number | null | 'bad' {
  const s = v.trim();
  if (s === '') return null;
  const pct = s.endsWith('%');
  const n = Number(pct ? s.slice(0, -1).trim() : s);
  if (!Number.isFinite(n)) return 'bad';
  const r = pct ? n / 100 : n;
  return r >= 0 && r <= 1 ? r : 'bad';
}

function parseCost(v: string): number | null | 'bad' {
  const s = v.trim();
  if (s === '') return null;
  const micro = yuanToMicro(s);
  return micro === null || micro < 0 ? 'bad' : micro;
}

function parseExtra(v: string): Record<string, unknown> | null | 'bad' {
  const s = v.trim();
  if (s === '') return null;
  try {
    const o: unknown = JSON.parse(s);
    return o && typeof o === 'object' && !Array.isArray(o) ? (o as Record<string, unknown>) : 'bad';
  } catch {
    return 'bad';
  }
}

// 逐行校验（随编辑实时更新）
function validateRow(r: Row, dupLabels: Set<string>, match: MatchState | undefined): string[] {
  const errs: string[] = [];
  const label = r.model_label.trim();
  if (!label) errs.push('model_label 必填');
  else if (dupLabels.has(label)) errs.push(`model_label「${label}」重复`);
  if (r.score.trim() === '') errs.push('score 必填');
  else if (!Number.isFinite(Number(r.score.trim()))) errs.push('score 需为数字');
  if (parseCost(r.cost) === 'bad') errs.push('单题成本需为非负数，最多 6 位小数');
  if (parseNonNegInt(r.avg_duration_ms) === 'bad') errs.push('avg_duration_ms 需为非负整数');
  if (parseErrorRate(r.error_rate) === 'bad') errs.push('error_rate 需在 0–1 之间（或 0%–100%）');
  if (parseNonNegInt(r.sample_count) === 'bad') errs.push('sample_count 需为非负整数');
  if (parseExtra(r.extra) === 'bad') errs.push('extra 需为 JSON 对象');
  if (r.virtual_model.trim() && match?.state === 'missing') errs.push(`虚拟模型「${r.virtual_model.trim()}」不存在（清空则按 model_label 匹配）`);
  if (r.virtual_model.trim() && match?.state === 'error') errs.push(`虚拟模型「${r.virtual_model.trim()}」查询失败，请点"重新匹配"`);
  return errs;
}

// 表格文本 → 行；表头必需，至少包含 model_label 与 score
function importTable(text: string): { rows: Row[]; error?: string; warnings: string[] } {
  const t = parseDelimited(text);
  if (t.header.length === 0) return { rows: [], error: '没有内容', warnings: [] };
  const header = t.header.map((h) => h.toLowerCase());
  const idx = new Map<ImportColumn, number>();
  const warnings: string[] = [];
  header.forEach((h, i) => {
    if ((IMPORT_COLUMNS as readonly string[]).includes(h)) idx.set(h as ImportColumn, i);
    else if (h) warnings.push(`已忽略未知列「${t.header[i]}」`);
  });
  if (!idx.has('model_label') || !idx.has('score')) {
    return { rows: [], error: `第 1 行必须是表头，且至少包含 model_label 和 score 两列（当前表头：${t.header.join(', ') || '空'}）`, warnings };
  }
  if (idx.has('cost_per_task_micro') && idx.has('cost_per_task')) warnings.push('同时有 cost_per_task_micro 与 cost_per_task 两列，以 cost_per_task_micro 为准');
  if (t.rows.length === 0) return { rows: [], error: '只有表头，没有数据行', warnings };

  const rows = t.rows.map(({ line, cells }) => {
    const get = (c: ImportColumn) => {
      const i = idx.get(c);
      return i === undefined ? '' : (cells[i] ?? '').trim();
    };
    const parseErrors: string[] = [];
    // 末尾缺少的可选列视为空；多出来的列说明分隔符或引号有问题
    if (cells.length > header.length) parseErrors.push(`有 ${cells.length} 列，多于表头的 ${header.length} 列（检查分隔符或引号）`);
    let cost = get('cost_per_task');
    const microRaw = get('cost_per_task_micro');
    if (microRaw !== '') {
      const micro = parseNonNegInt(microRaw);
      if (micro === 'bad' || micro === null) {
        parseErrors.push(`cost_per_task_micro「${microRaw}」需为非负整数（微单位）`);
        cost = '';
      } else {
        cost = microToDecimalText(micro);
      }
    }
    return {
      ...emptyRow(),
      line,
      model_label: get('model_label'),
      virtual_model: get('virtual_model'),
      score: get('score'),
      cost,
      avg_duration_ms: get('avg_duration_ms'),
      error_rate: get('error_rate'),
      sample_count: get('sample_count'),
      extra: get('extra'),
      parseErrors,
    };
  });
  return { rows, warnings };
}

// ---------- 页面 ----------

export default function BenchmarkRunNewPage() {
  const id = Number(useParams().id);
  const navigate = useNavigate();
  const toast = useToast();
  const canWrite = useCan('catalog:write');
  const bench = useAsync((signal) => getBenchmark(id, signal), [id]);

  const [runAt, setRunAt] = useState(nowLocalInput);
  const [origin, setOrigin] = useState<'manual' | 'import'>('manual');
  const [currency, setCurrency] = useState<BenchmarkCostCurrency>('USD');
  const [notes, setNotes] = useState('');
  const [publish, setPublish] = useState(false);
  const [rows, setRows] = useState<Row[]>(() => [emptyRow()]);
  const [pasteOpen, setPasteOpen] = useState(false);
  const [importNote, setImportNote] = useState<{ error?: string; warnings: string[] } | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<{ message: string; rowKey?: number } | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  // ---- 自动匹配虚拟模型：名称 → 匹配结果缓存；名称变化 400ms 后补查缺失的 ----
  const [matches, setMatches] = useState<Record<string, MatchState>>({});
  const [retryTick, setRetryTick] = useState(0);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const namesKey = useMemo(() => Array.from(new Set(rows.map(lookupName).filter(Boolean))).sort().join('\n'), [rows]);
  useEffect(() => {
    const names = namesKey ? namesKey.split('\n') : [];
    const todo = names.filter((n) => !matches[n] || matches[n].state === 'error');
    if (todo.length === 0) return;
    const timer = setTimeout(() => {
      setMatches((m) => ({ ...m, ...Object.fromEntries(todo.map((n) => [n, { state: 'pending' } as MatchState])) }));
      // 最多 4 个并发
      let next = 0;
      const worker = async () => {
        while (next < todo.length && mounted.current) {
          const name = todo[next++];
          let res: MatchState;
          try {
            const vm = await findVirtualModelByName(name);
            res = vm ? { state: 'found', id: vm.id, name: vm.name } : { state: 'missing' };
          } catch (err) {
            res = { state: 'error', message: describeError(err, '查询失败') };
          }
          if (mounted.current) setMatches((m) => ({ ...m, [name]: res }));
        }
      };
      void Promise.all(Array.from({ length: Math.min(4, todo.length) }, worker));
    }, 400);
    return () => clearTimeout(timer);
    // matches 只用于判断缺失项；把它放进依赖会在每次查询返回后重复调度
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [namesKey, retryTick]);

  // ---- 校验 ----
  const dataRows = rows.filter((r) => !isBlankRow(r));
  const dupLabels = useMemo(() => {
    const seen = new Set<string>();
    const dup = new Set<string>();
    for (const r of rows) {
      const l = r.model_label.trim();
      if (!l) continue;
      if (seen.has(l)) dup.add(l);
      seen.add(l);
    }
    return dup;
  }, [rows]);
  const rowErrors = new Map<number, string[]>();
  for (const r of dataRows) {
    const errs = [...r.parseErrors, ...validateRow(r, dupLabels, matches[lookupName(r)])];
    if (errs.length) rowErrors.set(r.key, errs);
  }
  const matchStats = dataRows.reduce(
    (acc, r) => {
      const m = matches[lookupName(r)];
      if (!lookupName(r)) return acc;
      if (!m || m.state === 'pending') acc.pending++;
      else if (m.state === 'found') acc.found++;
      else if (m.state === 'error') acc.failed++;
      else acc.missing++;
      return acc;
    },
    { found: 0, missing: 0, pending: 0, failed: 0 },
  );
  const runAtISO = localInputToRFC3339(runAt);
  const canSubmit = dataRows.length > 0 && rowErrors.size === 0 && matchStats.pending === 0 && !!runAtISO;

  // ---- 行编辑 ----
  const updateRow = (key: number, patch: Partial<Row>) => {
    setRows((rs) => rs.map((r) => (r.key === key ? { ...r, ...patch, parseErrors: [] } : r)));
    if (submitError?.rowKey === key) setSubmitError(null);
  };
  const removeRow = (key: number) => setRows((rs) => (rs.length > 1 ? rs.filter((r) => r.key !== key) : [emptyRow()]));

  const applyImport = (text: string, source: string) => {
    const res = importTable(text);
    setImportNote(res.error || res.warnings.length ? { error: res.error, warnings: res.warnings } : null);
    if (res.error) return false;
    // 当前只有空行时替换，否则追加
    setRows((rs) => (rs.every(isBlankRow) ? res.rows : [...rs.filter((r) => !isBlankRow(r)), ...res.rows]));
    setOrigin('import');
    setSubmitError(null);
    toast.success(`已从${source}导入 ${res.rows.length} 行`);
    return true;
  };

  const onFile = async (e: ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0];
    e.target.value = '';
    if (!f) return;
    applyImport(await f.text(), ` ${f.name} `);
  };

  const downloadTemplate = () => {
    const url = URL.createObjectURL(new Blob([TEMPLATE_CSV], { type: 'text/csv;charset=utf-8' }));
    const a = document.createElement('a');
    a.href = url;
    a.download = 'benchmark-run-template.csv';
    a.click();
    URL.revokeObjectURL(url);
  };

  const submit = async () => {
    if (!canSubmit || !runAtISO) return;
    const submitted = dataRows;
    const results: BenchmarkResultInput[] = submitted.map((r) => {
      const m = matches[lookupName(r)];
      const cost = parseCost(r.cost);
      const dur = parseNonNegInt(r.avg_duration_ms);
      const er = parseErrorRate(r.error_rate);
      const sc = parseNonNegInt(r.sample_count);
      const extra = parseExtra(r.extra);
      return {
        model_label: r.model_label.trim(),
        virtual_model_id: m?.state === 'found' ? m.id : null,
        score: Number(r.score.trim()),
        cost_per_task_micro: typeof cost === 'number' ? cost : null,
        avg_duration_ms: typeof dur === 'number' ? dur : null,
        error_rate: typeof er === 'number' ? er : null,
        sample_count: typeof sc === 'number' ? sc : null,
        extra: extra === 'bad' ? null : extra,
      };
    });
    const body: CreateBenchmarkRunBody = { origin, run_at: runAtISO, notes: notes.trim(), cost_currency: currency, publish, results };
    setSubmitting(true);
    setSubmitError(null);
    try {
      const run = await createBenchmarkRun(id, body);
      toast.success(`run #${run.id} 已${publish ? '录入并发布' : '保存为草稿'}（${run.result_count} 个模型）`);
      navigate(`/benchmarks/${id}?run=${run.id}`);
    } catch (err) {
      // 后端的 400 指向 results[i]：定位到对应的行
      const m = err instanceof ApiError ? /results\[(\d+)\]/.exec(err.detail) : null;
      const row = m ? submitted[Number(m[1])] : undefined;
      setSubmitError({ message: describeError(err, '保存失败'), rowKey: row?.key });
    } finally {
      setSubmitting(false);
    }
  };

  if (!canWrite) {
    return <EmptyState title="没有权限" description="录入基准测试 run 需要 catalog:write 权限，请联系管理员分配角色" />;
  }

  return (
    <DataState loading={bench.loading} error={bench.error} onRetry={bench.reload} skeleton="cards">
      {bench.data && (
        <div>
          <StickyActionBar
            backTo={`/benchmarks/${id}`}
            backLabel={bench.data.name}
            title={<span className="font-sans">录入 run</span>}
            actions={
              <>
                <Button onClick={() => navigate(`/benchmarks/${id}`)} disabled={submitting}>
                  取消
                </Button>
                <Button variant="primary" loading={submitting} disabled={!canSubmit} onClick={submit}>
                  {publish ? '保存并发布' : '保存为草稿'}
                </Button>
              </>
            }
          />

          <Card className="mb-6">
            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
              <Field label="评测时间" required hint={`按 ${ADMIN_TZ} 时区`} error={runAtISO ? undefined : '请选择评测时间'}>
                <Input type="datetime-local" mono value={runAt} invalid={!runAtISO} onChange={(e) => setRunAt(e.target.value)} />
              </Field>
              <Field label="来源">
                <RadioCards
                  options={[
                    { value: 'manual', label: '手工录入' },
                    { value: 'import', label: '表格导入', hint: '粘贴 / CSV 时自动选中' },
                  ]}
                  value={origin}
                  onChange={setOrigin}
                />
              </Field>
              <Field label="成本币种" hint="单题成本列的币种">
                <Select className="w-full" value={currency} onChange={(e) => setCurrency(e.target.value as BenchmarkCostCurrency)} options={COST_CURRENCY_OPTIONS} />
              </Field>
            </div>
            <Field label="备注" className="mt-4">
              <Textarea rows={2} value={notes} onChange={(e) => setNotes(e.target.value)} placeholder="数据来源、评测设置（温度、思考档位）等" />
            </Field>
            <div className="mt-4">
              <Switch
                checked={publish}
                onChange={setPublish}
                label={
                  <span>
                    保存后立即发布
                    <span className="text-gray-400 ml-1.5">
                      {publish ? '将替换当前已发布的 run（旧 run 退为历史）' : '先存为草稿，确认无误后在详情页发布'}
                    </span>
                  </span>
                }
              />
              {publish && bench.data.status !== 'published' && (
                <p className="text-[11px] text-amber-700 mt-1.5">基准本身尚未发布，run 发布后也不会出现在公开页，需在详情页发布基准。</p>
              )}
            </div>
          </Card>

          <div className="flex flex-wrap items-center gap-2 mb-3">
            <h2 className="text-sm font-semibold text-gray-900 mr-2">成绩（{dataRows.length} 行）</h2>
            <span className="text-[11px] text-gray-500">
              已关联 <span className="text-emerald-700 font-medium">{matchStats.found}</span> · 未关联 <span className="text-gray-700 font-medium">{matchStats.missing}</span>
              {matchStats.pending > 0 && <span> · 匹配中 {matchStats.pending}</span>}
              {matchStats.failed > 0 && (
                <span className="text-amber-700">
                  {' '}
                  · 查询失败 {matchStats.failed}
                  <button type="button" className="ml-1 text-purple-600 hover:text-purple-700 cursor-pointer" onClick={() => setRetryTick((t) => t + 1)}>
                    重新匹配
                  </button>
                </span>
              )}
              {rowErrors.size > 0 && <span className="text-rose-600"> · {rowErrors.size} 行有错误</span>}
            </span>
            <div className="ml-auto flex flex-wrap items-center gap-2">
              <Button size="sm" icon={<ClipboardPaste className="w-3.5 h-3.5" />} onClick={() => setPasteOpen(true)}>
                从表格粘贴
              </Button>
              <Button size="sm" icon={<FileUp className="w-3.5 h-3.5" />} onClick={() => fileRef.current?.click()}>
                导入 CSV
              </Button>
              <input ref={fileRef} type="file" accept=".csv,.tsv,.txt,text/csv,text/tab-separated-values" className="hidden" onChange={onFile} />
              <Button size="sm" icon={<Download className="w-3.5 h-3.5" />} onClick={downloadTemplate}>
                下载模板
              </Button>
              <Button size="sm" variant="ghost" onClick={() => setRows([emptyRow()])} disabled={rows.every(isBlankRow)}>
                清空
              </Button>
            </div>
          </div>

          {importNote && (
            <div className={cn('rounded-xl p-3 text-xs mb-3 border', importNote.error ? 'bg-rose-50 border-rose-200 text-rose-700' : 'bg-amber-50/80 border-amber-200 text-amber-900')}>
              {importNote.error && <div className="font-medium">导入失败：{importNote.error}</div>}
              {importNote.warnings.map((w) => (
                <div key={w}>{w}</div>
              ))}
            </div>
          )}

          {submitError && (
            <div className="bg-rose-50 border border-rose-200 text-rose-700 rounded-xl p-3 text-xs mb-3">
              {submitError.message}
              {submitError.rowKey !== undefined && <span className="ml-1">（已在下方标出对应行）</span>}
            </div>
          )}

          <ResultsGrid
            rows={rows}
            currency={currency}
            metricUnit={bench.data.metric_unit}
            matches={matches}
            rowErrors={rowErrors}
            highlightKey={submitError?.rowKey}
            onChange={updateRow}
            onRemove={removeRow}
          />
          <div className="mt-2 flex items-center gap-3">
            <Button size="sm" icon={<Plus className="w-3.5 h-3.5" />} onClick={() => setRows((rs) => [...rs, emptyRow()])}>
              添加一行
            </Button>
            <span className="text-[11px] text-gray-400">完全空白的行提交时自动忽略</span>
          </div>

          <FormatHelp currency={currency} />

          <PasteModal
            open={pasteOpen}
            onClose={() => setPasteOpen(false)}
            onImport={(text) => {
              if (applyImport(text, '粘贴内容')) setPasteOpen(false);
            }}
          />
        </div>
      )}
    </DataState>
  );
}

// ---------- 成绩表格 ----------

const CELL_INPUT = 'w-full bg-transparent border border-transparent rounded px-1.5 py-1 text-xs focus:outline-none focus:border-purple-500 focus:bg-white hover:border-gray-200';

function ResultsGrid({
  rows,
  currency,
  metricUnit,
  matches,
  rowErrors,
  highlightKey,
  onChange,
  onRemove,
}: {
  rows: Row[];
  currency: string;
  metricUnit: string;
  matches: Record<string, MatchState>;
  rowErrors: Map<number, string[]>;
  highlightKey?: number;
  onChange: (key: number, patch: Partial<Row>) => void;
  onRemove: (key: number) => void;
}) {
  const cell = (r: Row, field: keyof Row, opts: { mono?: boolean; placeholder?: string; align?: 'right'; width?: string } = {}) => (
    <td className={cn('px-1 py-1', opts.width)}>
      <input
        className={cn(CELL_INPUT, opts.mono && 'font-mono', opts.align === 'right' && 'text-right')}
        value={r[field] as string}
        placeholder={opts.placeholder}
        onChange={(e) => onChange(r.key, { [field]: e.target.value })}
      />
    </td>
  );
  return (
    <div className="bg-white border border-gray-200 rounded-xl shadow-xs overflow-x-auto">
      <table className="w-full text-xs min-w-[1000px]">
        <thead className="bg-gray-50 border-b border-gray-200 text-[11px] text-gray-500">
          <tr>
            <th className="px-2 py-2 text-left font-medium w-10">#</th>
            <th className="px-2 py-2 text-left font-medium">
              model_label <span className="text-rose-600">*</span>
            </th>
            <th className="px-2 py-2 text-left font-medium">virtual_model</th>
            <th className="px-2 py-2 text-left font-medium w-44">关联</th>
            <th className="px-2 py-2 text-right font-medium w-24">
              score <span className="text-rose-600">*</span>
              <div className="font-normal text-gray-400">{metricUnit}</div>
            </th>
            <th className="px-2 py-2 text-right font-medium w-32">
              单题成本
              <div className="font-normal text-gray-400">{currency}，小数</div>
            </th>
            <th className="px-2 py-2 text-right font-medium w-24">
              平均耗时
              <div className="font-normal text-gray-400">ms</div>
            </th>
            <th className="px-2 py-2 text-right font-medium w-24">
              错误率
              <div className="font-normal text-gray-400">0–1 或 %</div>
            </th>
            <th className="px-2 py-2 text-right font-medium w-24">样本数</th>
            <th className="w-8" />
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => {
            const errs = rowErrors.get(r.key);
            const name = lookupName(r);
            const costMicro = parseCost(r.cost);
            return [
              <tr key={r.key} className={cn('border-b border-gray-100 align-top', errs && 'bg-rose-50/40', highlightKey === r.key && 'bg-rose-100/60')}>
                <td className="px-2 py-2 text-gray-400 font-mono" title={r.line ? `导入来源第 ${r.line} 行` : undefined}>
                  {i + 1}
                </td>
                {cell(r, 'model_label', { placeholder: '展示名，例如 DeepSeek V4' })}
                {cell(r, 'virtual_model', { mono: true, placeholder: name && !r.virtual_model ? '按 model_label 匹配' : '可选，平台模型名' })}
                <td className="px-2 py-2">
                  <MatchCell name={name} match={matches[name]} explicit={!!r.virtual_model.trim()} />
                </td>
                {cell(r, 'score', { mono: true, align: 'right' })}
                <td className="px-1 py-1">
                  <input
                    className={cn(CELL_INPUT, 'font-mono text-right')}
                    value={r.cost}
                    placeholder="0.0123"
                    onChange={(e) => onChange(r.key, { cost: e.target.value })}
                  />
                  {typeof costMicro === 'number' && (
                    <div className="text-[10px] text-gray-400 text-right pr-1.5 font-mono" title="提交的 cost_per_task_micro（1,000,000 = 1 单位币种）">
                      = {costMicro.toLocaleString('en-US')} micro
                    </div>
                  )}
                </td>
                {cell(r, 'avg_duration_ms', { mono: true, align: 'right' })}
                {cell(r, 'error_rate', { mono: true, align: 'right' })}
                {cell(r, 'sample_count', { mono: true, align: 'right' })}
                <td className="px-1 py-2">
                  <IconButton label="删除该行" danger onClick={() => onRemove(r.key)}>
                    <Trash2 className="w-3.5 h-3.5" />
                  </IconButton>
                </td>
              </tr>,
              errs && (
                <tr key={`${r.key}-err`} className="border-b border-gray-100 bg-rose-50/40">
                  <td />
                  <td colSpan={9} className="px-2 pb-2 text-[11px] text-rose-600">
                    {r.line && <span className="text-rose-400 mr-1">导入第 {r.line} 行：</span>}
                    {errs.join('；')}
                  </td>
                </tr>
              ),
            ];
          })}
        </tbody>
      </table>
    </div>
  );
}

function MatchCell({ name, match, explicit }: { name: string; match: MatchState | undefined; explicit: boolean }) {
  if (!name) return <span className="text-gray-300">—</span>;
  if (!match || match.state === 'pending')
    return (
      <span className="inline-flex items-center gap-1 text-gray-400">
        <Loader2 className="w-3 h-3 animate-spin" />
        匹配中
      </span>
    );
  if (match.state === 'found')
    return (
      <a href={`/models/${match.id}`} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-emerald-700 hover:underline max-w-40" title={`已关联虚拟模型 #${match.id}`}>
        <CheckCircle2 className="w-3 h-3 shrink-0" />
        <span className="font-mono truncate">{match.name}</span>
      </a>
    );
  if (match.state === 'error')
    return (
      <span className="inline-flex items-center gap-1 text-amber-700" title={match.message}>
        <XCircle className="w-3 h-3" />
        查询失败
      </span>
    );
  return explicit ? (
    <span className="inline-flex items-center gap-1 text-rose-600">
      <XCircle className="w-3 h-3" />
      不存在
    </span>
  ) : (
    <span className="inline-flex items-center gap-1 text-gray-400" title="平台没有同名虚拟模型：成绩照常录入，只是不关联模型页">
      <CircleDashed className="w-3 h-3" />
      未关联
    </span>
  );
}

// ---------- 粘贴 / 格式说明 ----------

function PasteModal({ open, onClose, onImport }: { open: boolean; onClose: () => void; onImport: (text: string) => void }) {
  const [text, setText] = useState('');
  useEffect(() => {
    if (open) setText('');
  }, [open]);
  return (
    <Modal
      open={open}
      onClose={onClose}
      width="xl"
      title="从表格粘贴"
      description="在 Excel / Google 表格中选中含表头的区域，复制后粘贴到下面（Tab 分隔）；也可以粘贴逗号分隔的 CSV 文本"
      footer={
        <>
          <Button onClick={onClose}>取消</Button>
          <Button variant="primary" disabled={!text.trim()} onClick={() => onImport(text)}>
            解析并导入
          </Button>
        </>
      }
    >
      <Textarea autoFocus mono rows={12} value={text} onChange={(e) => setText(e.target.value)} placeholder={'model_label\tvirtual_model\tscore\nDeepSeek V4\tdeepseek/deepseek-v4\t78.5'} />
      <p className="text-[11px] text-gray-400 mt-2">当前表格只有空行时替换，否则追加到末尾。来源会自动切换为「表格导入」。</p>
    </Modal>
  );
}

function FormatHelp({ currency }: { currency: string }) {
  const sign = currencySign(currency);
  return (
    <details className="mt-6 bg-gray-50 border border-gray-200 rounded-xl p-4 text-xs text-gray-600 group">
      <summary className="cursor-pointer select-none font-medium text-gray-800 inline-flex items-center gap-1.5">
        <HelpCircle className="w-3.5 h-3.5 text-gray-400" />
        粘贴 / CSV 格式说明
      </summary>
      <div className="mt-3 space-y-2">
        <p>
          第 1 行必须是表头（列名大小写不敏感、顺序任意），至少包含 <code className="font-mono">model_label</code> 与 <code className="font-mono">score</code>；未知列会被忽略。
          CSV 用逗号分隔（支持双引号包裹），从电子表格复制的内容是 Tab 分隔，均自动识别。
        </p>
        <table className="w-full text-[11px]">
          <tbody className="[&_td]:py-1 [&_td]:pr-3 [&_td]:align-top">
            <tr>
              <td className="font-mono text-gray-900">model_label</td>
              <td>必填。展示名，同一 run 内不能重复；平台未上架的模型也能参评</td>
            </tr>
            <tr>
              <td className="font-mono text-gray-900">virtual_model</td>
              <td>可选。平台虚拟模型名；留空时按 model_label 自动匹配，匹配上的行会关联模型页</td>
            </tr>
            <tr>
              <td className="font-mono text-gray-900">score</td>
              <td>必填。数字，单位与基准的指标单位一致（例如 percent 填 78.5，即 {formatScore(78.5, 'percent')}）</td>
            </tr>
            <tr>
              <td className="font-mono text-gray-900">cost_per_task_micro</td>
              <td>
                可选。单题平均成本，整数微单位（1,000,000 = 1 {currency}），例如 12300 = {sign}0.0123。也可以改用 <code className="font-mono">cost_per_task</code> 列直接填小数
              </td>
            </tr>
            <tr>
              <td className="font-mono text-gray-900">avg_duration_ms</td>
              <td>可选。平均耗时，非负整数毫秒</td>
            </tr>
            <tr>
              <td className="font-mono text-gray-900">error_rate</td>
              <td>可选。0–1 的小数（0.015），或带百分号（1.5%）</td>
            </tr>
            <tr>
              <td className="font-mono text-gray-900">sample_count</td>
              <td>可选。样本数，非负整数</td>
            </tr>
            <tr>
              <td className="font-mono text-gray-900">extra</td>
              <td>可选。JSON 对象（例如样例媒体 URL），CSV 中需用双引号包裹</td>
            </tr>
          </tbody>
        </table>
        <pre className="bg-white border border-gray-200 rounded-lg p-3 font-mono text-[11px] text-gray-700 overflow-x-auto">{TEMPLATE_CSV}</pre>
        <p>整批提交：任一行有误时整个 run 都不会保存，错误会定位到对应行。</p>
      </div>
    </details>
  );
}
