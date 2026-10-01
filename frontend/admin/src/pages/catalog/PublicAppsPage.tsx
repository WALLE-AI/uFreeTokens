import { useEffect, useState } from 'react';
import { Info } from 'lucide-react';
import { useCan } from '../../api/auth';
import { describeError } from '../../api/errors';
import { createPublicAppRule, deletePublicAppRule, listPublicAppRules, listPublicApps } from '../../api/publicApps';
import {
  ConfirmDialog,
  DataState,
  DataTable,
  Field,
  FormModal,
  Input,
  PageHeader,
  Pills,
  RadioCards,
  SectionTitle,
  useToast,
  type Column,
  type RowAction,
} from '../../components/ui';
import { useAsync } from '../../hooks/useAsync';
import { useQueryParams } from '../../hooks/useQueryState';
import { formatCompact, formatInt } from '../../lib/money';
import { formatDateTime, formatRelative } from '../../lib/time';
import type { PublicAppCandidate, PublicAppRule, PublicAppRuleAction } from '../../types';

// 公开"热门应用"榜治理（技术方案 §8.2）：X-Title / HTTP-Referer 是调用方自报的，
// 运营可以屏蔽冒名应用、把别名合并到同一个应用、覆盖展示名。规则在公开接口下一次
// 缓存刷新（≤5 分钟）时生效。候选列表是未经隐私阈值过滤的原始数据，公开榜单
// 只展示调用账户数 ≥ 阈值的应用；阈值由 GET /public-apps 返回（网关的 rankings_min_accounts），
// 接口还没返回时先按默认值 3 显示。

const DEFAULT_MIN_ACCOUNTS = 3;
const DAY_OPTIONS = ['1', '7', '30', '90'];
const DISPLAY_NAME_MAX = 64;

const ACTION_LABELS: Record<PublicAppRuleAction, string> = {
  block: '屏蔽',
  merge: '合并',
  rename: '改名',
};

function RuleSummary({ rule }: { rule: PublicAppRule }) {
  const tone =
    rule.action === 'block'
      ? 'bg-rose-50 text-rose-700 border-rose-200'
      : rule.action === 'merge'
        ? 'bg-blue-50 text-blue-700 border-blue-200'
        : 'bg-purple-50 text-purple-700 border-purple-200';
  return (
    <span className="inline-flex items-center gap-1.5 min-w-0" title={rule.note || undefined}>
      <span className={`px-1.5 py-0.5 rounded-full border text-[10px] font-medium whitespace-nowrap ${tone}`}>{ACTION_LABELS[rule.action] ?? rule.action}</span>
      {rule.action === 'merge' && rule.merge_into && <span className="font-mono text-[11px] text-gray-600 truncate max-w-56">→ {rule.merge_into}</span>}
      {rule.display_name && <span className="text-[11px] text-gray-600 truncate max-w-40">「{rule.display_name}」</span>}
    </span>
  );
}

export default function PublicAppsPage() {
  const toast = useToast();
  const canWrite = useCan('catalog:write');
  const [qp, setQP] = useQueryParams();
  const days = DAY_OPTIONS.includes(qp.days) ? Number(qp.days) : 7;

  const apps = useAsync((signal) => listPublicApps(days, signal), [days]);
  const rules = useAsync((signal) => listPublicAppRules(signal), []);
  const reloadAll = () => {
    apps.reload();
    rules.reload();
  };

  const [editing, setEditing] = useState<{ appKey: string; appName: string; action: PublicAppRuleAction } | null>(null);
  const [deleting, setDeleting] = useState<PublicAppRule | null>(null);
  const [deleteBusy, setDeleteBusy] = useState(false);

  const candidates = apps.data?.data ?? [];
  const minAccounts = apps.data?.min_distinct_accounts ?? DEFAULT_MIN_ACCOUNTS;

  const removeRule = async () => {
    if (!deleting) return;
    setDeleteBusy(true);
    try {
      await deletePublicAppRule(deleting.id);
      toast.success('规则已删除，约 5 分钟内在公开榜单生效');
      setDeleting(null);
      reloadAll();
    } catch (err) {
      toast.error('删除失败', describeError(err));
    } finally {
      setDeleteBusy(false);
    }
  };

  const candidateColumns: Column<PublicAppCandidate>[] = [
    {
      key: 'app_name',
      header: '应用',
      render: (a) => (
        <div className="min-w-0">
          <div className="text-gray-900 truncate max-w-64">{a.app_name || <span className="text-gray-400">（未声明名称）</span>}</div>
          {a.app_url && <div className="text-[11px] text-gray-400 font-mono truncate max-w-64">{a.app_url}</div>}
        </div>
      ),
    },
    { key: 'app_key', header: 'app_key', render: (a) => <span className="font-mono text-[11px] text-gray-500 truncate max-w-64 inline-block align-bottom" title={a.app_key}>{a.app_key}</span> },
    { key: 'requests', header: '请求数', numeric: true, render: (a) => <span title={formatInt(a.requests)}>{formatCompact(a.requests)}</span> },
    { key: 'tokens', header: 'Tokens', numeric: true, render: (a) => <span title={formatInt(a.tokens)}>{formatCompact(a.tokens)}</span> },
    {
      key: 'distinct_accounts',
      header: '调用账户数',
      numeric: true,
      render: (a) =>
        a.distinct_accounts < minAccounts ? (
          <span className="text-amber-700" title={`少于 ${minAccounts} 个账户，不会出现在公开榜单`}>
            {a.distinct_accounts}
          </span>
        ) : (
          a.distinct_accounts
        ),
    },
    { key: 'rule', header: '当前规则', render: (a) => (a.rule ? <RuleSummary rule={a.rule} /> : <span className="text-gray-300">—</span>) },
  ];

  const candidateActions: RowAction<PublicAppCandidate>[] = [
    { label: '屏蔽', danger: true, hidden: (a) => !!a.rule, onClick: (a) => setEditing({ appKey: a.app_key, appName: a.app_name, action: 'block' }) },
    { label: '合并到…', hidden: (a) => !!a.rule, onClick: (a) => setEditing({ appKey: a.app_key, appName: a.app_name, action: 'merge' }) },
    { label: '改名', hidden: (a) => !!a.rule, onClick: (a) => setEditing({ appKey: a.app_key, appName: a.app_name, action: 'rename' }) },
    { label: '删除规则', danger: true, hidden: (a) => !a.rule, onClick: (a) => a.rule && setDeleting(a.rule) },
  ];

  const ruleColumns: Column<PublicAppRule>[] = [
    { key: 'app_key', header: 'app_key', render: (r) => <span className="font-mono text-[11px] text-gray-700">{r.app_key}</span> },
    { key: 'rule', header: '规则', render: (r) => <RuleSummary rule={r} /> },
    { key: 'note', header: '备注', render: (r) => <span className="text-gray-500 text-[11px] truncate max-w-64 inline-block align-bottom" title={r.note}>{r.note || '—'}</span> },
    {
      key: 'updated_at',
      header: '更新时间',
      render: (r) => (
        <span className="text-gray-500 text-[11px]" title={formatDateTime(r.updated_at)}>
          {formatRelative(r.updated_at)}
        </span>
      ),
    },
  ];

  return (
    <div>
      <PageHeader title="公开应用榜" description="治理公开「热门应用」排行：应用名与网址由调用方自报（X-Title / HTTP-Referer），可屏蔽冒名应用、合并别名、覆盖展示名" />

      <div className="bg-blue-50/60 border border-blue-100 rounded-xl p-3 text-xs text-gray-700 mb-6 flex items-start gap-2">
        <Info className="w-4 h-4 text-blue-600 shrink-0 mt-0.5" />
        <div className="space-y-0.5">
          <div>
            下方是未经隐私阈值过滤的原始数据：调用账户数少于 <b>{minAccounts}</b> 的应用（橙色）不会出现在公开榜单。app_key 为 <code className="font-mono">url:https://域名</code>
            （带 HTTP-Referer，按域名归并）或 <code className="font-mono">name:小写 X-Title</code>。
          </div>
          <div className="text-gray-500">屏蔽 = 永不上榜，用量计入"其他"；合并 = 用量计入目标应用；改名 = 覆盖展示名。规则在公开接口下次缓存刷新（≤5 分钟）时生效，无需重算数据。</div>
        </div>
      </div>

      <SectionTitle
        actions={
          <Pills
            value={String(days)}
            onChange={(v) => setQP({ days: v === '7' ? null : v })}
            options={DAY_OPTIONS.map((d) => ({ value: d, label: d === '1' ? '近 1 天' : `近 ${d} 天` }))}
          />
        }
      >
        候选应用{apps.data ? `（${candidates.length}）` : ''}
      </SectionTitle>
      <DataState loading={apps.loading} error={apps.error} onRetry={apps.reload}>
        {apps.data && (
          <DataTable
            columns={candidateColumns}
            rows={candidates}
            rowKey={(a) => a.app_key}
            rowActions={canWrite ? candidateActions : undefined}
            empty="该时间段内没有调用方声明应用信息"
          />
        )}
      </DataState>

      <div className="mt-10">
        <SectionTitle>规则{rules.data ? `（${rules.data.data.length}）` : ''}</SectionTitle>
        <DataState loading={rules.loading} error={rules.error} onRetry={rules.reload}>
          {rules.data && (
            <DataTable
              columns={ruleColumns}
              rows={rules.data.data}
              rowKey={(r) => r.id}
              rowActions={canWrite ? [{ label: '删除', danger: true, onClick: setDeleting }] : undefined}
              empty="还没有规则——在上方候选应用的操作菜单中屏蔽、合并或改名"
            />
          )}
        </DataState>
      </div>

      <RuleModal
        target={editing}
        candidates={candidates}
        onClose={() => setEditing(null)}
        onSaved={() => {
          setEditing(null);
          reloadAll();
        }}
      />

      {deleting && (
        <ConfirmDialog open onClose={() => setDeleting(null)} onConfirm={removeRule} loading={deleteBusy} level="danger" confirmLabel="删除" title="删除公开应用榜规则">
          <p className="text-xs">
            删除 <span className="font-mono">{deleting.app_key}</span> 的「{ACTION_LABELS[deleting.action] ?? deleting.action}」规则后，该应用将按自报信息参与公开榜单（约 5 分钟内生效）。
          </p>
        </ConfirmDialog>
      )}
    </div>
  );
}

// 新建规则：action 由入口预选，可在弹窗内切换
function RuleModal({
  target,
  candidates,
  onClose,
  onSaved,
}: {
  target: { appKey: string; appName: string; action: PublicAppRuleAction } | null;
  candidates: PublicAppCandidate[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const toast = useToast();
  const [action, setAction] = useState<PublicAppRuleAction>('block');
  const [mergeInto, setMergeInto] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [note, setNote] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (target) {
      setAction(target.action);
      setMergeInto('');
      setDisplayName(target.action === 'rename' ? target.appName : '');
      setNote('');
      setError(null);
    }
  }, [target]);

  if (!target) return null;

  const mergeErr = action === 'merge' && mergeInto.trim() === target.appKey ? '不能合并到自己' : undefined;
  const nameErr =
    displayName.trim().length > DISPLAY_NAME_MAX ? `最多 ${DISPLAY_NAME_MAX} 个字符` : action === 'rename' && !displayName.trim() ? '改名需要填写展示名' : undefined;
  const invalid = !!mergeErr || !!nameErr || (action === 'merge' && !mergeInto.trim());

  const submit = async () => {
    setSubmitting(true);
    setError(null);
    try {
      await createPublicAppRule({
        app_key: target.appKey,
        action,
        merge_into: action === 'merge' ? mergeInto.trim() : null,
        display_name: displayName.trim() || null,
        note: note.trim(),
      });
      toast.success(`已${ACTION_LABELS[action]}，约 5 分钟内在公开榜单生效`);
      onSaved();
    } catch (err) {
      setError(describeError(err, '保存失败'));
    } finally {
      setSubmitting(false);
    }
  };

  const mergeTargets = candidates.filter((c) => c.app_key !== target.appKey);
  return (
    <FormModal
      open
      onClose={onClose}
      width="lg"
      title={`新增规则 · ${target.appName || target.appKey}`}
      description={<span className="font-mono">{target.appKey}</span>}
      onSubmit={submit}
      submitLabel={action === 'block' ? '屏蔽' : '保存'}
      submitVariant={action === 'block' ? 'danger' : 'primary'}
      submitting={submitting}
      submitDisabled={invalid}
      error={error}
    >
      <Field label="动作" required>
        <RadioCards
          cols={3}
          options={[
            { value: 'block', label: '屏蔽', hint: '永不上榜，计入"其他"', tone: 'danger' },
            { value: 'merge', label: '合并到…', hint: '用量计入目标应用' },
            { value: 'rename', label: '改名', hint: '覆盖展示名' },
          ]}
          value={action}
          onChange={setAction}
        />
      </Field>
      {action === 'merge' && (
        <Field label="合并到（目标 app_key）" required hint="从候选应用中选择，或直接输入 app_key" error={mergeErr}>
          <Input mono autoFocus list="public-app-merge-targets" value={mergeInto} invalid={!!mergeErr} onChange={(e) => setMergeInto(e.target.value)} placeholder="url:https://example.com" />
          <datalist id="public-app-merge-targets">
            {mergeTargets.map((c) => (
              <option key={c.app_key} value={c.app_key}>
                {c.app_name}
              </option>
            ))}
          </datalist>
        </Field>
      )}
      <Field
        label="展示名"
        required={action === 'rename'}
        hint={action === 'rename' ? `公开榜单上显示的名称，最多 ${DISPLAY_NAME_MAX} 字` : '可选：覆盖展示名'}
        error={nameErr}
      >
        <Input value={displayName} invalid={!!nameErr} onChange={(e) => setDisplayName(e.target.value)} autoFocus={action === 'rename'} />
      </Field>
      <Field label="备注" hint="例如：冒用 XX 名义、与 YY 为同一产品">
        <Input value={note} onChange={(e) => setNote(e.target.value)} />
      </Field>
      <p className="text-[11px] text-gray-400">同一 app_key 只能有一条规则；如需修改，先删除原规则再新建。</p>
    </FormModal>
  );
}
