import type { Protocol, ReferencePrice, UpstreamModel } from '../../../types';

// 接入向导的状态（UI_DESIGN.md §5.1）。整个对象存 sessionStorage，刷新可恢复；
// 上游密钥明文**不**持久化（只在内存里，刷新后需要重新输入）。

export type Step = 1 | 2 | 3 | 4 | 5;

// 本平台状态：new = 虚拟模型和渠道都没有；vm_exists = 已有同名虚拟模型、
// 但这个上游账号还没有对应渠道（导入会新增一条渠道）；listed = 渠道已存在。
export type PlatformStatus = 'new' | 'vm_exists' | 'listed';

export interface RowConfig {
  name: string; // 虚拟模型名（对外 model ID），默认 = 上游模型 ID
  family: string;
  contextWindow: string;
  maxOutput: string;
  costIn: string; // USD / 百万 token
  costOut: string;
  markup: string | null; // 单行加价率覆盖（百分比），null = 用全局值
  sellIn: string | null; // 手工覆盖售价（CNY / 百万 token），null = 自动计算
  sellOut: string | null;
  keepSell: boolean; // 已有虚拟模型时默认不覆盖其现有售价
}

export type ImportState = 'pending' | 'running' | 'ok' | 'error';

export interface ImportResult {
  state: ImportState;
  error?: string;
  vmId?: number;
  channelId?: number;
  createdVm?: boolean;
  createdChannel?: boolean;
}

export interface WizardState {
  step: Step;
  provider: {
    mode: 'existing' | 'new';
    existingId: string;
    code: string;
    name: string;
    protocol: Protocol;
    createdId: number | null; // 新建成功后锁定，重复点击不重复创建
  };
  account: {
    mode: 'existing' | 'new';
    existingId: string;
    existingMultiplier: string;
    name: string;
    baseURL: string;
    multiplier: string;
    weight: string;
    createdId: number | null;
    keyLast4: string | null; // 已添加的密钥末 4 位（防重复添加）
  };
  connection: { ok: boolean; count: number; error: string | null; tested: boolean };
  models: UpstreamModel[];
  platformStatus: Record<string, PlatformStatus>;
  statusChecked: boolean;
  refPrices: Record<string, ReferencePrice>;
  refError: string | null;
  refChecked: boolean;
  selected: string[];
  globalMarkup: string; // 百分比
  rows: Record<string, RowConfig>;
  results: Record<string, ImportResult>;
  finished: boolean;
}

export const INITIAL_STATE: WizardState = {
  step: 1,
  provider: { mode: 'existing', existingId: '', code: '', name: '', protocol: 'openai', createdId: null },
  account: {
    mode: 'new',
    existingId: '',
    existingMultiplier: '1',
    name: '',
    baseURL: '',
    multiplier: '1',
    weight: '100',
    createdId: null,
    keyLast4: null,
  },
  connection: { ok: false, count: 0, error: null, tested: false },
  models: [],
  platformStatus: {},
  statusChecked: false,
  refPrices: {},
  refError: null,
  refChecked: false,
  selected: [],
  globalMarkup: '30',
  rows: {},
  results: {},
  finished: false,
};

const STORAGE_KEY = 'uft_admin_provider_wizard';

export function loadState(): WizardState {
  try {
    const raw = sessionStorage.getItem(STORAGE_KEY);
    if (!raw) return INITIAL_STATE;
    const parsed = JSON.parse(raw) as WizardState;
    // 进行中的导入在刷新后视为中断：running → pending，可以重新执行
    const results: Record<string, ImportResult> = {};
    for (const [k, v] of Object.entries(parsed.results ?? {})) {
      results[k] = v.state === 'running' ? { state: 'pending' } : v;
    }
    return { ...INITIAL_STATE, ...parsed, results };
  } catch {
    return INITIAL_STATE;
  }
}

export function saveState(s: WizardState) {
  sessionStorage.setItem(STORAGE_KEY, JSON.stringify(s));
}

export function clearState() {
  sessionStorage.removeItem(STORAGE_KEY);
}

export function isDirty(s: WizardState): boolean {
  if (s.finished) return false;
  return s.step > 1 || s.provider.createdId !== null || !!s.provider.code || !!s.provider.name || !!s.provider.existingId;
}

export function resolvedProviderId(s: WizardState): number | null {
  if (s.provider.createdId) return s.provider.createdId;
  return s.provider.mode === 'existing' && s.provider.existingId ? Number(s.provider.existingId) : null;
}

export function resolvedAccountId(s: WizardState): number | null {
  if (s.account.mode === 'new') return s.account.createdId;
  return s.account.existingId ? Number(s.account.existingId) : null;
}

export function resolvedMultiplier(s: WizardState): number {
  const m = Number(s.account.mode === 'new' ? s.account.multiplier : s.account.existingMultiplier);
  return m > 0 ? m : 1;
}

// family 默认值：owned_by（上游汇报的归属）或模型名第一个词，与后端 suggestFamily 口径接近
export function defaultFamily(m: UpstreamModel): string {
  if (m.owned_by && !/^(system|openai-internal|organization|user)$/i.test(m.owned_by)) return m.owned_by.toLowerCase();
  const last = m.id.split('/').pop() ?? m.id;
  return (last.toLowerCase().split(/[-_.:\s]/)[0] || 'unknown').slice(0, 32);
}

export function defaultRow(m: UpstreamModel, ref: ReferencePrice | undefined, status: PlatformStatus | undefined): RowConfig {
  return {
    name: m.id,
    family: defaultFamily(m),
    contextWindow: '128000',
    maxOutput: '8192',
    costIn: ref?.matched && ref.input ? ref.input : '',
    costOut: ref?.matched && ref.output ? ref.output : '',
    markup: null,
    sellIn: null,
    sellOut: null,
    keepSell: status === 'vm_exists',
  };
}

// ---------- 定价计算 ----------

export interface RowPricing {
  costInCNY: number | null;
  costOutCNY: number | null;
  sellIn: number | null;
  sellOut: number | null;
  margin: number | null; // min(1 - 成本/售价)，保守口径
  missingRef: boolean;
  errors: string[];
}

function pos(v: string | null | undefined): number | null {
  if (v === null || v === undefined || v.trim() === '') return null;
  const n = Number(v);
  return Number.isFinite(n) && n >= 0 ? n : null;
}

export function round4(n: number): number {
  return Math.round(n * 10000) / 10000;
}

export function computeRow(row: RowConfig, fx: number | null, multiplier: number, globalMarkup: string, ref: ReferencePrice | undefined): RowPricing {
  const errors: string[] = [];
  const cIn = pos(row.costIn);
  const cOut = pos(row.costOut);
  const markupPct = pos(row.markup ?? globalMarkup) ?? 0;
  const conv = (c: number | null) => (c === null || fx === null ? null : c * fx * multiplier);
  const costInCNY = conv(cIn);
  const costOutCNY = conv(cOut);
  const auto = (c: number | null) => (c === null ? null : round4(c * (1 + markupPct / 100)));
  const sellIn = row.sellIn !== null ? pos(row.sellIn) : auto(costInCNY);
  const sellOut = row.sellOut !== null ? pos(row.sellOut) : auto(costOutCNY);

  let margin: number | null = null;
  const check = (s: number | null, c: number | null) => {
    if (s === null || c === null || s === 0) return;
    const m = 1 - c / s;
    if (margin === null || m < margin) margin = m;
  };
  check(sellIn, costInCNY);
  check(sellOut, costOutCNY);

  if (!row.name.trim()) errors.push('缺少模型名');
  if (!row.family.trim()) errors.push('缺少 family');
  if (!(Number(row.contextWindow) > 0)) errors.push('上下文窗口无效');
  if (!(Number(row.maxOutput) > 0)) errors.push('最大输出无效');
  if (cIn === null || cOut === null) errors.push('缺少成本价');
  if (fx === null) errors.push('缺少 USD→CNY 汇率');
  if (!row.keepSell && (!sellIn || !sellOut)) errors.push('缺少售价');
  if (!row.keepSell && margin !== null && margin < 0) errors.push('负毛利');

  return { costInCNY, costOutCNY, sellIn, sellOut, margin, missingRef: !ref?.matched, errors };
}

// 以有限并发执行一批异步任务（检查本平台状态时避免一次打出上百个请求）
export async function mapLimit<T>(items: T[], limit: number, fn: (item: T) => Promise<void>, signal?: AbortSignal) {
  let i = 0;
  const workers = Array.from({ length: Math.min(limit, items.length) }, async () => {
    while (i < items.length && !signal?.aborted) {
      const item = items[i++];
      await fn(item);
    }
  });
  await Promise.all(workers);
}
