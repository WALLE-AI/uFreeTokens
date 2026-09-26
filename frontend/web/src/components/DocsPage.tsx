import React, { useState, useRef, useEffect } from 'react';
import {
  BookOpen,
  Code2,
  Terminal,
  Bot,
  CookingPot,
  Search,
  Copy,
  Check,
  ChevronRight,
  ChevronDown,
  Play,
  ArrowRight,
  Shield,
  FileText,
  Clock,
  Activity,
  MessageSquare,
  Volume2,
  Mic,
  Sliders,
  CheckCircle2,
  AlertTriangle,
  RotateCw,
  ExternalLink,
  Layers,
  Sparkles,
  Zap,
  Lock,
  Boxes,
  HelpCircle,
  Hash,
  ArrowLeft
} from 'lucide-react';
import { DocsSidebar } from './DocsSidebar';
import { DocArticleView } from './DocArticleView';
import { getDocArticle } from '../data/docsContentData';

export interface DocsPageProps {
  onBackToMarketplace?: () => void;
}

type HttpMethod = 'GET' | 'POST' | 'DELETE' | 'PATCH' | 'PUT';

interface ApiEndpoint {
  id: string;
  category: string;
  method: HttpMethod;
  name: string;
  path: string;
  summary: string;
  description: string;
  usageNote?: string;
  params: Array<{
    name: string;
    type: string;
    required: boolean;
    description: string;
    example?: string;
    options?: string[];
  }>;
  requestSnippet: {
    curl: string;
    js: string;
    python: string;
  };
  samplePayload?: string;
  responses: Record<number, string>;
}

const API_ENDPOINTS: ApiEndpoint[] = [
  {
    id: 'get-generation',
    category: '任务与用量 (Generations)',
    method: 'GET',
    name: '获取指定任务的请求与用量元数据',
    path: '/generation',
    summary: '查询任意历史调用任务的用量统计、成本明细与提供商路由信息',
    description: '根据历史生成的唯一 ID（Generation ID）获取详细的 Token 消耗、精准到小数点后 6 位的美元计费明细、响应延迟、故障转移记录以及上下文缓存节省统计。',
    usageNote: `// uFreeTokens 始终返回精准到原生 Tokenizer 的用量信息。
// Token 计数严格遵循各模型厂商原生分词规则，杜绝粗略估算。

type ResponseUsage = {
  /** 提示词 Token 消耗（包含图像分辨率等效 Token、输入音频与工具声明） */
  prompt_tokens: number;
  /** 模型生成的输出 Token 消耗 */
  completion_tokens: number;
  /** 本次调用真实结算费用（精确至小数点后 6 位，单位 USD） */
  total_cost: number;
  /** 上下文 Prompt 缓存减免金额（若命中上下文缓存） */
  cache_discount?: number;
};`,
    params: [
      {
        name: 'code',
        type: 'string',
        required: true,
        description: '从 OAuth 重定向接收的授权代码 (Authorization Code)，用于交换或验证访问凭证',
        example: '"auth_code_abc123def456"'
      },
      {
        name: 'code_challenge_method',
        type: 'enum<string> | null',
        required: false,
        description: '用于生成 PKCE code challenge 的哈希算法，推荐使用 S256',
        options: ['S256', 'plain', 'null'],
        example: '"S256"'
      },
      {
        name: 'code_verifier',
        type: 'string',
        required: true,
        description: '用于 PKCE 安全校验流程的原始随机密钥验证字符串',
        example: '"dBjftJmZ4CVP-m892K27uhNUULJp1r_wWigFWFO"'
      },
      {
        name: 'id',
        type: 'string',
        required: false,
        description: '要查询的特定生成任务唯一标识符 (Generation ID)',
        example: '"gen_01j7b5x4w8e9d3k2m1p0"'
      }
    ],
    requestSnippet: {
      curl: `curl -request GET \\
  --url https://ufreetokens.com/api/v1/generation \\
  --header 'Authorization: Bearer <YOUR_API_KEY>' \\
  --header 'Content-Type: application/json'`,
      js: `import { uFreeTokens } from "@ufreetokens/sdk";

const client = new uFreeTokens({ apiKey: process.env.UFREETOKENS_API_KEY });
const metadata = await client.generation.get({
  id: "gen_01j7b5x4w8e9d3k2m1p0"
});
console.log("任务用量与费用:", metadata);`,
      python: `from ufreetokens import uFreeTokens

client = uFreeTokens(api_key="your-api-key")
metadata = client.generation.get("gen_01j7b5x4w8e9d3k2m1p0")
print(f"总计消费: {metadata.total_cost} USD, Token数: {metadata.tokens_prompt + metadata.tokens_completion}")`
    },
    samplePayload: `--header 'Authorization: Bearer <YOUR_API_KEY>' \\
--header 'Content-Type: application/json' \\
--data '
{
  "code": "auth_code_abc123def456",
  "code_challenge_method": "S256",
  "code_verifier": "dBjftJmZ4CVP-m892K27uhNUULJp1r_wWigFWFO"
}'`,
    responses: {
      200: JSON.stringify({
        data: {
          id: "gen_01j7b5x4w8e9d3k2m1p0",
          api_type: "completions",
          app_id: 12345,
          created_at: "2026-09-17T12:00:00Z",
          model: "anthropic/claude-3.7-sonnet",
          provider_name: "Anthropic",
          total_cost: 0.004215,
          tokens_prompt: 450,
          tokens_completion: 128,
          native_tokens_prompt: 450,
          native_tokens_completion: 128,
          generation_time: 1.42,
          finish_reason: "stop"
        }
      }, null, 2),
      401: JSON.stringify({
        error: {
          code: 401,
          message: "用户 API Key 未找到，或请求头中的 Bearer Token 无效。"
        }
      }, null, 2),
      402: JSON.stringify({
        error: {
          code: 402,
          message: "账户可用余额不足，或已达到所在团队设定的单月消费配额上限。"
        }
      }, null, 2),
      404: JSON.stringify({
        error: {
          code: 404,
          message: "未找到指定的 Generation ID，或该任务数据已按数据保留合规策略过期销毁。"
        }
      }, null, 2),
      429: JSON.stringify({
        error: {
          code: 429,
          message: "请求频率超限。请在 2.5 秒后采用指数退避算法重试。"
        }
      }, null, 2),
      500: JSON.stringify({
        error: {
          code: 500,
          message: "上游模型提供商服务内部异常，系统正在尝试自动容灾切换至备选提供商。"
        }
      }, null, 2),
      502: JSON.stringify({
        error: {
          code: 502,
          message: "网关错误 (Bad Gateway)：上游节点响应超时。"
        }
      }, null, 2),
      524: JSON.stringify({
        error: {
          code: 524,
          message: "长文本生成过程发生 TCP 连接超时。"
        }
      }, null, 2),
      529: JSON.stringify({
        error: {
          code: 529,
          message: "上游模型推理集群目前高负载排队中。"
        }
      }, null, 2)
    }
  },
  {
    id: 'post-chat-completions',
    category: '对话补全 (Chat)',
    method: 'POST',
    name: '创建对话补全 (Chat Completion)',
    path: '/chat/completions',
    summary: '发起兼容 OpenAI 格式的标准多轮对话、多模态分析与函数调用',
    description: '支持流式 SSE 输出、结构化 JSON Schema 强类型约束输出、Tools/Function Calling 智能体工具调用、跨模型自动故障转移 (Fallback) 与原生提示词缓存减免。',
    usageNote: `// 零修改无缝兼容 OpenAI 官方 SDK：
// 仅需将 baseURL 指向 https://ufreetokens.com/api/v1
// 即可直接调用 Claude 3.7、GPT-4o、DeepSeek-V3、Gemini 2.5 等全部模型！`,
    params: [
      {
        name: 'model',
        type: 'string',
        required: true,
        description: '模型 ID 或动态路由标签，如 anthropic/claude-3.7-sonnet 或 ufreetokens/auto',
        example: '"anthropic/claude-3.7-sonnet"'
      },
      {
        name: 'messages',
        type: 'array<Message>',
        required: true,
        description: '对话上下文历史数组，每项包含 role（system/user/assistant/tool）与 content',
        example: '[{"role": "user", "content": "你好，请解释零数据保留 ZDR。"}]'
      },
      {
        name: 'stream',
        type: 'boolean',
        required: false,
        description: '是否通过 Server-Sent Events (SSE) 开启逐字打字机式流式实时返回',
        example: 'true'
      },
      {
        name: 'temperature',
        type: 'number',
        required: false,
        description: '采样随机性参数，取值 0.0 到 2.0，数学推理与代码任务推荐 0.1~0.3',
        example: '0.7'
      },
      {
        name: 'max_tokens',
        type: 'integer',
        required: false,
        description: '单次回复生成的最大 Token 上限',
        example: '4096'
      }
    ],
    requestSnippet: {
      curl: `curl -X POST https://ufreetokens.com/api/v1/chat/completions \\
  -H "Authorization: Bearer $UFREETOKENS_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "anthropic/claude-3.7-sonnet",
    "messages": [
      { "role": "user", "content": "用中文总结量子计算的核心原理" }
    ],
    "temperature": 0.7
  }'`,
      js: `import OpenAI from "openai";

const openai = new OpenAI({
  baseURL: "https://ufreetokens.com/api/v1",
  apiKey: process.env.UFREETOKENS_API_KEY,
});

const completion = await openai.chat.completions.create({
  model: "anthropic/claude-3.7-sonnet",
  messages: [{ role: "user", content: "用中文总结量子计算的核心原理" }],
});

console.log(completion.choices[0].message.content);`,
      python: `from openai import OpenAI

client = OpenAI(
    base_url="https://ufreetokens.com/api/v1",
    api_key="your-api-key"
)

response = client.chat.completions.create(
    model="anthropic/claude-3.7-sonnet",
    messages=[{"role": "user", "content": "用中文总结量子计算的核心原理"}]
)
print(response.choices[0].message.content)`
    },
    samplePayload: `{
  "model": "anthropic/claude-3.7-sonnet",
  "messages": [
    { "role": "system", "content": "你是一位专业的算法架构师。" },
    { "role": "user", "content": "解释分布式系统的共识算法 Raft。" }
  ],
  "temperature": 0.5,
  "max_tokens": 2048
}`,
    responses: {
      200: JSON.stringify({
        id: "gen_chat_9812497",
        choices: [
          {
            finish_reason: "stop",
            index: 0,
            message: {
              role: "assistant",
              content: "Raft 是一种专为可理解性而设计的分布式一致性共识算法..."
            }
          }
        ],
        created: 1726588000,
        model: "anthropic/claude-3.7-sonnet",
        usage: {
          prompt_tokens: 42,
          completion_tokens: 156,
          total_tokens: 198
        }
      }, null, 2),
      401: JSON.stringify({ error: { message: "API Key 无效或未授权" } }, null, 2),
      429: JSON.stringify({ error: { message: "已达到每分钟 Token 或并发上限" } }, null, 2),
      500: JSON.stringify({ error: { message: "模型提供商服务内部错误" } }, null, 2)
    }
  },
  {
    id: 'get-embeddings',
    category: '端点管理 (Endpoints)',
    method: 'GET',
    name: '获取全部 Embeddings 向量模型',
    path: '/embeddings/models',
    summary: '查询平台当前可用且支持检索增强（RAG）的全部嵌入模型清单与定价',
    description: '返回包含 OpenAI、Cohere、Voyage AI 等主流厂商嵌入模型的输出向量维度、单批次上下文 Token 限制与每百万 Token 资费。',
    params: [
      {
        name: 'dimension',
        type: 'integer',
        required: false,
        description: '按输出向量特征维度过滤，例如 1536 或 3072',
        example: '1536'
      }
    ],
    requestSnippet: {
      curl: `curl https://ufreetokens.com/api/v1/embeddings/models \\
  -H "Authorization: Bearer $UFREETOKENS_API_KEY"`,
      js: `const res = await fetch("https://ufreetokens.com/api/v1/embeddings/models", {
  headers: { Authorization: \`Bearer \${process.env.UFREETOKENS_API_KEY}\` }
});
const data = await res.json();
console.log("向量模型列表:", data);`,
      python: `import requests
res = requests.get(
  "https://ufreetokens.com/api/v1/embeddings/models",
  headers={"Authorization": f"Bearer {api_key}"}
)
print(res.json())`
    },
    responses: {
      200: JSON.stringify({
        data: [
          { id: "openai/text-embedding-3-small", dimensions: 1536, max_input: 8191, price_per_1m: 0.02 },
          { id: "openai/text-embedding-3-large", dimensions: 3072, max_input: 8191, price_per_1m: 0.13 },
          { id: "cohere/embed-multilingual-v3.0", dimensions: 1024, max_input: 512, price_per_1m: 0.10 }
        ]
      }, null, 2),
      401: JSON.stringify({ error: { message: "未授权或 Token 无效" } }, null, 2)
    }
  },
  {
    id: 'get-zdr-preview',
    category: '端点管理 (Endpoints)',
    method: 'GET',
    name: '预检 ZDR 零数据保留路由影响',
    path: '/endpoints/zdr',
    summary: '评估开启零数据保留 (Zero Data Retention) 策略对当前可用节点的影响',
    description: 'ZDR 承诺模型提供商和路由网关均不落地记录、不存储任何用户的提示词与输出日志，适合金融、医疗与企业级合规业务。此接口可预检开启 ZDR 后各模型的可用率与延迟影响。',
    params: [
      {
        name: 'model',
        type: 'string',
        required: false,
        description: '可选：要特别预检的目标模型标识符',
        example: '"anthropic/claude-3.7-sonnet"'
      }
    ],
    requestSnippet: {
      curl: `curl "https://ufreetokens.com/api/v1/endpoints/zdr?model=anthropic/claude-3.7-sonnet" \\
  -H "Authorization: Bearer $UFREETOKENS_API_KEY"`,
      js: `const res = await fetch("https://ufreetokens.com/api/v1/endpoints/zdr", {
  headers: { Authorization: \`Bearer \${process.env.UFREETOKENS_API_KEY}\` }
});
const zdrInfo = await res.json();
console.log("ZDR 节点状态:", zdrInfo);`,
      python: `import requests
res = requests.get("https://ufreetokens.com/api/v1/endpoints/zdr", headers={"Authorization": f"Bearer {key}"})
print(res.json())`
    },
    responses: {
      200: JSON.stringify({
        zdr_enabled: true,
        compliant_providers_count: 14,
        guaranteed_logging_policy: "Zero logs retained on disk or training caches",
        active_endpoints: [
          { provider: "Anthropic (Direct)", compliance: "SOC2 + HIPAA", status: "online", latency_ms: 185 },
          { provider: "Google Cloud Vertex", compliance: "ISO27001", status: "online", latency_ms: 192 }
        ]
      }, null, 2)
    }
  },
  {
    id: 'get-model-endpoints',
    category: '端点管理 (Endpoints)',
    method: 'GET',
    name: '查询指定模型的所有提供商节点',
    path: '/endpoints',
    summary: '查看某个模型背后实际接入的所有上游提供商节点、实时延迟与健康状况',
    description: '允许您精细化了解某个模型（例如 Claude 3.7 Sonnet 或 DeepSeek-V3）在不同提供商处的分发情况、吞吐量健康度与定价差异。',
    params: [
      {
        name: 'model',
        type: 'string',
        required: true,
        description: '目标模型 ID',
        example: '"deepseek/deepseek-chat"'
      }
    ],
    requestSnippet: {
      curl: `curl "https://ufreetokens.com/api/v1/endpoints?model=deepseek/deepseek-chat" \\
  -H "Authorization: Bearer $UFREETOKENS_API_KEY"`,
      js: `const res = await fetch("https://ufreetokens.com/api/v1/endpoints?model=deepseek/deepseek-chat", {
  headers: { Authorization: \`Bearer \${process.env.UFREETOKENS_API_KEY}\` }
});
console.log(await res.json());`,
      python: `import requests
res = requests.get("https://ufreetokens.com/api/v1/endpoints?model=deepseek/deepseek-chat", headers={"Authorization": f"Bearer {key}"})
print(res.json())`
    },
    responses: {
      200: JSON.stringify({
        data: [
          { provider: "DeepSeek Official", status: "healthy", latency_p50: 210, throughput_tps: 68 },
          { provider: "SiliconFlow", status: "healthy", latency_p50: 175, throughput_tps: 84 },
          { provider: "Fireworks", status: "healthy", latency_p50: 190, throughput_tps: 72 }
        ]
      }, null, 2)
    }
  },
  {
    id: 'get-files',
    category: '文件服务 (Files)',
    method: 'GET',
    name: '获取已上传文件列表',
    path: '/files',
    summary: '分页查询当前组织或个人上传至平台的文档、音视频与代码资产',
    description: '支持通过 cursor 游标翻页，获取文件的 MIME 类型、文件大小、上传时间与关联的任务引用。',
    params: [
      {
        name: 'cursor',
        type: 'string',
        required: false,
        description: '用于下一页检索的游标标识符（在 2026 年 7 月版本规范中进行了增强）',
        example: '"cur_file_987654"'
      },
      {
        name: 'limit',
        type: 'integer',
        required: false,
        description: '每页返回的文件数量（默认 20，最大 100）',
        example: '20'
      }
    ],
    requestSnippet: {
      curl: `curl "https://ufreetokens.com/api/v1/files?limit=20" \\
  -H "Authorization: Bearer $UFREETOKENS_API_KEY"`,
      js: `const res = await fetch("https://ufreetokens.com/api/v1/files", {
  headers: { Authorization: \`Bearer \${process.env.UFREETOKENS_API_KEY}\` }
});
console.log(await res.json());`,
      python: `import requests
res = requests.get("https://ufreetokens.com/api/v1/files", headers={"Authorization": f"Bearer {key}"})
print(res.json())`
    },
    responses: {
      200: JSON.stringify({
        data: [
          { id: "file_01j7ac89b4", filename: "financial_report_q2.pdf", bytes: 1420500, purpose: "assistants", created_at: 1726581000 }
        ],
        has_more: false,
        next_cursor: null
      }, null, 2)
    }
  },
  {
    id: 'post-speech',
    category: '语音合成 (TTS)',
    method: 'POST',
    name: '创建语音音频合成 (Create speech)',
    path: '/audio/speech',
    summary: '将输入的自然语言文本合成为高拟真人声音频文件流',
    description: '支持 MP3、WAV、Opus 与 AAC 格式输出，兼容多语言音色切换与语速微调。',
    params: [
      {
        name: 'model',
        type: 'string',
        required: true,
        description: 'TTS 语音合成模型标识符，如 openai/tts-1 或 elevenlabs/multilingual-v2',
        example: '"openai/tts-1"'
      },
      {
        name: 'input',
        type: 'string',
        required: true,
        description: '需要合成为语音的文字内容（单次最多支持 4096 个字符）',
        example: '"欢迎使用 uFreeTokens 开放平台，开启极速 AI 体验。"'
      },
      {
        name: 'voice',
        type: 'string',
        required: false,
        description: '语音音色选项 (alloy, echo, fable, onyx, nova, shimmer)',
        example: '"alloy"'
      }
    ],
    requestSnippet: {
      curl: `curl -X POST https://ufreetokens.com/api/v1/audio/speech \\
  -H "Authorization: Bearer $UFREETOKENS_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "openai/tts-1",
    "input": "欢迎使用 uFreeTokens 开放平台！",
    "voice": "alloy"
  }' --output output.mp3`,
      js: `import fs from "fs";
import OpenAI from "openai";

const openai = new OpenAI({ baseURL: "https://ufreetokens.com/api/v1" });
const mp3 = await openai.audio.speech.create({
  model: "openai/tts-1",
  voice: "alloy",
  input: "欢迎使用 uFreeTokens 开放平台！",
});
const buffer = Buffer.from(await mp3.arrayBuffer());
await fs.promises.writeFile("output.mp3", buffer);`,
      python: `from openai import OpenAI

client = OpenAI(base_url="https://ufreetokens.com/api/v1")
response = client.audio.speech.create(
    model="openai/tts-1",
    voice="alloy",
    input="欢迎使用 uFreeTokens 开放平台！"
)
response.stream_to_file("output.mp3")`
    },
    responses: {
      200: "// 二进制音频流数据 (Content-Type: audio/mpeg)",
      400: JSON.stringify({ error: { message: "输入文本超出最大字符数限制 (4096)" } }, null, 2)
    }
  },
  {
    id: 'post-transcriptions',
    category: '语音识别 (STT)',
    method: 'POST',
    name: '创建音频转写 (Create transcription)',
    path: '/audio/transcriptions',
    summary: '将上传的音频文件精准转录为包含时间戳标记的文本',
    description: '支持 Whisper 与各大主流转写引擎，兼容中英混杂、方言识别与标点恢复。',
    params: [
      {
        name: 'file',
        type: 'file',
        required: true,
        description: '待转写的音频文件 (mp3, wav, m4a, ogg, webm)',
        example: 'meeting_record.mp3'
      },
      {
        name: 'model',
        type: 'string',
        required: true,
        description: 'STT 转录模型标识符，如 openai/whisper-large-v3',
        example: '"openai/whisper-large-v3"'
      }
    ],
    requestSnippet: {
      curl: `curl -X POST https://ufreetokens.com/api/v1/audio/transcriptions \\
  -H "Authorization: Bearer $UFREETOKENS_API_KEY" \\
  -F "file=@audio.mp3" \\
  -F "model=openai/whisper-large-v3"`,
      js: `import fs from "fs";
import OpenAI from "openai";

const openai = new OpenAI({ baseURL: "https://ufreetokens.com/api/v1" });
const transcription = await openai.audio.transcriptions.create({
  file: fs.createReadStream("audio.mp3"),
  model: "openai/whisper-large-v3",
});
console.log("转写结果:", transcription.text);`,
      python: `from openai import OpenAI

client = OpenAI(base_url="https://ufreetokens.com/api/v1")
audio_file = open("audio.mp3", "rb")
transcription = client.audio.transcriptions.create(
  model="openai/whisper-large-v3", 
  file=audio_file
)
print(transcription.text)`
    },
    responses: {
      200: JSON.stringify({
        text: "各位同事大家早上好，今天我们主要讨论模型集市的新一代架构升级与文档规范交付。"
      }, null, 2)
    }
  },
  {
    id: 'get-guardrails',
    category: '安全护栏 (Guardrails)',
    method: 'GET',
    name: '获取安全护栏规则列表',
    path: '/guardrails',
    summary: '查询当前组织配置的所有内容审查安全护栏与合规脱敏策略',
    description: '包含 PII 敏感信息脱敏（身份证、银行卡、手机号）、Prompt 越狱注入攻击防御以及企业级内容合规过滤规则。',
    params: [
      {
        name: 'active_only',
        type: 'boolean',
        required: false,
        description: '是否仅返回当前处于激活状态的护栏规则',
        example: 'true'
      }
    ],
    requestSnippet: {
      curl: `curl https://ufreetokens.com/api/v1/guardrails \\
  -H "Authorization: Bearer $UFREETOKENS_API_KEY"`,
      js: `const res = await fetch("https://ufreetokens.com/api/v1/guardrails", {
  headers: { Authorization: \`Bearer \${process.env.UFREETOKENS_API_KEY}\` }
});
console.log(await res.json());`,
      python: `import requests
res = requests.get("https://ufreetokens.com/api/v1/guardrails", headers={"Authorization": f"Bearer {key}"})
print(res.json())`
    },
    responses: {
      200: JSON.stringify({
        data: [
          {
            id: "gr_corp_pii_01",
            name: "企业级敏感数据 PII 自动脱敏",
            enabled: true,
            rules: ["mask_ssn", "mask_credit_card", "mask_phone", "strip_prompt_injection"],
            enable_free_model_publication: false,
            enable_paid_model_training: false
          }
        ]
      }, null, 2)
    }
  },
  {
    id: 'get-analytics-activity',
    category: '数据分析 (Analytics)',
    method: 'GET',
    name: '按端点获取调用活动与并发统计',
    path: '/analytics/activity',
    summary: '查询各 API 端点的历史请求频次、QPS 峰值、平均延迟及错误率分布',
    description: '提供多维度的时序分析数据，用于企业级监控仪表板、异常告警与成本预算追踪。',
    params: [
      {
        name: 'timeframe',
        type: 'string',
        required: false,
        description: '时间窗口范围，可选 1h, 24h, 7d, 30d',
        example: '"24h"'
      }
    ],
    requestSnippet: {
      curl: `curl "https://ufreetokens.com/api/v1/analytics/activity?timeframe=24h" \\
  -H "Authorization: Bearer $UFREETOKENS_API_KEY"`,
      js: `const res = await fetch("https://ufreetokens.com/api/v1/analytics/activity", {
  headers: { Authorization: \`Bearer \${process.env.UFREETOKENS_API_KEY}\` }
});
console.log(await res.json());`,
      python: `import requests
res = requests.get("https://ufreetokens.com/api/v1/analytics/activity", headers={"Authorization": f"Bearer {key}"})
print(res.json())`
    },
    responses: {
      200: JSON.stringify({
        total_requests: 128450,
        average_latency_ms: 184,
        error_rate_percentage: 0.04,
        endpoints_breakdown: [
          { endpoint: "/chat/completions", share: 0.88 },
          { endpoint: "/generation", share: 0.08 },
          { endpoint: "/audio/speech", share: 0.04 }
        ]
      }, null, 2)
    }
  },
  {
    id: 'post-responses',
    category: 'Responses API',
    method: 'POST',
    name: '发送智能体响应评测 (POST /responses)',
    path: '/responses',
    summary: '配合 Ori Harness 智能体套件进行流式分析、评审与自动化评估',
    description: '在 2026 年 7 月更新规范中，原 judge_model 正式由 analyst_model 继承并提供兼容性映射，用于智能体思考过程与多步工具执行的实时在线审计。',
    params: [
      {
        name: 'analyst_model',
        type: 'string',
        required: true,
        description: '负责评审打分的分析模型（替代已弃用的 judge_model）',
        example: '"anthropic/claude-3.7-sonnet"'
      },
      {
        name: 'session_id',
        type: 'string',
        required: true,
        description: 'Ori Harness 智能体执行会话唯一追踪 ID',
        example: '"sess_ori_991823"'
      }
    ],
    requestSnippet: {
      curl: `curl -X POST https://ufreetokens.com/api/v1/responses \\
  -H "Authorization: Bearer $UFREETOKENS_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "analyst_model": "anthropic/claude-3.7-sonnet",
    "session_id": "sess_ori_991823"
  }'`,
      js: `const res = await fetch("https://ufreetokens.com/api/v1/responses", {
  method: "POST",
  headers: {
    Authorization: \`Bearer \${process.env.UFREETOKENS_API_KEY}\`,
    "Content-Type": "application/json"
  },
  body: JSON.stringify({
    analyst_model: "anthropic/claude-3.7-sonnet",
    session_id: "sess_ori_991823"
  })
});
console.log(await res.json());`,
      python: `import requests
res = requests.post("https://ufreetokens.com/api/v1/responses", headers={"Authorization": f"Bearer {key}"}, json={
    "analyst_model": "anthropic/claude-3.7-sonnet",
    "session_id": "sess_ori_991823"
})
print(res.json())`
    },
    responses: {
      200: JSON.stringify({
        status: "evaluating",
        evaluator: "anthropic/claude-3.7-sonnet",
        pass_rate: 0.96,
        safety_score: 9.8
      }, null, 2)
    }
  }
];

export const DocsPage: React.FC<DocsPageProps> = ({ onBackToMarketplace }) => {
  // Top Tabs
  const [topTab, setTopTab] = useState<'docs' | 'api-ref' | 'client-sdks' | 'agent-sdk' | 'cookbook'>('api-ref');
  
  // Active document ID matching DocsSidebar
  const [activeDocId, setActiveDocId] = useState<string>('provider-selection');
  const [isViewingApiEndpoint, setIsViewingApiEndpoint] = useState<boolean>(true);

  // Active API Endpoint in Reference mode
  const [selectedEndpointId, setSelectedEndpointId] = useState<string>('get-model-endpoints');
  const [sidebarSearch, setSidebarSearch] = useState<string>('');
  
  // Mobile active panel on smaller screens: 'sidebar' | 'content' | 'console'
  const [mobileActivePanel, setMobileActivePanel] = useState<'sidebar' | 'content' | 'console'>('content');

  const handleSelectDoc = (id: string, endpointId?: string) => {
    setActiveDocId(id);
    if (endpointId) {
      setSelectedEndpointId(endpointId);
      setIsViewingApiEndpoint(true);
      setTopTab('api-ref');
    } else {
      setIsViewingApiEndpoint(false);
      setTopTab('docs');
    }
    setMobileActivePanel('content');
  };

  // Selected Code Language in Right Panel
  const [codeLang, setCodeLang] = useState<'curl' | 'js' | 'python'>('curl');
  
  // Selected Response Status Code
  const [selectedStatusCode, setSelectedStatusCode] = useState<number>(200);

  // Expanded Sidebar Groups
  const [expandedGroups, setExpandedGroups] = useState<Record<string, boolean>>({
    'Endpoints': true,
    'Versioning': true,
    'Analytics': true,
    'TTS': true,
    'STT': true,
    'Chat': true,
    'Generations': true,
    'Guardrails': true,
    'Files': true,
    'Responses API': true
  });

  // Interactive "Try it" State
  const [isExecutingTryIt, setIsExecutingTryIt] = useState<boolean>(false);
  const [tryItOutput, setTryItOutput] = useState<{ status: number; time: string; body: string } | null>(null);

  // Copied toast indicators
  const [copiedSection, setCopiedSection] = useState<string | null>(null);

  // Collapsible Changelog Schema state
  const [expandedSchemas, setExpandedSchemas] = useState<Record<string, boolean>>({
    FusionPlugin: false,
    FusionServerToolConfig: false,
    Guardrail: false
  });

  // Main scroll container ref for anchor scrolling
  const mainScrollRef = useRef<HTMLDivElement>(null);
  const changelogRef = useRef<HTMLDivElement>(null);

  // Ask a question AI prompt state
  const [questionInput, setQuestionInput] = useState<string>('');
  const [aiChatMessages, setAiChatMessages] = useState<Array<{ role: 'user' | 'assistant'; content: string }>>([]);
  const [isAiAnswering, setIsAiAnswering] = useState<boolean>(false);
  const [showAiModal, setShowAiModal] = useState<boolean>(false);

  const selectedEndpoint = API_ENDPOINTS.find((ep) => ep.id === selectedEndpointId) || API_ENDPOINTS[0];

  // Normalize selectedStatusCode when selectedEndpoint changes
  useEffect(() => {
    const availableCodes = Object.keys(selectedEndpoint.responses).map(Number);
    if (!availableCodes.includes(selectedStatusCode)) {
      setSelectedStatusCode(availableCodes[0] || 200);
    }
  }, [selectedEndpointId]);

  const handleCopy = (text: string, sectionId: string) => {
    if (navigator.clipboard) {
      navigator.clipboard.writeText(text);
    }
    setCopiedSection(sectionId);
    setTimeout(() => setCopiedSection(null), 2000);
  };

  const toggleGroup = (groupName: string) => {
    setExpandedGroups((prev) => ({
      ...prev,
      [groupName]: !prev[groupName]
    }));
  };

  const toggleSchemaExpand = (schemaName: string) => {
    setExpandedSchemas((prev) => ({
      ...prev,
      [schemaName]: !prev[schemaName]
    }));
  };

  const handleTryIt = () => {
    setIsExecutingTryIt(true);
    setTryItOutput(null);
    const mockLatency = Math.floor(Math.random() * 80) + 120; // 120ms ~ 200ms
    setTimeout(() => {
      setIsExecutingTryIt(false);
      setSelectedStatusCode(200);
      setTryItOutput({
        status: 200,
        time: `${mockLatency}ms`,
        body: selectedEndpoint.responses[200] || '{\n  "status": "success"\n}'
      });
    }, 600);
  };

  const scrollToChangelog = () => {
    if (changelogRef.current) {
      changelogRef.current.scrollIntoView({ behavior: 'smooth', block: 'start' });
    }
  };

  const handleAskQuestion = (e: React.FormEvent) => {
    e.preventDefault();
    if (!questionInput.trim()) return;

    const userText = questionInput;
    setQuestionInput('');
    setAiChatMessages((prev) => [...prev, { role: 'user', content: userText }]);
    setShowAiModal(true);
    setIsAiAnswering(true);

    setTimeout(() => {
      setIsAiAnswering(false);
      let reply = `关于 "${userText}"：在 uFreeTokens 体系中，所有接口完全兼容 OpenAPI 与标准 HTTP/REST 规范。如需在生产环境接入，直接将 API Base URL 替换为 https://ufreetokens.com/api/v1，并带上 Bearer Token 即可。同时支持零数据保留（ZDR）与多模型自动容灾切换。`;
      if (userText.toLowerCase().includes('zdr') || userText.includes('零数据保留') || userText.includes('隐私')) {
        reply = `ZDR (Zero Data Retention) 保障：当请求路由至支持 ZDR 的端点时，模型提供商与路由网关均承诺不保留任何 Prompt 与输出日志。您可以通过 GET /endpoints/zdr 预检接口查询所有受 ZDR 保护的模型节点清单与合规标准（SOC2 / HIPAA）。`;
      } else if (userText.toLowerCase().includes('token') || userText.includes('计费') || userText.includes('费用')) {
        reply = `Token 计算与计费机制：平台针对不同模型采用其原生 Tokenizer 严格精准计量，并在每一次响应或通过 GET /generation 接口中返回精确至 $0.000001 的费用明细与 Prompt 缓存节省统计。`;
      } else if (userText.toLowerCase().includes('harness') || userText.includes('ori') || userText.includes('agent')) {
        reply = `Ori Harness 智能体套件专为编码与自动化 Agent 打造，可通过 npm i @ufreetokens/sdk 或专用 CLI 工具链运行。在 Responses API 中，2026 最新规范将 judge_model 统一演进为 analyst_model，实现流式代码审计与执行过程多轮评估。`;
      }
      setAiChatMessages((prev) => [...prev, { role: 'assistant', content: reply }]);
    }, 850);
  };

  const filteredEndpoints = API_ENDPOINTS.filter((ep) => {
    if (!sidebarSearch.trim()) return true;
    const q = sidebarSearch.toLowerCase();
    return (
      ep.name.toLowerCase().includes(q) ||
      ep.path.toLowerCase().includes(q) ||
      ep.category.toLowerCase().includes(q) ||
      ep.summary.toLowerCase().includes(q)
    );
  });

  return (
    <div className="min-h-screen bg-white text-gray-800 font-sans flex flex-col selection:bg-purple-100 selection:text-purple-900">
      {/* 1. Top Bar / Sub-Navigation */}
      <header className="border-b border-gray-200 bg-white/95 backdrop-blur-xs px-3 sm:px-4 py-2 flex items-center justify-between sticky top-12 z-30 select-none text-xs">
        <div className="flex items-center space-x-1 sm:space-x-2 overflow-x-auto no-scrollbar">
          {onBackToMarketplace && (
            <button
              onClick={onBackToMarketplace}
              className="flex items-center space-x-1 px-2.5 py-1.5 rounded-md text-gray-600 hover:text-gray-900 hover:bg-gray-100 cursor-pointer mr-1 transition-colors shrink-0 font-medium"
              title="返回模型集市列表"
            >
              <ArrowLeft className="w-3.5 h-3.5" />
              <span className="hidden sm:inline">模型集市</span>
            </button>
          )}

          {[
            { id: 'docs', label: '开发文档 (Docs)', icon: BookOpen },
            { id: 'api-ref', label: 'API 接口参考 (Reference)', icon: Code2 },
            { id: 'client-sdks', label: '客户端 SDK', icon: Terminal },
            { id: 'agent-sdk', label: 'Agent 智能体 SDK', icon: Bot },
            { id: 'cookbook', label: '实战手册 (Cookbook)', icon: CookingPot }
          ].map((tab) => {
            const Icon = tab.icon;
            const isSelected = topTab === tab.id;
            return (
              <button
                key={tab.id}
                onClick={() => {
                  setTopTab(tab.id as any);
                  if (tab.id === 'api-ref') {
                    setIsViewingApiEndpoint(true);
                  } else {
                    setIsViewingApiEndpoint(false);
                  }
                }}
                className={`flex items-center space-x-1.5 px-3 py-1.5 rounded-md font-medium transition-colors cursor-pointer whitespace-nowrap shrink-0 ${
                  isSelected
                    ? 'bg-purple-50 text-purple-700 shadow-xs border border-purple-200 font-semibold'
                    : 'text-gray-600 hover:text-gray-900 hover:bg-gray-100'
                }`}
              >
                <Icon className={`w-3.5 h-3.5 ${isSelected ? 'text-purple-600' : 'text-gray-400'}`} />
                <span>{tab.label}</span>
              </button>
            );
          })}
        </div>

        <div className="flex items-center space-x-2 sm:space-x-3 text-xs shrink-0 ml-2">
          <div className="hidden md:flex items-center text-emerald-800 gap-1.5 bg-emerald-50 border border-emerald-200 rounded px-2 py-0.5 font-mono text-[11px]">
            <span className="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-pulse"></span>
            <span>API v1 · 运行正常</span>
          </div>
          <a
            href="https://ufreetokens.com/docs"
            target="_blank"
            rel="noreferrer"
            className="text-gray-500 hover:text-gray-900 flex items-center gap-1 transition-colors px-2 py-1 rounded hover:bg-gray-100"
          >
            <span className="hidden sm:inline">uFreeTokens 官方规范</span>
            <ExternalLink className="w-3 h-3" />
          </a>
        </div>
      </header>

      {/* Mobile view switcher for small screens */}
      <div className="lg:hidden bg-gray-50 border-b border-gray-200 px-3 py-1.5 flex items-center justify-around text-xs sticky top-22 z-20">
        <button
          onClick={() => setMobileActivePanel('sidebar')}
          className={`px-3 py-1 rounded-md font-medium flex items-center gap-1.5 ${
            mobileActivePanel === 'sidebar' ? 'bg-purple-100 text-purple-800 border border-purple-200 font-semibold' : 'text-gray-600'
          }`}
        >
          <FileText className="w-3.5 h-3.5" />
          <span>导航目录</span>
        </button>
        <button
          onClick={() => setMobileActivePanel('content')}
          className={`px-3 py-1 rounded-md font-medium flex items-center gap-1.5 ${
            mobileActivePanel === 'content' ? 'bg-purple-100 text-purple-800 border border-purple-200 font-semibold' : 'text-gray-600'
          }`}
        >
          <BookOpen className="w-3.5 h-3.5" />
          <span>文档正文</span>
        </button>
        {(isViewingApiEndpoint || topTab === 'api-ref') && (
          <button
            onClick={() => setMobileActivePanel('console')}
            className={`px-3 py-1 rounded-md font-medium flex items-center gap-1.5 ${
              mobileActivePanel === 'console' ? 'bg-purple-100 text-purple-800 border border-purple-200 font-semibold' : 'text-gray-600'
            }`}
          >
            <Code2 className="w-3.5 h-3.5" />
            <span>调试终端</span>
          </button>
        )}
      </div>

      {/* ========================================================================= */}
      {/* UNIFIED WORKSPACE: Left Sidebar + Dynamic Content Canvas */}
      {/* ========================================================================= */}
      <div className="flex-1 flex flex-col lg:flex-row max-w-full overflow-hidden bg-white">
        {/* ========================================================================= */}
        {/* COLUMN 1: Left Navigation Sidebar (Matching screenshot) */}
        {/* ========================================================================= */}
        <DocsSidebar
          activeDocId={activeDocId}
          onSelectDoc={handleSelectDoc}
          className={`w-full lg:w-64 xl:w-72 shrink-0 max-h-[calc(100vh-6rem)] lg:sticky lg:top-24 ${
            mobileActivePanel !== 'sidebar' ? 'hidden lg:flex' : 'flex'
          }`}
          onCloseMobile={() => setMobileActivePanel('content')}
        />

        {/* ========================================================================= */}
        {/* VIEW A: API Reference (Endpoint Details + Right Execution Console) */}
        {/* ========================================================================= */}
        {(topTab === 'api-ref' || isViewingApiEndpoint) && (
          <>

          {/* ========================================================================= */}
          {/* COLUMN 2: Middle Content Panel (Endpoint Details + Live Changelog) */}
          {/* ========================================================================= */}
          <main
            ref={mainScrollRef}
            className={`flex-1 min-w-0 p-4 sm:p-6 lg:p-8 space-y-8 overflow-y-auto max-h-[calc(100vh-6rem)] pb-28 ${
              mobileActivePanel !== 'content' ? 'hidden lg:block' : 'block'
            }`}
          >
            {/* Breadcrumb & Copy page */}
            <div className="flex items-center justify-between text-xs text-gray-500">
              <div className="flex items-center space-x-1.5 font-medium">
                <span className="text-gray-400">API 接口参考</span>
                <span>/</span>
                <span className="text-purple-600 font-semibold">{selectedEndpoint.category}</span>
              </div>

              <div className="flex items-center space-x-2">
                <button
                  onClick={() => setMobileActivePanel('console')}
                  className="lg:hidden flex items-center space-x-1 px-2.5 py-1 bg-purple-50 border border-purple-200 rounded-md text-purple-700 text-xs font-semibold"
                >
                  <Code2 className="w-3.5 h-3.5" />
                  <span>调试控制台</span>
                </button>
                <button
                  onClick={() => handleCopy(window.location.href, 'page-url')}
                  className="flex items-center space-x-1.5 px-2.5 py-1 bg-white border border-gray-200 rounded-md text-gray-700 hover:text-gray-950 hover:bg-gray-50 transition-colors cursor-pointer text-xs shadow-xs"
                >
                  {copiedSection === 'page-url' ? (
                    <Check className="w-3.5 h-3.5 text-emerald-600" />
                  ) : (
                    <Copy className="w-3.5 h-3.5 text-gray-400" />
                  )}
                  <span>{copiedSection === 'page-url' ? '已复制链接' : '复制页面链接'}</span>
                </button>
              </div>
            </div>

            {/* Heading */}
            <div>
              <div className="flex items-center gap-2 mb-1">
                <span className="px-2 py-0.5 rounded text-[11px] font-mono font-bold bg-purple-50 text-purple-700 border border-purple-200">
                  {selectedEndpoint.category}
                </span>
                <span className="text-xs text-gray-400">OpenAPI 3.1 规范</span>
              </div>
              <h1 className="text-2xl sm:text-3xl font-extrabold text-gray-950 tracking-tight leading-snug">
                {selectedEndpoint.name}
              </h1>
              <p className="text-xs sm:text-sm text-gray-600 mt-2 leading-relaxed">
                {selectedEndpoint.description}
              </p>
            </div>

            {/* Endpoint Bar + Try It button */}
            <div className="flex items-center justify-between p-3 bg-gray-50 border border-gray-200 rounded-lg shadow-xs">
              <div className="flex items-center space-x-3 font-mono text-xs overflow-x-auto">
                <span className={`px-2 py-0.5 rounded font-bold shrink-0 ${
                  selectedEndpoint.method === 'GET'
                    ? 'text-emerald-700 bg-emerald-100 border border-emerald-300'
                    : selectedEndpoint.method === 'POST'
                    ? 'text-blue-700 bg-blue-100 border border-blue-300'
                    : selectedEndpoint.method === 'DELETE'
                    ? 'text-rose-700 bg-rose-100 border border-rose-300'
                    : 'text-amber-700 bg-amber-100 border border-amber-300'
                }`}>
                  {selectedEndpoint.method}
                </span>
                <span className="font-semibold text-gray-900 truncate">{selectedEndpoint.path}</span>
              </div>

              <button
                onClick={handleTryIt}
                disabled={isExecutingTryIt}
                className="flex items-center space-x-1.5 px-3.5 py-1.5 bg-emerald-600 hover:bg-emerald-700 text-white font-bold text-xs rounded-full shadow-sm transition-transform active:scale-95 cursor-pointer shrink-0 ml-2"
              >
                {isExecutingTryIt ? (
                  <RotateCw className="w-3.5 h-3.5 animate-spin" />
                ) : (
                  <Play className="w-3.5 h-3.5 fill-current" />
                )}
                <span>{isExecutingTryIt ? '请求中...' : '运行测试 (Try it)'}</span>
              </button>
            </div>

            {/* Usage Note Code Snippet */}
            {selectedEndpoint.usageNote && (
              <div className="bg-slate-900 border border-slate-800 rounded-lg p-4 font-mono text-xs text-slate-200 relative group shadow-sm">
                <div className="flex items-center justify-between mb-2 text-[11px] text-slate-400 font-sans border-b border-slate-800 pb-1.5">
                  <span className="font-semibold flex items-center gap-1.5 text-purple-300">
                    <Sparkles className="w-3.5 h-3.5" />
                    TypeScript 类型定义与原生 Tokenizer 计费说明
                  </span>
                  <button
                    onClick={() => handleCopy(selectedEndpoint.usageNote!, 'usage-note')}
                    className="p-1 rounded bg-slate-800 text-slate-300 hover:text-white transition-colors cursor-pointer flex items-center gap-1"
                    title="复制类型定义"
                  >
                    {copiedSection === 'usage-note' ? <Check className="w-3 h-3 text-emerald-400" /> : <Copy className="w-3 h-3" />}
                    <span className="text-[10px]">{copiedSection === 'usage-note' ? '已复制' : '复制代码'}</span>
                  </button>
                </div>
                <pre className="text-slate-300 overflow-x-auto whitespace-pre leading-relaxed text-[11px]">
                  {selectedEndpoint.usageNote}
                </pre>
              </div>
            )}

            {/* Request Parameters Section */}
            <div className="space-y-4 pt-2">
              <div className="flex items-center justify-between border-b border-gray-200 pb-2">
                <h2 className="text-sm font-bold text-gray-900 uppercase tracking-wider flex items-center gap-2">
                  <Sliders className="w-4 h-4 text-purple-600" />
                  <span>请求体 / 查询参数 (Body & Query Parameters)</span>
                </h2>
                <span className="px-2 py-0.5 rounded bg-gray-100 border border-gray-200 text-[11px] font-mono text-gray-600">
                  application/json
                </span>
              </div>

              <div className="divide-y divide-gray-100 space-y-4">
                {selectedEndpoint.params.map((param) => (
                  <div key={param.name} className="pt-3 first:pt-0 space-y-1.5">
                    <div className="flex items-center space-x-2 text-xs font-mono">
                      <span className="font-bold text-purple-700">{param.name}</span>
                      <span className="text-gray-500">{param.type}</span>
                      {param.required ? (
                        <span className="text-rose-700 text-[10px] font-semibold bg-rose-50 border border-rose-200 px-1 rounded">必填</span>
                      ) : (
                        <span className="text-gray-400 text-[10px]">选填</span>
                      )}
                    </div>

                    <p className="text-xs text-gray-600 leading-relaxed">
                      {param.description}
                    </p>

                    {param.options && (
                      <div className="flex items-center space-x-1.5 text-[11px] font-mono text-gray-600">
                        <span>可选枚举值:</span>
                        {param.options.map((opt) => (
                          <code key={opt} className="px-1.5 py-0.2 bg-purple-50 border border-purple-200 rounded text-purple-700">
                            {opt}
                          </code>
                        ))}
                      </div>
                    )}

                    {param.example && (
                      <div className="text-[11px] font-mono text-gray-500">
                        示例取值: <code className="text-emerald-700 bg-emerald-50 border border-emerald-200 px-1.5 py-0.5 rounded">{param.example}</code>
                      </div>
                    )}
                  </div>
                ))}
              </div>
            </div>

            {/* ======================================================================= */}
            {/* Detailed API Changelog Section (Pixel-perfect matching screenshot) */}
            {/* ======================================================================= */}
            <div ref={changelogRef} id="api-changelog-section" className="pt-10 border-t border-gray-200 space-y-8">
              <div className="flex items-center justify-between flex-wrap gap-2">
                <div>
                  <h2 className="text-xl font-black text-gray-950 tracking-tight flex items-center gap-2">
                    <Clock className="w-4 h-4 text-purple-600" />
                    <span>API 更新日志与规范演进 (Changelog)</span>
                  </h2>
                  <p className="text-xs text-gray-500 mt-1">
                    跟踪即时 OpenAPI 规范演进、破坏性变更 (Breaking Changes)、字段弃用与新发布端点。
                  </p>
                </div>

                {/* Tag filters on right */}
                <div className="flex items-center gap-1.5 text-[10px] font-medium">
                  {['图像分析', '模型矩阵', '语音 STT', '企业工作区'].map((tag) => (
                    <span key={tag} className="px-2 py-1 bg-white border border-gray-200 rounded text-gray-600 hover:text-gray-950 hover:border-gray-300 cursor-pointer transition-colors shadow-xs">
                      {tag}
                    </span>
                  ))}
                </div>
              </div>

              {/* Date Group: July 29, 2026 */}
              <div className="space-y-6">
                <div className="flex items-center space-x-2 text-xs flex-wrap gap-1">
                  <span className="px-2.5 py-1 rounded-full bg-emerald-50 border border-emerald-200 text-emerald-800 font-semibold font-mono">
                    2026 年 7 月 29 日
                  </span>
                  <span className="px-2 py-0.5 rounded bg-rose-50 border border-rose-200 text-rose-700 text-[11px] font-medium">
                    破坏性变更 (Breaking)
                  </span>
                  <span className="px-2 py-0.5 rounded bg-gray-100 text-gray-700 text-[11px]">
                    Files 文件服务
                  </span>
                  <span className="px-2 py-0.5 rounded bg-gray-100 text-gray-700 text-[11px]">
                    Responses 响应
                  </span>
                  <span className="px-2 py-0.5 rounded bg-gray-100 text-gray-700 text-[11px]">
                    Schemas 数据结构
                  </span>
                </div>

                {/* Breaking Changes */}
                <div className="space-y-3 bg-amber-50/40 border border-amber-200/80 rounded-lg p-4 text-xs">
                  <h3 className="font-bold text-amber-900 flex items-center gap-1.5 text-sm">
                    <AlertTriangle className="w-3.5 h-3.5 text-amber-600" />
                    <span>破坏性变更 (Breaking changes)</span>
                  </h3>

                  <div className="space-y-3 text-gray-700 pl-2 border-l border-amber-300">
                    <div>
                      <div className="flex items-center gap-2 flex-wrap">
                        <span className="font-mono text-purple-700 font-medium">
                          FusionCallAnalysisInProgressEvent
                        </span>
                        <span className="text-gray-500">: 新增必填属性</span>
                        <code className="text-emerald-700 bg-emerald-50 border border-emerald-200 px-1 py-0.5 rounded font-mono">analyst_model</code>
                        <span className="px-1.5 py-0.2 bg-gray-100 text-gray-700 text-[10px] rounded font-medium ml-2 border border-gray-200">
                          旧客户端无需修改 (No action needed)
                        </span>
                      </div>
                      <p className="text-[11px] text-gray-600 mt-1 leading-relaxed">
                        <strong>现有调用者无需任何迁移操作。</strong> <code className="text-purple-700 font-mono">analyst_model</code> 在此流式事件中正式继承并取代 <code className="text-purple-700 font-mono">judge_model</code>；系统自动将 <code className="text-purple-700 font-mono">judge_model</code> 作为向后兼容别名保留，两者返回值完全一致。
                      </p>
                      <div className="text-[10px] font-mono text-gray-400 mt-1">
                        使用端点: <span className="text-blue-600 font-semibold">POST /responses</span>
                      </div>
                    </div>

                    <div className="pt-2 border-t border-amber-200">
                      <div className="flex items-center gap-2 flex-wrap">
                        <code className="text-purple-700 font-mono">$.tags: responses</code>
                        <span className="text-gray-600">对象更名规范化</span>
                        <span className="px-1.5 py-0.2 bg-gray-100 text-gray-700 text-[10px] rounded font-medium ml-2 border border-gray-200">
                          无需修改代码
                        </span>
                      </div>
                      <p className="text-[11px] text-gray-600 mt-1 leading-relaxed">
                        <strong>无需任何操作。</strong> OpenAPI 中的 <code className="text-purple-700">responses</code> 标签统一重命名为大写的 <code className="text-purple-700">Responses</code>，以保持整套 API Reference 命名规范的一致性。接口路径 <code className="text-blue-600">POST /api/v1/responses</code> 与底层 Schema 保持完全不变。SDK 命名空间继续保留（TypeScript/Python 中为 <code className="text-purple-700">client.responses.send()</code>，Go 中为 <code className="text-purple-700">Sdk.Responses.Send()</code>）。
                      </p>
                    </div>
                  </div>
                </div>

                {/* Modified Endpoints */}
                <div className="space-y-3 bg-white border border-gray-200 rounded-lg p-4 text-xs shadow-xs">
                  <h3 className="font-bold text-gray-900 flex items-center gap-1.5 text-sm">
                    <Sliders className="w-3.5 h-3.5 text-purple-600" />
                    <span>已演进端点 (Modified endpoints)</span>
                  </h3>

                  <div className="space-y-2 font-mono text-[11px] text-gray-700">
                    <div className="flex items-baseline gap-2 flex-wrap">
                      <span className="text-emerald-700 font-bold">GET</span>
                      <span className="text-gray-900 font-semibold">/files</span>
                      <span className="text-gray-500">: 参数 <code className="text-purple-700">cursor</code> 校验增强；响应 Schema 补充文件哈希校验字段</span>
                    </div>
                    <div className="flex items-baseline gap-2 flex-wrap">
                      <span className="text-blue-700 font-bold">POST</span>
                      <span className="text-gray-900 font-semibold">/files</span>
                      <span className="text-gray-500">: 响应 Schema 补充已耗时与存储区域字段</span>
                    </div>
                    <div className="flex items-baseline gap-2 flex-wrap">
                      <span className="text-rose-700 font-bold">DELETE</span>
                      <span className="text-gray-900 font-semibold">/files/&#123;file_id&#125;</span>
                      <span className="text-gray-500">: 参数 <code className="text-purple-700">file_id</code> 支持批量逗号分隔安全擦除</span>
                    </div>
                    <div className="flex items-baseline gap-2 flex-wrap">
                      <span className="text-emerald-700 font-bold">GET</span>
                      <span className="text-gray-900 font-semibold">/files/&#123;file_id&#125;</span>
                      <span className="text-gray-500">: 响应新增 MIME 类型深度检测</span>
                    </div>
                    <div className="flex items-baseline gap-2 flex-wrap">
                      <span className="text-emerald-700 font-bold">GET</span>
                      <span className="text-gray-900 font-semibold">/files/&#123;file_id&#125;/content</span>
                      <span className="text-gray-500">: 增加断点续传 HTTP Range Header 支持</span>
                    </div>
                  </div>
                </div>

                {/* Modified Schemas */}
                <div className="space-y-3 bg-white border border-gray-200 rounded-lg p-4 text-xs shadow-xs">
                  <h3 className="font-bold text-gray-900 flex items-center gap-1.5 text-sm">
                    <Layers className="w-3.5 h-3.5 text-blue-600" />
                    <span>已演进数据结构 (Modified schemas)</span>
                  </h3>

                  <div className="space-y-3 divide-y divide-gray-100">
                    <div className="pt-2 first:pt-0 space-y-1">
                      <div className="font-mono text-purple-700 font-semibold">FusionAnalysisResult</div>
                      <p className="text-[11px] text-gray-500">字段注释更新，优化代码解释器执行日志格式</p>
                      <div className="text-[10px] font-mono text-gray-400">
                        使用端点: <span className="text-blue-600">POST /presets/&#123;slug&#125;/responses</span>, <span className="text-blue-600">POST /responses</span>
                      </div>
                    </div>

                    <div className="pt-2 space-y-1">
                      <div className="font-mono text-purple-700 font-semibold">FusionPlugin</div>
                      <p className="text-[11px] text-gray-500">更新插件沙箱权限描述，增加超时阻断回调</p>
                      <button
                        onClick={() => toggleSchemaExpand('FusionPlugin')}
                        className="text-[10px] font-mono text-purple-600 hover:text-purple-700 flex items-center gap-1 cursor-pointer transition-colors"
                      >
                        <span>{expandedSchemas['FusionPlugin'] ? '收起关联端点' : '查看使用该 Schema 的 6 个端点'}</span>
                        <ChevronDown className={`w-3 h-3 transition-transform ${expandedSchemas['FusionPlugin'] ? 'rotate-180' : ''}`} />
                      </button>
                      {expandedSchemas['FusionPlugin'] && (
                        <div className="p-2.5 bg-gray-50 rounded font-mono text-[10px] space-y-1 text-gray-700 border border-gray-200 mt-1">
                          <div>• POST /chat/completions (支持插件函数注入)</div>
                          <div>• POST /presets/&#123;slug&#125;/responses (预设流式响应)</div>
                          <div>• POST /responses (智能体执行评测)</div>
                          <div>• GET /plugins/active (获取已激活插件列表)</div>
                          <div>• POST /plugins/verify (插件安全沙箱鉴权校验)</div>
                          <div>• PATCH /plugins/config (动态更新插件环境变量)</div>
                        </div>
                      )}
                    </div>

                    <div className="pt-2 space-y-1">
                      <div className="font-mono text-purple-700 font-semibold">FusionServerToolConfig</div>
                      <p className="text-[11px] text-gray-500">
                        结构体注释更新；<code className="text-emerald-700 bg-emerald-50 px-1 rounded">effort</code> 参数支持 fine/coarse 粒度调节；更新 <code className="text-emerald-700 bg-emerald-50 px-1 rounded">max_tokens</code> 校验
                      </p>
                      <button
                        onClick={() => toggleSchemaExpand('FusionServerToolConfig')}
                        className="text-[10px] font-mono text-purple-600 hover:text-purple-700 flex items-center gap-1 cursor-pointer transition-colors"
                      >
                        <span>{expandedSchemas['FusionServerToolConfig'] ? '收起关联端点' : '查看使用该 Schema 的 4 个端点'}</span>
                        <ChevronDown className={`w-3 h-3 transition-transform ${expandedSchemas['FusionServerToolConfig'] ? 'rotate-180' : ''}`} />
                      </button>
                      {expandedSchemas['FusionServerToolConfig'] && (
                        <div className="p-2.5 bg-gray-50 rounded font-mono text-[10px] space-y-1 text-gray-700 border border-gray-200 mt-1">
                          <div>• POST /chat/completions</div>
                          <div>• POST /tools/execute</div>
                          <div>• GET /tools/server-status</div>
                          <div>• PATCH /tools/config</div>
                        </div>
                      )}
                    </div>

                    <div className="pt-2 space-y-1">
                      <div className="font-mono text-purple-700 font-semibold">Guardrail</div>
                      <p className="text-[11px] text-gray-500">
                        更新示例结构；新增 <code className="text-emerald-700 bg-emerald-50 px-1 rounded">enable_free_model_publication</code>（控制免费模型发布审计）与 <code className="text-emerald-700 bg-emerald-50 px-1 rounded">enable_paid_model_training</code>（商业训练授权阻断）
                      </p>
                      <button
                        onClick={() => toggleSchemaExpand('Guardrail')}
                        className="text-[10px] font-mono text-purple-600 hover:text-purple-700 flex items-center gap-1 cursor-pointer transition-colors"
                      >
                        <span>{expandedSchemas['Guardrail'] ? '收起关联端点' : '查看使用该 Schema 的 4 个端点'}</span>
                        <ChevronDown className={`w-3 h-3 transition-transform ${expandedSchemas['Guardrail'] ? 'rotate-180' : ''}`} />
                      </button>
                      {expandedSchemas['Guardrail'] && (
                        <div className="p-2.5 bg-gray-50 rounded font-mono text-[10px] space-y-1 text-gray-700 border border-gray-200 mt-1">
                          <div>• GET /guardrails (获取组织安全护栏列表)</div>
                          <div>• POST /guardrails (创建新安全防护规则)</div>
                          <div>• GET /guardrails/&#123;id&#125; (获取指定规则明细)</div>
                          <div>• PATCH /guardrails/&#123;id&#125; (修改护栏策略)</div>
                        </div>
                      )}
                    </div>
                  </div>
                </div>
              </div>

              {/* Date Group: July 27, 2026 */}
              <div className="space-y-4 pt-4 border-t border-gray-200">
                <div className="flex items-center space-x-2 text-xs">
                  <span className="px-2.5 py-1 rounded-full bg-gray-100 border border-gray-200 text-gray-800 font-semibold font-mono">
                    2026 年 7 月 27 日
                  </span>
                  <span className="px-2 py-0.5 rounded bg-gray-100 text-gray-700 text-[11px]">
                    Embeddings
                  </span>
                  <span className="px-2 py-0.5 rounded bg-gray-100 text-gray-700 text-[11px]">
                    Schemas
                  </span>
                </div>

                <div className="bg-white border border-gray-200 rounded-lg p-4 text-xs space-y-2 shadow-xs">
                  <div className="font-mono text-emerald-700 font-semibold">POST /embeddings</div>
                  <p className="text-gray-600 text-[11px]">
                    请求 Schema 更新：支持指定输出向量维度的截断限制校验（Dimension clamp validation），支持 Cohere V3 与 OpenAI 3-Large 维度自适应。
                  </p>
                </div>
              </div>
            </div>
          </main>

          {/* ========================================================================= */}
          {/* COLUMN 3: Right Interactive Code & Response Panel */}
          {/* ========================================================================= */}
          <aside className={`w-full lg:w-96 xl:w-[440px] shrink-0 border-t lg:border-t-0 lg:border-l border-gray-200 bg-gray-50/60 overflow-y-auto max-h-[calc(100vh-6rem)] lg:sticky lg:top-24 p-3 sm:p-4 space-y-4 text-xs pb-28 ${
            mobileActivePanel !== 'console' ? 'hidden lg:block' : 'block'
          }`}>
            {/* Request Header Bar */}
            <div className="flex items-center justify-between border-b border-gray-200 pb-2">
              <span className="font-semibold text-gray-900 truncate pr-2">
                {selectedEndpoint.name}
              </span>

              <div className="flex items-center space-x-1.5 shrink-0">
                {/* Language Switcher */}
                <div className="flex bg-white border border-gray-200 rounded p-0.5 shadow-xs">
                  {(['curl', 'js', 'python'] as const).map((lang) => (
                    <button
                      key={lang}
                      onClick={() => setCodeLang(lang)}
                      className={`px-2 py-0.5 rounded text-[10px] font-mono cursor-pointer transition-colors ${
                        codeLang === lang
                          ? 'bg-purple-600 text-white font-bold'
                          : 'text-gray-500 hover:text-gray-900'
                      }`}
                    >
                      {lang === 'curl' ? 'cURL' : lang === 'js' ? 'TS/JS' : 'Python'}
                    </button>
                  ))}
                </div>

                <button
                  onClick={() => handleCopy(selectedEndpoint.requestSnippet[codeLang], 'req-code')}
                  className="p-1 rounded bg-white border border-gray-200 text-gray-500 hover:text-gray-900 transition-colors cursor-pointer shadow-xs"
                  title="复制代码示例"
                >
                  {copiedSection === 'req-code' ? <Check className="w-3.5 h-3.5 text-emerald-600" /> : <Copy className="w-3.5 h-3.5" />}
                </button>
              </div>
            </div>

            {/* Request Code Snippet */}
            <div className="bg-slate-900 border border-slate-800 rounded-lg p-3 font-mono text-[11px] text-slate-200 overflow-x-auto shadow-xs">
              <pre className="whitespace-pre leading-relaxed text-purple-200">
                {selectedEndpoint.requestSnippet[codeLang]}
              </pre>
            </div>

            {/* Headers & Payload Display */}
            {selectedEndpoint.samplePayload && (
              <div className="bg-white border border-gray-200 rounded-lg p-3 font-mono text-[11px] text-gray-700 space-y-2 shadow-xs">
                <div className="flex items-center justify-between text-[10px] text-gray-500 border-b border-gray-100 pb-1 font-sans">
                  <span className="font-semibold uppercase tracking-wider text-gray-500">请求头与载荷 (Headers & Payload)</span>
                  <button
                    onClick={() => handleCopy(selectedEndpoint.samplePayload!, 'payload')}
                    className="hover:text-gray-900 cursor-pointer flex items-center gap-1 text-[10px] text-purple-600 font-medium"
                  >
                    {copiedSection === 'payload' ? <Check className="w-3 h-3 text-emerald-600" /> : <Copy className="w-3 h-3" />}
                    <span>{copiedSection === 'payload' ? '已复制' : '复制'}</span>
                  </button>
                </div>
                <pre className="whitespace-pre text-gray-600 overflow-x-auto text-[10px] leading-relaxed">
                  {selectedEndpoint.samplePayload}
                </pre>
              </div>
            )}

            {/* Response Section */}
            <div className="space-y-2 pt-2 border-t border-gray-200">
              {/* Status Tabs */}
              <div className="flex items-center justify-between">
                <div className="flex items-center space-x-1 overflow-x-auto py-1 no-scrollbar">
                  {Object.keys(selectedEndpoint.responses).map((statusStr) => {
                    const status = parseInt(statusStr, 10);
                    const isSelected = selectedStatusCode === status;
                    const is2xx = status >= 200 && status < 300;
                    const is4xx = status >= 400 && status < 500;
                    return (
                      <button
                        key={status}
                        onClick={() => setSelectedStatusCode(status)}
                        className={`px-2 py-0.5 rounded text-[10px] font-mono cursor-pointer transition-colors ${
                          isSelected
                            ? is2xx
                              ? 'bg-emerald-50 text-emerald-700 font-bold border border-emerald-300'
                              : is4xx
                              ? 'bg-rose-50 text-rose-700 font-bold border border-rose-300'
                              : 'bg-amber-50 text-amber-700 font-bold border border-amber-300'
                            : 'bg-white border border-gray-200 text-gray-500 hover:text-gray-800'
                        }`}
                      >
                        {status} {status === 200 ? '成功' : status === 401 ? '未鉴权' : status === 402 ? '欠费' : status === 429 ? '限流' : status === 500 ? '异常' : ''}
                      </button>
                    );
                  })}
                </div>

                <button
                  onClick={() => handleCopy(selectedEndpoint.responses[selectedStatusCode] || '', 'res-body')}
                  className="p-1 rounded bg-white border border-gray-200 text-gray-500 hover:text-gray-900 transition-colors cursor-pointer shrink-0 ml-1 shadow-xs"
                  title="复制响应内容"
                >
                  {copiedSection === 'res-body' ? <Check className="w-3.5 h-3.5 text-emerald-600" /> : <Copy className="w-3.5 h-3.5" />}
                </button>
              </div>

              {/* Live Try It Execution Banner */}
              {tryItOutput && (
                <div className="p-2.5 bg-emerald-50 border border-emerald-200 rounded text-[11px] flex items-center justify-between text-emerald-800 animate-fadeIn shadow-xs">
                  <div className="flex items-center gap-1.5 font-mono font-semibold">
                    <CheckCircle2 className="w-3.5 h-3.5 text-emerald-600" />
                    <span>状态: {tryItOutput.status} OK</span>
                  </div>
                  <span className="font-mono text-gray-500 text-[10px]">网络耗时: {tryItOutput.time}</span>
                </div>
              )}

              {/* Response JSON Viewer */}
              <div className="bg-slate-900 border border-slate-800 rounded-lg p-3 font-mono text-[11px] max-h-80 overflow-y-auto shadow-xs">
                <pre className="text-slate-200 whitespace-pre leading-relaxed">
                  {selectedEndpoint.responses[selectedStatusCode] || '暂无该状态码的响应结构'}
                </pre>
              </div>
            </div>
          </aside>
        </>
      )}

      {/* ========================================================================= */}
      {/* VIEW B: 开发文档 (DocArticleView Dynamic Documentation Article) */}
      {/* ========================================================================= */}
      {!isViewingApiEndpoint && topTab === 'docs' && (
        <main
          className={`flex-1 min-w-0 overflow-y-auto max-h-[calc(100vh-6rem)] ${
            mobileActivePanel !== 'content' ? 'hidden lg:block' : 'block'
          }`}
        >
          <DocArticleView
            docId={activeDocId}
            onNavigateToEndpoint={(epId) => {
              setSelectedEndpointId(epId);
              setIsViewingApiEndpoint(true);
              setTopTab('api-ref');
            }}
          />
        </main>
      )}

      {/* ========================================================================= */}
      {/* VIEW C: 客户端 SDK (Client SDKs) */}
      {/* ========================================================================= */}
      {!isViewingApiEndpoint && topTab === 'client-sdks' && (
        <main
          className={`flex-1 min-w-0 overflow-y-auto max-h-[calc(100vh-6rem)] p-4 sm:p-8 space-y-8 pb-28 ${
            mobileActivePanel !== 'content' ? 'hidden lg:block' : 'block'
          }`}
        >
          <div className="border-b border-gray-200 pb-6">
            <span className="px-2.5 py-1 rounded bg-blue-50 text-blue-700 text-xs font-semibold border border-blue-200">
              SDK 与开发者工具链
            </span>
            <h1 className="text-3xl font-extrabold text-gray-950 mt-3">
              官方与社区主流 SDK 支持
            </h1>
            <p className="text-gray-600 text-sm mt-2 leading-relaxed">
              支持 TypeScript、Python、Go、Rust、Java 等多语言接入，亦可无缝使用 OpenAI、LangChain、LlamaIndex 官方 SDK。
            </p>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
            {/* TS SDK */}
            <div className="bg-white border border-gray-200 rounded-xl p-5 space-y-4 shadow-xs">
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-2">
                  <div className="w-7 h-7 rounded bg-blue-100 text-blue-700 flex items-center justify-center font-bold text-xs">
                    TS
                  </div>
                  <span className="font-bold text-gray-950 text-sm">@ufreetokens/sdk</span>
                </div>
                <span className="text-[11px] font-mono text-gray-500">npm package</span>
              </div>
              <pre className="p-2.5 bg-gray-50 rounded font-mono text-xs text-purple-700 border border-gray-200">
                npm install @ufreetokens/sdk
              </pre>
              <p className="text-gray-600 text-xs leading-relaxed">
                包含严格的 TypeScript 类型提示、原生流式解析器、全自动重试中间件以及 Tokenizer 费用预估工具。
              </p>
            </div>

            {/* Python SDK */}
            <div className="bg-white border border-gray-200 rounded-xl p-5 space-y-4 shadow-xs">
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-2">
                  <div className="w-7 h-7 rounded bg-emerald-100 text-emerald-700 flex items-center justify-center font-bold text-xs">
                    PY
                  </div>
                  <span className="font-bold text-gray-950 text-sm">ufreetokens-python / openai</span>
                </div>
                <span className="text-[11px] font-mono text-gray-500">pip package</span>
              </div>
              <pre className="p-2.5 bg-gray-50 rounded font-mono text-xs text-emerald-700 border border-gray-200">
                pip install ufreetokens openai
              </pre>
              <p className="text-gray-600 text-xs leading-relaxed">
                全面兼容 Python 3.9+，支持异步 AsyncOpenAI、Pydantic 结构化输出解析与 Tenacity 指数退避重试。
              </p>
            </div>
          </div>
        </main>
      )}

      {/* ========================================================================= */}
      {/* VIEW D: Agent 智能体 SDK (Agent SDK) */}
      {/* ========================================================================= */}
      {!isViewingApiEndpoint && topTab === 'agent-sdk' && (
        <main
          className={`flex-1 min-w-0 overflow-y-auto max-h-[calc(100vh-6rem)] p-4 sm:p-8 space-y-8 pb-28 ${
            mobileActivePanel !== 'content' ? 'hidden lg:block' : 'block'
          }`}
        >
          <div className="border-b border-gray-200 pb-6">
            <span className="px-2.5 py-1 rounded bg-indigo-50 text-indigo-700 text-xs font-semibold border border-indigo-200">
              Agent 智能体套件
            </span>
            <h1 className="text-3xl font-extrabold text-gray-950 mt-3">
              Ori Harness 智能体与代码评估套件
            </h1>
            <p className="text-gray-600 text-sm mt-2 leading-relaxed">
              专为长链路自主 Agent 设计的评测、工具调用与多步执行沙箱。通过与 Responses API 配合，实时捕获 Agent 决策流并完成多模型交叉校验。
            </p>
          </div>

          <div className="bg-white border border-gray-200 rounded-xl p-6 space-y-4 shadow-xs">
            <h3 className="font-bold text-gray-950 text-base flex items-center gap-2">
              <Bot className="w-5 h-5 text-purple-600" />
              <span>智能体工具调用 (Tool / Function Calling) 规范</span>
            </h3>
            <p className="text-gray-600 text-xs leading-relaxed">
              支持在单次请求中注册多个工具定义，模型可自主决定何时执行何种工具。所有主流模型（Claude 3.7、GPT-4o、DeepSeek-V3）均原生支持返回标准格式的 tool_calls。
            </p>
            <pre className="p-4 bg-slate-900 rounded-lg font-mono text-xs text-purple-200 overflow-x-auto border border-slate-800">
{`// 注册天气查询工具示例
const tools = [
  {
    type: "function",
    function: {
      name: "get_weather",
      description: "查询指定城市的当前实时天气",
      parameters: {
        type: "object",
        properties: {
          location: { type: "string", description: "城市名称，如 '北京' 或 'Shanghai'" }
        },
        required: ["location"]
      }
    }
  }
];`}
            </pre>
          </div>
        </main>
      )}

      {/* ========================================================================= */}
      {/* VIEW E: 实战手册 (Cookbook) */}
      {/* ========================================================================= */}
      {!isViewingApiEndpoint && topTab === 'cookbook' && (
        <main
          className={`flex-1 min-w-0 overflow-y-auto max-h-[calc(100vh-6rem)] p-4 sm:p-8 space-y-8 pb-28 ${
            mobileActivePanel !== 'content' ? 'hidden lg:block' : 'block'
          }`}
        >
          <div className="border-b border-gray-200 pb-6">
            <span className="px-2.5 py-1 rounded bg-amber-50 text-amber-700 text-xs font-semibold border border-amber-200">
              Cookbook 实战手册
            </span>
            <h1 className="text-3xl font-extrabold text-gray-950 mt-3">
              生产级高阶场景代码食谱
            </h1>
            <p className="text-gray-600 text-sm mt-2 leading-relaxed">
              精选五大高频生产级场景，包含即拷即用的完整代码实现与最佳实践。
            </p>
          </div>

          <div className="space-y-6">
            {/* Recipe 1 */}
            <div className="bg-white border border-gray-200 rounded-xl p-5 space-y-3 shadow-xs">
              <div className="flex items-center justify-between">
                <h3 className="font-bold text-gray-950 text-base flex items-center gap-2">
                  <Zap className="w-4 h-4 text-amber-500" />
                  <span>场景一：SSE 逐字流式打字机响应与中止控制</span>
                </h3>
                <span className="text-[11px] font-mono text-purple-700 font-semibold bg-purple-50 px-1 rounded border border-purple-200">TypeScript / React</span>
              </div>
              <p className="text-gray-600 text-xs">
                使用 AbortController 实现前端在用户点击“停止生成”时，毫秒级断开后端连接，避免不必要的 Token 消耗。
              </p>
            </div>

            {/* Recipe 2 */}
            <div className="bg-white border border-gray-200 rounded-xl p-5 space-y-3 shadow-xs">
              <div className="flex items-center justify-between">
                <h3 className="font-bold text-gray-950 text-base flex items-center gap-2">
                  <Layers className="w-4 h-4 text-blue-600" />
                  <span>场景二：多模型自动故障转移 (Fallback) 列表</span>
                </h3>
                <span className="text-[11px] font-mono text-purple-700 font-semibold bg-purple-50 px-1 rounded border border-purple-200">Node.js</span>
              </div>
              <p className="text-gray-600 text-xs">
                在 model 字段中传入模型数组，如 <code className="text-purple-700 bg-purple-50 px-1 rounded">["anthropic/claude-3.7-sonnet", "openai/gpt-4o", "deepseek/deepseek-chat"]</code>，当首选模型发生上游限流或服务过载时，网关将毫秒级自动重试下一顺位模型。
              </p>
            </div>
          </div>
        </main>
      )}

      {/* Close UNIFIED WORKSPACE Container */}
      </div>

      {/* ========================================================================= */}
      {/* 3. Floating Bottom Ask a Question Input Bar (Matching screenshot bottom) */}
      {/* ========================================================================= */}
      <div className="fixed bottom-4 left-1/2 -translate-x-1/2 w-[92%] sm:w-[540px] md:w-[640px] z-40">
        <form
          onSubmit={handleAskQuestion}
          className="bg-white/95 backdrop-blur-md border border-gray-300 rounded-full px-4 py-2 flex items-center gap-2 shadow-xl shadow-gray-400/20 group focus-within:border-purple-500 transition-all"
        >
          <Sparkles className="w-4 h-4 text-purple-600 shrink-0" />
          <input
            type="text"
            placeholder="向 AI 提问关于 uFreeTokens API 接口规范、SDK 接入或更新日志..."
            value={questionInput}
            onChange={(e) => setQuestionInput(e.target.value)}
            className="flex-1 bg-transparent text-xs text-gray-900 placeholder-gray-400 focus:outline-none"
          />
          <button
            type="submit"
            disabled={!questionInput.trim()}
            className={`w-7 h-7 rounded-full flex items-center justify-center transition-all cursor-pointer ${
              questionInput.trim()
                ? 'bg-purple-600 hover:bg-purple-500 text-white shadow-sm'
                : 'bg-gray-100 text-gray-400'
            }`}
            title="发送提问"
          >
            <ArrowRight className="w-3.5 h-3.5 -rotate-90 stroke-[2.5]" />
          </button>
        </form>
      </div>

      {/* AI Assistant Modal when Question is submitted */}
      {showAiModal && (
        <div className="fixed inset-0 z-50 bg-black/40 backdrop-blur-xs flex items-center justify-center p-4">
          <div className="bg-white border border-gray-200 rounded-2xl w-full max-w-xl p-5 space-y-4 shadow-2xl text-xs">
            <div className="flex items-center justify-between border-b border-gray-200 pb-3">
              <div className="flex items-center space-x-2">
                <div className="w-6 h-6 rounded-full bg-purple-100 flex items-center justify-center text-purple-600">
                  <Bot className="w-3.5 h-3.5" />
                </div>
                <span className="font-bold text-gray-950 text-sm">uFreeTokens 智能文档问答助手</span>
              </div>
              <button
                onClick={() => setShowAiModal(false)}
                className="text-gray-400 hover:text-gray-700 cursor-pointer text-base px-1"
              >
                ✕
              </button>
            </div>

            <div className="max-h-80 overflow-y-auto space-y-3 pr-1">
              {aiChatMessages.map((msg, i) => (
                <div
                  key={i}
                  className={`p-3.5 rounded-xl leading-relaxed ${
                    msg.role === 'user'
                      ? 'bg-purple-50 border border-purple-200 text-purple-900 ml-8'
                      : 'bg-gray-50 border border-gray-200 text-gray-800 mr-8'
                  }`}
                >
                  <div className="text-[10px] font-semibold text-gray-500 mb-1">
                    {msg.role === 'user' ? '您的提问' : 'AI 回答'}
                  </div>
                  {msg.content}
                </div>
              ))}

              {isAiAnswering && (
                <div className="p-3 bg-gray-50 border border-gray-200 rounded-xl text-gray-500 flex items-center gap-2">
                  <RotateCw className="w-3.5 h-3.5 animate-spin text-purple-600" />
                  <span>正在检索 API 规范文档并生成解答...</span>
                </div>
              )}
            </div>

            <div className="flex justify-between items-center pt-2 border-t border-gray-200">
              <span className="text-[11px] text-gray-500">基于最新 2026 OpenAPI 规范</span>
              <button
                onClick={() => setShowAiModal(false)}
                className="px-4 py-1.5 bg-gray-100 hover:bg-gray-200 text-gray-800 rounded-lg text-xs font-medium cursor-pointer transition-colors"
              >
                关闭
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
