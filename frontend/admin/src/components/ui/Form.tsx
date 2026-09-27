import { forwardRef, type InputHTMLAttributes, type ReactNode, type SelectHTMLAttributes, type TextareaHTMLAttributes } from 'react';
import { ChevronDown, Search } from 'lucide-react';
import { cn } from '../../lib/cn';

// §11.5 表单配方
const INPUT_BASE =
  'w-full bg-white border rounded-lg px-3 py-2 text-xs text-gray-900 placeholder-gray-400 focus:outline-none transition-colors disabled:bg-gray-50 disabled:text-gray-500';

function borderClass(invalid?: boolean) {
  return invalid ? 'border-rose-300 focus:border-rose-500' : 'border-gray-200 focus:border-purple-500';
}

export interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  invalid?: boolean;
  mono?: boolean;
}

export const Input = forwardRef<HTMLInputElement, InputProps>(function Input({ invalid, mono, className, ...rest }, ref) {
  return <input ref={ref} className={cn(INPUT_BASE, borderClass(invalid), mono && 'font-mono', className)} {...rest} />;
});

export interface TextareaProps extends TextareaHTMLAttributes<HTMLTextAreaElement> {
  invalid?: boolean;
  mono?: boolean;
}

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaProps>(function Textarea(
  { invalid, mono, className, ...rest },
  ref,
) {
  return <textarea ref={ref} className={cn(INPUT_BASE, borderClass(invalid), mono && 'font-mono', 'min-h-20', className)} {...rest} />;
});

export interface SelectOption {
  value: string;
  label: string;
}

export interface SelectProps extends Omit<SelectHTMLAttributes<HTMLSelectElement>, 'children'> {
  options: SelectOption[];
  placeholder?: string;
}

export function Select({ options, placeholder, className, ...rest }: SelectProps) {
  return (
    <div className={cn('relative inline-block', className)}>
      <select
        className="appearance-none w-full bg-white border border-gray-200 rounded-lg px-3 py-1.5 pr-8 text-xs text-gray-700 font-medium hover:border-gray-300 focus:outline-none focus:border-purple-400 shadow-xs cursor-pointer"
        {...rest}
      >
        {placeholder !== undefined && <option value="">{placeholder}</option>}
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
      <ChevronDown className="w-3.5 h-3.5 text-gray-400 absolute right-2.5 top-1/2 -translate-y-1/2 pointer-events-none" />
    </div>
  );
}

export interface SearchInputProps extends InputHTMLAttributes<HTMLInputElement> {}

export const SearchInput = forwardRef<HTMLInputElement, SearchInputProps>(function SearchInput({ className, ...rest }, ref) {
  return (
    <div className={cn('relative', className)}>
      <Search className="w-3.5 h-3.5 text-gray-400 absolute left-3 top-1/2 -translate-y-1/2 pointer-events-none" />
      <input
        ref={ref}
        data-page-search="true"
        className="w-full bg-gray-50 border border-gray-200 rounded-xl pl-8 pr-3.5 py-2 text-xs text-gray-900 placeholder-gray-400 focus:outline-none focus:border-purple-400 focus:bg-white transition-colors"
        {...rest}
      />
    </div>
  );
});

export interface FieldProps {
  label: ReactNode;
  htmlFor?: string;
  required?: boolean;
  hint?: ReactNode;
  error?: ReactNode;
  children: ReactNode;
  className?: string;
}

export function Field({ label, htmlFor, required, hint, error, children, className }: FieldProps) {
  return (
    <div className={className}>
      <label htmlFor={htmlFor} className="block text-xs font-medium text-gray-700 mb-1">
        {label}
        {required && <span className="text-rose-600 ml-0.5">*</span>}
      </label>
      {children}
      {error ? (
        <p className="text-[11px] text-rose-600 mt-1">{error}</p>
      ) : hint ? (
        <p className="text-[11px] text-gray-400 mt-1">{hint}</p>
      ) : null}
    </div>
  );
}
