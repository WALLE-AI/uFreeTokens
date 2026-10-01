import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router';
import MiniSearch from 'minisearch';
import { CornerDownLeft, FileText, Hash, Search } from 'lucide-react';
import type { Locale } from '../i18n';
import { useLocale, useT } from '../i18n';

// 搜索索引的原始文档：每个 ##/### 小节一条，外加每个 API operation 一条。
// 由 tools/docsPlugin.ts 在构建期生成（virtual:docs-search/<lang>）。
export interface SearchDoc {
  id: string;
  path: string;
  page: string;
  heading: string;
  text: string;
}

// 中英文混合分词：英文/数字按词，中文按单字 + 相邻二字组合（bigram），
// 这样"余额不足"能被"余额"或"不足"命中，不需要引入中文分词词典。
function tokenize(text: string): string[] {
  const tokens: string[] = [];
  for (const part of text.toLowerCase().match(/[\p{Script=Han}]+|[a-z0-9_./-]+/gu) ?? []) {
    if (/\p{Script=Han}/u.test(part)) {
      const chars = [...part];
      chars.forEach((c, i) => {
        tokens.push(c);
        if (i + 1 < chars.length) tokens.push(c + chars[i + 1]);
      });
    } else {
      tokens.push(part);
      // 把 chat/completions、stream_options 这类复合词也拆成子词。
      part.split(/[./_-]+/).forEach((p) => p && p !== part && tokens.push(p));
    }
  }
  return tokens;
}

const loaders: Record<Locale, () => Promise<{ default: SearchDoc[] }>> = {
  zh: () => import('virtual:docs-search/zh'),
  en: () => import('virtual:docs-search/en'),
};

const cache = new Map<Locale, MiniSearch<SearchDoc>>();

async function getIndex(locale: Locale): Promise<MiniSearch<SearchDoc>> {
  const hit = cache.get(locale);
  if (hit) return hit;
  const { default: docs } = await loaders[locale]();
  const ms = new MiniSearch<SearchDoc>({
    fields: ['page', 'heading', 'text'],
    storeFields: ['path', 'page', 'heading', 'text'],
    tokenize,
    searchOptions: { boost: { heading: 3, page: 2 }, prefix: true, fuzzy: 0.1, combineWith: 'AND' },
  });
  ms.addAll(docs);
  cache.set(locale, ms);
  return ms;
}

function snippet(text: string, query: string): string {
  const q = query.trim().toLowerCase().split(/\s+/)[0] ?? '';
  const i = q ? text.toLowerCase().indexOf(q) : -1;
  const start = Math.max(0, i - 30);
  return (start > 0 ? '…' : '') + text.slice(start, start + 120) + (text.length > start + 120 ? '…' : '');
}

export const SearchDialog: React.FC<{ open: boolean; onClose: () => void }> = ({ open, onClose }) => {
  const locale = useLocale();
  const t = useT();
  const navigate = useNavigate();
  const inputRef = useRef<HTMLInputElement>(null);
  const [index, setIndex] = useState<MiniSearch<SearchDoc> | null>(null);
  const [query, setQuery] = useState('');
  const [cursor, setCursor] = useState(0);

  useEffect(() => {
    if (!open) return;
    setQuery('');
    setCursor(0);
    getIndex(locale).then(setIndex);
    requestAnimationFrame(() => inputRef.current?.focus());
  }, [open, locale]);

  const results = useMemo(() => {
    if (!index || !query.trim()) return [];
    return index.search(query).slice(0, 12) as unknown as Array<SearchDoc & { id: string }>;
  }, [index, query]);

  if (!open) return null;

  const go = (path: string) => {
    onClose();
    navigate(path);
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setCursor((c) => Math.min(c + 1, results.length - 1));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setCursor((c) => Math.max(c - 1, 0));
    } else if (e.key === 'Enter' && results[cursor]) {
      go(results[cursor].path);
    } else if (e.key === 'Escape') {
      onClose();
    }
  };

  return (
    <div className="fixed inset-0 z-50 bg-gray-900/40 backdrop-blur-[2px] flex items-start justify-center pt-[12vh] px-4" onMouseDown={onClose}>
      <div
        role="dialog"
        aria-modal="true"
        className="w-full max-w-xl bg-white rounded-xl shadow-2xl border border-gray-200 overflow-hidden"
        onMouseDown={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-2 px-4 border-b border-gray-200">
          <Search className="w-4 h-4 text-gray-400" />
          <input
            ref={inputRef}
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
              setCursor(0);
            }}
            onKeyDown={onKeyDown}
            placeholder={t('searchPlaceholder')}
            className="flex-1 py-3 text-sm outline-none bg-transparent"
          />
          <kbd className="text-[10px] font-mono text-gray-400 border border-gray-200 rounded px-1.5 py-0.5">Esc</kbd>
        </div>
        <div className="max-h-[50vh] overflow-y-auto">
          {query.trim() && results.length === 0 && <p className="px-4 py-8 text-center text-sm text-gray-500">{t('searchEmpty')}</p>}
          {results.map((r, i) => (
            <button
              key={r.id}
              type="button"
              onMouseEnter={() => setCursor(i)}
              onClick={() => go(r.path)}
              className={`w-full text-left px-4 py-2.5 flex gap-3 items-start cursor-pointer ${i === cursor ? 'bg-purple-50' : ''}`}
            >
              {r.heading ? <Hash className="w-4 h-4 mt-0.5 text-gray-400 shrink-0" /> : <FileText className="w-4 h-4 mt-0.5 text-gray-400 shrink-0" />}
              <span className="min-w-0 flex-1">
                <span className="block text-sm text-gray-900 font-medium truncate">
                  {r.heading ? (
                    <>
                      <span className="text-gray-500 font-normal">{r.page} › </span>
                      {r.heading}
                    </>
                  ) : (
                    r.page
                  )}
                </span>
                {r.text && <span className="block text-xs text-gray-500 truncate">{snippet(r.text, query)}</span>}
              </span>
              {i === cursor && <CornerDownLeft className="w-3.5 h-3.5 mt-1 text-purple-500 shrink-0" />}
            </button>
          ))}
        </div>
        <div className="px-4 py-2 border-t border-gray-100 text-[11px] text-gray-400">{t('searchHint')}</div>
      </div>
    </div>
  );
};
