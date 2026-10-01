// MDX 正文 → 给人/给 LLM 看的纯 Markdown。页面上的"复制为 Markdown"和构建期
// 生成的 llms.txt 共用这一个函数（后者由 tools/docsPlugin.ts 引用），所以这里
// 只能是纯字符串处理，不能依赖浏览器或 Node API。

export const BASE_URL_PLACEHOLDER = '{{BASE_URL}}';

export function replaceBaseURL(text: string, origin: string): string {
  return text.split(BASE_URL_PLACEHOLDER).join(origin.replace(/\/$/, ''));
}

export function mdxToMarkdown(title: string, description: string, body: string, origin: string): string {
  const lines = body.replace(/\r\n/g, '\n').split('\n');
  const out: string[] = [];
  let inFence = false;
  for (const line of lines) {
    if (/^\s*```/.test(line)) {
      inFence = !inFence;
      // 代码块的 title="..." 元信息对纯 Markdown 没有意义，去掉只留语言。
      out.push(line.replace(/^(\s*```\w*).*$/, '$1'));
      continue;
    }
    if (inFence) {
      out.push(line);
      continue;
    }
    const trimmed = line.trim();
    const titled = /^<(Callout|Step)\b[^>]*\btitle="([^"]*)"[^>]*>$/.exec(trimmed);
    if (titled) {
      out.push(`**${titled[2]}**`);
      continue;
    }
    if (/^<LiveModelList\s*\/>$/.test(trimmed)) {
      out.push(`(Live model list: ${BASE_URL_PLACEHOLDER}/v1/catalog)`);
      continue;
    }
    if (/^<ErrorCodeTable\s*\/>$/.test(trimmed)) {
      out.push(`(Error code table: see ${BASE_URL_PLACEHOLDER}/docs/en/errors)`);
      continue;
    }
    // 其余单独成行的组件标签（<Steps>、</Callout>、<CodeTabs> 等）直接丢掉。
    if (/^<\/?[A-Z]\w*[^>]*>$/.test(trimmed)) continue;
    out.push(line);
  }
  const md = `# ${title}\n\n${description ? `> ${description}\n\n` : ''}${out.join('\n').replace(/\n{3,}/g, '\n\n').trim()}\n`;
  return replaceBaseURL(md, origin);
}
