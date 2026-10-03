import { useEffect, useState } from 'react';
import { ChevronDown, ChevronRight, Info, Loader2, Sparkles, Wand2 } from 'lucide-react';
import { getVirtualModelMetadataSuggestion, setVirtualModelMetadata } from '../../api/catalog';
import { ApiError, errorMessage } from '../../api/errors';
import { Button, Field, Input, Textarea, useToast } from '../../components/ui';
import type { DesignArenaKey, MetadataSuggestion, ModelScores, ScoreKey, VirtualModelDetail } from '../../types';
import { TagInput, formatContext, formatPrice } from './shared';
import { Can } from '../../components/ui/Can';
import { useEnums } from '../../hooks/useEnums';
import { SCORE_KEY_LABELS } from './benchmarkShared';

// 展示元数据编辑（UI_DESIGN.md §5.4 第 5 点）：左侧表单，右侧实时预览 web 模型库卡片。
// scores 用数字输入框而不是自由 JSON，强制遵守后端白名单的 snake_case 键名
// （internal/admin/scores.go，技术方案 §3.1）：intelligence_index / coding_index /
// agentic_index，以及嵌套的 design_arena.{code, ui_component, …}；其他键 PUT 时返回 400。
// 读取时兼容旧数据的 camelCase 键（intelligenceIndex、designArena.codeCategories…），
// 保存时一律写 snake_case。
// 「自动填充」按 GET /virtual-models/{id}/metadata/suggestion 的建议值只填草稿里的空字段
// （名称、厂商、介绍、标签），不碰评分、不覆盖已填内容，保存仍由运营确认。
// 「AI 生成」用 ?llm=1 让服务端 LLM 写一段中文介绍，替换草稿里的介绍（可点还原撤销）。
// 榜单单项分数（arena_text、gpqa_diamond…）由评测榜单导入发布时按 benchmarks.score_key
// 自动投影写入；PUT 是整体替换，所以这里必须列出全部白名单键，否则保存会把它们清掉。

// 综合指数（模型库卡片上展示）
const INDEX_FIELDS: Array<{ key: ScoreKey; label: string }> = [
  { key: 'intelligence_index', label: '智能指数' },
  { key: 'coding_index', label: '编程指数' },
  { key: 'agentic_index', label: 'Agent 指数' },
];

// 榜单单项分数（外部评测榜单投影）
const BOARD_SCORE_KEYS: ScoreKey[] = [
  'arena_text',
  'arena_chinese',
  'arena_coding',
  'arena_webdev',
  'arena_vision',
  'gpqa_diamond',
  'swe_bench_verified',
  'hle',
  'terminal_bench',
  'aider_polyglot',
  'arc_agi_2',
  'livebench',
  'epoch_eci',
  'opencompass',
  'superclue',
];
const BOARD_SCORE_FIELDS = BOARD_SCORE_KEYS.map((key) => ({ key, label: SCORE_KEY_LABELS[key] ?? key }));

const SCORE_FIELDS = [...INDEX_FIELDS, ...BOARD_SCORE_FIELDS];
const KNOWN_SCORE_KEYS = new Set<string>([...SCORE_FIELDS.map((f) => f.key), 'design_arena']);

const DESIGN_ARENA_FIELDS: Array<{ key: DesignArenaKey; label: string }> = [
  { key: 'code', label: '代码' },
  { key: 'ui_component', label: 'UI 组件' },
  { key: 'game_dev', label: '游戏开发' },
  { key: 'data_viz', label: '数据可视化' },
  { key: 'three_d', label: '3D' },
  { key: 'image', label: '图像' },
  { key: 'video', label: '视频' },
  { key: 'svg', label: 'SVG' },
];

// 旧数据的 camelCase 键（过渡期兼容，只读不写）
const LEGACY_SCORE_KEYS: Partial<Record<ScoreKey, string>> = {
  intelligence_index: 'intelligenceIndex',
  coding_index: 'codingIndex',
  agentic_index: 'agenticIndex',
};
const LEGACY_DESIGN_ARENA_KEYS: Record<DesignArenaKey, string> = {
  code: 'codeCategories',
  ui_component: 'uiComponent',
  game_dev: 'gameDev',
  data_viz: 'dataViz',
  three_d: 'threeD',
  image: 'image',
  video: 'video',
  svg: 'svg',
};

interface Draft {
  display_name: string;
  description: string;
  provider_display: string;
  tags: string[];
  scores: Record<ScoreKey, string>;
  design_arena: Record<DesignArenaKey, string>;
}

function asRecord(v: unknown): Record<string, unknown> {
  return v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : {};
}

// 优先取 snake_case 键，没有再取旧的 camelCase 键；非数字一律视为空
function pickNumber(obj: Record<string, unknown>, key: string, legacy?: string): string {
  const v = obj[key] ?? (legacy ? obj[legacy] : undefined);
  return typeof v === 'number' && Number.isFinite(v) ? v.toString() : '';
}

function fromDetail(d: VirtualModelDetail): Draft {
  const md = d.metadata;
  const s = asRecord(md?.scores);
  const da = asRecord(s.design_arena ?? s.designArena);
  return {
    display_name: md?.display_name ?? '',
    description: md?.description ?? '',
    provider_display: md?.provider_display ?? '',
    tags: md?.tags ?? [],
    scores: Object.fromEntries(SCORE_FIELDS.map(({ key }) => [key, pickNumber(s, key, LEGACY_SCORE_KEYS[key])])) as Record<ScoreKey, string>,
    design_arena: Object.fromEntries(
      DESIGN_ARENA_FIELDS.map(({ key }) => [key, pickNumber(da, key, LEGACY_DESIGN_ARENA_KEYS[key])]),
    ) as Record<DesignArenaKey, string>,
  };
}

const isBadNumber = (v: string) => v.trim() !== '' && !Number.isFinite(Number(v));

type FillableField = 'display_name' | 'provider_display' | 'description' | 'tags';

// 建议值来源的展示文案
function sourceLabel(source?: string, detail?: string): string {
  switch (source) {
    case 'external':
      return detail?.startsWith('openrouter') ? '来自 OpenRouter' : detail?.startsWith('models_dev') ? '来自 models.dev' : '来自外部目录';
    case 'vendor':
      return '来自内置厂商表';
    case 'llm':
      return detail ? `AI 生成（${detail}），请核对` : 'AI 生成，请核对';
    default:
      return '由模型名 / 能力推导';
  }
}

// 把建议值填进草稿的空字段，返回新草稿与每个被填字段的来源说明
function applySuggestion(draft: Draft, sug: MetadataSuggestion): { draft: Draft; filled: Partial<Record<FillableField, string>> } {
  const next = { ...draft };
  const filled: Partial<Record<FillableField, string>> = {};
  for (const key of ['display_name', 'provider_display', 'description'] as const) {
    const s = sug[key];
    if (draft[key].trim() === '' && s.value) {
      next[key] = s.value;
      filled[key] = sourceLabel(s.source, s.detail);
    }
  }
  const tags = sug.tags.value ?? [];
  if (draft.tags.length === 0 && tags.length > 0) {
    next.tags = tags;
    filled.tags = sourceLabel(sug.tags.source);
  }
  return { draft: next, filled };
}

// 后端白名单里有、但本页面还不认识的数值键（GET /meta/enums 的 score_keys 比前端新时）：原样保留，
// 避免整体替换时被清掉。
function passthroughScores(md: VirtualModelDetail['metadata'], allowed: string[] | undefined): Record<string, number> {
  const s = asRecord(md?.scores);
  const out: Record<string, number> = {};
  for (const k of allowed ?? []) {
    if (KNOWN_SCORE_KEYS.has(k)) continue;
    const v = s[k];
    if (typeof v === 'number' && Number.isFinite(v)) out[k] = v;
  }
  return out;
}

// 草稿 → 提交的 scores：空值省略；design_arena 全空时整个省略；全部为空返回 null（清空评分）
function toScores(draft: Draft, passthrough: Record<string, number>): ModelScores | null {
  const scores: ModelScores = { ...(passthrough as ModelScores) };
  for (const { key } of SCORE_FIELDS) {
    const v = draft.scores[key].trim();
    if (v !== '') scores[key] = Number(v);
  }
  const da: NonNullable<ModelScores['design_arena']> = {};
  for (const { key } of DESIGN_ARENA_FIELDS) {
    const v = draft.design_arena[key].trim();
    if (v !== '') da[key] = Number(v);
  }
  if (Object.keys(da).length > 0) scores.design_arena = da;
  return Object.keys(scores).length > 0 ? scores : null;
}

export function ModelMetadataSection({ model, onSaved }: { model: VirtualModelDetail; onSaved: () => void }) {
  const toast = useToast();
  const [draft, setDraft] = useState<Draft>(() => fromDetail(model));
  const [saving, setSaving] = useState(false);
  const [suggesting, setSuggesting] = useState(false);
  const [generating, setGenerating] = useState(false);
  // 服务端是否配置了 LLM（建议接口的 llm_available）；未知时按可用处理，点了再看结果
  const [llmAvailable, setLlmAvailable] = useState<boolean | undefined>(undefined);
  useEffect(() => {
    const ctrl = new AbortController();
    getVirtualModelMetadataSuggestion(model.id, { signal: ctrl.signal })
      .then((s) => setLlmAvailable(s.llm_available))
      .catch(() => undefined);
    return () => ctrl.abort();
  }, [model.id]);
  const [filled, setFilled] = useState<Partial<Record<FillableField, string>>>({});
  const enums = useEnums();
  useEffect(() => {
    setDraft(fromDetail(model));
    setFilled({});
  }, [model]);
  const boardFilled = BOARD_SCORE_FIELDS.filter(({ key }) => draft.scores[key].trim() !== '').length;
  const [showBoard, setShowBoard] = useState(false);

  // 出错的字段：顶层用 key，Design Arena 用 design_arena.<key>
  const scoreErrors = new Set<string>([
    ...SCORE_FIELDS.filter(({ key }) => isBadNumber(draft.scores[key])).map(({ key }) => key),
    ...DESIGN_ARENA_FIELDS.filter(({ key }) => isBadNumber(draft.design_arena[key])).map(({ key }) => `design_arena.${key}`),
  ]);
  const dirty = JSON.stringify(draft) !== JSON.stringify(fromDetail(model));

  const autofill = async () => {
    setSuggesting(true);
    try {
      const sug = await getVirtualModelMetadataSuggestion(model.id);
      const res = applySuggestion(draft, sug);
      const n = Object.keys(res.filled).length;
      if (n === 0) {
        toast.info('没有可自动填充的空字段（已填写的内容不会被覆盖，如需替换请先清空该字段）');
        return;
      }
      setDraft(res.draft);
      setFilled({ ...filled, ...res.filled });
      toast.success(`已自动填充 ${n} 项，请核对后保存`);
    } catch (err) {
      toast.error('获取建议值失败', errorMessage(err));
    } finally {
      setSuggesting(false);
    }
  };
  const generateDescription = async () => {
    setGenerating(true);
    try {
      const sug = await getVirtualModelMetadataSuggestion(model.id, { llm: true });
      const d = sug.description;
      if (!d.value) {
        toast.info('没有生成出可用的介绍');
        return;
      }
      const replaced = draft.description.trim() !== '' && draft.description.trim() !== d.value;
      setDraft({ ...draft, description: d.value });
      setFilled({ ...filled, description: sourceLabel(d.source, d.detail) });
      toast.success(replaced ? '已生成介绍并替换草稿中的原文（未保存，可点还原撤销）' : '已生成介绍，请核对后保存');
    } catch (err) {
      if (err instanceof ApiError && err.code === 'llm_not_configured') {
        setLlmAvailable(false);
        toast.info('服务端未配置 LLM（配置段 datasync.llm_*），无法生成介绍');
      } else {
        toast.error('生成介绍失败', errorMessage(err));
      }
    } finally {
      setGenerating(false);
    }
  };
  // 被自动填充、尚未改动过的字段在提示里标出来源；运营一改就去掉
  const filledHint = (key: FillableField, fallback?: string) =>
    filled[key] ? <span className="text-purple-600">已自动填充 · {filled[key]}</span> : fallback;
  const edit = <K extends keyof Draft>(key: K, value: Draft[K]) => {
    setDraft({ ...draft, [key]: value });
    if (key in filled) {
      const rest = { ...filled };
      delete rest[key as FillableField];
      setFilled(rest);
    }
  };

  const save = async () => {
    setSaving(true);
    try {
      await setVirtualModelMetadata(model.id, {
        display_name: draft.display_name.trim(),
        description: draft.description.trim(),
        provider_display: draft.provider_display.trim(),
        tags: draft.tags,
        scores: toScores(draft, passthroughScores(model.metadata, enums?.score_keys)),
      });
      toast.success('展示元数据已保存');
      setFilled({});
      onSaved();
    } catch (err) {
      toast.error('保存失败', errorMessage(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="grid grid-cols-1 lg:grid-cols-5 gap-6">
      <div className="lg:col-span-3 bg-white border border-gray-200 rounded-xl p-5 shadow-xs space-y-4">
        {!model.metadata && (
          <div className="bg-amber-50/80 border border-amber-200 rounded-xl p-3 flex items-start gap-2 text-xs text-amber-900">
            <Info className="w-4 h-4 shrink-0" />
            尚未录入展示元数据，公开模型库会回退到前端内置的默认文案。可点「自动填充」按外部目录与模型名生成初稿。
          </div>
        )}
        <div className="flex items-center justify-between gap-2">
          <span className="text-[11px] text-gray-400">自动填充只补空字段，不改评分；评分由评测榜单发布时写入。</span>
          <Can perm="catalog:write">
            <Button size="sm" icon={<Wand2 className="w-3.5 h-3.5" />} loading={suggesting} onClick={autofill}>
              自动填充
            </Button>
          </Can>
        </div>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <Field label="展示名称" hint={filledHint('display_name', '模型库卡片标题；留空则显示模型 ID')}>
            <Input value={draft.display_name} onChange={(e) => edit('display_name', e.target.value)} placeholder="DeepSeek V4 Flash" />
          </Field>
          <Field label="厂商展示名" hint={filledHint('provider_display', '卡片左上角的厂商名')}>
            <Input value={draft.provider_display} onChange={(e) => edit('provider_display', e.target.value)} placeholder="DeepSeek" />
          </Field>
        </div>
        <Field
          label={
            <span className="flex items-center justify-between">
              介绍文案
              <Can perm="catalog:write">
                <button
                  type="button"
                  disabled={generating || llmAvailable === false}
                  onClick={generateDescription}
                  title={
                    llmAvailable === false
                      ? '服务端未配置 LLM：在配置段 datasync 填 llm_base_url / llm_model，并设置环境变量 UFT_DATASYNC_LLM_API_KEY'
                      : '用 LLM 结合外部目录原文与模型参数生成一两句中文介绍'
                  }
                  className="inline-flex items-center gap-1 text-[11px] font-normal text-purple-700 hover:text-purple-900 disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer"
                >
                  {generating ? <Loader2 className="w-3 h-3 animate-spin" /> : <Sparkles className="w-3 h-3" />}
                  AI 生成
                </button>
              </Can>
            </span>
          }
          hint={filledHint('description')}
        >
          <Textarea rows={4} value={draft.description} onChange={(e) => edit('description', e.target.value)} placeholder="一两句话说明模型的特点与适用场景" />
        </Field>
        <Field label="标签" hint={filledHint('tags', '回车或逗号添加，例如 reasoning、coding')}>
          <TagInput value={draft.tags} onChange={(tags) => edit('tags', tags)} placeholder="添加标签…" />
        </Field>
        <div>
          <div className="text-xs font-medium text-gray-700 mb-1">评分（0–100，可留空）</div>
          <div className="grid grid-cols-3 gap-3">
            {INDEX_FIELDS.map(({ key, label }) => (
              <Field key={key} label={<span className="text-[11px] text-gray-500 font-normal">{label}</span>} error={scoreErrors.has(key) ? '需为数字' : undefined}>
                <Input
                  mono
                  inputMode="decimal"
                  value={draft.scores[key]}
                  invalid={scoreErrors.has(key)}
                  onChange={(e) => setDraft({ ...draft, scores: { ...draft.scores, [key]: e.target.value } })}
                />
              </Field>
            ))}
          </div>
        </div>
        <div>
          <button
            type="button"
            onClick={() => setShowBoard((v) => !v)}
            className="text-xs font-medium text-gray-700 mb-1 inline-flex items-center gap-1 cursor-pointer hover:text-purple-700"
          >
            {showBoard ? <ChevronDown className="w-3.5 h-3.5" /> : <ChevronRight className="w-3.5 h-3.5" />}
            榜单单项分数
            <span className="text-[11px] text-gray-400 font-normal ml-1">
              已填 {boardFilled} / {BOARD_SCORE_FIELDS.length} · 评测榜单发布时按基准的 score_key 自动写入，手工修改会在下次发布时被覆盖
            </span>
          </button>
          {(showBoard || BOARD_SCORE_FIELDS.some(({ key }) => scoreErrors.has(key))) && (
            <div className="grid grid-cols-2 sm:grid-cols-3 gap-3 mt-1">
              {BOARD_SCORE_FIELDS.map(({ key, label }) => (
                <Field
                  key={key}
                  label={
                    <span className="text-[11px] text-gray-500 font-normal">
                      {label} <span className="font-mono text-gray-400">{key}</span>
                    </span>
                  }
                  error={scoreErrors.has(key) ? '需为数字' : undefined}
                >
                  <Input
                    mono
                    inputMode="decimal"
                    value={draft.scores[key]}
                    invalid={scoreErrors.has(key)}
                    onChange={(e) => setDraft({ ...draft, scores: { ...draft.scores, [key]: e.target.value } })}
                  />
                </Field>
              ))}
            </div>
          )}
        </div>
        <div>
          <div className="text-xs font-medium text-gray-700 mb-1">
            Design Arena 分项评分
            <span className="text-[11px] text-gray-400 font-normal ml-1.5">可留空；全部留空则不写入 design_arena</span>
          </div>
          <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
            {DESIGN_ARENA_FIELDS.map(({ key, label }) => {
              const bad = scoreErrors.has(`design_arena.${key}`);
              return (
                <Field key={key} label={<span className="text-[11px] text-gray-500 font-normal">{label}</span>} error={bad ? '需为数字' : undefined}>
                  <Input
                    mono
                    inputMode="decimal"
                    value={draft.design_arena[key]}
                    invalid={bad}
                    onChange={(e) => setDraft({ ...draft, design_arena: { ...draft.design_arena, [key]: e.target.value } })}
                  />
                </Field>
              );
            })}
          </div>
        </div>
        <div className="flex items-center justify-end gap-2 pt-2 border-t border-gray-100">
          {dirty && <span className="text-[11px] text-amber-700 mr-auto">有未保存的修改</span>}
          <Button
            disabled={!dirty || saving}
            onClick={() => {
              setDraft(fromDetail(model));
              setFilled({});
            }}
          >
            还原
          </Button>
          <Can perm="catalog:write"><Button variant="primary" loading={saving} disabled={!dirty || scoreErrors.size > 0} onClick={save}>
            保存元数据
          </Button></Can>
        </div>
      </div>

      <div className="lg:col-span-2">
        <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold mb-2">模型库卡片预览</div>
        <CatalogCardPreview model={model} draft={draft} />
        <p className="text-[11px] text-gray-400 mt-2">预览用当前生效售价；web 模型库约 60 秒缓存后更新。</p>
      </div>
    </div>
  );
}

// 本地轻量版 web ModelGridCard（frontend/web/src/components/ModelGridCard.tsx 的样式）
function CatalogCardPreview({ model, draft }: { model: VirtualModelDetail; draft: Draft }) {
  const provider = draft.provider_display.trim() || model.family;
  const title = draft.display_name.trim() || model.name;
  const initial = provider.slice(0, 1).toUpperCase();
  const sp = model.sell_price;
  return (
    <div className="max-w-64 flex flex-col rounded-xl border border-gray-200 bg-white p-3.5 hover:border-purple-300 hover:shadow-md transition-all">
      <div className="flex items-center gap-1.5">
        <span className="w-5 h-5 rounded bg-purple-600 text-white text-[10px] font-semibold flex items-center justify-center">{initial}</span>
        <span className="text-[11px] text-gray-400 truncate">{provider}</span>
      </div>
      <h3 className="mt-1.5 font-bold text-gray-900 text-xs leading-snug line-clamp-2">{title}</h3>
      {model.status === 'deprecated' && (
        <div className="mt-1.5">
          <span className="px-1.5 py-0.5 text-[10px] rounded border font-medium leading-none bg-amber-50 text-amber-700 border-amber-200">已废弃</span>
        </div>
      )}
      <p className="mt-1.5 text-gray-500 text-xxs leading-relaxed line-clamp-3 min-h-10">{draft.description.trim() || <span className="italic text-gray-300">（暂无介绍）</span>}</p>
      <div className="flex flex-wrap items-center gap-1.5 mt-2.5 text-xxs text-gray-400">
        <span className="px-1.5 py-0.5 bg-gray-100 text-gray-600 rounded font-medium">{formatContext(model.context_window)}</span>
        {draft.tags.slice(0, 3).map((t) => (
          <span key={t} className="px-1.5 py-0.5 bg-gray-100 text-gray-500 rounded uppercase tracking-wider text-[9px]">
            {t}
          </span>
        ))}
      </div>
      {INDEX_FIELDS.some(({ key }) => draft.scores[key].trim() !== '') && (
        <div className="flex flex-wrap gap-1 mt-2">
          {INDEX_FIELDS.filter(({ key }) => draft.scores[key].trim() !== '').map(({ key, label }) => (
            <span key={key} className="px-1.5 py-0.5 text-[10px] rounded border bg-purple-50 text-purple-700 border-purple-200 font-mono">
              {label} {draft.scores[key]}
            </span>
          ))}
        </div>
      )}
      <div className="mt-2 pt-2 border-t border-gray-100 text-[11px] text-gray-700 font-semibold space-y-0.5">
        {sp ? (
          <>
            <div className="truncate">输入 ¥{formatPrice(sp.input)} / 百万 Token</div>
            <div className="truncate">输出 ¥{formatPrice(sp.output)} / 百万 Token</div>
          </>
        ) : (
          <div className="text-amber-700 font-normal">未设置售价</div>
        )}
      </div>
      <div className="mt-1.5 text-[10px] text-gray-400 font-mono truncate">{model.name}</div>
    </div>
  );
}
