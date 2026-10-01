import { Model, ModelScores } from '../types';
import { CatalogModel } from '../api/catalog';

export const INITIAL_MODELS: Model[] = [
  {
    id: 'deepseek/deepseek-v4.1-flash',
    name: 'DeepSeek: DeepSeek V4.1 Flash',
    provider: 'deepseek',
    providerDisplay: 'DeepSeek',
    author: 'deepseek',
    series: 'DeepSeek',
    description: 'DeepSeek V4.1 Flash is a sparse mixture-of-experts model from DeepSeek, and the first built on the company\'s Causal Encoder-Decoder (CED) architecture. It activates 8B parameters on input and 16B on output from a 552B-parameter backbone, an asymmetric split that keeps per-token compute low relative to the model capacity while achieving state-of-the-art inference efficiency.',
    iconBg: 'bg-blue-600 text-white',
    badge: null,
    date: 'Sep 10, 2026',
    releaseDate: '2026-09-10',
    contextTokens: 1048576,
    contextDisplay: '1.0M 上下文',
    maxOutputTokens: 16384,
    inputPricePerM: 0.15,
    outputPricePerM: 0.60,
    inputPriceDisplay: '$0.15 / 百万 Input Token',
    outputPriceDisplay: '$0.60 / 百万 Output Token',
    tokensDisplay: '2.19T tokens',
    modalities: ['text', 'file'],
    category: 'Reasoning',
    tags: ['text', 'reasoning', 'coding'],
    variants: ['standard', 'thinking'],
    hasDiscount: true,
    distillable: true,
    zeroDataRetention: true,
    inRegionRouting: ['US', 'EU'],
    supportedParameters: ['tools', 'temperature', 'top_p', 'json_object', 'response_format'],
    toolCallingCapability: 96,
    modelAgeMonths: 0,
    scores: {
      intelligenceIndex: 39.5,
      codingIndex: 82,
      agenticIndex: 68,
      designArena: {
        codeCategories: 1420,
        uiComponent: 1390,
        gameDev: 1380,
        dataViz: 1400,
        threeD: 1370,
        image: 1350,
        video: 1320,
        svg: 1340,
      }
    }
  },
  {
    id: 'union/union-alpha',
    name: 'Union Alpha',
    provider: 'union',
    providerDisplay: 'Union AI',
    author: 'union-labs',
    series: 'Other',
    description: 'Union Alpha 是由第三方提交的多模态模型，专为研究、编程和 Agent 工作流构建，在通用任务中提供前沿级性能。uFreeTokens 在测试期间将请求直接路由至第三方开发者。',
    iconBg: 'bg-black text-white',
    badge: null,
    date: '2026年9月16日',
    releaseDate: '2026-09-16',
    contextTokens: 262144,
    contextDisplay: '262K 上下文',
    maxOutputTokens: 8192,
    inputPricePerM: 0,
    outputPricePerM: 0,
    inputPriceDisplay: '$0 / 百万 Input Token',
    outputPriceDisplay: '$0 / 百万 Output Token',
    tokensDisplay: '373B tokens',
    modalities: ['text', 'image', 'file'],
    category: 'Programming',
    tags: ['text', 'image', 'agent', 'free'],
    variants: ['free', 'standard'],
    hasDiscount: false,
    distillable: true,
    zeroDataRetention: true,
    inRegionRouting: ['US', 'EU'],
    supportedParameters: ['tools', 'temperature', 'top_p', 'json_object'],
    toolCallingCapability: 95,
    modelAgeMonths: 1,
    scores: {
      intelligenceIndex: 52,
      codingIndex: 78,
      agenticIndex: 56,
      designArena: {
        codeCategories: 1370,
        uiComponent: 1365,
        gameDev: 1390,
        dataViz: 1340,
        threeD: 1410,
        image: 1360,
        video: 1850,
        svg: 1320,
      }
    }
  },
  {
    id: 'deepseek/deepseek-pro',
    name: 'DeepSeek: DeepSeek Pro 最新版',
    provider: 'deepseek',
    providerDisplay: 'DeepSeek',
    author: 'deepseek',
    series: 'DeepSeek',
    description: '该模型始终重定向至 DeepSeek Pro 系列中的最新模型。具备极高的数学推导与代码生成能力，适配复杂逻辑规划。',
    iconBg: 'bg-blue-600 text-white',
    badge: null,
    date: '2026年9月14日',
    releaseDate: '2026-09-14',
    contextTokens: 1048576,
    contextDisplay: '1.05M 上下文',
    maxOutputTokens: 16384,
    inputPricePerM: 0.70,
    outputPricePerM: 2.96,
    inputPriceDisplay: '$0.70 / 百万 Input Token',
    outputPriceDisplay: '$2.96 / 百万 Output Token',
    tokensDisplay: '128B tokens',
    modalities: ['text'],
    category: 'Programming',
    tags: ['text', 'coding', 'reasoning'],
    variants: ['standard', 'thinking'],
    hasDiscount: false,
    distillable: true,
    zeroDataRetention: false,
    inRegionRouting: ['US'],
    supportedParameters: ['tools', 'temperature', 'top_p', 'response_format'],
    toolCallingCapability: 92,
    modelAgeMonths: 1,
    scores: {
      intelligenceIndex: 54,
      codingIndex: 82,
      agenticIndex: 54,
      designArena: {
        codeCategories: 1387,
        uiComponent: 1389,
        gameDev: 1413,
        dataViz: 1366,
        threeD: 1432,
        image: 1385,
        video: 1920,
        svg: 1352,
      }
    }
  },
  {
    id: 'deepseek/deepseek-flash',
    name: 'DeepSeek: DeepSeek Flash 最新版',
    provider: 'deepseek',
    providerDisplay: 'DeepSeek',
    author: 'deepseek',
    series: 'DeepSeek',
    description: '该模型始终重定向至 DeepSeek Flash 系列中的最新模型。超低延迟与极具竞争力的性价比，适合高频对话与流式输出。',
    iconBg: 'bg-blue-600 text-white',
    badge: null,
    date: '2026年9月14日',
    releaseDate: '2026-09-14',
    contextTokens: 1048576,
    contextDisplay: '1.05M 上下文',
    maxOutputTokens: 8192,
    inputPricePerM: 0.15,
    outputPricePerM: 0.60,
    inputPriceDisplay: '$0.15 / 百万 Input Token',
    outputPriceDisplay: '$0.60 / 百万 Output Token',
    tokensDisplay: '89B tokens',
    modalities: ['text'],
    category: 'Roleplay',
    tags: ['text', 'fast', 'budget'],
    variants: ['standard', 'batch'],
    hasDiscount: false,
    distillable: true,
    zeroDataRetention: false,
    inRegionRouting: ['US'],
    supportedParameters: ['temperature', 'top_p', 'tools'],
    toolCallingCapability: 85,
    modelAgeMonths: 1,
    scores: {
      intelligenceIndex: 48,
      codingIndex: 72,
      agenticIndex: 49,
      designArena: {
        codeCategories: 1290,
        uiComponent: 1310,
        gameDev: 1340,
        dataViz: 1305,
        threeD: 1350,
        image: 1300,
        video: 1720,
        svg: 1280,
      }
    }
  },
  {
    id: 'inference/schematron-v2-turbo',
    name: 'Inference.net: Schematron V2 Turbo',
    provider: 'inference.net',
    providerDisplay: 'Inference.net',
    author: 'aion-labs',
    series: 'Other',
    description: 'Schematron V2 Turbo 是来自 Inference.net 的 3B 参数 HTML 转 JSON 提取模型。优先考虑高吞吐量的高并发抽取工作流。抽取指令必须通过 JSON Schema 作为 response_format 提供。',
    iconBg: 'bg-black text-white',
    badge: null,
    date: '2026年9月12日',
    releaseDate: '2026-09-12',
    contextTokens: 128000,
    contextDisplay: '128K 上下文',
    maxOutputTokens: 4096,
    inputPricePerM: 0.03,
    outputPricePerM: 0.15,
    inputPriceDisplay: '$0.03 / 百万 Input Token',
    outputPriceDisplay: '$0.15 / 百万 Output Token',
    tokensDisplay: '166M tokens',
    modalities: ['text', 'file'],
    category: 'Extraction',
    tags: ['text', 'extraction', 'fast'],
    variants: ['standard', 'batch'],
    hasDiscount: false,
    distillable: false,
    zeroDataRetention: true,
    inRegionRouting: ['US', 'EU'],
    supportedParameters: ['response_format', 'temperature'],
    toolCallingCapability: 70,
    modelAgeMonths: 1,
    scores: {
      intelligenceIndex: 39,
      codingIndex: 65,
      agenticIndex: 42,
      designArena: {
        codeCategories: 1190,
        uiComponent: 1220,
        gameDev: 1150,
        dataViz: 1240,
        threeD: 1100,
        image: 1050,
        video: 1400,
        svg: 1180,
      }
    }
  },
  {
    id: 'inference/schematron-v2-small',
    name: 'Inference.net: Schematron V2 Small',
    provider: 'inference.net',
    providerDisplay: 'Inference.net',
    author: 'aion-labs',
    series: 'Other',
    description: 'Schematron V2 Small 是来自 Inference.net 的 3B 参数 HTML 转 JSON 提取模型。针对复杂 Schema 和长页面的提取质量进行了专项优化。',
    iconBg: 'bg-black text-white',
    badge: null,
    date: '2026年9月12日',
    releaseDate: '2026-09-12',
    contextTokens: 128000,
    contextDisplay: '128K 上下文',
    maxOutputTokens: 4096,
    inputPricePerM: 0.05,
    outputPricePerM: 0.23,
    inputPriceDisplay: '$0.05 / 百万 Input Token',
    outputPriceDisplay: '$0.23 / 百万 Output Token',
    tokensDisplay: '54M tokens',
    modalities: ['text', 'file'],
    category: 'Extraction',
    tags: ['text', 'extraction'],
    variants: ['standard'],
    hasDiscount: false,
    distillable: false,
    zeroDataRetention: true,
    inRegionRouting: ['US', 'EU'],
    supportedParameters: ['response_format', 'temperature'],
    toolCallingCapability: 74,
    modelAgeMonths: 1,
    scores: {
      intelligenceIndex: 41,
      codingIndex: 68,
      agenticIndex: 44,
      designArena: {
        codeCategories: 1210,
        uiComponent: 1240,
        gameDev: 1170,
        dataViz: 1255,
        threeD: 1130,
        image: 1080,
        video: 1450,
        svg: 1200,
      }
    }
  },
  {
    id: 'meta/muse-voice-transcribe',
    name: 'Meta: Muse Voice Transcribe 1.0',
    provider: 'meta',
    providerDisplay: 'Meta AI',
    author: 'meta',
    series: 'Meta',
    description: 'Muse Voice Transcribe 1.0 是 Meta 推出的同步语音转文本模型。适用于 PTT、端点检测和说话人感知转写。具有针对领域词汇的关键字偏置功能。',
    iconBg: 'bg-blue-500 text-white',
    badge: '18+',
    badgeColor: 'bg-amber-100 text-amber-800 border-amber-200',
    date: '2026年9月11日',
    releaseDate: '2026-09-11',
    contextTokens: 64000,
    contextDisplay: null,
    maxOutputTokens: 2048,
    inputPricePerM: 0.18,
    outputPricePerM: 0,
    inputPriceDisplay: '$0.18 / 小时',
    outputPriceDisplay: null,
    isHourly: true,
    tokensDisplay: '3.33M 字符',
    modalities: ['audio'],
    category: 'Audio',
    tags: ['audio', 'transcription', 'voice'],
    variants: ['standard'],
    hasDiscount: false,
    distillable: true,
    zeroDataRetention: false,
    inRegionRouting: ['US'],
    supportedParameters: ['temperature'],
    toolCallingCapability: 20,
    modelAgeMonths: 1,
    scores: {
      intelligenceIndex: 35,
      codingIndex: 20,
      agenticIndex: 25,
      designArena: {
        codeCategories: 950,
        uiComponent: 920,
        gameDev: 910,
        dataViz: 980,
        threeD: 900,
        image: 950,
        video: 1100,
        svg: 910,
      }
    }
  },
  {
    id: 'openai/gpt-astra-latest',
    name: 'OpenAI: GPT Astra Latest',
    provider: 'openai',
    providerDisplay: 'OpenAI',
    author: 'openai',
    series: 'GPT',
    description: '该模型始终重定向至 GPT Astra 家族中的最新旗舰模型。全能旗舰多模态智能体，擅长前沿科学演算、复杂长代码架构设计与全域多工具协作。',
    iconBg: 'bg-emerald-600 text-white',
    badge: null,
    date: '2026年9月11日',
    releaseDate: '2026-09-11',
    contextTokens: 1048576,
    contextDisplay: '1.05M 上下文',
    maxOutputTokens: 32768,
    inputPricePerM: 10.00,
    outputPricePerM: 50.00,
    inputPriceDisplay: '$10 / 百万 Input Token',
    outputPriceDisplay: '$50 / 百万 Output Token',
    tokensDisplay: null,
    modalities: ['text', 'image', 'file', 'audio', 'video'],
    category: 'Reasoning',
    tags: ['text', 'image', 'video', 'audio', 'agent', 'multimodal'],
    variants: ['standard', 'extended', 'thinking'],
    hasDiscount: false,
    distillable: false,
    zeroDataRetention: true,
    inRegionRouting: ['US', 'EU'],
    supportedParameters: ['tools', 'temperature', 'top_p', 'json_object', 'seed'],
    toolCallingCapability: 100,
    modelAgeMonths: 1,
    scores: {
      intelligenceIndex: 54,
      codingIndex: 82,
      agenticIndex: 58,
      designArena: {
        codeCategories: 1387,
        uiComponent: 1389,
        gameDev: 1413,
        dataViz: 1366,
        threeD: 1432,
        image: 1385,
        video: 2000,
        svg: 1352,
      }
    }
  },
  {
    id: 'openai/gpt-sol-latest',
    name: 'OpenAI: GPT Sol Latest',
    provider: 'openai',
    providerDisplay: 'OpenAI',
    author: 'openai',
    series: 'GPT',
    description: '该模型始终重定向至 GPT Sol 家族中的最新模型。兼备出色的推理精度与日常高性价比，适合作为企业级主力智能体底座。',
    iconBg: 'bg-emerald-600 text-white',
    badge: '优惠 50%',
    badgeColor: 'bg-emerald-100 text-emerald-700 border-emerald-200',
    date: '2026年9月11日',
    releaseDate: '2026-09-11',
    contextTokens: 1048576,
    contextDisplay: '1.05M 上下文',
    maxOutputTokens: 16384,
    inputPricePerM: 2.00,
    outputPricePerM: 10.00,
    inputPriceDisplay: '$2 / 百万 Input Token',
    outputPriceDisplay: '$10 / 百万 Output Token',
    tokensDisplay: '15.4B tokens',
    modalities: ['text', 'image', 'file'],
    category: 'Programming',
    tags: ['text', 'image', 'discount'],
    variants: ['standard', 'extended', 'batch'],
    hasDiscount: true,
    discountPercent: 50,
    distillable: false,
    zeroDataRetention: true,
    inRegionRouting: ['US', 'EU'],
    supportedParameters: ['tools', 'temperature', 'top_p', 'json_object'],
    toolCallingCapability: 96,
    modelAgeMonths: 1,
    scores: {
      intelligenceIndex: 51,
      codingIndex: 79,
      agenticIndex: 55,
      designArena: {
        codeCategories: 1350,
        uiComponent: 1360,
        gameDev: 1380,
        dataViz: 1345,
        threeD: 1390,
        image: 1355,
        video: 1880,
        svg: 1330,
      }
    }
  },
  {
    id: 'sakana/fugu-ultra-v2',
    name: 'Sakana: Fugu Ultra v2',
    provider: 'sakana',
    providerDisplay: 'Sakana AI',
    author: 'sakana',
    series: 'Sakana',
    description: 'Fugu Ultra v2 是 Sakana AI Fugu 家族的高性能模型。不仅是单一单体模型，Fugu 还是一个自学习的多智能体编排系统。在演化算法和自适应优化中展现前瞻能力。',
    iconBg: 'bg-rose-500 text-white',
    badge: null,
    date: '2026年9月11日',
    releaseDate: '2026-09-11',
    contextTokens: 1000000,
    contextDisplay: '1M 上下文',
    maxOutputTokens: 8192,
    inputPricePerM: 5.00,
    outputPricePerM: 30.00,
    inputPriceDisplay: '$5 / 百万 Input Token',
    outputPriceDisplay: '$30 / 百万 Output Token',
    tokensDisplay: '2.24B tokens',
    modalities: ['text', 'image'],
    category: 'Reasoning',
    tags: ['text', 'image', 'evolutionary'],
    variants: ['standard', 'thinking'],
    hasDiscount: false,
    distillable: false,
    zeroDataRetention: false,
    inRegionRouting: ['US'],
    supportedParameters: ['tools', 'temperature', 'top_p'],
    toolCallingCapability: 90,
    modelAgeMonths: 1,
    scores: {
      intelligenceIndex: 50,
      codingIndex: 75,
      agenticIndex: 57,
      designArena: {
        codeCategories: 1330,
        uiComponent: 1340,
        gameDev: 1365,
        dataViz: 1330,
        threeD: 1370,
        image: 1340,
        video: 1840,
        svg: 1315,
      }
    }
  },
  {
    id: 'anthropic/claude-3-7-sonnet',
    name: 'Anthropic: Claude 3.7 Sonnet',
    provider: 'anthropic',
    providerDisplay: 'Anthropic',
    author: 'anthropic',
    series: 'Claude',
    description: 'Claude 3.7 Sonnet 具备可调节的思考深度混合推理架构。在编码开发、视觉理解与长篇严谨推论上属于行业标杆。',
    iconBg: 'bg-amber-600 text-white',
    badge: '热门推荐',
    badgeColor: 'bg-purple-100 text-purple-700 border-purple-200',
    date: '2026年9月08日',
    releaseDate: '2026-09-08',
    contextTokens: 200000,
    contextDisplay: '200K 上下文',
    maxOutputTokens: 64000,
    inputPricePerM: 3.00,
    outputPricePerM: 15.00,
    inputPriceDisplay: '$3 / 百万 Input Token',
    outputPriceDisplay: '$15 / 百万 Output Token',
    tokensDisplay: '184B tokens',
    modalities: ['text', 'image', 'file'],
    category: 'Programming',
    tags: ['text', 'image', 'coding', 'thinking'],
    variants: ['standard', 'thinking', 'batch'],
    hasDiscount: false,
    distillable: false,
    zeroDataRetention: true,
    inRegionRouting: ['US', 'EU'],
    supportedParameters: ['tools', 'temperature', 'top_p'],
    toolCallingCapability: 98,
    modelAgeMonths: 2,
    scores: {
      intelligenceIndex: 54,
      codingIndex: 82,
      agenticIndex: 58,
      designArena: {
        codeCategories: 1385,
        uiComponent: 1395,
        gameDev: 1405,
        dataViz: 1370,
        threeD: 1420,
        image: 1380,
        video: 1950,
        svg: 1360,
      }
    }
  },
  {
    id: 'google/gemini-2-5-flash',
    name: 'Google: Gemini 2.5 Flash',
    provider: 'google',
    providerDisplay: 'Google',
    author: 'google',
    series: 'Gemini',
    description: 'Gemini 2.5 Flash 是轻量高速的原生多模态模型，支持百万级 Token 超长上下文与音频、视频实时解析。',
    iconBg: 'bg-blue-500 text-white',
    badge: '极速响应',
    badgeColor: 'bg-sky-100 text-sky-700 border-sky-200',
    date: '2026年9月05日',
    releaseDate: '2026-09-05',
    contextTokens: 1048576,
    contextDisplay: '1M 上下文',
    maxOutputTokens: 8192,
    inputPricePerM: 0.10,
    outputPricePerM: 0.40,
    inputPriceDisplay: '$0.10 / 百万 Input Token',
    outputPriceDisplay: '$0.40 / 百万 Output Token',
    tokensDisplay: '92B tokens',
    modalities: ['text', 'image', 'file', 'audio', 'video'],
    category: 'Multimodal',
    tags: ['text', 'image', 'video', 'audio', 'fast'],
    variants: ['standard', 'free'],
    hasDiscount: false,
    distillable: true,
    zeroDataRetention: true,
    inRegionRouting: ['US', 'EU'],
    supportedParameters: ['tools', 'temperature', 'top_p', 'response_format'],
    toolCallingCapability: 94,
    modelAgeMonths: 2,
    scores: {
      intelligenceIndex: 51,
      codingIndex: 77,
      agenticIndex: 53,
      designArena: {
        codeCategories: 1340,
        uiComponent: 1350,
        gameDev: 1370,
        dataViz: 1360,
        threeD: 1380,
        image: 1375,
        video: 1980,
        svg: 1340,
      }
    }
  },
  {
    id: 'meta/llama-3-3-70b-instruct',
    name: 'Meta: Llama 3.3 70B Instruct',
    provider: 'meta',
    providerDisplay: 'Meta AI',
    author: 'meta',
    series: 'Meta',
    description: '开源大语言模型巅峰之作，70B 具备匹敌早期超大模型的评测成绩，支持高并发企业私有化或者边缘调用。',
    iconBg: 'bg-blue-600 text-white',
    badge: null,
    date: '2026年8月28日',
    releaseDate: '2026-08-28',
    contextTokens: 128000,
    contextDisplay: '128K 上下文',
    maxOutputTokens: 4096,
    inputPricePerM: 0.12,
    outputPricePerM: 0.30,
    inputPriceDisplay: '$0.12 / 百万 Input Token',
    outputPriceDisplay: '$0.30 / 百万 Output Token',
    tokensDisplay: '410B tokens',
    modalities: ['text'],
    category: 'Programming',
    tags: ['text', 'open-weights', 'coding'],
    variants: ['standard', 'free'],
    hasDiscount: false,
    distillable: true,
    zeroDataRetention: true,
    inRegionRouting: ['US', 'EU'],
    supportedParameters: ['tools', 'temperature', 'top_p'],
    toolCallingCapability: 91,
    modelAgeMonths: 3,
    scores: {
      intelligenceIndex: 49,
      codingIndex: 76,
      agenticIndex: 51,
      designArena: {
        codeCategories: 1320,
        uiComponent: 1335,
        gameDev: 1350,
        dataViz: 1330,
        threeD: 1360,
        image: 1325,
        video: 1750,
        svg: 1310,
      }
    }
  }
];

// synthesizeCallableModel 给一个真实存在于 GET /v1/models 目录、但公开目录
// GET /v1/catalog 和 mock 数据里都没有对应条目的模型 id 生成一张最小可用的
// 卡片——比如目录接口暂时不可用时的降级路径，或者这个模型对 free tier 不
// 可见（/v1/catalog 只返回 free tier 可见的模型，但已连接的 Key 可能是更高
// tier，能调用 /v1/catalog 看不到的模型）。价格、评分留空/0 并打上"演示
// 数据"角标，明确告诉用户这些数字不是真的；卡片本身代表一个已连接账户真实
// 能调用的模型（isCallable=true）。
export function synthesizeCallableModel(id: string): Model {
  const slashIndex = id.indexOf('/');
  const provider = slashIndex > 0 ? id.slice(0, slashIndex) : id;
  const name = slashIndex > 0 ? id.slice(slashIndex + 1) : id;
  return {
    id,
    name,
    provider,
    providerDisplay: provider,
    author: provider,
    series: 'Other',
    description: '该模型来自网关的真实模型目录，尚无运营录入的详细介绍与评分。',
    iconBg: 'bg-gray-700 text-white',
    badge: '演示数据',
    badgeColor: 'bg-gray-100 text-gray-600 border-gray-200',
    date: '—',
    releaseDate: '1970-01-01',
    contextTokens: 0,
    contextDisplay: null,
    maxOutputTokens: 0,
    inputPricePerM: 0,
    outputPricePerM: 0,
    inputPriceDisplay: '价格待运营录入',
    outputPriceDisplay: null,
    modalities: ['text'],
    category: 'Other',
    tags: [],
    variants: ['standard'],
    distillable: false,
    zeroDataRetention: false,
    inRegionRouting: [],
    supportedParameters: [],
    toolCallingCapability: 0,
    modelAgeMonths: 0,
    scores: { intelligenceIndex: 0, codingIndex: 0, agenticIndex: 0 },
    isCallable: true,
  };
}

function formatContextDisplay(tokens: number): string | null {
  if (tokens <= 0) return null;
  if (tokens >= 1_000_000) return `${(tokens / 1_000_000).toFixed(1)}M 上下文`;
  if (tokens >= 1_000) return `${Math.round(tokens / 1_000)}K 上下文`;
  return `${tokens} 上下文`;
}

// 顶层三个指数的键名：snake_case 是技术方案 §3.1 约定的正式键（后端
// PUT /virtual-models/{id}/metadata 只接受这套），camelCase 是约定之前运营
// 录入的旧写法——数据修正 SQL 跑完之前两种都可能出现，过渡期都认。
const SCORE_INDEX_KEYS: ['intelligenceIndex' | 'codingIndex' | 'agenticIndex', string, string][] = [
  ['intelligenceIndex', 'intelligence_index', 'intelligenceIndex'],
  ['codingIndex', 'coding_index', 'codingIndex'],
  ['agenticIndex', 'agentic_index', 'agenticIndex'],
];

// 公开评测榜单投影出来的可选成绩键（后端按 best variant 写入 scores）。
// 和三个指数不同，缺失时保持 undefined（不回落到 0），图表/对比据此跳过没有
// 该项成绩的模型。第三列同样兼容 camelCase 写法。
export type ExternalScoreKey =
  | 'arenaText'
  | 'arenaChinese'
  | 'arenaCoding'
  | 'arenaWebdev'
  | 'arenaVision'
  | 'gpqaDiamond'
  | 'sweBenchVerified'
  | 'hle'
  | 'terminalBench'
  | 'aiderPolyglot'
  | 'arcAgi2'
  | 'livebench'
  | 'epochEci'
  | 'opencompass'
  | 'superclue';

const EXTERNAL_SCORE_KEYS: [ExternalScoreKey, string, string][] = [
  ['arenaText', 'arena_text', 'arenaText'],
  ['arenaChinese', 'arena_chinese', 'arenaChinese'],
  ['arenaCoding', 'arena_coding', 'arenaCoding'],
  ['arenaWebdev', 'arena_webdev', 'arenaWebdev'],
  ['arenaVision', 'arena_vision', 'arenaVision'],
  ['gpqaDiamond', 'gpqa_diamond', 'gpqaDiamond'],
  ['sweBenchVerified', 'swe_bench_verified', 'sweBenchVerified'],
  ['hle', 'hle', 'hle'],
  ['terminalBench', 'terminal_bench', 'terminalBench'],
  ['aiderPolyglot', 'aider_polyglot', 'aiderPolyglot'],
  ['arcAgi2', 'arc_agi_2', 'arcAgi2'],
  ['livebench', 'livebench', 'livebench'],
  ['epochEci', 'epoch_eci', 'epochEci'],
  ['opencompass', 'opencompass', 'opencompass'],
  ['superclue', 'superclue', 'superclue'],
];

// 外部成绩键的展示元信息：label 用于对比弹窗/图表选择器，unit 决定格式化
// 方式（elo 取整、percent 一位小数加 %、index 一位小数），source 是数据来源
// 署名。
export const EXTERNAL_SCORE_META: Record<ExternalScoreKey, { label: string; unit: 'elo' | 'percent' | 'index'; source: string }> = {
  arenaText: { label: 'LMArena 文本', unit: 'elo', source: 'LMArena (CC BY 4.0)' },
  arenaChinese: { label: 'LMArena 中文', unit: 'elo', source: 'LMArena (CC BY 4.0)' },
  arenaCoding: { label: 'LMArena 编程', unit: 'elo', source: 'LMArena (CC BY 4.0)' },
  arenaWebdev: { label: 'LMArena WebDev', unit: 'elo', source: 'LMArena (CC BY 4.0)' },
  arenaVision: { label: 'LMArena 视觉', unit: 'elo', source: 'LMArena (CC BY 4.0)' },
  gpqaDiamond: { label: 'GPQA Diamond', unit: 'percent', source: 'Epoch AI (CC BY 4.0)' },
  sweBenchVerified: { label: 'SWE-bench Verified', unit: 'percent', source: 'Epoch AI (CC BY 4.0)' },
  hle: { label: "Humanity's Last Exam", unit: 'percent', source: 'Epoch AI (CC BY 4.0)' },
  terminalBench: { label: 'Terminal-Bench', unit: 'percent', source: 'Epoch AI (CC BY 4.0)' },
  aiderPolyglot: { label: 'Aider Polyglot', unit: 'percent', source: 'Epoch AI (CC BY 4.0)' },
  arcAgi2: { label: 'ARC-AGI-2', unit: 'percent', source: 'Epoch AI (CC BY 4.0)' },
  livebench: { label: 'LiveBench', unit: 'percent', source: 'Epoch AI (CC BY 4.0)' },
  epochEci: { label: 'Epoch ECI', unit: 'index', source: 'Epoch AI (CC BY 4.0)' },
  opencompass: { label: 'OpenCompass', unit: 'index', source: 'OpenCompass' },
  superclue: { label: 'SuperCLUE', unit: 'index', source: 'SuperCLUE' },
};

export const EXTERNAL_SCORE_FIELDS: ExternalScoreKey[] = EXTERNAL_SCORE_KEYS.map(([field]) => field);

// formatExternalScore 按 EXTERNAL_SCORE_META 的单位格式化一项外部成绩。
export function formatExternalScore(key: ExternalScoreKey, value: number | undefined): string {
  if (value === undefined || !Number.isFinite(value)) return '—';
  const unit = EXTERNAL_SCORE_META[key].unit;
  if (unit === 'elo') return `${Math.round(value)} Elo`;
  if (unit === 'percent') return `${value.toFixed(1)}%`;
  return value.toFixed(1);
}

type DesignArenaScores = NonNullable<ModelScores['designArena']>;

// design_arena 子键 → ModelScores.designArena 字段；第三列是旧 camelCase 写法
// （注意 code 对应的旧键是 codeCategories，不是 code）。
const DESIGN_ARENA_KEYS: [keyof DesignArenaScores, string, string][] = [
  ['codeCategories', 'code', 'codeCategories'],
  ['uiComponent', 'ui_component', 'uiComponent'],
  ['gameDev', 'game_dev', 'gameDev'],
  ['dataViz', 'data_viz', 'dataViz'],
  ['threeD', 'three_d', 'threeD'],
  ['image', 'image', 'image'],
  ['video', 'video', 'video'],
  ['svg', 'svg', 'svg'],
];

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

// pickNumber 先读正式键，读不到再读旧键；非数字一律当作没录入。
function pickNumber(obj: Record<string, unknown> | undefined, key: string, legacyKey: string): number | undefined {
  const v = obj?.[key] ?? obj?.[legacyKey];
  return typeof v === 'number' && Number.isFinite(v) ? v : undefined;
}

// normalizeScores 把 GET /v1/catalog 原样透传的 virtual_model_metadata.scores
// 转成 ModelScores：识别 §3.1 约定的 snake_case 键（含嵌套的 design_arena），
// 过渡期兼容旧 camelCase 键。识别不到（运营没录这一项）时逐项回退到 mock
// 覆盖表的值，再不行就是 0——0 分在 UI 上显眼到足以说明"这不是真实评分"，
// 比编造一个看起来合理的数字更诚实。
function normalizeScores(raw: Record<string, unknown> | undefined, fallback?: ModelScores): ModelScores {
  const scores: ModelScores = { intelligenceIndex: 0, codingIndex: 0, agenticIndex: 0 };
  for (const [field, key, legacyKey] of SCORE_INDEX_KEYS) {
    scores[field] = pickNumber(raw, key, legacyKey) ?? fallback?.[field] ?? 0;
  }

  // design_arena 是一层嵌套对象；真实数据和 mock 兜底都没有任何一项时保持
  // undefined，左侧栏 Design Arena 筛选会把它当 0 分处理。
  const rawArena = raw?.design_arena ?? raw?.designArena;
  const arenaObj = isRecord(rawArena) ? rawArena : undefined;
  const arena: DesignArenaScores = {};
  let hasArena = false;
  for (const [field, key, legacyKey] of DESIGN_ARENA_KEYS) {
    const v = pickNumber(arenaObj, key, legacyKey) ?? fallback?.designArena?.[field];
    if (v !== undefined) {
      arena[field] = v;
      hasArena = true;
    }
  }
  if (hasArena) scores.designArena = arena;

  for (const [field, key, legacyKey] of EXTERNAL_SCORE_KEYS) {
    const v = pickNumber(raw, key, legacyKey) ?? fallback?.[field];
    if (v !== undefined) scores[field] = v;
  }
  return scores;
}

// modelFromCatalog 把 GET /v1/catalog 的一条真实模型数据转成 Model——技术
// 方案迭代6：模型库以这个接口为主数据源，mockOverride（按 id 从
// INITIAL_MODELS 里找到的同名条目，找不到则 undefined）只用来补运营还没
// 录入的展示层字段（描述、系列、图标底色……），硬性字段（上下文窗口、价格、
// 能力）永远以 catalog 的真实数据为准，绝不被 mock 覆盖——那样会让"离线
// 兜底数据"喧宾夺主，用户看到的价格和后端实际计费对不上。
export function modelFromCatalog(cm: CatalogModel, mockOverride?: Model): Model {
  const slashIndex = cm.name.indexOf('/');
  const provider = slashIndex > 0 ? cm.name.slice(0, slashIndex) : cm.name;

  const inputComponent = cm.sellPrice?.components.find((c) => c.meter === 'input' && c.unit === 'per_1m_tokens');
  const outputComponent = cm.sellPrice?.components.find((c) => c.meter === 'output' && c.unit === 'per_1m_tokens');
  const inputPricePerM = inputComponent?.unitPrice ?? mockOverride?.inputPricePerM ?? 0;
  const outputPricePerM = outputComponent?.unitPrice ?? mockOverride?.outputPricePerM ?? 0;
  // 售价来自 sell_price.currency（技术方案：对外售价统一 CNY），用 ¥ 而不是
  // mock 数据惯用的 $，避免用户把真实计价误认成美元。
  const priceSymbol = cm.sellPrice?.currency === 'USD' ? '$' : '¥';

  return {
    id: cm.name,
    name: cm.displayName || mockOverride?.name || cm.name,
    provider: mockOverride?.provider || provider,
    providerDisplay: cm.providerDisplay || mockOverride?.providerDisplay || provider,
    author: mockOverride?.author || provider,
    series: mockOverride?.series || 'Other',
    description: cm.description || mockOverride?.description || '',
    iconBg: mockOverride?.iconBg || 'bg-gray-700 text-white',
    badge: mockOverride?.badge ?? null,
    badgeColor: mockOverride?.badgeColor,
    date: mockOverride?.date || '—',
    releaseDate: mockOverride?.releaseDate || '1970-01-01',
    contextTokens: cm.contextWindow,
    contextDisplay: formatContextDisplay(cm.contextWindow),
    maxOutputTokens: cm.maxOutput,
    inputPricePerM,
    outputPricePerM,
    inputPriceDisplay: `${priceSymbol}${inputPricePerM} / 百万 Input Token`,
    outputPriceDisplay: `${priceSymbol}${outputPricePerM} / 百万 Output Token`,
    tokensDisplay: mockOverride?.tokensDisplay,
    modalities: mockOverride?.modalities || ['text'],
    category: mockOverride?.category || 'Other',
    tags: cm.tags && cm.tags.length > 0 ? cm.tags : mockOverride?.tags || [],
    variants: mockOverride?.variants || ['standard'],
    hasDiscount: mockOverride?.hasDiscount,
    discountPercent: mockOverride?.discountPercent,
    distillable: mockOverride?.distillable ?? false,
    zeroDataRetention: mockOverride?.zeroDataRetention ?? false,
    inRegionRouting: mockOverride?.inRegionRouting || [],
    supportedParameters: cm.capabilities,
    toolCallingCapability: mockOverride?.toolCallingCapability ?? (cm.capabilities.includes('tools') ? 80 : 0),
    modelAgeMonths: mockOverride?.modelAgeMonths ?? 0,
    isDeprecated: cm.status === 'deprecated' || mockOverride?.isDeprecated,
    scores: normalizeScores(cm.scores, mockOverride?.scores),
  };
}

// 注意：这里以前带着写死的 count（全部444/文本444/……），和真实模型数据完全
// 脱钩——即便后端目录清空、filteredModels 变成 0 条，这排数字也纹丝不动。
// count 现在改为在 App.tsx 里用 matchesPrimaryTag 对 baseModels 实时统计。
export const PRIMARY_TAGS: { id: string; title: string }[] = [
  { id: 'all', title: '全部' },
  { id: 'text', title: 'T 文本' },
  { id: 'image', title: '图像' },
  { id: 'video', title: '视频' },
  { id: 'voice', title: '语音' },
  { id: 'transcribe', title: '转写' },
  { id: 'embedding', title: '嵌入' },
  { id: 'rerank', title: '重排序' },
  { id: 'audio', title: '音频' },
];

// 模态过滤 Tag 栏的匹配规则——App.tsx 的过滤流水线和分类计数统计共用这一份
// 逻辑，避免两处各写一套、后续改动漏掉一处导致计数和实际过滤结果对不上。
export function matchesPrimaryTag(model: Model, tag: string): boolean {
  switch (tag) {
    case 'all':
      return true;
    case 'text':
      return model.modalities.includes('text');
    case 'image':
      return model.modalities.includes('image');
    case 'video':
      return model.modalities.includes('video');
    case 'audio':
      return model.modalities.includes('audio');
    case 'voice':
      return model.tags.includes('voice') || model.modalities.includes('audio');
    case 'transcribe':
      return model.tags.includes('transcription');
    case 'embedding':
      return model.tags.includes('embedding') || model.category === 'Extraction';
    case 'rerank':
      return model.tags.includes('rerank');
    default:
      return true;
  }
}
