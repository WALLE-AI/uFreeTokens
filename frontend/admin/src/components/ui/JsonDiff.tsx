import { cn } from '../../lib/cn';

type Change = { path: string; kind: 'added' | 'removed' | 'changed' | 'same'; before?: unknown; after?: unknown };

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

function flatten(v: unknown, prefix = '', out: Record<string, unknown> = {}): Record<string, unknown> {
  if (isPlainObject(v)) {
    const keys = Object.keys(v);
    if (keys.length === 0 && prefix) out[prefix] = {};
    for (const k of keys) flatten(v[k], prefix ? `${prefix}.${k}` : k, out);
  } else if (prefix) {
    out[prefix] = v;
  } else if (v !== undefined && v !== null) {
    out['(value)'] = v;
  }
  return out;
}

export function diffValues(before: unknown, after: unknown): Change[] {
  const a = flatten(before);
  const b = flatten(after);
  const keys = Array.from(new Set([...Object.keys(a), ...Object.keys(b)])).sort();
  return keys.map((k) => {
    const inA = k in a;
    const inB = k in b;
    if (inA && !inB) return { path: k, kind: 'removed', before: a[k] };
    if (!inA && inB) return { path: k, kind: 'added', after: b[k] };
    const same = JSON.stringify(a[k]) === JSON.stringify(b[k]);
    return { path: k, kind: same ? 'same' : 'changed', before: a[k], after: b[k] };
  });
}

function fmt(v: unknown): string {
  if (v === undefined) return '';
  if (typeof v === 'string') return v;
  return JSON.stringify(v);
}

// JsonDiff：审计日志 Before/After、价格变更前后对比。差异色按 §11.9：
// 新增 bg-emerald-50；删除 bg-rose-50 line-through；修改 旧值灰色删除线 → 新值加粗。
export function JsonDiff({ before, after, hideUnchanged = false }: { before: unknown; after: unknown; hideUnchanged?: boolean }) {
  const changes = diffValues(before, after).filter((c) => !hideUnchanged || c.kind !== 'same');
  if (changes.length === 0) return <div className="text-xs text-gray-400">无内容</div>;
  return (
    <div className="border border-gray-200 rounded-xl overflow-hidden font-mono text-[11px]">
      {changes.map((c) => (
        <div
          key={c.path}
          className={cn(
            'grid grid-cols-[minmax(0,2fr)_minmax(0,5fr)] gap-3 px-3 py-1.5 border-b border-gray-100 last:border-b-0',
            c.kind === 'added' && 'bg-emerald-50',
            c.kind === 'removed' && 'bg-rose-50',
          )}
        >
          <div className="text-gray-500 truncate" title={c.path}>
            {c.path}
          </div>
          <div className="break-all">
            {c.kind === 'added' && <span className="text-emerald-700">+ {fmt(c.after)}</span>}
            {c.kind === 'removed' && <span className="text-rose-700 line-through">{fmt(c.before)}</span>}
            {c.kind === 'same' && <span className="text-gray-600">{fmt(c.after)}</span>}
            {c.kind === 'changed' && (
              <span>
                <span className="text-gray-400 line-through">{fmt(c.before)}</span>
                <span className="text-gray-400 mx-1.5">→</span>
                <span className="text-gray-900 font-medium">{fmt(c.after)}</span>
              </span>
            )}
          </div>
        </div>
      ))}
    </div>
  );
}
