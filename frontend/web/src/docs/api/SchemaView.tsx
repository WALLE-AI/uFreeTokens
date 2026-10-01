import React, { useState } from 'react';
import { ChevronRight } from 'lucide-react';
import { useLocale, useT } from '../i18n';
import { descHtml, refName, resolve, type Schema } from './openapi';

function typeLabel(raw: Schema | undefined): string {
  const s = resolve(raw);
  if (!s) return 'any';
  const variants = s.oneOf ?? s.anyOf;
  if (variants) return variants.map(typeLabel).join(' | ');
  const types = Array.isArray(s.type) ? s.type : s.type ? [s.type] : [];
  const main = types.filter((t) => t !== 'null');
  let label = main[0] ?? (s.properties ? 'object' : 'any');
  if (label === 'array') label = `${typeLabel(s.items)}[]`;
  else if (label === 'object' && refName(raw)) label = refName(raw)!;
  if (s.format) label += ` (${s.format})`;
  if (types.includes('null') || s.nullable) label += ' | null';
  return label;
}

// childrenOf 返回一个字段可以展开的子结构（对象本身，或数组元素是对象）。
function childrenOf(raw: Schema | undefined): Schema | undefined {
  const s = resolve(raw);
  if (!s) return undefined;
  if (s.properties) return s;
  const item = resolve(s.items);
  if (item?.properties) return item;
  const variant = (s.oneOf ?? s.anyOf)?.map((v) => resolve(v)).find((v) => v?.properties);
  return variant;
}

const Html: React.FC<{ html: string; className?: string }> = ({ html, className = '' }) =>
  html ? <div className={`docs-prose docs-prose-tight docs-html text-gray-600 ${className}`} dangerouslySetInnerHTML={{ __html: html }} /> : null;

const Property: React.FC<{ name: string; schema: Schema; required: boolean; depth: number }> = ({ name, schema, required, depth }) => {
  const t = useT();
  const locale = useLocale();
  const s = resolve(schema) ?? {};
  const child = childrenOf(schema);
  const [open, setOpen] = useState(false);
  const html = descHtml(schema, locale) || descHtml(s, locale);

  return (
    <li className="py-3">
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
        <code className="font-mono text-[13px] font-semibold text-gray-950">{name}</code>
        <span className="font-mono text-[11px] text-gray-500">{typeLabel(schema)}</span>
        {required && <span className="text-[10px] font-semibold text-rose-600">{t('required')}</span>}
      </div>
      <Html html={html} className="mt-1" />
      {s.enum && (
        <p className="mt-1 text-xs text-gray-500">
          {t('enumValues')}:{' '}
          {s.enum.map((v) => (
            <code key={String(v)} className="mr-1 px-1 rounded bg-gray-100 font-mono text-gray-700">
              {JSON.stringify(v)}
            </code>
          ))}
        </p>
      )}
      {s.default !== undefined && (
        <p className="mt-1 text-xs text-gray-500">
          {t('defaultValue')}: <code className="px-1 rounded bg-gray-100 font-mono text-gray-700">{JSON.stringify(s.default)}</code>
        </p>
      )}
      {child && depth < 6 && (
        <div className="mt-2">
          <button
            type="button"
            onClick={() => setOpen((o) => !o)}
            className="inline-flex items-center gap-1 text-xs font-medium text-gray-600 border border-gray-200 rounded-full px-2.5 py-0.5 hover:bg-gray-50 cursor-pointer"
          >
            <ChevronRight className={`w-3 h-3 transition-transform ${open ? 'rotate-90' : ''}`} />
            {Object.keys(child.properties ?? {}).length} {locale === 'zh' ? '个子字段' : 'child fields'}
          </button>
          {open && (
            <div className="mt-2 ml-1 pl-4 border-l border-gray-200">
              <SchemaView schema={child} depth={depth + 1} />
            </div>
          )}
        </div>
      )}
    </li>
  );
};

export const SchemaView: React.FC<{ schema: Schema | undefined; depth?: number }> = ({ schema, depth = 0 }) => {
  const t = useT();
  const locale = useLocale();
  const s = resolve(schema);
  if (!s) return null;
  const target = childrenOf(schema);
  if (!target) {
    return (
      <p className="text-xs text-gray-600">
        <span className="font-mono">{typeLabel(schema)}</span>
      </p>
    );
  }
  const req = new Set(target.required ?? []);
  return (
    <div>
      {depth === 0 && <Html html={descHtml(s, locale)} className="mb-2" />}
      <ul className="divide-y divide-gray-100">
        {Object.entries(target.properties ?? {}).map(([name, prop]) => (
          <Property key={name} name={name} schema={prop} required={req.has(name)} depth={depth} />
        ))}
      </ul>
      {target.additionalProperties === true && <p className="pt-2 text-xs text-gray-500 italic">{t('passthrough')}</p>}
    </div>
  );
};
