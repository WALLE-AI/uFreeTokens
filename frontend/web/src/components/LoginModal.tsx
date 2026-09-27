import React, { useState } from 'react';
import { X, LogIn } from 'lucide-react';
import { login, getMe } from '../api/console';
import { consoleAuthStore } from '../api/auth';
import { ApiError } from '../api/errors';

interface LoginModalProps {
  onClose: () => void;
  onSwitchToRegister?: () => void;
}

export const LoginModal: React.FC<LoginModalProps> = ({ onClose, onSwitchToRegister }) => {
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!email.trim() || !password) {
      setError('请填写邮箱和密码');
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      await login(email.trim(), password);
      const me = await getMe();
      consoleAuthStore.setMe(me);
      onClose();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : '登录失败，请稍后重试');
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
            <LogIn className="w-4 h-4 text-purple-600" />
            <h3 className="text-sm font-bold text-gray-900">登录控制台</h3>
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
            <label className="block text-gray-700 font-medium mb-1">密码</label>
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
          {error && <p className="text-rose-500">{error}</p>}

          <div className="flex items-center justify-between pt-2">
            {onSwitchToRegister ? (
              <button
                type="button"
                onClick={onSwitchToRegister}
                className="text-purple-600 hover:text-purple-800 font-medium cursor-pointer"
              >
                还没有账号？去注册
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
                {submitting ? '登录中...' : '登录'}
              </button>
            </div>
          </div>
        </form>
      </div>
    </div>
  );
};
