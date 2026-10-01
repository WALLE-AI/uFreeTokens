import React, { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router';
import { Gauge, KeyRound, Lock, Unlock, Wallet } from 'lucide-react';
import { CopyButton } from '../components/CopyButton';
import { MethodBadge } from '../components/MethodBadge';
import { Callout } from '../components/mdx';
import { CodeTabs } from '../components/mdx/CodeBlock';
import { NotFound } from '../NotFound';
import { pick, useLocale, useT } from '../i18n';
import { replaceBaseURL } from '../markdown';
import { apiOrigin } from '../origin';
import { SchemaView } from './SchemaView';
import { TryIt } from './TryIt';
import { buildSnippet, SNIPPET_LANGS } from './snippets';
import { descHtml, findOperation, jsonContent, resolve, summaryOf, type Response } from './openapi';

const Html: React.FC<{ html: string }> = ({ html }) =>
  html ? <div className="docs-prose docs-html" dangerouslySetInnerHTML={{ __html: html }} /> : null;

const Block: React.FC<{ title: string; children: React.ReactNode }> = ({ title, children }) => (
  <section className="mt-10">
    <h2 className="text-base font-bold text-gray-950 pb-2 mb-1 border-b border-gray-200">{title}</h2>
    {children}
  </section>
);

// 运行时生成的代码不经过 shiki，用和 MDX 代码块一样的外观，交给 CodeTabs 组织成标签页。
const PlainPre: React.FC<{ 'data-title': string; code: string }> = ({ code }) => (
  <div className="group relative">
    <CopyButton
      getText={() => code}
      className="absolute right-2 top-2 p-1 rounded bg-[#161b22]/90 text-gray-400 hover:text-gray-100 opacity-0 group-hover:opacity-100 focus:opacity-100"
    />
    <pre className="m-0 px-4 py-3 overflow-x-auto text-[12px] leading-relaxed font-mono text-gray-200 bg-[#0d1117]">{code}</pre>
  </div>
);

function ExampleBlock({ title, value }: { title: string; value: unknown }) {
  const text = typeof value === 'string' ? value : JSON.stringify(value, null, 2);
  return (
    <div className="rounded-lg border border-[#30363d] overflow-hidden">
      <div className="flex items-center justify-between px-3 py-1.5 bg-[#161b22] border-b border-[#30363d] text-[11px] text-gray-400">
        <span className="font-mono">{title}</span>
        <CopyButton getText={() => text} className="text-gray-400 hover:text-gray-100" />
      </div>
      <pre className="m-0 px-4 py-3 max-h-80 overflow-auto text-[12px] leading-relaxed font-mono text-gray-200 bg-[#0d1117]">{text}</pre>
    </div>
  );
}

const ResponseItem: React.FC<{ status: string; response: Response }> = ({ status, response: raw }) => {
  const locale = useLocale();
  const t = useT();
  const response = resolve(raw) ?? raw;
  const code = Number(status);
  const [open, setOpen] = useState(code < 300);
  const contentTypes = Object.entries(response.content ?? {});
  const color = code < 300 ? 'text-emerald-700' : code < 500 ? 'text-amber-700' : 'text-rose-700';
  const label = locale === 'zh' ? response['x-i18n']?.zh?.description || response.description : response.description;

  return (
    <li className="py-3">
      <button type="button" onClick={() => setOpen((o) => !o)} className="w-full flex items-center gap-3 text-left cursor-pointer">
        <span className={`font-mono font-bold text-sm ${color}`}>{status}</span>
        <span className="text-sm text-gray-700 flex-1">{label}</span>
        {response['x-error-codes']?.map((c) => (
          <Link
            key={c}
            to={`/docs/${locale}/errors#code-${c}`}
            onClick={(e) => e.stopPropagation()}
            className="font-mono text-[11px] text-purple-700 bg-purple-50 border border-purple-100 rounded px-1.5 hover:underline"
          >
            {c}
          </Link>
        ))}
      </button>
      {open && (
        <div className="mt-3 space-y-4">
          {Object.keys(response.headers ?? {}).length > 0 && (
            <div className="text-xs text-gray-600">
              <span className="font-semibold text-gray-700">{t('responseHeaders')}：</span>
              {Object.keys(response.headers ?? {}).map((h) => (
                <code key={h} className="ml-1 font-mono bg-gray-100 rounded px-1">
                  {h}
                </code>
              ))}
            </div>
          )}
          {contentTypes.map(([type, media]) => (
            <div key={type} className="space-y-3">
              {contentTypes.length > 1 && <p className="text-xs font-mono text-gray-500">{type}</p>}
              {code < 400 && <SchemaView schema={media.schema} />}
              {media.example !== undefined && <ExampleBlock title={`${status} · ${type}`} value={media.example} />}
            </div>
          ))}
        </div>
      )}
    </li>
  );
};

export const OperationPage: React.FC = () => {
  const { operationId = '' } = useParams();
  const locale = useLocale();
  const t = useT();
  const entry = findOperation(operationId);

  useEffect(() => {
    if (!entry) return;
    document.title = `${entry.method} ${entry.path} · uFreeTokens ${t('apiReference')}`;
    document.querySelector('meta[name="description"]')?.setAttribute('content', summaryOf(entry.op, locale));
    window.scrollTo(0, 0);
  }, [entry, locale]);

  if (!entry) return <NotFound />;
  const { op } = entry;
  const notImplemented = op['x-status'] === 'not_implemented';
  const query = (op.parameters ?? []).filter((p) => p.in === 'query');
  const body = jsonContent(op.requestBody);
  const rateLimit = pick(op['x-rate-limit'], locale);
  const responses = Object.entries(op.responses ?? {}).sort(([a], [b]) => Number(a) - Number(b));

  return (
    <div className="grid grid-cols-1 xl:grid-cols-[minmax(0,1fr)_420px] gap-10">
      <article className="min-w-0">
        <p className="text-xs font-medium text-purple-700">{op.tags?.[0]}</p>
        <h1 className="mt-1 text-[28px] leading-tight font-extrabold text-gray-950 tracking-tight">{summaryOf(op, locale)}</h1>

        <div className="mt-4 flex items-center gap-2 rounded-lg border border-gray-200 bg-gray-50 px-3 py-2">
          <MethodBadge method={entry.method} />
          <code className="font-mono text-sm text-gray-900 truncate flex-1">{entry.path}</code>
          <CopyButton getText={() => `${apiOrigin()}${entry.path}`} className="text-gray-400 hover:text-gray-700" />
        </div>

        <div className="mt-3 flex flex-wrap gap-2 text-[11px]">
          {notImplemented ? (
            <span className="px-2 py-0.5 rounded-full bg-gray-100 text-gray-600 border border-gray-200 font-medium">{t('notImplemented')}</span>
          ) : (
            <>
              <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full bg-gray-50 text-gray-700 border border-gray-200">
                {op['x-auth'] === 'none' ? <Unlock className="w-3 h-3" /> : <Lock className="w-3 h-3" />}
                {op['x-auth'] === 'none' ? t('authNone') : t('authApiKey')}
              </span>
              {op['x-billable'] && (
                <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full bg-amber-50 text-amber-800 border border-amber-200">
                  <Wallet className="w-3 h-3" />
                  {t('billable')}
                </span>
              )}
            </>
          )}
        </div>

        {notImplemented && (
          <div className="mt-6">
            <Callout type="warning" title={t('notImplemented')}>
              <p>{t('notImplementedBody')}</p>
            </Callout>
          </div>
        )}

        <div className="mt-6">
          <Html html={descHtml(op, locale)} />
        </div>

        {rateLimit && (
          <div className="mt-4 flex gap-2 text-sm text-gray-600 rounded-lg border border-gray-200 px-3 py-2">
            <Gauge className="w-4 h-4 mt-0.5 text-gray-400 shrink-0" />
            <p>
              <span className="font-semibold text-gray-800">{t('rateLimit')}：</span>
              {rateLimit}{' '}
              <Link to={`/docs/${locale}/rate-limits`} className="text-purple-700 hover:underline">
                →
              </Link>
            </p>
          </div>
        )}

        {query.length > 0 && (
          <Block title={t('parameters')}>
            <ul className="divide-y divide-gray-100">
              {query.map((p) => (
                <li key={p.name} className="py-3">
                  <div className="flex flex-wrap items-baseline gap-2">
                    <code className="font-mono text-[13px] font-semibold text-gray-950">{p.name}</code>
                    <span className="font-mono text-[11px] text-gray-500">
                      {p.schema?.type}
                      {p.schema?.format ? ` (${p.schema.format})` : ''}
                    </span>
                    {p.required && <span className="text-[10px] font-semibold text-rose-600">{t('required')}</span>}
                  </div>
                  <div className="mt-1 text-gray-600">
                    <Html html={descHtml(p, locale)} />
                  </div>
                </li>
              ))}
            </ul>
          </Block>
        )}

        {body?.schema && (
          <Block title={t('requestBody')}>
            <SchemaView schema={body.schema} />
          </Block>
        )}

        {responses.length > 0 && (
          <Block title={t('responses')}>
            <ul className="divide-y divide-gray-100">
              {responses.map(([status, r]) => (
                <ResponseItem key={status} status={status} response={r} />
              ))}
            </ul>
          </Block>
        )}
      </article>

      <aside className="min-w-0 space-y-4 xl:sticky xl:top-[108px] xl:self-start xl:max-h-[calc(100vh-124px)] xl:overflow-y-auto">
        {!notImplemented && (
          <>
            <CodeTabs>
              {SNIPPET_LANGS.map((lang) => (
                <PlainPre key={lang} data-title={lang} code={replaceBaseURL(buildSnippet(lang, entry), apiOrigin())} />
              ))}
            </CodeTabs>
            <TryIt entry={entry} />
          </>
        )}
        <p className="flex items-center gap-1.5 text-[11px] text-gray-400">
          <KeyRound className="w-3 h-3" />
          <Link to={`/docs/${locale}/authentication`} className="hover:text-gray-600 hover:underline">
            {t('apiKey')}
          </Link>
        </p>
      </aside>
    </div>
  );
};
