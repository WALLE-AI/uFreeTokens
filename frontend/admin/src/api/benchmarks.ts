import { request } from './client';
import type {
  Benchmark,
  BenchmarkCategory,
  BenchmarkCostCurrency,
  BenchmarkDetail,
  BenchmarkOrigin,
  BenchmarkResultInput,
  BenchmarkRunDetail,
  BenchmarkStatus,
  BenchmarkSummary,
  ListData,
  ModelAlias,
  Paginated,
  SetModelAliasResult,
} from '../types';
import type { PageQuery } from './catalog';

// 基准测试运营接口（技术方案 §3.5，docs/admin-api.md 路由表）：读 catalog:read，写 catalog:write。
// 一对一薄封装，不做缓存。

export function listBenchmarks(q: { category?: string; status?: string } = {}, signal?: AbortSignal) {
  return request<ListData<BenchmarkSummary>>('/benchmarks', { query: { ...q }, signal });
}

export interface CreateBenchmarkBody {
  slug: string; // 小写字母/数字，单个连字符分隔，例如 gpqa-diamond；创建后不可修改
  name: string;
  category: BenchmarkCategory;
  description?: string;
  metric_name: string;
  metric_unit?: string; // 空 = percent
  higher_is_better?: boolean;
  source_name?: string | null;
  source_url?: string | null;
  status?: BenchmarkStatus;
  sort_order?: number;
  score_key?: string | null;
}

export function createBenchmark(body: CreateBenchmarkBody) {
  return request<Benchmark>('/benchmarks', { method: 'POST', body });
}

// 详情响应带 ETag，之后对同一路径的 PATCH 由 client 自动带 If-Match
export function getBenchmark(id: number, signal?: AbortSignal) {
  return request<BenchmarkDetail>(`/benchmarks/${id}`, { signal });
}

// 修改 / 发布（status=published）/ 归档（status=archived）。source_name、source_url 传空字符串表示清除。
export function updateBenchmark(
  id: number,
  body: Partial<Pick<Benchmark, 'name' | 'category' | 'description' | 'metric_name' | 'metric_unit' | 'higher_is_better' | 'status' | 'sort_order'>> & {
    source_name?: string;
    source_url?: string;
    score_key?: string; // GET /meta/enums 的 score_keys 之一；"" 清除
  },
) {
  return request<Benchmark>(`/benchmarks/${id}`, { method: 'PATCH', body });
}

export interface CreateBenchmarkRunBody {
  origin: Extract<BenchmarkOrigin, 'manual' | 'import'>;
  run_at: string; // RFC3339
  notes: string;
  cost_currency: BenchmarkCostCurrency;
  publish: boolean;
  results: BenchmarkResultInput[];
}

// 整批写入（全部成功或全部失败）；400 的错误信息形如 "results[3].score is required"
export function createBenchmarkRun(benchmarkId: number, body: CreateBenchmarkRunBody) {
  return request<BenchmarkRunDetail>(`/benchmarks/${benchmarkId}/runs`, { method: 'POST', body, timeoutMs: 60_000 });
}

export function getBenchmarkRun(runId: number, signal?: AbortSignal) {
  return request<BenchmarkRunDetail>(`/benchmark-runs/${runId}`, { signal });
}

// 发布后，同一基准原先已发布的 run 自动退为历史
export function publishBenchmarkRun(runId: number) {
  return request<BenchmarkRunDetail>(`/benchmark-runs/${runId}/publish`, { method: 'POST' });
}

// 只能删除从未发布过的 run（published_at 为空），否则 409
export function deleteBenchmarkRun(runId: number) {
  return request<void>(`/benchmark-runs/${runId}`, { method: 'DELETE' });
}

// ---------- 榜单模型映射（外部评测榜单的模型名 → 虚拟模型） ----------
// 读 catalog:read，写 catalog:write。

export function listModelAliases(q: PageQuery & { namespace?: string; status?: string; q?: string } = {}, signal?: AbortSignal) {
  return request<Paginated<ModelAlias>>('/model-aliases', { query: { ...q }, signal });
}

export function listModelAliasNamespaces(signal?: AbortSignal) {
  return request<ListData<string>>('/model-aliases/namespaces', { signal });
}

export interface SetModelAliasBody {
  namespace: string;
  external_label: string;
  status: 'confirmed' | 'ignored' | 'auto'; // auto = 交回自动匹配（下次导入重新匹配）
  virtual_model_id?: number; // confirmed 时二选一
  virtual_model?: string;
}

// 保存后服务端立即把已导入的榜单成绩重新关联，并重新投影已发布 run 的 scores
export function setModelAlias(body: SetModelAliasBody) {
  return request<SetModelAliasResult>('/model-aliases', { method: 'PUT', body });
}
