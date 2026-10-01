import { useState } from 'react';
import { Plus } from 'lucide-react';
import { useAuth } from '../../api/auth';
import { describeError } from '../../api/errors';
import { createAdminUser, listAdminRoles, listAdminUsers, updateAdminUser } from '../../api/session';
import { Button, Checkbox, ConfirmDialog, DataState, DataTable, Field, FormModal, Input, PageHeader, useToast, type Column } from '../../components/ui';
import { useAsync } from '../../hooks/useAsync';
import { formatDateTime, formatRelative } from '../../lib/time';
import type { AdminRole, AdminUser } from '../../types';

// 管理员与角色（B5）：super_admin 创建管理员、分配角色、停用、重置密码。
// 停用或重置密码会立即注销该管理员的全部会话；最后一个 super_admin 不能被停用或降级。

type Editing = { mode: 'create' } | { mode: 'edit'; user: AdminUser } | { mode: 'password'; user: AdminUser };

export default function AdminUsersPage() {
  const toast = useToast();
  const { me } = useAuth();
  const users = useAsync((signal) => listAdminUsers(signal), []);
  const roles = useAsync((signal) => listAdminRoles(signal), []);
  const [editing, setEditing] = useState<Editing | null>(null);
  const [toggling, setToggling] = useState<AdminUser | null>(null);
  const [busy, setBusy] = useState(false);
  const roleName = (code: string) => roles.data?.data.find((r) => r.code === code)?.name ?? code;

  const toggleStatus = async () => {
    if (!toggling) return;
    setBusy(true);
    try {
      const next = toggling.status === 'active' ? 'disabled' : 'active';
      await updateAdminUser(toggling.id, { status: next });
      toast.success(next === 'disabled' ? `已停用 ${toggling.name}，其会话已全部注销` : `已启用 ${toggling.name}`);
      setToggling(null);
      users.reload();
    } catch (err) {
      toast.error('操作失败', describeError(err));
    } finally {
      setBusy(false);
    }
  };

  // 管理员丢失验证器时由超级管理员清除其两步验证绑定（同时注销其全部会话）
  const resetTotp = async (u: AdminUser) => {
    try {
      await updateAdminUser(u.id, { reset_totp: true });
      toast.success(`已重置 ${u.name} 的两步验证，其会话已全部注销`);
      users.reload();
    } catch (err) {
      toast.error('操作失败', describeError(err));
    }
  };

  const columns: Column<AdminUser>[] = [
    {
      key: 'name',
      header: '管理员',
      render: (u) => (
        <div>
          <div className="text-gray-900 font-medium">
            {u.name}
            {u.id === me?.id && <span className="ml-1.5 text-[10px] text-purple-600">（我）</span>}
          </div>
          <div className="text-[11px] text-gray-400">{u.email}</div>
        </div>
      ),
    },
    { key: 'roles', header: '角色', render: (u) => <span className="text-gray-700">{u.roles.map(roleName).join('、') || '—'}</span> },
    { key: 'totp', header: '两步验证', render: (u) => <span className={u.totp_enabled ? 'text-emerald-600' : 'text-gray-400'}>{u.totp_enabled ? '已启用' : '未启用'}</span> },
    {
      key: 'status',
      header: '状态',
      render: (u) => <span className={u.status === 'active' ? 'text-emerald-600' : 'text-gray-400'}>{u.status === 'active' ? '正常' : '已停用'}</span>,
    },
    {
      key: 'last_login',
      header: '最近登录',
      render: (u) => (
        <span className="text-gray-500 text-[11px]" title={formatDateTime(u.last_login_at)}>
          {u.last_login_at ? formatRelative(u.last_login_at) : '从未'}
        </span>
      ),
    },
    { key: 'created', header: '创建时间', render: (u) => <span className="text-gray-500 text-[11px]">{formatDateTime(u.created_at).slice(0, 16)}</span> },
  ];

  return (
    <div>
      <PageHeader
        title="管理员与角色"
        description="运营后台的登录账号与权限。每个接口按角色的权限点校验，所有写操作以登录身份记入审计日志。"
        actions={
          <Button variant="primary" icon={<Plus className="w-3.5 h-3.5" />} onClick={() => setEditing({ mode: 'create' })}>
            新建管理员
          </Button>
        }
      />
      <DataState loading={users.loading} error={users.error} onRetry={users.reload} skeleton="table">
        {users.data && (
          <DataTable
            columns={columns}
            rows={users.data.data}
            rowKey={(u) => u.id}
            rowActions={[
              { label: '编辑角色', onClick: (u) => setEditing({ mode: 'edit', user: u }) },
              { label: '重置密码', onClick: (u) => setEditing({ mode: 'password', user: u }) },
              { label: '重置两步验证', hidden: (u) => !u.totp_enabled, onClick: (u) => void resetTotp(u) },
              { label: '停用', danger: true, hidden: (u) => u.status !== 'active' || u.id === me?.id, onClick: setToggling },
              { label: '启用', hidden: (u) => u.status === 'active', onClick: setToggling },
            ]}
            empty="还没有管理员账号"
          />
        )}
      </DataState>

      {roles.data && (
        <div className="mt-6 grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-3">
          {roles.data.data.map((r) => (
            <div key={r.code} className="bg-white border border-gray-200 rounded-xl p-3 text-xs">
              <div className="font-medium text-gray-900">
                {r.name} <span className="font-mono text-gray-400">{r.code}</span>
              </div>
              <div className="mt-1.5 flex flex-wrap gap-1">
                {r.permissions.map((p) => (
                  <span key={p} className="px-1.5 py-0.5 rounded bg-gray-50 border border-gray-200 font-mono text-[10px] text-gray-600">
                    {p === '*' ? '全部权限' : p}
                  </span>
                ))}
              </div>
            </div>
          ))}
        </div>
      )}

      {editing && (
        <AdminUserModal
          editing={editing}
          roles={roles.data?.data ?? []}
          onClose={() => setEditing(null)}
          onDone={() => {
            setEditing(null);
            users.reload();
          }}
        />
      )}

      <ConfirmDialog
        open={!!toggling}
        onClose={() => setToggling(null)}
        onConfirm={toggleStatus}
        loading={busy}
        level={toggling?.status === 'active' ? 'danger' : 'normal'}
        confirmLabel={toggling?.status === 'active' ? '停用' : '启用'}
        title={toggling?.status === 'active' ? '停用管理员' : '启用管理员'}
      >
        <p className="text-xs">
          {toggling?.status === 'active'
            ? `停用后 ${toggling?.name} 的全部会话立即失效，无法再登录。`
            : `启用后 ${toggling?.name} 可以重新登录。`}
        </p>
      </ConfirmDialog>
    </div>
  );
}

function AdminUserModal({ editing, roles, onClose, onDone }: { editing: Editing; roles: AdminRole[]; onClose: () => void; onDone: () => void }) {
  const toast = useToast();
  const user = editing.mode === 'create' ? null : editing.user;
  const [email, setEmail] = useState('');
  const [name, setName] = useState(user?.name ?? '');
  const [password, setPassword] = useState('');
  const [selected, setSelected] = useState<Set<string>>(new Set(user?.roles ?? []));
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async () => {
    setError(null);
    if (editing.mode !== 'edit' && password.length < 10) return setError('密码至少 10 个字符');
    if (editing.mode !== 'password' && selected.size === 0) return setError('至少选择一个角色');
    setSubmitting(true);
    try {
      if (editing.mode === 'create') {
        await createAdminUser({ email: email.trim(), name: name.trim(), password, roles: [...selected] });
        toast.success(`已创建管理员 ${name.trim()}`);
      } else if (editing.mode === 'edit') {
        await updateAdminUser(editing.user.id, { name: name.trim(), roles: [...selected] });
        toast.success('已保存');
      } else {
        await updateAdminUser(editing.user.id, { password });
        toast.success(`已重置 ${editing.user.name} 的密码，其会话已全部注销`);
      }
      onDone();
    } catch (err) {
      setError(describeError(err));
    } finally {
      setSubmitting(false);
    }
  };

  const title = editing.mode === 'create' ? '新建管理员' : editing.mode === 'edit' ? `编辑 ${editing.user.name}` : `重置 ${editing.user.name} 的密码`;
  return (
    <FormModal open onClose={onClose} title={title} onSubmit={submit} submitting={submitting} error={error}>
      {editing.mode === 'create' && (
        <Field label="邮箱" htmlFor="admin-email" required>
          <Input id="admin-email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} />
        </Field>
      )}
      {editing.mode !== 'password' && (
        <Field label="姓名" htmlFor="admin-name" required hint="显示在审计日志里">
          <Input id="admin-name" value={name} maxLength={64} onChange={(e) => setName(e.target.value)} />
        </Field>
      )}
      {editing.mode !== 'edit' && (
        <Field label={editing.mode === 'create' ? '初始密码' : '新密码'} htmlFor="admin-password" required hint="至少 10 个字符；请通过安全渠道告知本人并要求其登录后修改">
          <Input id="admin-password" type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} />
        </Field>
      )}
      {editing.mode !== 'password' && (
        <Field label="角色" required>
          <div className="space-y-1.5">
            {roles.map((r) => (
              <label key={r.code} className="flex items-center gap-2 text-xs text-gray-700 cursor-pointer">
                <Checkbox
                  checked={selected.has(r.code)}
                  onChange={(v) =>
                    setSelected((s) => {
                      const n = new Set(s);
                      if (v) n.add(r.code);
                      else n.delete(r.code);
                      return n;
                    })
                  }
                />
                {r.name} <span className="font-mono text-gray-400">{r.code}</span>
              </label>
            ))}
          </div>
        </Field>
      )}
    </FormModal>
  );
}
