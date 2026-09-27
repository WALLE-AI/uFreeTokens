import { useEffect, useId, useState, type FormEvent, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { AlertTriangle, ExternalLink, X } from 'lucide-react';
import { cn } from '../../lib/cn';
import { useEscape } from '../../hooks/useDismiss';
import { Button, IconButton } from './Button';
import { Input } from './Form';

// 打开任何弹窗/抽屉时锁定页面滚动，并通过 data-modal-open 让全局单键快捷键失效。
function useModalLock(open: boolean) {
  useEffect(() => {
    if (!open) return;
    const prev = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    document.body.dataset.modalOpen = 'true';
    return () => {
      document.body.style.overflow = prev;
      delete document.body.dataset.modalOpen;
    };
  }, [open]);
}

export interface ModalProps {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  description?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
  width?: 'md' | 'lg' | 'xl';
  // 提交中禁止 Esc/遮罩关闭，避免误以为操作被取消
  busy?: boolean;
}

const WIDTH = { md: 'max-w-md', lg: 'max-w-lg', xl: 'max-w-2xl' };

// Modal：§11.5 弹窗配方（遮罩 bg-black/40 backdrop-blur-xs + rounded-2xl 面板）
export function Modal({ open, onClose, title, description, children, footer, width = 'md', busy }: ModalProps) {
  useModalLock(open);
  useEscape(open && !busy, onClose);
  if (!open) return null;
  return createPortal(
    <div
      className="fixed inset-0 z-50 bg-black/40 backdrop-blur-xs flex items-center justify-center p-4 animate-in fade-in duration-200"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget && !busy) onClose();
      }}
    >
      <div
        role="dialog"
        aria-modal="true"
        className={cn('bg-white rounded-2xl w-full shadow-2xl max-h-[90vh] flex flex-col animate-in fade-in zoom-in-95 duration-200', WIDTH[width])}
      >
        <div className="px-6 pt-6 pb-4 border-b border-gray-100 flex items-start justify-between gap-4">
          <div>
            <h3 className="text-sm font-semibold text-gray-900">{title}</h3>
            {description && <p className="text-xs text-gray-500 mt-1">{description}</p>}
          </div>
          <IconButton label="关闭" onClick={onClose} disabled={busy}>
            <X className="w-4 h-4" />
          </IconButton>
        </div>
        <div className="px-6 py-4 overflow-y-auto text-xs">{children}</div>
        {footer && <div className="px-6 py-4 border-t border-gray-100 flex justify-end gap-2">{footer}</div>}
      </div>
    </div>,
    document.body,
  );
}

export interface FormModalProps extends Omit<ModalProps, 'footer' | 'busy'> {
  onSubmit: () => Promise<void> | void;
  submitLabel?: string;
  submitting?: boolean;
  submitDisabled?: boolean;
  submitVariant?: 'primary' | 'danger';
  error?: ReactNode; // 非字段级错误（字段级错误放在对应 Field 下）
}

// FormModal：创建类操作。提交中按钮 loading 且禁止重复提交（UI_DESIGN.md §2）。
export function FormModal({
  onSubmit,
  submitLabel = '提交',
  submitting,
  submitDisabled,
  submitVariant = 'primary',
  error,
  children,
  ...rest
}: FormModalProps) {
  // 每个弹窗独立的表单 id：抽屉里再开弹窗等叠加场景下，提交按钮不会指向别的表单
  const formId = useId();
  const handle = (e: FormEvent) => {
    e.preventDefault();
    if (submitting || submitDisabled) return;
    void onSubmit();
  };
  return (
    <Modal
      {...rest}
      busy={submitting}
      footer={
        <>
          <Button onClick={rest.onClose} disabled={submitting}>
            取消
          </Button>
          <Button variant={submitVariant} type="submit" form={formId} loading={submitting} disabled={submitDisabled}>
            {submitLabel}
          </Button>
        </>
      }
    >
      <form id={formId} onSubmit={handle} className="space-y-4">
        {children}
        {error && <div className="bg-rose-50 border border-rose-200 text-rose-700 rounded-xl p-3 text-xs">{error}</div>}
      </form>
    </Modal>
  );
}

export type ConfirmLevel = 'normal' | 'danger' | 'typed';

export interface ConfirmDialogProps {
  open: boolean;
  onClose: () => void;
  onConfirm: () => Promise<void> | void;
  title: ReactNode;
  children?: ReactNode; // 操作说明、操作前 → 操作后对比等
  level?: ConfirmLevel;
  // typed 级别：必须输入 confirmText（如账户名、金额）才能点确认
  confirmText?: string;
  confirmLabel?: string;
  loading?: boolean;
}

// ConfirmDialog 三级：普通确认 / 危险确认（rose 按钮）/ 输入确认（UI_DESIGN.md §2）。
export function ConfirmDialog({
  open,
  onClose,
  onConfirm,
  title,
  children,
  level = 'normal',
  confirmText,
  confirmLabel = '确认',
  loading,
}: ConfirmDialogProps) {
  const [typed, setTyped] = useState('');
  useEffect(() => {
    if (open) setTyped('');
  }, [open]);
  const needTyped = level === 'typed' && !!confirmText;
  const blocked = needTyped && typed.trim() !== confirmText;
  return (
    <Modal
      open={open}
      onClose={onClose}
      busy={loading}
      title={
        <span className="flex items-center gap-2">
          {level !== 'normal' && <AlertTriangle className="w-4 h-4 text-rose-600" />}
          {title}
        </span>
      }
      footer={
        <>
          <Button onClick={onClose} disabled={loading}>
            取消
          </Button>
          <Button
            variant={level === 'normal' ? 'primary' : 'danger'}
            loading={loading}
            disabled={blocked}
            onClick={() => void onConfirm()}
          >
            {confirmLabel}
          </Button>
        </>
      }
    >
      <div className="space-y-3 text-gray-700">
        {children}
        {needTyped && (
          <div>
            <p className="text-xs text-gray-600 mb-1.5">
              请输入 <span className="font-mono font-semibold text-gray-900 bg-gray-100 px-1 rounded">{confirmText}</span> 以确认
            </p>
            <Input
              autoFocus
              mono
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && !blocked && !loading) void onConfirm();
              }}
            />
          </div>
        )}
      </div>
    </Modal>
  );
}

export interface DetailDrawerProps {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  subtitle?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
  // 顶部"在新页面打开"：跳到完整详情页
  fullPageHref?: string;
  onOpenFullPage?: () => void;
}

// DetailDrawer：右侧滑出，"看一眼不离开列表"（§11.5 抽屉配方）
export function DetailDrawer({ open, onClose, title, subtitle, children, footer, fullPageHref, onOpenFullPage }: DetailDrawerProps) {
  useModalLock(open);
  useEscape(open, onClose);
  if (!open) return null;
  return createPortal(
    <div className="fixed inset-0 z-50 animate-in fade-in duration-200">
      <div className="absolute inset-0 bg-black/20" onMouseDown={onClose} />
      <aside
        role="dialog"
        aria-modal="true"
        className="fixed inset-y-0 right-0 w-[560px] max-w-[95vw] bg-white border-l border-gray-200 shadow-2xl flex flex-col animate-in slide-in-from-right duration-200"
      >
        <div className="px-5 py-4 border-b border-gray-100 flex items-start justify-between gap-3">
          <div className="min-w-0">
            <h3 className="text-sm font-semibold text-gray-900 truncate">{title}</h3>
            {subtitle && <div className="text-[11px] text-gray-400 mt-0.5">{subtitle}</div>}
          </div>
          <div className="flex items-center gap-1 shrink-0">
            {(fullPageHref || onOpenFullPage) && (
              <a
                href={fullPageHref}
                onClick={(e) => {
                  if (onOpenFullPage) {
                    e.preventDefault();
                    onOpenFullPage();
                  }
                }}
                className="p-1 rounded-md text-gray-400 hover:text-gray-700 cursor-pointer"
                title="在新页面打开"
              >
                <ExternalLink className="w-4 h-4" />
              </a>
            )}
            <IconButton label="关闭" onClick={onClose}>
              <X className="w-4 h-4" />
            </IconButton>
          </div>
        </div>
        <div className="flex-1 overflow-y-auto px-5 py-4 text-xs">{children}</div>
        {footer && <div className="px-5 py-3 border-t border-gray-100 flex justify-end gap-2">{footer}</div>}
      </aside>
    </div>,
    document.body,
  );
}
