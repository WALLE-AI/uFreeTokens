import React, { useState } from 'react';
import { X, KeyRound, Trash2, ShieldCheck } from 'lucide-react';
import { authStore, useApiKey } from '../api/auth';

interface ConnectKeyModalProps {
  onClose: () => void;
}

export const ConnectKeyModal: React.FC<ConnectKeyModalProps> = ({ onClose }) => {
  const currentKey = useApiKey();
  const [value, setValue] = useState('');
  const [error, setError] = useState<string | null>(null);

  const handleConnect = (e: React.FormEvent) => {
    e.preventDefault();
    const trimmed = value.trim();
    if (!trimmed) {
      setError('请输入 API Key');
      return;
    }
    authStore.setApiKey(trimmed);
    onClose();
  };

  const handleDisconnect = () => {
    authStore.clear();
    setValue('');
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
            <KeyRound className="w-4 h-4 text-purple-600" />
            <h3 className="text-sm font-bold text-gray-900">连接 API Key</h3>
          </div>
          <button onClick={onClose} className="text-gray-400 hover:text-gray-600 p-1 cursor-pointer">
            <X className="w-4 h-4" />
          </button>
        </div>

        {currentKey && (
          <div className="mt-4 bg-emerald-50 border border-emerald-100 rounded-lg p-3 flex items-center justify-between text-xs">
            <div className="flex items-center gap-1.5 text-emerald-700 min-w-0">
              <ShieldCheck className="w-3.5 h-3.5 shrink-0" />
              <span className="font-mono truncate">{maskKey(currentKey)}</span>
            </div>
            <button
              onClick={handleDisconnect}
              className="flex items-center gap-1 text-rose-600 hover:text-rose-700 font-medium shrink-0 cursor-pointer"
            >
              <Trash2 className="w-3.5 h-3.5" />
              <span>断开并清除</span>
            </button>
          </div>
        )}

        <form onSubmit={handleConnect} className="mt-4 space-y-3 text-xs">
          <div>
            <label className="block text-gray-700 font-medium mb-1">
              {currentKey ? '更换为新的 API Key' : 'API Key'}
            </label>
            <input
              type="password"
              value={value}
              onChange={(e) => {
                setValue(e.target.value);
                setError(null);
              }}
              placeholder="sk-..."
              className="w-full border border-gray-200 rounded-lg px-3 py-2 font-mono focus:outline-none focus:border-purple-500"
              autoFocus
            />
            {error && <p className="text-rose-500 mt-1">{error}</p>}
          </div>
          <p className="text-[11px] text-gray-400 leading-relaxed">
            仅保存在本机浏览器（localStorage），不会上传到任何服务器。可以在
            个人中心的“API 密钥”页创建一把新 Key。
          </p>
          <div className="flex justify-end gap-2 pt-2">
            <button
              type="button"
              onClick={onClose}
              className="px-3.5 py-1.5 border border-gray-200 text-gray-700 rounded-lg hover:bg-gray-50 cursor-pointer"
            >
              取消
            </button>
            <button
              type="submit"
              className="px-4 py-1.5 bg-purple-600 hover:bg-purple-700 text-white rounded-lg font-medium cursor-pointer shadow-xs"
            >
              连接
            </button>
          </div>
        </form>
      </div>
    </div>
  );
};

function maskKey(key: string): string {
  if (key.length <= 10) return key;
  return `${key.slice(0, 6)}...${key.slice(-4)}`;
}
