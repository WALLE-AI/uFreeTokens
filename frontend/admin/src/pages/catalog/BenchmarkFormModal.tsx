import { useEffect, useState } from 'react';
import { createBenchmark, updateBenchmark } from '../../api/benchmarks';
import { describeError } from '../../api/errors';
import { Field, FormModal, Input, Select, Switch, Textarea, useToast } from '../../components/ui';
import { useEnums } from '../../hooks/useEnums';
import type { Benchmark, BenchmarkCategory, BenchmarkStatus } from '../../types';
import { BENCHMARK_CATEGORY_OPTIONS, BENCHMARK_STATUS_OPTIONS, METRIC_UNIT_SUGGESTIONS, SCORE_KEY_LABELS } from './benchmarkShared';

// 新建 / 编辑基准定义。编辑走 PATCH /benchmarks/{id}，If-Match 由 api/client 根据
// 详情页 GET 拿到的 ETag 自动带上；slug 创建后不可修改；状态变更（发布 / 归档）
// 在详情页的操作菜单里单独做。

const SLUG_RE = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;

interface Draft {
  slug: string;
  name: string;
  category: BenchmarkCategory;
  description: string;
  metric_name: string;
  metric_unit: string;
  higher_is_better: boolean;
  source_name: string;
  source_url: string;
  status: BenchmarkStatus;
  sort_order: string;
  score_key: string; // '' = 不投影
}

function initial(b?: Benchmark): Draft {
  return {
    slug: b?.slug ?? '',
    name: b?.name ?? '',
    category: b?.category ?? 'reasoning',
    description: b?.description ?? '',
    metric_name: b?.metric_name ?? 'accuracy',
    metric_unit: b?.metric_unit ?? 'percent',
    higher_is_better: b?.higher_is_better ?? true,
    source_name: b?.source_name ?? '',
    source_url: b?.source_url ?? '',
    status: b?.status ?? 'draft',
    sort_order: String(b?.sort_order ?? 0),
    score_key: b?.score_key ?? '',
  };
}

export function BenchmarkFormModal({
  open,
  onClose,
  benchmark,
  onSaved,
}: {
  open: boolean;
  onClose: () => void;
  benchmark?: Benchmark; // 传入即编辑模式
  onSaved: (b: Benchmark) => void;
}) {
  const toast = useToast();
  const enums = useEnums();
  const editing = !!benchmark;
  // 评分投影键：以后端字典为准（字典没拉到时用本地已知的键兜底）
  const scoreKeyOptions = (enums?.score_keys ?? Object.keys(SCORE_KEY_LABELS)).map((k) => ({
    value: k,
    label: SCORE_KEY_LABELS[k] ? `${SCORE_KEY_LABELS[k]} · ${k}` : k,
  }));
  const [d, setD] = useState<Draft>(() => initial(benchmark));
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (open) {
      setD(initial(benchmark));
      setError(null);
    }
  }, [open, benchmark]);

  const set = <K extends keyof Draft>(k: K, v: Draft[K]) => setD((x) => ({ ...x, [k]: v }));

  const slugErr = !editing && d.slug.trim() !== '' && !SLUG_RE.test(d.slug.trim()) ? '只能用小写字母、数字和单个连字符，例如 gpqa-diamond' : undefined;
  const urlErr = d.source_url.trim() !== '' && !/^https?:\/\/\S+$/i.test(d.source_url.trim()) ? '需为 http(s):// 开头的完整地址' : undefined;
  const sortErr = !/^-?\d+$/.test(d.sort_order.trim()) ? '需为整数' : undefined;
  const invalid = !!slugErr || !!urlErr || !!sortErr || !d.name.trim() || !d.metric_name.trim() || (!editing && !d.slug.trim());

  const submit = async () => {
    setSubmitting(true);
    setError(null);
    try {
      let saved: Benchmark;
      if (!benchmark) {
        saved = await createBenchmark({
          slug: d.slug.trim(),
          name: d.name.trim(),
          category: d.category,
          description: d.description.trim(),
          metric_name: d.metric_name.trim(),
          metric_unit: d.metric_unit.trim() || 'percent',
          higher_is_better: d.higher_is_better,
          source_name: d.source_name.trim() || null,
          source_url: d.source_url.trim() || null,
          status: d.status,
          sort_order: Number(d.sort_order.trim()),
          score_key: d.score_key || null,
        });
        toast.success(`基准「${saved.name}」已创建`);
      } else {
        // 只提交改动过的字段；来源字段传空字符串表示清除
        const body: Parameters<typeof updateBenchmark>[1] = {};
        if (d.name.trim() !== benchmark.name) body.name = d.name.trim();
        if (d.category !== benchmark.category) body.category = d.category;
        if (d.description.trim() !== benchmark.description) body.description = d.description.trim();
        if (d.metric_name.trim() !== benchmark.metric_name) body.metric_name = d.metric_name.trim();
        if ((d.metric_unit.trim() || 'percent') !== benchmark.metric_unit) body.metric_unit = d.metric_unit.trim() || 'percent';
        if (d.higher_is_better !== benchmark.higher_is_better) body.higher_is_better = d.higher_is_better;
        if (d.source_name.trim() !== (benchmark.source_name ?? '')) body.source_name = d.source_name.trim();
        if (d.source_url.trim() !== (benchmark.source_url ?? '')) body.source_url = d.source_url.trim();
        if (Number(d.sort_order.trim()) !== benchmark.sort_order) body.sort_order = Number(d.sort_order.trim());
        if (d.score_key !== (benchmark.score_key ?? '')) body.score_key = d.score_key;
        if (Object.keys(body).length === 0) {
          onClose();
          return;
        }
        saved = await updateBenchmark(benchmark.id, body);
        toast.success('基准信息已更新');
      }
      onSaved(saved);
      onClose();
    } catch (err) {
      setError(describeError(err, '保存失败'));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <FormModal
      open={open}
      onClose={onClose}
      width="lg"
      title={benchmark ? `编辑基准 · ${benchmark.name}` : '新建基准测试'}
      description={editing ? undefined : '基准定义创建后，在详情页录入评测 run（手工或表格导入），发布后出现在公开基准测试页'}
      onSubmit={submit}
      submitLabel={editing ? '保存' : '创建'}
      submitting={submitting}
      submitDisabled={invalid}
      error={error}
    >
      <div className="grid grid-cols-2 gap-4">
        <Field label="Slug" required={!editing} hint={editing ? '创建后不可修改' : '用于公开页 URL /benchmarks/<slug>'} error={slugErr}>
          <Input mono autoFocus={!editing} disabled={editing} value={d.slug} invalid={!!slugErr} onChange={(e) => set('slug', e.target.value.toLowerCase())} placeholder="gpqa-diamond" />
        </Field>
        <Field label="名称" required>
          <Input autoFocus={editing} value={d.name} onChange={(e) => set('name', e.target.value)} placeholder="GPQA Diamond" />
        </Field>
      </div>
      <div className="grid grid-cols-2 gap-4">
        <Field label="类别" required>
          <Select className="w-full" value={d.category} onChange={(e) => set('category', e.target.value as BenchmarkCategory)} options={BENCHMARK_CATEGORY_OPTIONS} />
        </Field>
        {!editing && (
          <Field label="初始状态" hint="草稿不会出现在公开页">
            <Select className="w-full" value={d.status} onChange={(e) => set('status', e.target.value as BenchmarkStatus)} options={BENCHMARK_STATUS_OPTIONS} />
          </Field>
        )}
      </div>
      <Field label="介绍">
        <Textarea rows={3} value={d.description} onChange={(e) => set('description', e.target.value)} placeholder="评测内容、题量与打分方式" />
      </Field>
      <div className="grid grid-cols-3 gap-4">
        <Field label="指标名" required hint="accuracy / elo / pass@1 …">
          <Input mono value={d.metric_name} onChange={(e) => set('metric_name', e.target.value)} />
        </Field>
        <Field label="指标单位" hint="留空 = percent">
          <Input mono list="benchmark-metric-units" value={d.metric_unit} onChange={(e) => set('metric_unit', e.target.value)} />
          <datalist id="benchmark-metric-units">
            {METRIC_UNIT_SUGGESTIONS.map((u) => (
              <option key={u} value={u} />
            ))}
          </datalist>
        </Field>
        <Field label="排序" hint="越小越靠前" error={sortErr}>
          <Input mono inputMode="numeric" value={d.sort_order} invalid={!!sortErr} onChange={(e) => set('sort_order', e.target.value)} />
        </Field>
      </div>
      <Switch checked={d.higher_is_better} onChange={(v) => set('higher_is_better', v)} label={d.higher_is_better ? '分数越高越好' : '分数越低越好（例如耗时、错误数）'} />
      <Field
        label="评分投影"
        hint="发布 run 时把各模型成绩写入虚拟模型展示元数据的这个评分键（模型库、排行榜使用）；同一模型多个档位取最好成绩。来源许可不允许公开展示时不投影。"
      >
        <Select className="w-full" value={d.score_key} placeholder="不投影" onChange={(e) => set('score_key', e.target.value)} options={scoreKeyOptions} />
      </Field>
      <div className="grid grid-cols-2 gap-4">
        <Field label="来源名称" hint="外部榜单名；自建评测留空">
          <Input value={d.source_name} onChange={(e) => set('source_name', e.target.value)} placeholder="Artificial Analysis" />
        </Field>
        <Field label="来源链接" error={urlErr}>
          <Input mono value={d.source_url} invalid={!!urlErr} onChange={(e) => set('source_url', e.target.value)} placeholder="https://…" />
        </Field>
      </div>
    </FormModal>
  );
}
