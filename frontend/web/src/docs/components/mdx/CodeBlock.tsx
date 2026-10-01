import React, { createContext, useContext, useRef, useSyncExternalStore } from 'react';
import { CopyButton } from '../CopyButton';
import { BASE_URL_PLACEHOLDER } from '../../markdown';
import { apiOrigin } from '../../origin';

// 把 shiki 输出里的 {{BASE_URL}} 占位符换成真实地址。占位符整体落在一个
// 字符串 token 里（shiki 不会把它拆开），逐个文本节点替换即可。
function replacePlaceholder(node: React.ReactNode, origin: string): React.ReactNode {
  if (typeof node === 'string') return node.includes(BASE_URL_PLACEHOLDER) ? node.split(BASE_URL_PLACEHOLDER).join(origin) : node;
  if (Array.isArray(node)) return node.map((n, i) => <React.Fragment key={i}>{replacePlaceholder(n, origin)}</React.Fragment>);
  if (React.isValidElement<{ children?: React.ReactNode }>(node) && node.props.children !== undefined) {
    return React.cloneElement(node, undefined, replacePlaceholder(node.props.children, origin));
  }
  return node;
}

function languageOf(children: React.ReactNode): string {
  const code = React.Children.toArray(children).find(React.isValidElement) as React.ReactElement<{ className?: string }> | undefined;
  return /language-(\S+)/.exec(code?.props.className ?? '')?.[1] ?? '';
}

// 在 CodeTabs 里时由标签栏负责展示标题，代码块自己不再画标题栏。
export const InTabsContext = createContext(false);

type PreProps = React.HTMLAttributes<HTMLPreElement> & { 'data-title'?: string };

export const CodeBlock: React.FC<PreProps> = ({ children, 'data-title': title, className = '', style, ...rest }) => {
  const inTabs = useContext(InTabsContext);
  const preRef = useRef<HTMLPreElement>(null);
  const lang = languageOf(children);
  const content = replacePlaceholder(children, apiOrigin());
  const getText = () => preRef.current?.textContent ?? '';

  return (
    <div className={`group relative ${inTabs ? '' : 'rounded-lg border border-[#30363d] overflow-hidden'}`}>
      {!inTabs && (title || lang) && (
        <div className="flex items-center justify-between px-3 py-1.5 bg-[#161b22] border-b border-[#30363d] text-[11px] text-gray-400">
          <span className="font-mono">{title || lang}</span>
          <CopyButton getText={getText} className="text-gray-400 hover:text-gray-100 p-0.5" />
        </div>
      )}
      {(inTabs || !(title || lang)) && (
        <CopyButton
          getText={getText}
          className="absolute right-2 top-2 p-1 rounded bg-[#161b22]/90 text-gray-400 hover:text-gray-100 opacity-0 group-hover:opacity-100 focus:opacity-100"
        />
      )}
      <pre
        ref={preRef}
        {...rest}
        style={{ ...style, backgroundColor: '#0d1117' }}
        className={`${className} m-0 px-4 py-3 overflow-x-auto text-[12.5px] leading-relaxed font-mono`}
      >
        {content}
      </pre>
    </div>
  );
};

// ---------- CodeTabs：同一段示例的多语言版本，全站同步选中的语言 ----------

const TAB_KEY = 'uft.docs.codeTab';
const listeners = new Set<() => void>();

function readTab(): string {
  try {
    return window.localStorage.getItem(TAB_KEY) ?? '';
  } catch {
    return '';
  }
}

function writeTab(title: string): void {
  try {
    window.localStorage.setItem(TAB_KEY, title);
  } catch {
    // ignore
  }
  listeners.forEach((l) => l());
}

function subscribe(l: () => void): () => void {
  listeners.add(l);
  return () => listeners.delete(l);
}

// MDX 会把 JSX 里的每个代码块包一层 Fragment，这里拆开拿到真正的 <pre>。
function codeBlocksOf(children: React.ReactNode): React.ReactElement<PreProps>[] {
  return React.Children.toArray(children).flatMap((c) => {
    if (!React.isValidElement<{ children?: React.ReactNode }>(c)) return [];
    if (c.type === React.Fragment) return codeBlocksOf(c.props.children);
    return [c as React.ReactElement<PreProps>];
  });
}

export const CodeTabs: React.FC<{ children?: React.ReactNode }> = ({ children }) => {
  const preferred = useSyncExternalStore(subscribe, readTab, () => '');
  const blocks = codeBlocksOf(children);
  const titles = blocks.map((b, i) => b.props['data-title'] || `#${i + 1}`);
  const active = Math.max(0, titles.indexOf(preferred));

  return (
    <div className="rounded-lg border border-[#30363d] overflow-hidden bg-[#0d1117]">
      <div role="tablist" className="flex items-center gap-0.5 px-1.5 bg-[#161b22] border-b border-[#30363d] overflow-x-auto">
        {titles.map((title, i) => (
          <button
            key={title}
            type="button"
            role="tab"
            aria-selected={i === active}
            onClick={() => writeTab(title)}
            className={`px-2.5 py-1.5 text-[11px] font-mono whitespace-nowrap border-b-2 -mb-px cursor-pointer transition-colors ${
              i === active ? 'text-gray-100 border-purple-400' : 'text-gray-400 border-transparent hover:text-gray-200'
            }`}
          >
            {title}
          </button>
        ))}
      </div>
      <InTabsContext.Provider value={true}>{blocks[active]}</InTabsContext.Provider>
    </div>
  );
};
