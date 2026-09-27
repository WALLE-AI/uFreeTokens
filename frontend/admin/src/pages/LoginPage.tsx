import { useState, type FormEvent } from 'react';
import { useNavigate, useSearchParams } from 'react-router';
import { Eye, EyeOff, ShieldCheck } from 'lucide-react';
import { authStore } from '../api/auth';
import { verifyToken } from '../api/audit';
import { ApiError, errorMessage } from '../api/errors';
import { Button, Field, Input } from '../components/ui';
import { ADMIN_ENV, ENV_BADGE_CLASS, ENV_DOT_CLASS, ENV_LABEL } from '../lib/env';
import { cn } from '../lib/cn';

// 只允许站内相对路径回跳，防止 ?next=https://evil 之类的开放重定向
function safeNext(next: string | null): string {
  if (!next || !next.startsWith('/') || next.startsWith('//')) return '/';
  return next;
}

// 登录页（ARCHITECTURE.md §3 Phase 0）：输入被下发的管理员令牌 + 操作人姓名。
// 用 GET /audit-logs?limit=1 校验令牌；姓名作为写请求的 X-Actor-Name 进入审计。
export default function LoginPage() {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const [token, setToken] = useState('');
  const [actor, setActor] = useState(authStore.get().actorName ?? '');
  const [showToken, setShowToken] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<{ field?: 'token' | 'actor'; message: string } | null>(null);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (submitting) return;
    const t = token.trim();
    const a = actor.trim();
    if (!t) return setError({ field: 'token', message: '请输入管理员令牌' });
    if (!a) return setError({ field: 'actor', message: '请输入操作人姓名，它会记录到审计日志中' });
    if (a.length > 64) return setError({ field: 'actor', message: '姓名不超过 64 个字符' });
    setSubmitting(true);
    setError(null);
    try {
      await verifyToken(t);
      authStore.set(t, a);
      navigate(safeNext(params.get('next')), { replace: true });
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) setError({ field: 'token', message: '令牌无效，请确认后重试' });
      else setError({ message: errorMessage(err, '登录失败') });
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className={cn('min-h-screen bg-gray-50 flex items-center justify-center p-4', ADMIN_ENV === 'production' && 'border-t-2 border-rose-500')}>
      <div className="w-full max-w-sm">
        <div className="flex flex-col items-center mb-6">
          <span className="w-10 h-10 rounded-xl bg-purple-600 text-white flex items-center justify-center shadow-xs mb-3">
            <ShieldCheck className="w-5 h-5" />
          </span>
          <h1 className="text-lg font-bold text-gray-900">uFreeTokens 运营后台</h1>
          <span className={cn('mt-2 inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-[11px] font-medium border', ENV_BADGE_CLASS[ADMIN_ENV])}>
            <span className={cn('w-1.5 h-1.5 rounded-full', ENV_DOT_CLASS[ADMIN_ENV])} />
            {ENV_LABEL[ADMIN_ENV]}
          </span>
        </div>

        <form onSubmit={submit} className="bg-white border border-gray-200 rounded-2xl shadow-xs p-6 space-y-4">
          <Field label="管理员令牌" htmlFor="token" required error={error?.field === 'token' ? error.message : undefined} hint="仅保存在当前标签页，关闭即失效">
            <div className="relative">
              <Input
                id="token"
                mono
                autoFocus
                autoComplete="off"
                type={showToken ? 'text' : 'password'}
                value={token}
                invalid={error?.field === 'token'}
                onChange={(e) => setToken(e.target.value)}
                className="pr-9"
              />
              <button
                type="button"
                onClick={() => setShowToken((s) => !s)}
                className="absolute right-2.5 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-700 cursor-pointer"
                aria-label={showToken ? '隐藏令牌' : '显示令牌'}
              >
                {showToken ? <EyeOff className="w-3.5 h-3.5" /> : <Eye className="w-3.5 h-3.5" />}
              </button>
            </div>
          </Field>
          <Field label="操作人" htmlFor="actor" required error={error?.field === 'actor' ? error.message : undefined} hint="你的姓名，所有写操作会以此身份记入审计日志">
            <Input id="actor" value={actor} maxLength={64} invalid={error?.field === 'actor'} onChange={(e) => setActor(e.target.value)} placeholder="例如：张三" />
          </Field>
          {error && !error.field && <div className="bg-rose-50 border border-rose-200 text-rose-700 rounded-xl p-3 text-xs">{error.message}</div>}
          <Button variant="primary" type="submit" loading={submitting} className="w-full py-2">
            登录
          </Button>
        </form>
        <p className="text-[11px] text-gray-400 text-center mt-4">内部系统，仅限授权运营人员使用。所有操作均会被审计。</p>
      </div>
    </div>
  );
}
