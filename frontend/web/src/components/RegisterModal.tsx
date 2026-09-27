import React, { useState } from 'react';
import { X, UserPlus } from 'lucide-react';
import { register, login, getMe } from '../api/console';
import { consoleAuthStore } from '../api/auth';
import { ApiError } from '../api/errors';

interface RegisterModalProps {
  onClose: () => void;
  onSwitchToLogin?: () => void;
}

const MIN_PASSWORD_LEN = 8;

export const RegisterModal: React.FC<RegisterModalProps> = ({ onClose, onSwitchToLogin }) => {
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!email.trim() || !password) {
      setError('请填写邮箱和密码');
      return;
    }
    if (password.length < MIN_PASSWORD_LEN) {
      setError(`密码至少需要 ${MIN_PASSWORD_LEN} 位`);
      return;
    }
    if (password !== confirmPassword) {
      setError('两次输入的密码不一致');
      return;
    }

    setSubmitting(true);
    setError(null);
    try {
      await register(email.trim(), password);
      // 注册接口本身不签发会话（技术方案：注册 → 登录是两个独立步骤），这里
      // 用刚提交的同一份凭据紧接着登录一次，对用户来说体验上是"注册即登录"。
      await login(email.trim(), password);
      const me = await getMe();
      consoleAuthStore.setMe(me);
      onClose();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : '注册失败，请稍后重试');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/40 backdrop-blur-xs"
      onClick={onClose}
    >
      <div
        className="bg-white rounded-2xl shadow-2xl max-w-md w-full p-6 border border-gray-200 animate-in fade-in zoom-in-95 duration-150"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between pb-3 border-b border-gray-100">
          <div className="flex items-center gap-2">
            <UserPlus className="w-4 h-4 text-purple-600" />
            <h3 className="text-sm font-bold text-gray-900">注册账号</h3>
          </div>
          <button onClick={onClose} className="text-gray-400 hover:text-gray-600 p-1 cursor-pointer">
            <X className="w-4 h-4" />
          </button>
        </div>

        <form onSubmit={handleSubmit} className="mt-4 space-y-3 text-xs">
          <div>
            <label className="block text-gray-700 font-medium mb-1">邮箱</label>
            <input
              type="email"
              value={email}
              onChange={(e) => {
                setEmail(e.target.value);
                setError(null);
              }}
              placeholder="you@example.com"
              className="w-full border border-gray-200 rounded-lg px-3 py-2 focus:outline-none focus:border-purple-500"
              autoFocus
            />
          </div>
          <div>
            <label className="block text-gray-700 font-medium mb-1">密码（至少 {MIN_PASSWORD_LEN} 位）</label>
            <input
              type="password"
              value={password}
              onChange={(e) => {
                setPassword(e.target.value);
                setError(null);
              }}
              placeholder="••••••••"
              className="w-full border border-gray-200 rounded-lg px-3 py-2 focus:outline-none focus:border-purple-500"
            />
          </div>
          <div>
            <label className="block text-gray-700 font-medium mb-1">确认密码</label>
            <input
              type="password"
              value={confirmPassword}
              onChange={(e) => {
                setConfirmPassword(e.target.value);
                setError(null);
              }}
              placeholder="••••••••"
              className="w-full border border-gray-200 rounded-lg px-3 py-2 focus:outline-none focus:border-purple-500"
            />
          </div>
          {error && <p className="text-rose-500">{error}</p>}

          <div className="flex items-center justify-between pt-2">
            {onSwitchToLogin ? (
              <button
                type="button"
                onClick={onSwitchToLogin}
                className="text-purple-600 hover:text-purple-800 font-medium cursor-pointer"
              >
                已有账号？去登录
              </button>
            ) : (
              <span />
            )}
            <div className="flex gap-2">
              <button
                type="button"
                onClick={onClose}
                className="px-3.5 py-1.5 border border-gray-200 text-gray-700 rounded-lg hover:bg-gray-50 cursor-pointer"
              >
                取消
              </button>
              <button
                type="submit"
                disabled={submitting}
                className="px-4 py-1.5 bg-purple-600 hover:bg-purple-700 disabled:opacity-50 text-white rounded-lg font-medium cursor-pointer shadow-xs"
              >
                {submitting ? '注册中...' : '注册'}
              </button>
            </div>
          </div>
        </form>
      </div>
    </div>
  );
};
