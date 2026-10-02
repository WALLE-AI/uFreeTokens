import { createContext, useContext } from 'react';

export const LOCALES = ['zh', 'en'] as const;
export type Locale = (typeof LOCALES)[number];

const STORAGE_KEY = 'uft.docs.lang';

export function isLocale(v: string | undefined): v is Locale {
  return v === 'zh' || v === 'en';
}

// resolveLocale 决定访问不带语言前缀的 /docs 时去哪个语言：
// 上次手动选过的 > 浏览器语言（zh* → zh，其余 → en）。URL 里显式带了语言时
// 不走这里，URL 优先。
export function resolveLocale(): Locale {
  try {
    const saved = window.localStorage.getItem(STORAGE_KEY) ?? undefined;
    if (isLocale(saved)) return saved;
  } catch {
    // localStorage 不可用时按浏览器语言走。
  }
  return navigator.language?.toLowerCase().startsWith('zh') ? 'zh' : 'en';
}

export function rememberLocale(locale: Locale): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, locale);
  } catch {
    // ignore
  }
}

const UI = {
  zh: {
    docs: '开发文档',
    apiReference: 'API 参考',
    search: '搜索文档',
    searchPlaceholder: '搜索文档、接口、错误码…',
    searchEmpty: '没有找到相关内容',
    searchHint: '↑↓ 选择 · Enter 打开 · Esc 关闭',
    backToModels: '模型库',
    menu: '目录',
    onThisPage: '本页内容',
    previous: '上一篇',
    next: '下一篇',
    copyMarkdown: '复制为 Markdown',
    copied: '已复制',
    copy: '复制',
    notTranslated: '此页面尚未翻译',
    notTranslatedBody: '以下为中文原文。',
    outdatedTranslation: '译文可能已过期',
    outdatedTranslationBody: '中文原文在翻译后有更新，内容以中文版为准。',
    readSource: '查看中文原文',
    notFound: '页面不存在',
    notFoundBody: '这个地址没有对应的文档，可能已被移动或删除。',
    goHome: '返回文档首页',
    loading: '加载中…',
    loadFailed: '加载失败，请刷新重试',
    'section.getting-started': '入门',
    'section.guides': '指南',
    'section.integrations': '集成',
    'section.changelog': '更新日志',
    'section.api': '概述',
    notImplemented: '尚未实现',
    notImplementedBody: '该接口已预留路径，当前调用会返回 503 not_implemented。',
    authNone: '无需鉴权',
    authApiKey: '需要 API Key',
    billable: '计费',
    rateLimit: '限流',
    parameters: '查询参数',
    requestBody: '请求体',
    responses: '响应',
    errors: '可能的错误',
    required: '必填',
    passthrough: '其余字段原样透传给上游。',
    enumValues: '可选值',
    defaultValue: '默认值',
    responseHeaders: '响应头',
    tryIt: '在线调试',
    send: '发送请求',
    sending: '请求中…',
    abort: '中止',
    apiKey: 'API Key',
    apiKeyConnected: '已使用你在本站连接的 Key',
    apiKeyPlaceholder: 'sk-uft-…（只保存在当前页面内存中）',
    apiKeyMissing: '请先填写 API Key',
    billableWarning: '这是计费接口，真实调用会从你的余额扣费。',
    body: '请求体 (JSON)',
    formFields: '表单字段 (JSON)',
    uploadFile: '上传文件',
    fileMissing: '请先选择要上传的文件',
    audioResponse: '音频',
    imagePreview: '图片预览',
    invalidJson: '请求体不是合法的 JSON',
    response: '响应',
    duration: '耗时',
    requestId: '请求 ID',
    noResponse: '发送请求后在这里查看响应',
    networkError: '网络错误：请求没有到达网关',
    errorDoc: '查看错误说明',
    code: '错误码',
    httpStatus: 'HTTP 状态',
    retryable: '可重试',
    description: '说明',
    yes: '是',
    no: '否',
    liveModelsEmpty: '暂无可用模型',
    liveModelsFailed: '模型列表加载失败',
    contextWindow: '上下文',
    capabilities: '能力',
    priceIn: '输入（元/1M）',
    priceOut: '输出（元/1M）',
    language: 'Language',
  },
  en: {
    docs: 'Docs',
    apiReference: 'API Reference',
    search: 'Search docs',
    searchPlaceholder: 'Search docs, endpoints, error codes…',
    searchEmpty: 'No results found',
    searchHint: '↑↓ navigate · Enter open · Esc close',
    backToModels: 'Models',
    menu: 'Menu',
    onThisPage: 'On this page',
    previous: 'Previous',
    next: 'Next',
    copyMarkdown: 'Copy as Markdown',
    copied: 'Copied',
    copy: 'Copy',
    notTranslated: 'This page is not yet translated',
    notTranslatedBody: 'The Chinese original is shown below.',
    outdatedTranslation: 'This translation may be outdated',
    outdatedTranslationBody: 'The Chinese source has changed since this page was translated; the Chinese version is authoritative.',
    readSource: 'Read the Chinese source',
    notFound: 'Page not found',
    notFoundBody: 'There is no document at this address. It may have been moved or removed.',
    goHome: 'Back to docs home',
    loading: 'Loading…',
    loadFailed: 'Failed to load, please refresh',
    'section.getting-started': 'Getting started',
    'section.guides': 'Guides',
    'section.integrations': 'Integrations',
    'section.changelog': 'Changelog',
    'section.api': 'Overview',
    notImplemented: 'Not implemented',
    notImplementedBody: 'This path is reserved; calling it currently returns 503 not_implemented.',
    authNone: 'No auth',
    authApiKey: 'API key required',
    billable: 'Billable',
    rateLimit: 'Rate limit',
    parameters: 'Query parameters',
    requestBody: 'Request body',
    responses: 'Responses',
    errors: 'Possible errors',
    required: 'required',
    passthrough: 'Other fields are passed through to the upstream unchanged.',
    enumValues: 'Allowed values',
    defaultValue: 'Default',
    responseHeaders: 'Response headers',
    tryIt: 'Try it',
    send: 'Send request',
    sending: 'Sending…',
    abort: 'Abort',
    apiKey: 'API key',
    apiKeyConnected: 'Using the key you connected on this site',
    apiKeyPlaceholder: 'sk-uft-… (kept in page memory only)',
    apiKeyMissing: 'Enter an API key first',
    billableWarning: 'This endpoint is billable; a real call is charged to your balance.',
    body: 'Request body (JSON)',
    formFields: 'Form fields (JSON)',
    uploadFile: 'Upload file',
    fileMissing: 'Choose a file to upload first',
    audioResponse: 'Audio',
    imagePreview: 'Image preview',
    invalidJson: 'Request body is not valid JSON',
    response: 'Response',
    duration: 'Time',
    requestId: 'Request ID',
    noResponse: 'Send a request to see the response here',
    networkError: 'Network error: the request did not reach the gateway',
    errorDoc: 'See error reference',
    code: 'Code',
    httpStatus: 'HTTP status',
    retryable: 'Retryable',
    description: 'Description',
    yes: 'Yes',
    no: 'No',
    liveModelsEmpty: 'No models available',
    liveModelsFailed: 'Failed to load the model list',
    contextWindow: 'Context',
    capabilities: 'Capabilities',
    priceIn: 'Input (CNY/1M)',
    priceOut: 'Output (CNY/1M)',
    language: '语言',
  },
} satisfies Record<Locale, Record<string, string>>;

// 中英文 key 必须完全一致：英文少一条，这里的赋值就过不了 tsc。
export type UIKey = keyof (typeof UI)['zh'];
const _enHasAllKeys: Record<UIKey, string> = UI.en;
void _enHasAllKeys;

export const LocaleContext = createContext<Locale>('zh');

export function useLocale(): Locale {
  return useContext(LocaleContext);
}

export function useT(): (key: UIKey) => string {
  const locale = useLocale();
  return (key) => UI[locale][key];
}

// pick 从 OpenAPI 的 {en, zh} 结构里取当前语言，缺失时回退英文。
export function pick(text: { en?: string; zh?: string } | undefined, locale: Locale): string {
  if (!text) return '';
  return text[locale] || text.en || '';
}
