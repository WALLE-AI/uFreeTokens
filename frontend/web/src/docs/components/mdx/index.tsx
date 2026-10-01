import React, { useEffect, useState } from 'react';
import { Link } from 'react-router';
import { AlertTriangle, Info, Lightbulb, Link2, OctagonAlert } from 'lucide-react';
import type { MDXComponents } from 'mdx/types';
import { listCatalog, type CatalogModel } from '../../../api/catalog';
import { errorCodes } from '../../api/openapi';
import { pick, useLocale, useT } from '../../i18n';
import { CodeBlock, CodeTabs } from './CodeBlock';

// ---------- 排版元素 ----------
// 段落间距由 index.css 里的 .docs-prose 统一控制，这里只管每种元素自身的样式。

function heading(Tag: 'h2' | 'h3') {
  const H: React.FC<React.HTMLAttributes<HTMLHeadingElement>> = ({ id, children, ...rest }) => (
    <Tag
      id={id}
      {...rest}
      className={`group scroll-mt-28 font-bold text-gray-950 tracking-tight ${Tag === 'h2' ? 'text-xl' : 'text-base'}`}
    >
      {children}
      {id && (
        <a href={`#${id}`} className="ml-1.5 inline-block align-middle text-gray-300 opacity-0 group-hover:opacity-100 hover:text-purple-600" aria-label="anchor">
          <Link2 className="w-4 h-4" />
        </a>
      )}
    </Tag>
  );
  return H;
}

const Anchor: React.FC<React.AnchorHTMLAttributes<HTMLAnchorElement>> = ({ href = '', children, ...rest }) => {
  const cls = 'text-purple-700 font-medium underline decoration-purple-300 underline-offset-2 hover:decoration-purple-700';
  if (href.startsWith('/')) {
    return (
      <Link to={href} className={cls}>
        {children}
      </Link>
    );
  }
  const external = /^https?:/.test(href);
  return (
    <a href={href} {...rest} className={cls} {...(external ? { target: '_blank', rel: 'noreferrer' } : {})}>
      {children}
    </a>
  );
};

// MDX 把代码块里的 <code> 也交给这个组件：shiki 输出的子节点是一组 <span>，
// 行内代码的子节点是单个字符串，据此区分。
const InlineCode: React.FC<React.HTMLAttributes<HTMLElement>> = ({ children, className, ...rest }) =>
  typeof children === 'string' ? (
    <code className="px-1.5 py-0.5 rounded bg-gray-100 border border-gray-200 text-[0.85em] font-mono text-gray-900 break-words">
      {children}
    </code>
  ) : (
    <code className={className} {...rest}>
      {children}
    </code>
  );

// ---------- 自定义组件 ----------

const CALLOUT = {
  info: { icon: Info, cls: 'bg-blue-50/70 border-blue-200 text-blue-950', iconCls: 'text-blue-600' },
  tip: { icon: Lightbulb, cls: 'bg-emerald-50/70 border-emerald-200 text-emerald-950', iconCls: 'text-emerald-600' },
  warning: { icon: AlertTriangle, cls: 'bg-amber-50/80 border-amber-200 text-amber-950', iconCls: 'text-amber-600' },
  danger: { icon: OctagonAlert, cls: 'bg-rose-50/80 border-rose-200 text-rose-950', iconCls: 'text-rose-600' },
};

export const Callout: React.FC<{ type?: keyof typeof CALLOUT; title?: string; children?: React.ReactNode }> = ({
  type = 'info',
  title,
  children,
}) => {
  const c = CALLOUT[type] ?? CALLOUT.info;
  const Icon = c.icon;
  return (
    <div className={`flex gap-3 rounded-lg border px-4 py-3 text-sm ${c.cls}`}>
      <Icon className={`w-4 h-4 mt-0.5 shrink-0 ${c.iconCls}`} />
      <div className="min-w-0 flex-1 docs-prose docs-prose-tight">
        {title && <p className="font-semibold">{title}</p>}
        {children}
      </div>
    </div>
  );
};

export const Steps: React.FC<{ children?: React.ReactNode }> = ({ children }) => {
  const steps = React.Children.toArray(children).filter(React.isValidElement) as React.ReactElement<StepProps>[];
  return (
    <ol className="relative ml-3.5 border-l border-gray-200 space-y-6 list-none p-0">
      {steps.map((s, i) => React.cloneElement(s, { index: i + 1, key: i }))}
    </ol>
  );
};

interface StepProps {
  title?: string;
  index?: number;
  children?: React.ReactNode;
}

export const Step: React.FC<StepProps> = ({ title, index, children }) => (
  <li className="relative pl-7">
    <span className="absolute -left-3.5 top-0 w-7 h-7 rounded-full bg-purple-100 text-purple-700 border-2 border-white text-xs font-bold flex items-center justify-center">
      {index}
    </span>
    {title && <p className="font-semibold text-gray-950 text-[15px] leading-7">{title}</p>}
    <div className="docs-prose mt-2">{children}</div>
  </li>
);

function priceOf(m: CatalogModel, meter: string): string {
  const c = m.sellPrice?.components.find((x) => x.meter === meter && x.unit === 'per_1m_tokens');
  return c ? String(c.unitPrice) : '—';
}

export const LiveModelList: React.FC = () => {
  const t = useT();
  const [models, setModels] = useState<CatalogModel[] | null>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let alive = true;
    listCatalog()
      .then((m) => alive && setModels(m.filter((x) => x.status !== 'deprecated')))
      .catch(() => alive && setFailed(true));
    return () => {
      alive = false;
    };
  }, []);

  if (failed) return <p className="text-sm text-rose-700">{t('liveModelsFailed')}</p>;
  if (!models) return <p className="text-sm text-gray-500">{t('loading')}</p>;
  if (models.length === 0) return <p className="text-sm text-gray-500">{t('liveModelsEmpty')}</p>;
  return (
    <div className="overflow-x-auto max-h-96 rounded-lg border border-gray-200">
      <table className="w-full text-xs">
        <thead className="bg-gray-50 sticky top-0">
          <tr className="text-left text-gray-600">
            <th className="px-3 py-2 font-semibold">model</th>
            <th className="px-3 py-2 font-semibold">{t('contextWindow')}</th>
            <th className="px-3 py-2 font-semibold">{t('capabilities')}</th>
            <th className="px-3 py-2 font-semibold text-right">{t('priceIn')}</th>
            <th className="px-3 py-2 font-semibold text-right">{t('priceOut')}</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-gray-100">
          {models.map((m) => (
            <tr key={m.name}>
              <td className="px-3 py-1.5 font-mono text-gray-900 whitespace-nowrap">{m.name}</td>
              <td className="px-3 py-1.5 text-gray-600">{m.contextWindow ? m.contextWindow.toLocaleString() : '—'}</td>
              <td className="px-3 py-1.5 text-gray-600">{m.capabilities.join(', ') || '—'}</td>
              <td className="px-3 py-1.5 text-gray-900 text-right font-mono">{priceOf(m, 'input')}</td>
              <td className="px-3 py-1.5 text-gray-900 text-right font-mono">{priceOf(m, 'output')}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
};

// 错误码说明是带 `code` 的纯文本（OpenAPI 里的 {en, zh}），只需处理行内代码。
function inlineCode(text: string): React.ReactNode[] {
  return text.split(/`([^`]+)`/).map((part, i) =>
    i % 2 === 1 ? (
      <code key={i} className="px-1 rounded bg-gray-100 font-mono text-[0.9em] text-gray-900">
        {part}
      </code>
    ) : (
      part
    ),
  );
}

export const ErrorCodeTable: React.FC = () => {
  const t = useT();
  const locale = useLocale();
  const codes = errorCodes();
  return (
    <div className="overflow-x-auto rounded-lg border border-gray-200">
      <table className="w-full text-xs">
        <thead className="bg-gray-50">
          <tr className="text-left text-gray-600">
            <th className="px-3 py-2 font-semibold">{t('code')}</th>
            <th className="px-3 py-2 font-semibold">{t('httpStatus')}</th>
            <th className="px-3 py-2 font-semibold">{t('retryable')}</th>
            <th className="px-3 py-2 font-semibold">{t('description')}</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-gray-100">
          {codes.map((c) => (
            <tr key={c.code} id={`code-${c.code}`} className="scroll-mt-28 target:bg-purple-50">
              <td className="px-3 py-2 font-mono text-gray-900 whitespace-nowrap">{c.code}</td>
              <td className="px-3 py-2 font-mono text-gray-700">{c.status}</td>
              <td className="px-3 py-2 text-gray-700">{c.retryable ? t('yes') : t('no')}</td>
              <td className="px-3 py-2 text-gray-700 leading-relaxed">{inlineCode(pick(c.description, locale))}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
};

export const mdxComponents: MDXComponents = {
  h2: heading('h2'),
  h3: heading('h3'),
  a: Anchor,
  code: InlineCode,
  pre: CodeBlock,
  table: ({ children }) => (
    <div className="overflow-x-auto rounded-lg border border-gray-200">
      <table className="w-full text-[13px]">{children}</table>
    </div>
  ),
  thead: ({ children }) => <thead className="bg-gray-50 text-left text-gray-600">{children}</thead>,
  th: ({ children, style }) => (
    <th style={style} className="px-3 py-2 font-semibold border-b border-gray-200">
      {children}
    </th>
  ),
  td: ({ children, style }) => (
    <td style={style} className="px-3 py-2 align-top border-b border-gray-100 text-gray-700">
      {children}
    </td>
  ),
  Callout,
  Steps,
  Step,
  CodeTabs,
  LiveModelList,
  ErrorCodeTable,
};
