import { request } from './client';

// 基准测试的分类，和后端 benchmarks.category 的 CHECK 约束一致
// （技术方案 §3.2；外部榜单导入后新增 general / coding / chinese / embedding）。
export type BenchmarkCategory =
  | 'general'
  | 'coding'
  | 'agents'
  | 'reasoning'
  | 'chinese'
  | 'search'
  | 'media'
  | 'artifacts'
  | 'embedding';

export const BENCHMARK_CATEGORIES: BenchmarkCategory[] = [
  'general',
  'coding',
  'agents',
  'reasoning',
  'chinese',
  'search',
  'media',
  'artifacts',
  'embedding',
];

// 分类的中文短名，基准测试页的分类标签和模型详情页的「评测成绩」分组共用。
export const BENCHMARK_CATEGORY_LABELS: Record<BenchmarkCategory, string> = {
  general: '综合',
  coding: '编程',
  agents: '智能体',
  reasoning: '推理',
  chinese: '中文',
  search: '搜索',
  media: '多模态',
  artifacts: '生成/创作',
  embedding: '向量',
};

// formatBenchmarkScore 按 metric_unit 格式化分数：elo 取整并带 "Elo"，
// percent 保留 1 位小数加 %，index / score 保留 1 位小数；其它未知单位原样
// 带上单位文字。
export function formatBenchmarkScore(score: number, unit: string): string {
  if (!Number.isFinite(score)) return '—';
  switch (unit) {
    case 'elo':
      return `${Math.round(score).toLocaleString('en-US')} Elo`;
    case 'percent':
      return `${score.toFixed(1)}%`;
    case 'index':
    case 'score':
    case '':
      return score.toFixed(1);
    default: {
      const text = Number.isInteger(score) ? score.toLocaleString('en-US') : score.toLocaleString('en-US', { maximumFractionDigits: 2 });
      return `${text} ${unit}`;
    }
  }
}

// origin 标注一次评测的来源：self_eval 是平台用自家网关跑出来的实测数据，
// manual / import 是运营录入的外部公开结果（技术方案 §4 第 5 点）。
export type BenchmarkOrigin = 'manual' | 'import' | 'self_eval';

// BenchmarkRun 是该基准最近一次已发布的评测批次。成本金额的币种以
// costCurrency 为准（USD 或 CNY），同一批次内的所有成本都是这个币种。
export interface BenchmarkRun {
  id: number;
  origin: BenchmarkOrigin;
  runAt: string;
  costCurrency: string;
  notes: string;
}

// BenchmarkChampion 是接口层按评测结果现算的单项冠军，不单独落库。
// model 是平台公开目录里的模型名（即 /models/<id> 的 id），参评模型没在
// 平台上架时为 null，只能展示 modelLabel。
export interface BenchmarkChampion {
  modelLabel: string;
  model: string | null;
  score: number;
  costPerTaskMicro: number | null;
  avgDurationMs: number | null;
}

export interface BenchmarkChampions {
  quality: BenchmarkChampion | null;
  value: BenchmarkChampion | null;
  speed: BenchmarkChampion | null;
}

// BenchmarkSummary 是 GET /v1/benchmarks 列表里的一项。sourceName/sourceUrl
// 是外部数据来源（sourceName 即需要展示的署名文字），自建评测时为 null；
// license 是外部数据的许可协议（如 CC-BY-4.0）；run 为 null 表示该基准还没有
// 已发布的评测批次。
export interface BenchmarkSummary {
  slug: string;
  name: string;
  category: BenchmarkCategory;
  description: string;
  metricName: string;
  metricUnit: string;
  higherIsBetter: boolean;
  sourceName: string | null;
  sourceUrl: string | null;
  license: string | null;
  run: BenchmarkRun | null;
  modelsCount: number;
  champions: BenchmarkChampions;
}

export interface BenchmarkResult {
  rank: number;
  modelLabel: string;
  model: string | null;
  score: number;
  costPerTaskMicro: number | null;
  avgDurationMs: number | null;
  errorRate: number | null;
  sampleCount: number | null;
  extra: Record<string, unknown> | null;
}

export interface BenchmarkDetail extends BenchmarkSummary {
  results: BenchmarkResult[];
}

interface RawBenchmarkRun {
  id: number;
  origin: BenchmarkOrigin;
  run_at: string;
  cost_currency: string;
  notes?: string;
}

interface RawBenchmarkChampion {
  model_label: string;
  model?: string | null;
  score: number;
  cost_per_task_micro?: number | null;
  avg_duration_ms?: number | null;
}

interface RawBenchmarkSummary {
  slug: string;
  name: string;
  category: BenchmarkCategory;
  description?: string;
  metric_name: string;
  metric_unit: string;
  higher_is_better: boolean;
  source_name?: string | null;
  source_url?: string | null;
  license?: string | null;
  run?: RawBenchmarkRun | null;
  models_count: number;
  champions?: {
    quality?: RawBenchmarkChampion | null;
    value?: RawBenchmarkChampion | null;
    speed?: RawBenchmarkChampion | null;
  } | null;
}

interface RawBenchmarkResult {
  rank: number;
  model_label: string;
  model?: string | null;
  score: number;
  cost_per_task_micro?: number | null;
  avg_duration_ms?: number | null;
  error_rate?: number | null;
  sample_count?: number | null;
  extra?: Record<string, unknown> | null;
}

interface RawBenchmarkDetail extends RawBenchmarkSummary {
  results?: RawBenchmarkResult[];
}

function mapChampion(raw: RawBenchmarkChampion | null | undefined): BenchmarkChampion | null {
  if (!raw) return null;
  return {
    modelLabel: raw.model_label,
    model: raw.model ?? null,
    score: raw.score,
    costPerTaskMicro: raw.cost_per_task_micro ?? null,
    avgDurationMs: raw.avg_duration_ms ?? null,
  };
}

function mapSummary(raw: RawBenchmarkSummary): BenchmarkSummary {
  return {
    slug: raw.slug,
    name: raw.name,
    category: raw.category,
    description: raw.description ?? '',
    metricName: raw.metric_name,
    metricUnit: raw.metric_unit,
    higherIsBetter: raw.higher_is_better,
    // 空字符串和 null 一样当作"没有外部来源"处理，避免渲染出空链接。
    sourceName: raw.source_name || null,
    sourceUrl: raw.source_url || null,
    license: raw.license || null,
    run: raw.run
      ? {
          id: raw.run.id,
          origin: raw.run.origin,
          runAt: raw.run.run_at,
          costCurrency: raw.run.cost_currency,
          notes: raw.run.notes ?? '',
        }
      : null,
    modelsCount: raw.models_count,
    champions: {
      quality: mapChampion(raw.champions?.quality),
      value: mapChampion(raw.champions?.value),
      speed: mapChampion(raw.champions?.speed),
    },
  };
}

function mapResult(raw: RawBenchmarkResult): BenchmarkResult {
  return {
    rank: raw.rank,
    modelLabel: raw.model_label,
    model: raw.model ?? null,
    score: raw.score,
    costPerTaskMicro: raw.cost_per_task_micro ?? null,
    avgDurationMs: raw.avg_duration_ms ?? null,
    errorRate: raw.error_rate ?? null,
    sampleCount: raw.sample_count ?? null,
    extra: raw.extra ?? null,
  };
}

// listBenchmarks 调用 GET /v1/benchmarks——公开接口，带不带 API Key 返回相同
// 内容（技术方案 §3.4），只列出已发布的基准。category 省略时返回全部分类。
export async function listBenchmarks(category?: BenchmarkCategory, signal?: AbortSignal): Promise<BenchmarkSummary[]> {
  const qs = category ? `?category=${encodeURIComponent(category)}` : '';
  const raw = await request<{ object: string; data: RawBenchmarkSummary[] }>(`/v1/benchmarks${qs}`, { signal });
  return (raw.data ?? []).map(mapSummary);
}

// getBenchmark 调用 GET /v1/benchmarks/{slug}，返回最新已发布批次的完整排行榜
// （results 已按 rank 排好序）。slug 不存在或未发布时后端返回
// 404 not_found，由调用方按 ApiError.status 区分展示。
export async function getBenchmark(slug: string, signal?: AbortSignal): Promise<BenchmarkDetail> {
  const raw = await request<RawBenchmarkDetail>(`/v1/benchmarks/${encodeURIComponent(slug)}`, { signal });
  return {
    ...mapSummary(raw),
    results: (raw.results ?? []).map(mapResult),
  };
}

// ModelBenchmarkResult 是 GET /v1/model-benchmarks 里的一项：某个模型在一个
// 已发布基准上的成绩（同一模型多个变体参评时只取最好的一个）。
export interface ModelBenchmarkResult {
  slug: string;
  name: string;
  category: BenchmarkCategory;
  metricName: string;
  metricUnit: string;
  higherIsBetter: boolean;
  sourceName: string | null;
  sourceUrl: string | null;
  license: string | null;
  runAt: string | null;
  modelLabel: string;
  score: number;
  rank: number | null;
  modelsCount: number | null;
  extra: Record<string, unknown> | null;
}

interface RawModelBenchmarkResult {
  slug: string;
  name: string;
  category: BenchmarkCategory;
  metric_name: string;
  metric_unit: string;
  higher_is_better: boolean;
  source_name?: string | null;
  source_url?: string | null;
  license?: string | null;
  run_at?: string | null;
  model_label: string;
  score: number;
  rank?: number | null;
  models_count?: number | null;
  extra?: Record<string, unknown> | null;
}

// getModelBenchmarks 调用 GET /v1/model-benchmarks?model=<id>——公开接口，
// model 是 /v1/catalog 里的模型名；没有任何成绩时返回空数组。
export async function getModelBenchmarks(model: string, signal?: AbortSignal): Promise<ModelBenchmarkResult[]> {
  const raw = await request<{ object: string; model: string; data: RawModelBenchmarkResult[] }>(
    `/v1/model-benchmarks?model=${encodeURIComponent(model)}`,
    { signal },
  );
  return (raw.data ?? []).map((r) => ({
    slug: r.slug,
    name: r.name,
    category: r.category,
    metricName: r.metric_name,
    metricUnit: r.metric_unit,
    higherIsBetter: r.higher_is_better,
    sourceName: r.source_name || null,
    sourceUrl: r.source_url || null,
    license: r.license || null,
    runAt: r.run_at || null,
    modelLabel: r.model_label,
    score: r.score,
    rank: r.rank ?? null,
    modelsCount: r.models_count ?? null,
    extra: r.extra ?? null,
  }));
}
