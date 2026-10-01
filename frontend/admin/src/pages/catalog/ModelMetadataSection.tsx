import { useEffect, useState } from 'react';
import { Info } from 'lucide-react';
import { setVirtualModelMetadata } from '../../api/catalog';
import { errorMessage } from '../../api/errors';
import { Button, Field, Input, Textarea, useToast } from '../../components/ui';
import type { ModelScores, VirtualModelDetail } from '../../types';
import { TagInput, formatContext, formatPrice } from './shared';
import { Can } from '../../components/ui/Can';

// 展示元数据编辑（UI_DESIGN.md §5.4 第 5 点）：左侧表单，右侧实时预览 web 模型库卡片。
// scores 用三个数字输入框而不是自由 JSON，强制遵守 web 约定的
// intelligenceIndex / codingIndex / agenticIndex 键名（frontend/web/src/data/models.ts normalizeScores）。

const SCORE_FIELDS: Array<{ key: keyof ModelScores; label: string }> = [
  { key: 'intelligenceIndex', label: '智能指数' },
  { key: 'codingIndex', label: '编程指数' },
  { key: 'agenticIndex', label: 'Agent 指数' },
];

interface Draft {
  display_name: string;
  description: string;
  provider_display: string;
  tags: string[];
  scores: Record<keyof ModelScores, string>;
}

function fromDetail(d: VirtualModelDetail): Draft {
  const md = d.metadata;
  const s = md?.scores ?? {};
  return {
    display_name: md?.display_name ?? '',
    description: md?.description ?? '',
    provider_display: md?.provider_display ?? '',
    tags: md?.tags ?? [],
    scores: {
      intelligenceIndex: s.intelligenceIndex?.toString() ?? '',
      codingIndex: s.codingIndex?.toString() ?? '',
      agenticIndex: s.agenticIndex?.toString() ?? '',
    },
  };
}

export function ModelMetadataSection({ model, onSaved }: { model: VirtualModelDetail; onSaved: () => void }) {
  const toast = useToast();
  const [draft, setDraft] = useState<Draft>(() => fromDetail(model));
  const [saving, setSaving] = useState(false);
  useEffect(() => setDraft(fromDetail(model)), [model]);

  const scoreErrors = SCORE_FIELDS.filter(({ key }) => draft.scores[key].trim() !== '' && !Number.isFinite(Number(draft.scores[key])));
  const dirty = JSON.stringify(draft) !== JSON.stringify(fromDetail(model));

  const save = async () => {
    const scores: ModelScores = {};
    for (const { key } of SCORE_FIELDS) {
      const v = draft.scores[key].trim();
      if (v !== '') scores[key] = Number(v);
    }
    setSaving(true);
    try {
      await setVirtualModelMetadata(model.id, {
        display_name: draft.display_name.trim(),
        description: draft.description.trim(),
        provider_display: draft.provider_display.trim(),
        tags: draft.tags,
        scores: Object.keys(scores).length > 0 ? scores : null,
      });
      toast.success('展示元数据已保存');
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
            尚未录入展示元数据，公开模型库会回退到前端内置的默认文案。
          </div>
        )}
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <Field label="展示名称" hint="模型库卡片标题；留空则显示模型 ID">
            <Input value={draft.display_name} onChange={(e) => setDraft({ ...draft, display_name: e.target.value })} placeholder="DeepSeek V4 Flash" />
          </Field>
          <Field label="厂商展示名" hint="卡片左上角的厂商名">
            <Input value={draft.provider_display} onChange={(e) => setDraft({ ...draft, provider_display: e.target.value })} placeholder="DeepSeek" />
          </Field>
        </div>
        <Field label="介绍文案">
          <Textarea rows={4} value={draft.description} onChange={(e) => setDraft({ ...draft, description: e.target.value })} placeholder="一两句话说明模型的特点与适用场景" />
        </Field>
        <Field label="标签" hint="回车或逗号添加，例如 reasoning、coding">
          <TagInput value={draft.tags} onChange={(tags) => setDraft({ ...draft, tags })} placeholder="添加标签…" />
        </Field>
        <div>
          <div className="text-xs font-medium text-gray-700 mb-1">评分（0–100，可留空）</div>
          <div className="grid grid-cols-3 gap-3">
            {SCORE_FIELDS.map(({ key, label }) => (
              <Field key={key} label={<span className="text-[11px] text-gray-500 font-normal">{label}</span>} error={scoreErrors.some((f) => f.key === key) ? '需为数字' : undefined}>
                <Input
                  mono
                  inputMode="decimal"
                  value={draft.scores[key]}
                  invalid={scoreErrors.some((f) => f.key === key)}
                  onChange={(e) => setDraft({ ...draft, scores: { ...draft.scores, [key]: e.target.value } })}
                />
              </Field>
            ))}
          </div>
        </div>
        <div className="flex items-center justify-end gap-2 pt-2 border-t border-gray-100">
          {dirty && <span className="text-[11px] text-amber-700 mr-auto">有未保存的修改</span>}
          <Button disabled={!dirty || saving} onClick={() => setDraft(fromDetail(model))}>
            还原
          </Button>
          <Can perm="catalog:write"><Button variant="primary" loading={saving} disabled={!dirty || scoreErrors.length > 0} onClick={save}>
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
      {SCORE_FIELDS.some(({ key }) => draft.scores[key].trim() !== '') && (
        <div className="flex flex-wrap gap-1 mt-2">
          {SCORE_FIELDS.filter(({ key }) => draft.scores[key].trim() !== '').map(({ key, label }) => (
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
