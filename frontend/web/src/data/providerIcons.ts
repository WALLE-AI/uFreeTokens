// 厂商图标资源来自 https://getllms.org/ai-model-icons/gallery（SVG，按各自
// 品牌方的通常用法收录——使用前留意该站点的提示："check the brand owner
// rules before using any icon in a public, commercial, or app-store
// context"）。文件存在 public/icons/providers/ 下，构建时按静态资源处理，
// 用 /icons/providers/<slug>.svg 直接引用，不走 import（148 个文件没必要
// 各自走一次模块打包）。
//
// PROVIDER_ICON_SLUGS 是实际下载下来的文件名（不含扩展名）。
export const PROVIDER_ICON_SLUGS = [
  'openai', 'chatgpt', 'anthropic', 'claude', 'google-gemini', 'google-deepmind',
  'meta-ai', 'xai', 'mistral-ai', 'cohere', 'perplexity', 'deepseek', 'alibaba-qwen',
  'zhipu-ai', 'z-ai', 'glm', 'moonshot-ai', 'baichuan-ai', 'minimax', '01-ai',
  'bytedance-doubao', 'baidu-ernie', 'tencent-hunyuan', 'huawei-pangu', 'stability-ai',
  'midjourney', 'runway', 'elevenlabs', 'hugging-face', 'replicate', 'together-ai',
  'fireworks-ai', 'groq', 'ollama', 'lm-studio', 'ai21-labs', 'aleph-alpha',
  'inflection-ai', 'reka-ai', 'liquid-ai', 'arcee-ai', 'nvidia-ai', 'microsoft-ai',
  'microsoft-copilot', 'amazon-bedrock', 'apple-intelligence', 'ibm-watsonx',
  'salesforce-ai', 'databricks', 'snowflake-arctic', 'cerebras', 'sambanova',
  'character-ai', 'adept-ai', 'poolside', 'magic', 'replit-ai', 'tabnine', 'codeium',
  'black-forest-labs', 'ideogram', 'luma-ai', 'pika', 'kling-ai', 'higgsfield',
  'adobe-firefly', 'canva-ai', 'suno', 'udio', 'deepgram', 'assemblyai', 'cartesia',
  'playht', 'respeecher', 'siliconflow', 'deepinfra', 'novita-ai', 'baseten', 'modal',
  'fal-ai', 'cloudflare-workers-ai', 'nebius-ai', 'parasail', 'friendli-ai', 'clarifai',
  'openrouter', 'vertex-ai', 'azure-ai-foundry', 'baidu-qianfan',
  'alibaba-cloud-model-studio', 'stepfun', 'iflytek-spark', 'sensetime-sensechat',
  '360-zhinao', 'kunlun-tiangong', 'xiaomi-mimo', 'ant-ling', 'vivo-bluelm',
  'oppo-andesgpt', 'meituan-longcat', 'jd-chatrhino', 'shanghai-ai-lab', 'baai',
  'openbmb', 'fudan-moss', 'tii-falcon', 'g42', 'mbzuai', 'allen-ai', 'eleutherai',
  'bigscience', 'nous-research', 'nomic-ai', 'jina-ai', 'voyage-ai', 'upstage',
  'naver-hyperclova', 'skt-aix', 'rinna', 'sakana-ai', 'preferred-networks',
  'sarvam-ai', 'krutrim', 'ai4bharat', 'yandexgpt', 'sber-gigachat', 'writer',
  'lighton', 'phind', 'you-com', 'poe', 'morph-labs', 'grok', 'github-copilot',
  'gemini', 'sora', 'dall-e', 'qwen', 'kimi', 'doubao', 'cici-ai', 'hunyuan', 'ernie',
  'pangu', 'stable-diffusion', 'flux', 'llama', 'phi',
] as const;

const SLUG_SET = new Set<string>(PROVIDER_ICON_SLUGS);

// PROVIDER_ALIASES 把 model.provider / model.author / GET /v1/catalog 里
// "org/model" 的 org 前缀（常见 HuggingFace 风格命名）归一化到上面的某个
// slug。键是归一化后的字符串（小写、空白/点/下划线换成短横线），只收录
// 明确认识的厂商——认不出的一律返回 null，调用方回退到原来的纯色方块占位，
// 不强行猜测拼出一个可能张冠李戴的图标。
const PROVIDER_ALIASES: Record<string, string> = {
  // OpenAI
  openai: 'chatgpt', // chatgpt.svg 是方形品牌标记，openai.svg 是横向 wordmark，方块场景选前者
  'openai-org': 'chatgpt',
  chatgpt: 'chatgpt',
  gpt: 'chatgpt',
  // Anthropic
  anthropic: 'anthropic',
  claude: 'claude',
  // Google
  google: 'google-gemini',
  'google-ai': 'google-gemini',
  gemini: 'google-gemini',
  deepmind: 'google-deepmind',
  'google-deepmind': 'google-deepmind',
  // Meta
  meta: 'meta-ai',
  'meta-ai': 'meta-ai',
  'meta-llama': 'meta-ai',
  facebook: 'meta-ai',
  facebookresearch: 'meta-ai',
  llama: 'llama',
  // xAI
  xai: 'xai',
  'x-ai': 'xai',
  grok: 'grok',
  // Mistral
  mistral: 'mistral-ai',
  mistralai: 'mistral-ai',
  // Cohere
  cohere: 'cohere',
  cohereforai: 'cohere',
  // Perplexity
  perplexity: 'perplexity',
  'perplexity-ai': 'perplexity',
  // DeepSeek
  deepseek: 'deepseek',
  'deepseek-ai': 'deepseek',
  // Alibaba / Qwen
  qwen: 'alibaba-qwen',
  alibaba: 'alibaba-qwen',
  'alibaba-qwen': 'alibaba-qwen',
  'qwen-ai': 'alibaba-qwen',
  // Zhipu / GLM / Z.ai
  zhipu: 'zhipu-ai',
  zhipuai: 'zhipu-ai',
  'zhipu-ai': 'zhipu-ai',
  'z-ai': 'z-ai',
  'zai-org': 'z-ai',
  glm: 'glm',
  thudm: 'glm',
  // Moonshot / Kimi
  moonshot: 'moonshot-ai',
  moonshotai: 'moonshot-ai',
  kimi: 'kimi',
  // Baichuan
  baichuan: 'baichuan-ai',
  'baichuan-inc': 'baichuan-ai',
  // MiniMax
  minimax: 'minimax',
  minimaxai: 'minimax',
  // 01.AI
  '01-ai': '01-ai',
  '01ai': '01-ai',
  yi: '01-ai',
  // ByteDance / Doubao
  bytedance: 'bytedance-doubao',
  doubao: 'doubao',
  // Baidu
  baidu: 'baidu-ernie',
  ernie: 'ernie',
  // Tencent
  tencent: 'tencent-hunyuan',
  hunyuan: 'hunyuan',
  // Huawei
  huawei: 'huawei-pangu',
  pangu: 'pangu',
  // Stability AI
  stability: 'stability-ai',
  stabilityai: 'stability-ai',
  'stable-diffusion': 'stable-diffusion',
  // Black Forest Labs / FLUX
  'black-forest-labs': 'black-forest-labs',
  flux: 'flux',
  // Infra / inference providers
  huggingface: 'hugging-face',
  'hugging-face': 'hugging-face',
  hf: 'hugging-face',
  together: 'together-ai',
  togethercomputer: 'together-ai',
  fireworks: 'fireworks-ai',
  fireworksai: 'fireworks-ai',
  groq: 'groq',
  ollama: 'ollama',
  siliconflow: 'siliconflow',
  deepinfra: 'deepinfra',
  novita: 'novita-ai',
  openrouter: 'openrouter',
  replicate: 'replicate',
  cerebras: 'cerebras',
  sambanova: 'sambanova',
  nvidia: 'nvidia-ai',
  microsoft: 'microsoft-ai',
  amazon: 'amazon-bedrock',
  aws: 'amazon-bedrock',
  bedrock: 'amazon-bedrock',
  ibm: 'ibm-watsonx',
  watsonx: 'ibm-watsonx',
  databricks: 'databricks',
  // Others seen in mock/demo data
  sakana: 'sakana-ai',
  'sakana-ai': 'sakana-ai',
  poe: 'poe',
  midjourney: 'midjourney',
  runway: 'runway',
  elevenlabs: 'elevenlabs',
  suno: 'suno',
  udio: 'udio',
  ideogram: 'ideogram',
};

function normalize(raw: string): string {
  return raw
    .trim()
    .toLowerCase()
    .replace(/[._\s]+/g, '-')
    .replace(/-+/g, '-')
    .replace(/^-|-$/g, '');
}

// getProviderIconPath 返回一个厂商标识（provider/author/org 前缀，大小写、
// 分隔符不敏感）对应的图标静态资源路径；找不到已知映射时返回 null，调用方
// （ProviderIcon 组件）据此回退到纯色方块 + 字符占位。
export function getProviderIconPath(rawProvider: string): string | null {
  if (!rawProvider) return null;
  const key = normalize(rawProvider);
  const slug = PROVIDER_ALIASES[key] ?? (SLUG_SET.has(key) ? key : null);
  if (!slug) return null;
  return `/icons/providers/${slug}.svg`;
}
