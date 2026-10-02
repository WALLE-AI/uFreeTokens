import type { FXRate, Meter, PriceUnit, Protocol, ReferencePrice, UpstreamModel } from '../../../types';

// 接入向导的状态（UI_DESIGN.md §5.1）。整个对象存 sessionStorage，刷新可恢复；
// 上游密钥明文**不**持久化（只在内存里，刷新后需要重新输入）。

export type Step = 1 | 2 | 3 | 4 | 5;

// 本平台状态：new = 虚拟模型和渠道都没有；vm_exists = 已有同名虚拟模型、
// 但这个上游账号还没有对应渠道（导入会新增一条渠道）；listed = 渠道已存在。
export type PlatformStatus = 'new' | 'vm_exists' | 'listed';

// ModelKind 是向导里的模型种类：决定导入的 type / capabilities 与计价方式
// （网关按 type + 能力决定模型服务哪个接口，见多模态技术方案 §2.1）。
export type ModelKind = 'chat' | 'embedding' | 'rerank' | 'image' | 'tts' | 'asr';

// 非对话模型只有一个计量项：costIn / sellIn 就是这个计量项的单价。
export const KIND_INFO: Record<ModelKind, { label: string; type: string; meter?: Meter; unit?: PriceUnit; unitLabel: string }> = {
  chat: { label: '对话', type: 'chat', unitLabel: '百万 token（入 / 出）' },
  embedding: { label: '嵌入', type: 'embedding', meter: 'input', unit: 'per_1m_tokens', unitLabel: '百万 token' },
  rerank: { label: '重排序', type: 'rerank', meter: 'input', unit: 'per_1m_tokens', unitLabel: '百万 token' },
  image: { label: '图像生成', type: 'image', meter: 'image', unit: 'per_image', unitLabel: '张' },
  tts: { label: '语音合成', type: 'audio', meter: 'input_char', unit: 'per_1m_chars', unitLabel: '百万字符' },
  asr: { label: '语音识别', type: 'audio', meter: 'audio_second', unit: 'per_second', unitLabel: '秒' },
};

// inferKind 按模型 ID 猜种类（运营可在定价步骤里改），覆盖常见的开源模型命名。
export function inferKind(id: string): ModelKind {
  const s = id.toLowerCase();
  if (/rerank/.test(s)) return 'rerank';
  if (/embed|bge-(m3|large|base|small)|gte-|e5-/.test(s)) return 'embedding';
  if (/cosyvoice|tts|speech|moss-ttsd|fish-speech/.test(s)) return 'tts';
  if (/sensevoice|whisper|asr|transcri|paraformer/.test(s)) return 'asr';
  if (/kolors|flux|stable-diffusion|sdxl|sd3|qwen-image|z-image|dall-e|gpt-image|seedream|cogview|hunyuan-image/.test(s)) return 'image';
  return 'chat';
}

export function inferVision(id: string): boolean {
  return /(^|[-_/])vl([-_]|$)|vision|-vl-|4\.\dv\b|omni|pixtral|llava/i.test(id);
}

export interface RowConfig {
  name: string; // 虚拟模型名（对外 model ID），默认 = 上游模型 ID
  kind: ModelKind;
  vision: boolean; // 对话模型是否可输入图片（导入为 vision 能力）
  voicePrefix: boolean; // 语音合成：短音色名补上游模型前缀（SiliconFlow 需要）
  family: string;
  contextWindow: string;
  maxOutput: string;
  costIn: string; // 成本币种（WizardState.costCurrency）/ 百万 token；非对话模型是唯一计量项的单价
  costOut: string; // 只有对话模型用
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
    presetId: string; // 选用的供应商预设（data/providerPresets.ts），空 = 自定义
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
  costCurrency: string; // 成本价币种（USD / CNY / 已配置汇率的其他币种），参考价固定是 USD
  rows: Record<string, RowConfig>;
  results: Record<string, ImportResult>;
  finished: boolean;
}

export const INITIAL_STATE: WizardState = {
  step: 1,
  provider: { mode: 'existing', existingId: '', code: '', name: '', protocol: 'openai', presetId: '', createdId: null },
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
  costCurrency: 'USD',
  rows: {},
  results: {},
  finished: false,
};

const STORAGE_KEY = 'uft_admin_provider_wizard';

// 已有供应商追加模型（/providers/:id/models/add）按供应商分开存，不和接入向导互相覆盖
export const addModelsStorageKey = (providerId: number) => `uft_admin_add_models_${providerId}`;

export function loadState(key = STORAGE_KEY, fallback: WizardState = INITIAL_STATE): WizardState {
  try {
    const raw = sessionStorage.getItem(key);
    if (!raw) return fallback;
    const parsed = JSON.parse(raw) as WizardState;
    // 进行中的导入在刷新后视为中断：running → pending，可以重新执行
    const results: Record<string, ImportResult> = {};
    for (const [k, v] of Object.entries(parsed.results ?? {})) {
      results[k] = v.state === 'running' ? { state: 'pending' } : v;
    }
    // 旧版本保存的状态没有 kind 等字段，按对话模型补齐
    const rows: Record<string, RowConfig> = {};
    for (const [k, r] of Object.entries(parsed.rows ?? {})) {
      rows[k] = { ...r, kind: r.kind ?? 'chat', vision: r.vision ?? false, voicePrefix: r.voicePrefix ?? false };
    }
    return { ...fallback, ...parsed, results, rows };
  } catch {
    return fallback;
  }
}

export function saveState(s: WizardState, key = STORAGE_KEY) {
  sessionStorage.setItem(key, JSON.stringify(s));
}

export function clearState(key = STORAGE_KEY) {
  sessionStorage.removeItem(key);
}

// seedForExisting：已有供应商追加模型的初始状态，供应商与上游账号都已确定，直接从 ③ 选择模型开始
export function seedForExisting(provider: { id: number; protocol: Protocol }, account: { id: number; cost_multiplier: string } | null): WizardState {
  return {
    ...INITIAL_STATE,
    step: 3,
    provider: { ...INITIAL_STATE.provider, mode: 'existing', existingId: String(provider.id), protocol: provider.protocol },
    account: account
      ? { ...INITIAL_STATE.account, mode: 'existing', existingId: String(account.id), existingMultiplier: account.cost_multiplier }
      : { ...INITIAL_STATE.account, mode: 'existing' },
  };
}

// 追加模式是否有未完成的工作（已经选了模型或开始导入）
export function isAppendDirty(s: WizardState): boolean {
  if (s.finished) return false;
  return s.selected.length > 0 || Object.keys(s.results).length > 0;
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

export function defaultRow(m: UpstreamModel, ref: ReferencePrice | undefined, status: PlatformStatus | undefined, providerCode = ''): RowConfig {
  const kind = inferKind(m.id);
  // 参考价只有 token 单价，只对按 token 计价的种类有意义
  const tokenRef = (kind === 'chat' || kind === 'embedding' || kind === 'rerank') && ref?.matched;
  return {
    name: m.id,
    kind,
    vision: kind === 'chat' && inferVision(m.id),
    voicePrefix: kind === 'tts' && /siliconflow/i.test(providerCode),
    family: defaultFamily(m),
    contextWindow: '128000',
    maxOutput: '8192',
    costIn: tokenRef && ref?.input ? ref.input : '',
    costOut: tokenRef && kind === 'chat' && ref?.output ? ref.output : '',
    markup: null,
    sellIn: null,
    sellOut: null,
    keepSell: status === 'vm_exists',
  };
}

// ---------- 定价计算 ----------
// computeRow 只用于第 ④ 步编辑时的即时预览（浮点近似）。真正提交的售价由服务端
// 按加价率用 decimal 计算，第 ⑤ 步先调用 import-models?dry_run 展示服务端核算结果。

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

// fx：成本币种 → CNY 的汇率（CNY 本身为 1），null = 未配置
export function computeRow(
  row: RowConfig,
  fx: number | null,
  multiplier: number,
  globalMarkup: string,
  ref: ReferencePrice | undefined,
  currency = 'USD',
): RowPricing {
  const errors: string[] = [];
  // 非对话模型只有一个计量项（costIn / sellIn），输出侧不参与
  const single = (row.kind ?? 'chat') !== 'chat';
  const cIn = pos(row.costIn);
  const cOut = single ? null : pos(row.costOut);
  const markupPct = pos(row.markup ?? globalMarkup) ?? 0;
  const conv = (c: number | null) => (c === null || fx === null ? null : c * fx * multiplier);
  const costInCNY = conv(cIn);
  const costOutCNY = conv(cOut);
  const auto = (c: number | null) => (c === null ? null : round4(c * (1 + markupPct / 100)));
  const sellIn = row.sellIn !== null ? pos(row.sellIn) : auto(costInCNY);
  const sellOut = single ? null : row.sellOut !== null ? pos(row.sellOut) : auto(costOutCNY);

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
  if (cIn === null || (!single && cOut === null)) errors.push('缺少成本价');
  if (fx === null) errors.push(`缺少 ${currency}→CNY 汇率`);
  if (!row.keepSell && (!sellIn || (!single && !sellOut))) errors.push('缺少售价');
  if (!row.keepSell && margin !== null && margin < 0) errors.push('负毛利');

  return { costInCNY, costOutCNY, sellIn, sellOut, margin, missingRef: !ref?.matched, errors };
}

// 成本币种 → CNY 汇率：CNY 为 1；其他币种取最新汇率，没有配置返回 null
export function fxToCNY(rates: FXRate[] | undefined, currency: string): number | null {
  if (currency === 'CNY') return 1;
  const r = rates?.find((x) => x.base === currency && x.quote === 'CNY');
  return r ? Number(r.rate) : null;
}
