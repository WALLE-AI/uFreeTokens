import type { BenchmarkCategory, BenchmarkCostCurrency, BenchmarkOrigin, BenchmarkRun, BenchmarkStatus } from '../../types';
import { microToYuan } from '../../lib/money';

// 基准测试页面共用的选项、标签与格式化（仅 src/pages/catalog 的基准测试页面使用）。

export const BENCHMARK_CATEGORY_OPTIONS: Array<{ value: BenchmarkCategory; label: string }> = [
  { value: 'general', label: 'General 综合' },
  { value: 'coding', label: 'Coding 编程' },
  { value: 'agents', label: 'Agents 智能体' },
  { value: 'reasoning', label: 'Reasoning 逻辑推演' },
  { value: 'chinese', label: 'Chinese 中文' },
  { value: 'search', label: 'Search 联网检索' },
  { value: 'media', label: 'Media 多媒体' },
  { value: 'artifacts', label: 'Artifacts 工件生成' },
  { value: 'embedding', label: 'Embedding 向量' },
];

// virtual_model_metadata.scores 的键（后端白名单 internal/admin/scores.go）。基准设置了 score_key 时，
// 发布 run 会把各模型成绩投影进对应键（来源许可不允许公开展示的除外）。
export const SCORE_KEY_LABELS: Record<string, string> = {
  intelligence_index: '智能指数',
  coding_index: '编程指数',
  agentic_index: 'Agent 指数',
  arena_text: 'Arena 文本',
  arena_chinese: 'Arena 中文',
  arena_coding: 'Arena 编程',
  arena_webdev: 'Arena WebDev',
  arena_vision: 'Arena 视觉',
  gpqa_diamond: 'GPQA Diamond',
  swe_bench_verified: 'SWE-bench Verified',
  hle: "Humanity's Last Exam",
  terminal_bench: 'Terminal-Bench',
  aider_polyglot: 'Aider Polyglot',
  arc_agi_2: 'ARC-AGI-2',
  livebench: 'LiveBench',
  epoch_eci: 'Epoch ECI',
  opencompass: 'OpenCompass',
  superclue: 'SuperCLUE',
};

export function scoreKeyLabel(k: string | null | undefined): string {
  if (!k) return '';
  return SCORE_KEY_LABELS[k] ? `${SCORE_KEY_LABELS[k]}（${k}）` : k;
}

export const BENCHMARK_STATUS_OPTIONS: Array<{ value: BenchmarkStatus; label: string }> = [
  { value: 'draft', label: '草稿' },
  { value: 'published', label: '已发布' },
  { value: 'archived', label: '已归档' },
];

export const BENCHMARK_ORIGIN_LABELS: Record<BenchmarkOrigin, string> = {
  manual: '手工录入',
  import: '表格导入',
  self_eval: '自建评测',
};

export const COST_CURRENCY_OPTIONS: Array<{ value: BenchmarkCostCurrency; label: string }> = [
  { value: 'USD', label: 'USD 美元' },
  { value: 'CNY', label: 'CNY 人民币' },
];

// 常见的指标单位；也可以自由输入
export const METRIC_UNIT_SUGGESTIONS = ['percent', 'score', 'elo', 'ms', 'tokens/s'];

export function categoryLabel(c: string): string {
  return BENCHMARK_CATEGORY_OPTIONS.find((o) => o.value === c)?.label ?? c;
}

// run 的展示状态：published=当前发布；曾发布（published_at 非空）但已被取代=历史；否则草稿
export type RunState = 'published' | 'history' | 'draft';
export function runState(r: Pick<BenchmarkRun, 'published' | 'published_at'>): RunState {
  if (r.published) return 'published';
  return r.published_at ? 'history' : 'draft';
}

// 成绩显示：percent 单位加 %，其他单位跟在数字后面
export function formatScore(score: number, unit: string): string {
  const n = score.toLocaleString('en-US', { maximumFractionDigits: 4 });
  if (unit === 'percent') return `${n}%`;
  return unit ? `${n} ${unit}` : n;
}

export function currencySign(c: string): string {
  return c === 'USD' ? '$' : c === 'CNY' ? '¥' : `${c} `;
}

// 单题成本（微单位，1,000,000 = 1 USD/CNY）→ "$0.0123"
export function formatCostMicro(micro: number | null | undefined, currency: string): string {
  if (micro === null || micro === undefined) return '—';
  return `${currencySign(currency)}${microToYuan(micro)}`;
}

// 微单位 → 输入框里的小数文本（不补 0、不加千分位），例如 12300 → "0.0123"
export function microToDecimalText(micro: number): string {
  const neg = micro < 0;
  const abs = BigInt(Math.abs(Math.trunc(micro)));
  const intPart = (abs / 1_000_000n).toString();
  const frac = (abs % 1_000_000n).toString().padStart(6, '0').replace(/0+$/, '');
  return `${neg ? '-' : ''}${intPart}${frac ? `.${frac}` : ''}`;
}

export function formatDurationMs(ms: number | null | undefined): string {
  if (ms === null || ms === undefined) return '—';
  if (ms >= 10_000) return `${(ms / 1000).toFixed(1)} s`;
  return `${ms.toLocaleString('en-US')} ms`;
}

export function formatErrorRate(r: number | null | undefined): string {
  if (r === null || r === undefined) return '—';
  return `${(r * 100).toLocaleString('en-US', { maximumFractionDigits: 2 })}%`;
}
