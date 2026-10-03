import { Fragment, memo, type ReactNode } from 'react';
import { Link } from 'react-router';

// 轻量 Markdown（实施方案 M1-F04：不引入 react-markdown，避免包体增长）：段落、标题、列表、
// 粗体、行内代码、代码块、表格；对象引用「调价 #88」「渠道 #41」等渲染为主区导航链接。
// 只生成 React 元素，不使用 innerHTML（模型输出不可信）。

const OBJECT_LINKS: Array<{ re: RegExp; href: (id: string) => string }> = [
  { re: /^调价$/, href: (id) => `/pricing/changes?id=${id}` },
  { re: /^待上架$/, href: () => `/pricing/listings` },
  { re: /^优惠$/, href: () => `/pricing/offers` },
  { re: /^(模型|虚拟模型)$/, href: (id) => `/models/${id}` },
  { re: /^渠道$/, href: (id) => `/channels/${id}` },
  { re: /^数据源$/, href: () => `/pricing/sources` },
  { re: /^账户$/, href: (id) => `/accounts/${id}` },
  { re: /^供应商$/, href: (id) => `/providers/${id}` },
  { re: /^基准运行$/, href: () => `/benchmarks` },
];

const OBJ_RE = /(调价|待上架|优惠|虚拟模型|模型|渠道|数据源|账户|供应商|基准运行)\s?#(\d+)/g;

// linkify 把文本中的对象引用替换为 <Link>（在左侧主区导航，Dock 保持打开）。
export function linkify(text: string, keyPrefix = ''): ReactNode[] {
  const out: ReactNode[] = [];
  let last = 0;
  for (const m of text.matchAll(OBJ_RE)) {
    const def = OBJECT_LINKS.find((d) => d.re.test(m[1]));
    if (!def) continue;
    if (m.index! > last) out.push(text.slice(last, m.index));
    out.push(
      <Link key={`${keyPrefix}l${m.index}`} to={def.href(m[2])} className="text-purple-700 hover:underline font-medium">
        {m[0]}
      </Link>,
    );
    last = m.index! + m[0].length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

function inline(text: string, key: string): ReactNode[] {
  const out: ReactNode[] = [];
  // **粗体** 与 `代码`
  const re = /(\*\*[^*]+\*\*|`[^`]+`)/g;
  let last = 0;
  let i = 0;
  for (const m of text.matchAll(re)) {
    if (m.index! > last) out.push(...linkify(text.slice(last, m.index), `${key}t${i}`));
    const tok = m[0];
    if (tok.startsWith('**')) out.push(<strong key={`${key}b${i}`} className="font-semibold text-gray-900">{linkify(tok.slice(2, -2), `${key}bb${i}`)}</strong>);
    else out.push(<code key={`${key}c${i}`} className="font-mono text-[11px] bg-gray-100 text-gray-800 px-1 rounded">{tok.slice(1, -1)}</code>);
    last = m.index! + tok.length;
    i++;
  }
  if (last < text.length) out.push(...linkify(text.slice(last), `${key}e`));
  return out;
}

function splitRow(line: string): string[] {
  return line.trim().replace(/^\|/, '').replace(/\|$/, '').split('|').map((c) => c.trim());
}

export const Markdown = memo(function Markdown({ text }: { text: string }) {
  const lines = text.replace(/\r\n/g, '\n').split('\n');
  const blocks: ReactNode[] = [];
  let i = 0;
  let k = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (line.trim() === '') {
      i++;
      continue;
    }
    if (line.trim().startsWith('```')) {
      const code: string[] = [];
      i++;
      while (i < lines.length && !lines[i].trim().startsWith('```')) code.push(lines[i++]);
      i++;
      blocks.push(
        <pre key={k++} className="bg-gray-50 border border-gray-200 rounded-lg p-2.5 overflow-x-auto font-mono text-[11px] text-gray-800">
          {code.join('\n')}
        </pre>,
      );
      continue;
    }
    const h = /^(#{1,4})\s+(.*)$/.exec(line);
    if (h) {
      blocks.push(
        <div key={k++} className={h[1].length <= 2 ? 'text-[13px] font-semibold text-gray-900 mt-1' : 'text-xs font-semibold text-gray-800 mt-1'}>
          {inline(h[2], `h${k}`)}
        </div>,
      );
      i++;
      continue;
    }
    if (line.trim().startsWith('|') && i + 1 < lines.length && /^\s*\|?\s*:?-{2,}/.test(lines[i + 1])) {
      const head = splitRow(line);
      i += 2;
      const rows: string[][] = [];
      while (i < lines.length && lines[i].trim().startsWith('|')) rows.push(splitRow(lines[i++]));
      blocks.push(
        <div key={k++} className="overflow-x-auto border border-gray-200 rounded-lg">
          <table className="w-full text-[11px]">
            <thead className="bg-gray-50 text-gray-500">
              <tr>
                {head.map((c, j) => (
                  <th key={j} className="text-left font-medium px-2 py-1.5 whitespace-nowrap">
                    {inline(c, `th${k}${j}`)}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {rows.map((r, ri) => (
                <tr key={ri}>
                  {r.map((c, j) => (
                    <td key={j} className="px-2 py-1.5 text-gray-700 align-top">
                      {inline(c, `td${k}${ri}${j}`)}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>,
      );
      continue;
    }
    if (/^\s*([-*]|\d+\.)\s+/.test(line)) {
      const ordered = /^\s*\d+\./.test(line);
      const items: string[] = [];
      while (i < lines.length && /^\s*([-*]|\d+\.)\s+/.test(lines[i])) items.push(lines[i++].replace(/^\s*([-*]|\d+\.)\s+/, ''));
      const List = ordered ? 'ol' : 'ul';
      blocks.push(
        <List key={k++} className={ordered ? 'list-decimal pl-5 space-y-0.5' : 'list-disc pl-5 space-y-0.5'}>
          {items.map((it, j) => (
            <li key={j}>{inline(it, `li${k}${j}`)}</li>
          ))}
        </List>,
      );
      continue;
    }
    const para: string[] = [];
    while (i < lines.length && lines[i].trim() !== '' && !/^(#{1,4}\s|```|\s*([-*]|\d+\.)\s|\s*\|)/.test(lines[i])) para.push(lines[i++]);
    if (para.length === 0) para.push(lines[i++]);
    blocks.push(
      <p key={k++}>
        {para.map((p, j) => (
          <Fragment key={j}>
            {j > 0 && <br />}
            {inline(p, `p${k}${j}`)}
          </Fragment>
        ))}
      </p>,
    );
  }
  return <div className="space-y-2 text-xs leading-relaxed text-gray-800 break-words">{blocks}</div>;
});
