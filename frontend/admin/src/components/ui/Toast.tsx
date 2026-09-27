import { createContext, useCallback, useContext, useMemo, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { X } from 'lucide-react';
import { cn } from '../../lib/cn';

type ToastKind = 'success' | 'error' | 'info';

interface ToastItem {
  id: number;
  kind: ToastKind;
  message: ReactNode;
  detail?: ReactNode;
}

interface ToastApi {
  success: (message: ReactNode) => void;
  error: (message: ReactNode, detail?: ReactNode) => void;
  info: (message: ReactNode) => void;
}

const ToastContext = createContext<ToastApi | null>(null);

const AUTO_DISMISS_MS = 2200;

// 全局 toast：样式沿用 web Header.tsx 的局部 toast（bg-gray-900/90 右上角浮层），
// 提升为全局。成功/信息 2.2s 自动消失；失败不自动消失，需手动关闭（UI_DESIGN.md §2）。
export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([]);
  const seq = useRef(0);

  const dismiss = useCallback((id: number) => setItems((xs) => xs.filter((x) => x.id !== id)), []);

  const push = useCallback(
    (kind: ToastKind, message: ReactNode, detail?: ReactNode) => {
      const id = ++seq.current;
      setItems((xs) => [...xs.slice(-4), { id, kind, message, detail }]);
      if (kind !== 'error') setTimeout(() => dismiss(id), AUTO_DISMISS_MS);
    },
    [dismiss],
  );

  const api = useMemo<ToastApi>(
    () => ({
      success: (m) => push('success', m),
      error: (m, d) => push('error', m, d),
      info: (m) => push('info', m),
    }),
    [push],
  );

  return (
    <ToastContext.Provider value={api}>
      {children}
      {createPortal(
        <div className="fixed top-28 right-4 z-50 flex flex-col items-end gap-2 pointer-events-none">
          {items.map((t) => (
            <div
              key={t.id}
              role={t.kind === 'error' ? 'alert' : 'status'}
              className="pointer-events-auto bg-gray-900/90 backdrop-blur-md text-white px-3.5 py-2 rounded-lg shadow-lg text-xs flex items-start gap-2 max-w-sm animate-in fade-in slide-in-from-top-2 duration-200"
            >
              <span
                className={cn(
                  'w-1.5 h-1.5 rounded-full mt-1.5 shrink-0',
                  t.kind === 'error' ? 'bg-rose-400' : t.kind === 'success' ? 'bg-purple-400' : 'bg-gray-400',
                )}
              />
              <div className="min-w-0">
                <div>{t.message}</div>
                {t.detail && <div className="text-[11px] text-gray-300 mt-0.5 font-mono break-all">{t.detail}</div>}
              </div>
              {t.kind === 'error' && (
                <button type="button" onClick={() => dismiss(t.id)} className="ml-1 text-gray-400 hover:text-white cursor-pointer" aria-label="关闭">
                  <X className="w-3.5 h-3.5" />
                </button>
              )}
            </div>
          ))}
        </div>,
        document.body,
      )}
    </ToastContext.Provider>
  );
}

export function useToast(): ToastApi {
  const ctx = useContext(ToastContext);
  if (!ctx) throw new Error('useToast must be used inside <ToastProvider>');
  return ctx;
}
