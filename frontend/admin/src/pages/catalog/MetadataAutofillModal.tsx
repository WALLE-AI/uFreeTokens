import { useEffect, useState } from 'react';
import { autofillVirtualModelMetadata } from '../../api/catalog';
import { errorMessage } from '../../api/errors';
import { Button, Modal, useToast } from '../../components/ui';
import type { AutofillItemResult } from '../../types';

// 批量自动填充展示元数据（POST /virtual-models/metadata/autofill）：先 dry_run 预览每个模型
// 将要写入的字段，确认后再写入。只补空字段、不碰评分、不调 LLM；逐条独立，部分失败不影响其余。

const FIELD_LABELS: Record<string, string> = {
  display_name: '展示名称',
  provider_display: '厂商',
  description: '介绍',
  tags: '标签',
};

function ChangeList({ changes }: { changes: AutofillItemResult['changes'] }) {
  const entries = Object.entries(changes ?? {});
  if (entries.length === 0) return <span className="text-gray-400">无可填充的空字段</span>;
  return (
    <div className="space-y-0.5">
      {entries.map(([k, v]) => (
        <div key={k} className="flex gap-1.5 min-w-0">
          <span className="text-gray-400 shrink-0">{FIELD_LABELS[k] ?? k}</span>
          <span className="text-gray-800 truncate" title={Array.isArray(v) ? v.join(', ') : String(v)}>
            {Array.isArray(v) ? v.join(', ') : String(v)}
          </span>
        </div>
      ))}
    </div>
  );
}

export function MetadataAutofillModal({ ids, onClose, onDone }: { ids: number[] | null; onClose: () => void; onDone: () => void }) {
  const toast = useToast();
  const [preview, setPreview] = useState<AutofillItemResult[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [applying, setApplying] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!ids) return;
    let cancelled = false;
    setPreview(null);
    setError(null);
    setLoading(true);
    autofillVirtualModelMetadata(ids, true)
      .then((r) => !cancelled && setPreview(r.results))
      .catch((err) => !cancelled && setError(errorMessage(err)))
      .finally(() => !cancelled && setLoading(false));
    return () => {
      cancelled = true;
    };
  }, [ids]);

  const fillable = (preview ?? []).filter((r) => r.ok && Object.keys(r.changes ?? {}).length > 0);

  const apply = async () => {
    setApplying(true);
    try {
      const { results } = await autofillVirtualModelMetadata(
        fillable.map((r) => r.id),
        false,
      );
      const applied = results.filter((r) => r.applied).length;
      const failed = results.filter((r) => !r.ok);
      if (failed.length > 0) {
        toast.error(`已填充 ${applied} 个模型，${failed.length} 个失败`, failed.map((r) => `#${r.id}：${r.error?.message ?? ''}`).join('\n'));
      } else {
        toast.success(`已为 ${applied} 个模型自动填充展示元数据`);
      }
      onDone();
    } catch (err) {
      toast.error('批量填充失败', errorMessage(err));
    } finally {
      setApplying(false);
    }
  };

  return (
    <Modal
      open={!!ids}
      onClose={onClose}
      busy={applying}
      width="xl"
      title="批量自动填充展示元数据"
      description="按外部目录、内置厂商表与模型名生成建议值，只补空字段，不覆盖已有内容、不改评分。确认前不会写入。"
      footer={
        <>
          <Button onClick={onClose} disabled={applying}>
            取消
          </Button>
          <Button variant="primary" loading={applying} disabled={loading || fillable.length === 0} onClick={apply}>
            写入 {fillable.length} 个模型
          </Button>
        </>
      }
    >
      {loading && <div className="text-xs text-gray-500 py-6 text-center">正在生成预览…</div>}
      {error && <div className="text-xs text-rose-600 py-2">{error}</div>}
      {preview && (
        <div className="max-h-[60vh] overflow-y-auto border border-gray-200 rounded-lg divide-y divide-gray-100 text-xs">
          {preview.map((r) => (
            <div key={r.id} className="grid grid-cols-5 gap-3 px-3 py-2">
              <div className="col-span-2 font-mono text-gray-900 truncate" title={r.name}>
                {r.name || `#${r.id}`}
              </div>
              <div className="col-span-3 min-w-0">
                {r.ok ? <ChangeList changes={r.changes} /> : <span className="text-rose-600">{r.error?.message}</span>}
              </div>
            </div>
          ))}
        </div>
      )}
    </Modal>
  );
}
