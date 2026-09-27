import { useEffect, useState } from 'react';
import { Input } from '../../components/ui';

// DraftInput：文本类筛选在回车 / 失焦时才提交，避免每敲一个字就请求一次（与审计页同一做法）。
export function DraftInput({
  value,
  onCommit,
  placeholder,
  width = 'w-36',
  mono = true,
  type = 'text',
}: {
  value: string;
  onCommit: (v: string) => void;
  placeholder: string;
  width?: string;
  mono?: boolean;
  type?: 'text' | 'date';
}) {
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  const commit = () => {
    if (draft.trim() !== value) onCommit(draft.trim());
  };
  return (
    // Input 内置 w-full，宽度用外层包裹控制
    <div className={width}>
      <Input
        mono={mono}
        type={type}
        value={draft}
        placeholder={placeholder}
        aria-label={placeholder}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={commit}
        onKeyDown={(e) => {
          if (e.key === 'Enter') commit();
        }}
      />
    </div>
  );
}
