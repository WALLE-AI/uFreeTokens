import React, { useEffect, useRef, useState } from 'react';
import { Check, Copy } from 'lucide-react';
import { useT } from '../i18n';

export function useCopy(): [boolean, (text: string) => void] {
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);
  const copy = (text: string) => {
    navigator.clipboard?.writeText(text).then(
      () => {
        setCopied(true);
        clearTimeout(timer.current);
        timer.current = setTimeout(() => setCopied(false), 1500);
      },
      () => undefined,
    );
  };
  return [copied, copy];
}

interface CopyButtonProps {
  // getText 在点击时才取值，代码块要复制的是渲染后（已替换 Base URL）的文本。
  getText: () => string;
  label?: string;
  className?: string;
}

export const CopyButton: React.FC<CopyButtonProps> = ({ getText, label, className = '' }) => {
  const t = useT();
  const [copied, copy] = useCopy();
  return (
    <button
      type="button"
      onClick={() => copy(getText())}
      className={`inline-flex items-center gap-1 cursor-pointer transition-colors ${className}`}
      aria-label={label ?? t('copy')}
    >
      {copied ? <Check className="w-3.5 h-3.5 text-emerald-500" /> : <Copy className="w-3.5 h-3.5" />}
      {label !== undefined && <span>{copied ? t('copied') : label}</span>}
    </button>
  );
};
