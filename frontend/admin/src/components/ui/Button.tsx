import type { ButtonHTMLAttributes, ReactNode } from 'react';
import { Loader2 } from 'lucide-react';
import { cn } from '../../lib/cn';

export type ButtonVariant = 'primary' | 'secondary' | 'dark' | 'danger' | 'ghost';

// §11.5 按钮配方。"danger" 是 web 中没有的唯一变体，只用于 ConfirmDialog 的最终确认。
const VARIANTS: Record<ButtonVariant, string> = {
  primary: 'bg-purple-600 hover:bg-purple-700 text-white shadow-xs',
  secondary: 'border border-gray-200 text-gray-700 bg-white hover:bg-gray-50 shadow-2xs',
  dark: 'bg-gray-900 hover:bg-gray-800 text-white',
  danger: 'bg-rose-600 hover:bg-rose-700 text-white shadow-xs',
  ghost: 'text-gray-600 hover:text-gray-900 hover:bg-gray-50',
};

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  loading?: boolean;
  icon?: ReactNode;
  size?: 'sm' | 'md';
}

export function Button({
  variant = 'secondary',
  loading = false,
  icon,
  size = 'md',
  className,
  disabled,
  children,
  type = 'button',
  ...rest
}: ButtonProps) {
  return (
    <button
      type={type}
      disabled={disabled || loading}
      className={cn(
        'inline-flex items-center justify-center gap-1.5 rounded-lg text-xs font-medium cursor-pointer transition-colors disabled:opacity-50 disabled:cursor-not-allowed',
        size === 'md' ? 'px-3 py-1.5' : 'px-2 py-1',
        VARIANTS[variant],
        className,
      )}
      {...rest}
    >
      {loading ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : icon}
      {children}
    </button>
  );
}

export interface IconButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  danger?: boolean;
  label: string;
}

export function IconButton({ danger, label, className, children, type = 'button', ...rest }: IconButtonProps) {
  return (
    <button
      type={type}
      aria-label={label}
      title={label}
      className={cn(
        'p-1 rounded-md text-gray-400 cursor-pointer transition-colors',
        danger ? 'hover:text-rose-600' : 'hover:text-gray-700',
        className,
      )}
      {...rest}
    >
      {children}
    </button>
  );
}
