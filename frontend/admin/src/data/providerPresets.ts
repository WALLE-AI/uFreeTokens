import type { Protocol } from '../types';

// 供应商预设：新建供应商时可直接从下拉里挑选，自动带出 code / 名称 / 协议 /
// Base URL / 申请密钥的地址。主体来自 opensource/freellmapi（MIT）的 provider
// 注册表（server/src/providers/index.ts + client/src/components/keys/shared.tsx），
// 另补了几家 freellmapi 没收录、但国内运营常用的主流厂商（source: 'extra'）。
//
// 预设只是"填表模板"——落库的仍然是普通供应商记录，code 与预设 id 相同，
// 图标按 code 反查（见 providerIcons.ts），不需要后端额外字段。

export type PresetGroup = 'global' | 'cn' | 'router' | 'community';

export interface ProviderPreset {
  id: string; // 同时作为供应商 code
  name: string;
  protocol: Protocol;
  baseUrl: string; // 含 {account_id} 之类占位符时需要用户替换
  keyUrl?: string; // 申请 / 管理 API Key 的页面
  group: PresetGroup;
  icon?: string; // public/icons/providers 下的文件名（含扩展名）
  keyless?: boolean; // 上游不强制密钥
  supported?: false; // 协议不兼容本网关（例如只有 Responses API / 只做 TTS），列出但不可选
  note?: string;
  source?: 'extra';
}

export const PRESET_GROUPS: Array<{ value: PresetGroup; label: string }> = [
  { value: 'global', label: '海外厂商 / 云平台' },
  { value: 'cn', label: '国内厂商' },
  { value: 'router', label: '聚合路由' },
  { value: 'community', label: '免费额度 / 社区' },
];

export const PROVIDER_PRESETS: ProviderPreset[] = [
  // ---------- 海外厂商 / 云平台 ----------
  { id: 'openai', name: 'OpenAI', protocol: 'openai', baseUrl: 'https://api.openai.com/v1', keyUrl: 'https://platform.openai.com/api-keys', group: 'global', icon: 'chatgpt.svg', source: 'extra' },
  { id: 'anthropic', name: 'Anthropic', protocol: 'anthropic', baseUrl: 'https://api.anthropic.com/v1', keyUrl: 'https://console.anthropic.com/settings/keys', group: 'global', icon: 'anthropic.svg', source: 'extra' },
  { id: 'google', name: 'Google AI Studio', protocol: 'gemini', baseUrl: 'https://generativelanguage.googleapis.com/v1beta', keyUrl: 'https://aistudio.google.com/apikey', group: 'global', icon: 'google-gemini.svg' },
  { id: 'xai', name: 'xAI', protocol: 'openai', baseUrl: 'https://api.x.ai/v1', keyUrl: 'https://console.x.ai', group: 'global', icon: 'xai.svg', source: 'extra' },
  { id: 'mistral', name: 'Mistral', protocol: 'openai', baseUrl: 'https://api.mistral.ai/v1', keyUrl: 'https://console.mistral.ai/api-keys/', group: 'global', icon: 'mistral-ai.svg' },
  { id: 'cohere', name: 'Cohere', protocol: 'openai', baseUrl: 'https://api.cohere.ai/compatibility/v1', keyUrl: 'https://dashboard.cohere.com/api-keys', group: 'global', icon: 'cohere.svg', note: 'OpenAI 兼容端点' },
  { id: 'groq', name: 'Groq', protocol: 'openai', baseUrl: 'https://api.groq.com/openai/v1', keyUrl: 'https://console.groq.com/keys', group: 'global', icon: 'groq.svg' },
  { id: 'cerebras', name: 'Cerebras', protocol: 'openai', baseUrl: 'https://api.cerebras.ai/v1', keyUrl: 'https://cloud.cerebras.ai', group: 'global', icon: 'cerebras.svg' },
  { id: 'nvidia', name: 'NVIDIA NIM', protocol: 'openai', baseUrl: 'https://integrate.api.nvidia.com/v1', keyUrl: 'https://build.nvidia.com/settings/api-keys', group: 'global', icon: 'nvidia-ai.svg', note: '单次只支持一个工具调用' },
  { id: 'github', name: 'GitHub Models', protocol: 'openai', baseUrl: 'https://models.github.ai/inference', keyUrl: 'https://github.com/settings/tokens', group: 'global', note: '密钥为 GitHub Personal Access Token' },
  {
    id: 'cloudflare',
    name: 'Cloudflare Workers AI',
    protocol: 'openai',
    baseUrl: 'https://api.cloudflare.com/client/v4/accounts/{account_id}/ai/v1',
    keyUrl: 'https://dash.cloudflare.com',
    group: 'global',
    icon: 'cloudflare-workers-ai.svg',
    note: 'Base URL 里的 {account_id} 需替换为你的 Cloudflare 账号 ID；密钥填 API Token',
  },
  { id: 'huggingface', name: 'HuggingFace Router', protocol: 'openai', baseUrl: 'https://router.huggingface.co/v1', keyUrl: 'https://huggingface.co/settings/tokens', group: 'global', icon: 'hugging-face.svg' },
  { id: 'ollama', name: 'Ollama Cloud', protocol: 'openai', baseUrl: 'https://ollama.com/v1', keyUrl: 'https://ollama.com/settings/keys', group: 'global', icon: 'ollama.svg' },
  { id: 'together', name: 'Together AI', protocol: 'openai', baseUrl: 'https://api.together.xyz/v1', keyUrl: 'https://api.together.ai/settings/api-keys', group: 'global', icon: 'together-ai.svg', source: 'extra' },
  { id: 'fireworks', name: 'Fireworks AI', protocol: 'openai', baseUrl: 'https://api.fireworks.ai/inference/v1', keyUrl: 'https://fireworks.ai/account/api-keys', group: 'global', icon: 'fireworks-ai.svg', source: 'extra' },
  { id: 'deepinfra', name: 'DeepInfra', protocol: 'openai', baseUrl: 'https://api.deepinfra.com/v1/openai', keyUrl: 'https://deepinfra.com/dash/api_keys', group: 'global', icon: 'deepinfra.svg', source: 'extra' },
  { id: 'reka', name: 'Reka', protocol: 'openai', baseUrl: 'https://api.reka.ai/v1', keyUrl: 'https://platform.reka.ai', group: 'global', icon: 'reka-ai.svg', note: '预付费额度' },
  { id: 'siliconflow', name: 'SiliconFlow（国际站）', protocol: 'openai', baseUrl: 'https://api.siliconflow.com/v1', keyUrl: 'https://siliconflow.com', group: 'global', icon: 'siliconflow.svg' },
  { id: 'ovh', name: 'OVH AI Endpoints', protocol: 'openai', baseUrl: 'https://oai.endpoints.kepler.ai.cloud.ovh.net/v1', keyUrl: 'https://endpoints.ai.cloud.ovh.net', group: 'global', keyless: true, note: '无需密钥（匿名限流）' },

  // ---------- 国内厂商 ----------
  { id: 'deepseek', name: 'DeepSeek', protocol: 'openai', baseUrl: 'https://api.deepseek.com/v1', keyUrl: 'https://platform.deepseek.com/api_keys', group: 'cn', icon: 'deepseek.svg', source: 'extra' },
  { id: 'moonshot', name: 'Moonshot（Kimi）', protocol: 'openai', baseUrl: 'https://api.moonshot.cn/v1', keyUrl: 'https://platform.moonshot.cn/console/api-keys', group: 'cn', icon: 'moonshot-ai.svg', source: 'extra' },
  { id: 'dashscope', name: '阿里云百炼', protocol: 'openai', baseUrl: 'https://dashscope.aliyuncs.com/compatible-mode/v1', keyUrl: 'https://bailian.console.aliyun.com/?apiKey=1', group: 'cn', icon: 'alibaba-cloud-model-studio.svg', source: 'extra' },
  { id: 'minimax', name: 'MiniMax', protocol: 'openai', baseUrl: 'https://api.minimaxi.com/v1', keyUrl: 'https://platform.minimaxi.com', group: 'cn', icon: 'minimax.svg', source: 'extra' },
  { id: 'zhipu', name: '智谱 AI（Z.ai）', protocol: 'openai', baseUrl: 'https://open.bigmodel.cn/api/paas/v4', keyUrl: 'https://open.bigmodel.cn/usercenter/apikeys', group: 'cn', icon: 'zhipu-ai.svg', note: '海外控制台 Key 请改用 https://api.z.ai/api/paas/v4' },
  { id: 'qianfan', name: '百度千帆', protocol: 'openai', baseUrl: 'https://qianfan.baidubce.com/v2', keyUrl: 'https://console.bce.baidu.com/qianfan/overview', group: 'cn', icon: 'baidu-qianfan.svg', note: '需实名认证；ERNIE 部分模型免费' },
  { id: 'volcengine', name: '火山方舟', protocol: 'openai', baseUrl: 'https://ark.cn-beijing.volces.com/api/v3', keyUrl: 'https://console.volcengine.com/ark', group: 'cn', icon: 'bytedance-doubao.svg', note: '需实名认证；每日免费额度' },
  { id: 'longcat', name: '美团 LongCat', protocol: 'openai', baseUrl: 'https://api.longcat.chat/openai/v1', keyUrl: 'https://longcat.chat/platform', group: 'cn', icon: 'meituan-longcat.svg', note: '每日免费额度，邮箱即可注册' },
  { id: 'xfyun', name: '讯飞星火', protocol: 'openai', baseUrl: 'https://spark-api-open.xf-yun.com/v1', keyUrl: 'https://console.xfyun.cn', group: 'cn', icon: 'iflytek-spark.svg', note: '密钥填控制台的 APIPassword；Lite 免费' },
  { id: 'modelscope', name: 'ModelScope 魔搭', protocol: 'openai', baseUrl: 'https://api-inference.modelscope.cn/v1', keyUrl: 'https://modelscope.cn/my/myaccesstoken', group: 'cn', note: '需绑定阿里云账号' },
  { id: 'radeon', name: 'AMD Radeon Cloud', protocol: 'openai', baseUrl: 'https://developer.amd.com.cn/radeon/api/v1', keyUrl: 'https://developer.amd.com.cn/radeon/tokenfactory', group: 'cn', note: '免费共享模型；单次只支持一个工具调用，响应较慢' },

  // ---------- 聚合路由 ----------
  { id: 'openrouter', name: 'OpenRouter', protocol: 'openai', baseUrl: 'https://openrouter.ai/api/v1', keyUrl: 'https://openrouter.ai/keys', group: 'router', icon: 'openrouter.svg' },
  { id: 'requesty', name: 'Requesty', protocol: 'openai', baseUrl: 'https://router.requesty.ai/v1', keyUrl: 'https://www.requesty.ai', group: 'router' },
  { id: 'routeway', name: 'Routeway', protocol: 'openai', baseUrl: 'https://api.routeway.ai/v1', keyUrl: 'https://routeway.ai', group: 'router', note: '上游会校验浏览器 User-Agent' },
  { id: 'bazaarlink', name: 'BazaarLink', protocol: 'openai', baseUrl: 'https://bazaarlink.ai/api/v1', keyUrl: 'https://bazaarlink.ai', group: 'router' },
  { id: 'nara', name: 'NaraRouter', protocol: 'openai', baseUrl: 'https://router.bynara.id/v1', keyUrl: 'https://router.bynara.id', group: 'router' },
  { id: 'orcarouter', name: 'OrcaRouter', protocol: 'openai', baseUrl: 'https://api.orcarouter.ai/v1', keyUrl: 'https://www.orcarouter.ai', group: 'router' },
  { id: 'unorouter', name: 'UnoRouter', protocol: 'openai', baseUrl: 'https://api.unorouter.com/v1', keyUrl: 'https://unorouter.com', group: 'router' },
  { id: 'anyapi', name: 'AnyAPI', protocol: 'openai', baseUrl: 'https://api.anyapi.ai/v1', keyUrl: 'https://anyapi.ai', group: 'router', note: '宣传有免费额度，但实测免费层不稳定' },
  { id: 'electronhub', name: 'ElectronHub', protocol: 'openai', baseUrl: 'https://api.electronhub.ai/v1', keyUrl: 'https://app.electronhub.ai', group: 'router', note: '每周共享免费额度' },
  { id: 'router9', name: 'Router9', protocol: 'openai', baseUrl: 'https://api.router9.com/v1', keyUrl: 'https://www.router9.com', group: 'router', note: '每月共享免费额度' },
  { id: 'kilo', name: 'Kilo Gateway', protocol: 'openai', baseUrl: 'https://api.kilo.ai/api/gateway/v1', keyUrl: 'https://app.kilo.ai', group: 'router', keyless: true, note: '无需密钥' },
  { id: 'opencode', name: 'OpenCode Zen', protocol: 'openai', baseUrl: 'https://opencode.ai/zen/v1', keyUrl: 'https://opencode.ai/auth', group: 'router', icon: 'opencode.png', note: '密钥免费，仅付费模型计费' },
  { id: 'llmtr', name: 'LLMTR', protocol: 'openai', baseUrl: 'https://llmtr.com/v1', keyUrl: 'https://llmtr.com', group: 'router', note: '免费模型每日配额' },
  { id: 'navy', name: 'NavyAI', protocol: 'openai', baseUrl: 'https://api.navy/v1', keyUrl: 'https://api.navy', group: 'router', note: '上游会校验 User-Agent' },
  { id: 'xkiro', name: 'xKiro', protocol: 'openai', baseUrl: 'https://api.xkiro.com/v1', keyUrl: 'https://xkiro.com', group: 'router' },
  { id: 'clod', name: 'CLōD', protocol: 'openai', baseUrl: 'https://api.clod.io/v1', keyUrl: 'https://newapp.clod.io', group: 'router', note: '每日共享免费请求' },
  { id: 'blaze', name: 'BlazeAPI', protocol: 'openai', baseUrl: 'https://api.blazeapi.org/paid/v1', keyUrl: 'https://blazeapi.org/dashboard', group: 'router', note: '每日免费 token，需 Discord 验证' },
  { id: 'lucidity', name: 'Lucidity Composite', protocol: 'openai', baseUrl: 'https://composite.lucidity.sh/v1', keyUrl: 'https://composite.lucidity.sh', group: 'router', note: '免费模型每日请求数' },
  { id: 'airforce', name: 'Api.Airforce', protocol: 'openai', baseUrl: 'https://api.airforce/v1', keyUrl: 'https://api.airforce', group: 'router', note: '每日免费请求，限 1 次/分钟' },
  { id: 'ainative', name: 'AINative Studio', protocol: 'openai', baseUrl: 'https://api.ainative.studio/api/v1', keyUrl: 'https://ainative.studio', group: 'router' },

  // ---------- 免费额度 / 社区 ----------
  { id: 'llm7', name: 'LLM7', protocol: 'openai', baseUrl: 'https://api.llm7.io/v1', keyUrl: 'https://llm7.io', group: 'community', note: '匿名可用' },
  { id: 'pollinations', name: 'Pollinations', protocol: 'openai', baseUrl: 'https://gen.pollinations.ai/v1', keyUrl: 'https://enter.pollinations.ai', group: 'community' },
  { id: 'aihorde', name: 'AI Horde', protocol: 'openai', baseUrl: 'https://oai.aihorde.net/v1', keyUrl: 'https://aihorde.net/register', group: 'community', keyless: true, note: '匿名密钥 0000000000，排队较慢' },
  { id: 'agnes', name: 'Agnes AI', protocol: 'openai', baseUrl: 'https://apihub.agnes-ai.com/v1', keyUrl: 'https://platform.agnes-ai.com', group: 'community' },
  { id: 'aion', name: 'Aion Labs', protocol: 'openai', baseUrl: 'https://api.aionlabs.ai/v1', keyUrl: 'https://www.aionlabs.ai', group: 'community' },
  { id: 'sealion', name: 'SEA-LION', protocol: 'openai', baseUrl: 'https://api.sea-lion.ai/v1', keyUrl: 'https://sea-lion.ai', group: 'community' },
  { id: 'bai', name: 'B.AI', protocol: 'openai', baseUrl: 'https://api.b.ai/v1', keyUrl: 'https://b.ai', group: 'community', note: '推广期免费模型' },
  { id: 'logfare', name: 'Logfare', protocol: 'openai', baseUrl: 'https://logfare.ai/v1', keyUrl: 'https://logfare.ai', group: 'community', note: '合理使用免费模型' },
  { id: 'waterfall', name: 'Waterfall', protocol: 'openai', baseUrl: 'https://api.getwaterfall.org/v1', keyUrl: 'https://getwaterfall.org', group: 'community', note: '社区免费模型' },
  { id: 'dreamprompting', name: 'DreamPrompting', protocol: 'openai', baseUrl: 'https://dreamprompting.com/api/v1', keyUrl: 'https://dreamprompting.com', group: 'community', note: '滚动 24 小时免费额度' },
  { id: 'septor', name: 'Septor Labs', protocol: 'openai', baseUrl: 'https://api.septorlabs.com/v1', keyUrl: 'https://septorlabs.com/dashboard', group: 'community', note: '免费模型每日配额' },
  { id: 'experiential', name: 'Experiential Labs', protocol: 'openai', baseUrl: 'https://api.experientiallabs.ai/v1', keyUrl: 'https://platform.experientiallabs.ai', group: 'community', note: '每月共享免费额度' },
  { id: 'speka', name: 'Speka', protocol: 'openai', baseUrl: 'https://speka.me/v1', keyUrl: 'https://speka.me/dashboard/keys', group: 'community', note: '每月 $1 共享额度' },
  { id: 'moondream', name: 'Moondream', protocol: 'openai', baseUrl: 'https://api.moondream.ai/v1', keyUrl: 'https://moondream.ai/c/cloud/api-keys', group: 'community', supported: false, note: '私有视觉 API，非标准 Chat Completions' },
  { id: 'sail', name: 'Sail Research', protocol: 'openai', baseUrl: 'https://api.sailresearch.com/v1', keyUrl: 'https://app.sailresearch.com', group: 'community', supported: false, note: '仅支持 Responses API' },
  { id: 'aclide', name: 'ACLIDE', protocol: 'openai', baseUrl: 'https://aclide.com/v1', keyUrl: 'https://aclide.com/en/dashboard/api-keys', group: 'community', supported: false, note: '仅支持 Responses API' },
  { id: 'speechify', name: 'Speechify', protocol: 'openai', baseUrl: 'https://api.speechify.ai/v1', keyUrl: 'https://platform.speechify.ai', group: 'community', supported: false, note: '仅文字转语音（TTS）' },
];

const BY_ID = new Map(PROVIDER_PRESETS.map((p) => [p.id, p]));

export function findPreset(code: string | null | undefined): ProviderPreset | undefined {
  return code ? BY_ID.get(code.trim().toLowerCase()) : undefined;
}

// hasPlaceholder：Base URL 里还有没替换的 {xxx} 占位符
export function hasPlaceholder(url: string): boolean {
  return /\{[^}]+\}/.test(url);
}
