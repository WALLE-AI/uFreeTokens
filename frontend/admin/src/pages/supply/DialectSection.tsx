import { useEffect, useState } from 'react';
import { History, RotateCcw, Save } from 'lucide-react';
import { getAccountDialect, listDialectPresets, setAccountDialect } from '../../api/catalog';
import { describeError } from '../../api/errors';
import { Button, SectionTitle, Select, useToast } from '../../components/ui';
import { Can } from '../../components/ui/Can';
import { useAsync } from '../../hooks/useAsync';
import { formatDateTime } from '../../lib/time';
import { cn } from '../../lib/cn';

// 上游账号的「供应商方言」（多供应商接口统一技术实施方案 §3.3）：选择内置预设或编辑
// JSON 覆盖，展示各端点的支持情况与历史版本。保存后网关在下一次目录快照刷新（约 10 秒）后生效。

const ENDPOINT_LABELS: Record<string, string> = {
  chat: '对话',
  embeddings: '向量',
  rerank: '重排序',
  images: '图像',
  speech: '语音合成',
  transcriptions: '语音识别',
};

const pretty = (v: unknown) => (v === null || v === undefined ? '' : JSON.stringify(v, null, 2));

export function DialectSection({ accountId, onSaved }: { accountId: number; onSaved?: () => void }) {
  const toast = useToast();
  const [reloadKey, setReloadKey] = useState(0);
  const dialect = useAsync((signal) => getAccountDialect(accountId, signal), [accountId, reloadKey]);
  const presets = useAsync((signal) => listDialectPresets(signal), []);
  const [draft, setDraft] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [showHistory, setShowHistory] = useState(false);

  const d = dialect.data;
  useEffect(() => {
    if (d) setDraft(pretty(d.dialect));
  }, [d]);

  const save = async (value: unknown, okText: string) => {
    setSaving(true);
    setError(null);
    try {
      await setAccountDialect(accountId, value);
      toast.success(`${okText}，网关约 10 秒后生效`);
      setReloadKey((k) => k + 1);
      onSaved?.();
    } catch (err) {
      setError(describeError(err));
    } finally {
      setSaving(false);
    }
  };

  const saveDraft = () => {
    const text = draft.trim();
    if (!text) return save(null, '已移除方言（纯透传）');
    try {
      return save(JSON.parse(text), '方言已保存');
    } catch {
      setError('不是合法的 JSON');
    }
  };

  if (!d) return null;
  const preset = d.effective?.preset;
  const presetNotes = presets.data?.data.find((p) => p.name === preset)?.notes ?? d.effective?.notes;

  return (
    <div>
      <SectionTitle>供应商方言</SectionTitle>
      <div className="space-y-3 text-xs">
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-gray-500">当前：</span>
          <span className="font-mono font-medium text-gray-900">{preset ? `预设 ${preset}` : d.dialect ? '自定义' : '无（纯 OpenAI 透传）'}</span>
          {!d.dialect && d.suggested_preset && (
            <Can perm="provider_key:write">
              <Button size="sm" loading={saving} onClick={() => save({ preset: d.suggested_preset }, `已绑定预设 ${d.suggested_preset}`)}>
                绑定同名预设 {d.suggested_preset}
              </Button>
            </Can>
          )}
        </div>
        {presetNotes && <p className="text-gray-500 leading-relaxed">{presetNotes}</p>}

        <div className="flex flex-wrap gap-1.5">
          {Object.entries(ENDPOINT_LABELS).map(([k, label]) => (
            <span
              key={k}
              className={cn(
                'px-2 py-0.5 rounded-full border',
                d.endpoints[k] ? 'bg-emerald-50 border-emerald-200 text-emerald-700' : 'bg-gray-50 border-gray-200 text-gray-400 line-through',
              )}
              title={d.endpoints[k] ? '支持' : '该供应商不支持（导入此类模型会被拒绝）'}
            >
              {label}
            </span>
          ))}
        </div>

        <Can perm="provider_key:write">
          <div className="space-y-2">
            <div className="flex items-center gap-2">
              <Select
                className="py-1 w-48"
                value=""
                onChange={(e) => e.target.value && setDraft(pretty({ preset: e.target.value }))}
                options={[{ value: '', label: '从预设开始…' }, ...(presets.data?.data ?? []).map((p) => ({ value: p.name, label: p.name }))]}
              />
              <span className="text-gray-400">在预设基础上可以追加覆盖字段（深合并）；清空并保存 = 移除方言</span>
            </div>
            <textarea
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              spellCheck={false}
              rows={Math.min(14, Math.max(4, draft.split('\n').length + 1))}
              placeholder='{"preset": "openrouter"}'
              className="w-full rounded-lg border border-gray-200 px-3 py-2 font-mono text-[12px] leading-relaxed outline-none focus:border-purple-400"
            />
            {error && <div className="text-rose-600">{error}</div>}
            <div className="flex items-center gap-2">
              <Button size="sm" variant="primary" icon={<Save className="w-3.5 h-3.5" />} loading={saving} onClick={saveDraft}>
                保存方言
              </Button>
              {d.history.length > 0 && (
                <Button size="sm" variant="ghost" icon={<History className="w-3.5 h-3.5" />} onClick={() => setShowHistory((v) => !v)}>
                  历史版本（{d.history.length}）
                </Button>
              )}
            </div>
            {showHistory && (
              <ul className="border border-gray-200 rounded-xl divide-y divide-gray-100">
                {d.history.map((h, i) => (
                  <li key={i} className="px-3 py-2 flex items-start gap-3">
                    <div className="flex-1 min-w-0">
                      <div className="text-gray-500">{formatDateTime(h.saved_at)}</div>
                      <pre className="mt-1 font-mono text-[11px] text-gray-700 whitespace-pre-wrap break-all">{JSON.stringify(h.dialect)}</pre>
                    </div>
                    <Button size="sm" icon={<RotateCcw className="w-3.5 h-3.5" />} loading={saving} onClick={() => save(h.dialect, '已回退到历史版本')}>
                      回退
                    </Button>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </Can>
      </div>
    </div>
  );
}
