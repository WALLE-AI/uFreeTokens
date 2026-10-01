import { useState, type FormEvent } from 'react';
import { useNavigate, useSearchParams } from 'react-router';
import { Eye, EyeOff, ShieldCheck } from 'lucide-react';
import { authStore } from '../api/auth';
import { fetchMe, login } from '../api/session';
import { ApiError, errorMessage } from '../api/errors';
import { Button, Field, Input } from '../components/ui';
import { ADMIN_ENV, ENV_BADGE_CLASS, ENV_DOT_CLASS, ENV_LABEL } from '../lib/env';
import { cn } from '../lib/cn';

// 只允许站内相对路径回跳，防止 ?next=https://evil 之类的开放重定向
function safeNext(next: string | null): string {
  if (!next || !next.startsWith('/') || next.startsWith('//')) return '/';
  return next;
}

type Mode = 'password' | 'token';

// 登录页（B5）：管理员邮箱 + 密码登录，拿到会话令牌后取 /me（身份与权限）。
// 应急令牌（UFT_ADMIN_TOKEN）作为折叠的备用入口保留，身份为 system、全部权限。
export default function LoginPage() {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const [mode, setMode] = useState<Mode>('password');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [token, setToken] = useState('');
  const [totp, setTotp] = useState('');
  const [needTotp, setNeedTotp] = useState(false);
  const [showSecret, setShowSecret] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<{ field?: 'email' | 'password' | 'token' | 'totp'; message: string } | null>(null);

  const finish = async (sessionToken: string) => {
    const me = await fetchMe(sessionToken);
    authStore.set(sessionToken, me);
    navigate(safeNext(params.get('next')), { replace: true });
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (submitting) return;
    setError(null);
    if (mode === 'password') {
      if (!email.trim()) return setError({ field: 'email', message: '请输入邮箱' });
      if (!password) return setError({ field: 'password', message: '请输入密码' });
    } else if (!token.trim()) {
      return setError({ field: 'token', message: '请输入应急令牌' });
    }
    setSubmitting(true);
    try {
      if (mode === 'password') {
        const res = await login(email.trim(), password, needTotp ? totp.trim() : undefined);
        await finish(res.token);
      } else {
        await finish(token.trim());
      }
    } catch (err) {
      if (err instanceof ApiError && err.code === 'totp_required') {
        setNeedTotp(true);
        setError(null);
      } else if (err instanceof ApiError && err.code === 'invalid_totp') {
        setError({ field: 'totp', message: '验证码不正确或已使用，请等待下一个验证码' });
      } else if (err instanceof ApiError && err.status === 401) {
        setError(mode === 'password' ? { field: 'password', message: '邮箱或密码错误' } : { field: 'token', message: '令牌无效，请确认后重试' });
      } else if (err instanceof ApiError && err.status === 429) {
        setError({ message: '失败次数过多，请 15 分钟后再试' });
      } else {
        setError({ message: errorMessage(err, '登录失败') });
      }
    } finally {
      setSubmitting(false);
    }
  };

  const secretToggle = (
    <button
      type="button"
      onClick={() => setShowSecret((s) => !s)}
      className="absolute right-2.5 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-700 cursor-pointer"
      aria-label={showSecret ? '隐藏' : '显示'}
    >
      {showSecret ? <EyeOff className="w-3.5 h-3.5" /> : <Eye className="w-3.5 h-3.5" />}
    </button>
  );

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
          {mode === 'password' ? (
            <>
              <Field label="邮箱" htmlFor="email" required error={error?.field === 'email' ? error.message : undefined}>
                <Input
                  id="email"
                  type="email"
                  autoFocus
                  autoComplete="username"
                  value={email}
                  invalid={error?.field === 'email'}
                  onChange={(e) => setEmail(e.target.value)}
                />
              </Field>
              <Field label="密码" htmlFor="password" required error={error?.field === 'password' ? error.message : undefined}>
                <div className="relative">
                  <Input
                    id="password"
                    type={showSecret ? 'text' : 'password'}
                    autoComplete="current-password"
                    value={password}
                    invalid={error?.field === 'password'}
                    onChange={(e) => setPassword(e.target.value)}
                    className="pr-9"
                  />
                  {secretToggle}
                </div>
              </Field>
              {needTotp && (
                <Field label="两步验证码" htmlFor="totp" required hint="打开验证器 App 查看 6 位验证码" error={error?.field === 'totp' ? error.message : undefined}>
                  <Input
                    id="totp"
                    mono
                    autoFocus
                    inputMode="numeric"
                    autoComplete="one-time-code"
                    maxLength={6}
                    value={totp}
                    invalid={error?.field === 'totp'}
                    onChange={(e) => setTotp(e.target.value.replace(/\D/g, ''))}
                  />
                </Field>
              )}
            </>
          ) : (
            <Field
              label="应急令牌"
              htmlFor="token"
              required
              error={error?.field === 'token' ? error.message : undefined}
              hint="仅在管理员账号不可用时使用；以 system 身份操作并完整审计"
            >
              <div className="relative">
                <Input
                  id="token"
                  mono
                  autoFocus
                  autoComplete="off"
                  type={showSecret ? 'text' : 'password'}
                  value={token}
                  invalid={error?.field === 'token'}
                  onChange={(e) => setToken(e.target.value)}
                  className="pr-9"
                />
                {secretToggle}
              </div>
            </Field>
          )}
          {error && !error.field && <div className="bg-rose-50 border border-rose-200 text-rose-700 rounded-xl p-3 text-xs">{error.message}</div>}
          <Button variant="primary" type="submit" loading={submitting} className="w-full py-2">
            登录
          </Button>
          <button
            type="button"
            onClick={() => {
              setMode((m) => (m === 'password' ? 'token' : 'password'));
              setError(null);
            }}
            className="w-full text-[11px] text-gray-400 hover:text-gray-600 cursor-pointer"
          >
            {mode === 'password' ? '使用应急令牌登录' : '返回账号密码登录'}
          </button>
        </form>
        <p className="text-[11px] text-gray-400 text-center mt-4">内部系统，仅限授权运营人员使用。所有操作均会被审计。</p>
      </div>
    </div>
  );
}
