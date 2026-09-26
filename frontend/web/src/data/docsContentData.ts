export interface DocContentArticle {
  id: string;
  category: string;
  title: string;
  subtitle: string;
  badge?: string;
  overview: string;
  keyFeatures: string[];
  codeExamples?: {
    title: string;
    lang: string;
    code: string;
  }[];
  configSnippet?: string;
  notes?: string;
}

export const DOCS_ARTICLES: Record<string, DocContentArticle> = {
  overview: {
    id: 'overview',
    category: 'Overview',
    title: 'uFreeTokens 统一架构总览',
    subtitle: '面向下一代 AI 应用的全局分布式模型路由与中立聚合层',
    badge: 'Core Architecture',
    overview:
      'uFreeTokens 作为全球领先的中立大模型路由基础设施，向下聚合了包括 Anthropic、OpenAI、DeepSeek、Google、Meta、Mistral、Cohere 在内的全部主流顶尖模型与 40+ 硬件推理供应商，向上通过 100% 兼容 OpenAI 的统一 REST/SSE 接口向开发者提供免运维、自动容灾与最佳性价比调度服务。',
    keyFeatures: [
      '单套 Base URL https://ufreetokens.com/api/v1 驱动全球 300+ 前沿大模型',
      '跨供应商毫秒级健康探测与智能故障转移 (Auto Fallback)',
      '企业级 ZDR (零数据保留) 合规协议与数据隐私兜底保障',
      '精准 Tokenizer 原生计量与精细至每百万 Token 的动态折扣透明审计'
    ],
    codeExamples: [
      {
        title: '统一请求地址 (cURL)',
        lang: 'bash',
        code: `curl https://ufreetokens.com/api/v1/chat/completions \\
  -H "Authorization: Bearer $UFREETOKENS_API_KEY" \\
  -H "HTTP-Referer: https://myapp.example.com" \\
  -H "X-Title: My AI Application" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "ufreetokens/auto",
    "messages": [{"role": "user", "content": "你好，请简述 uFreeTokens 架构优势"}]
  }'`
      }
    ]
  },
  quickstart: {
    id: 'quickstart',
    category: 'Quickstart',
    title: '3 分钟极速接入指南',
    subtitle: '即拷即用的接入代码与环境初始化配置',
    badge: '3-Min Onboarding',
    overview:
      '仅需替换现有项目的 baseURL 与 API Key，即可在几分钟内为现有应用接入全网最丰富的模型矩阵，并自动享受提示词缓存与故障转移保护。',
    keyFeatures: [
      '第 1 步：在个人中心创建 Bearer Token 并在生产环境配置环境变量',
      '第 2 步：将现有 OpenAI SDK 实例的 baseURL 变更为 https://ufreetokens.com/api/v1',
      '第 3 步：在 model 参数中直接指定如 anthropic/claude-3.7-sonnet、deepseek/deepseek-chat 等任意模型'
    ],
    codeExamples: [
      {
        title: 'TypeScript / Node.js 接入',
        lang: 'typescript',
        code: `import OpenAI from "openai";

const openai = new OpenAI({
  baseURL: "https://ufreetokens.com/api/v1",
  apiKey: process.env.UFREETOKENS_API_KEY,
  defaultHeaders: {
    "HTTP-Referer": "https://yourapp.domain",
    "X-Title": "My Application",
  }
});

async function main() {
  const completion = await openai.chat.completions.create({
    model: "anthropic/claude-3.7-sonnet",
    messages: [{ role: "user", content: "Write a high-performance LRU cache in Rust." }],
  });
  console.log(completion.choices[0].message.content);
}
main();`
      },
      {
        title: 'Python 接入',
        lang: 'python',
        code: `from openai import OpenAI
import os

client = OpenAI(
    base_url="https://ufreetokens.com/api/v1",
    api_key=os.environ.get("UFREETOKENS_API_KEY"),
)

response = client.chat.completions.create(
    model="deepseek/deepseek-chat",
    messages=[
        {"role": "system", "content": "You are an expert engineer."},
        {"role": "user", "content": "Explain quantum entanglement simply."}
    ],
)
print(response.choices[0].message.content)`
      }
    ]
  },
  'provider-selection': {
    id: 'provider-selection',
    category: 'Models & Routing',
    title: '推理提供商自定义偏好选择 (Provider Selection)',
    subtitle: '针对时延、定价、地域与合规要求灵活挑选最佳物理推理节点',
    badge: 'Smart Routing',
    overview:
      '同一款模型（如 Llama-3.3-70B 或 DeepSeek-V3）通常由包括 Together AI、Groq、DeepInfra、Lepton、Novita 等多家云厂商同时提供。平台允许开发者显式指定供应商排序、排除特定节点或强制仅选用开启 ZDR 的供应商。',
    keyFeatures: [
      'order: 优先尝试指定的提供商列表（如 ["Groq", "Together"]）',
      'allow_fallbacks: 是否在指定提供商均故障时自动降级到其他可用节点',
      'require_parameters: 强制过滤出支持特定参数（如 tools 或 logprobs）的节点',
      'data_collection: 设置为 "deny" 时仅选用承诺不存储日志的 ZDR 节点'
    ],
    codeExamples: [
      {
        title: '请求体包含 provider 偏好配置 (JSON)',
        lang: 'json',
        code: `{
  "model": "meta-llama/llama-3.3-70b-instruct",
  "messages": [{"role": "user", "content": "Solve this equation"}],
  "provider": {
    "order": ["Groq", "Together", "DeepInfra"],
    "allow_fallbacks": true,
    "data_collection": "deny",
    "quantizations": ["fp8", "bf16"]
  }
}`
      }
    ]
  },
  'model-fallbacks': {
    id: 'model-fallbacks',
    category: 'Models & Routing',
    title: '跨模型自动容灾降级 (Model Fallbacks)',
    subtitle: '在主选模型发生限流 (429) 或上游宕机时毫秒级自动切换备用模型',
    badge: 'Zero Downtime',
    overview:
      '传统应用需要开发者在业务逻辑中编写复杂的 try/catch 和重试逻辑。在 uFreeTokens，只需在 model 字段传入有序模型数组，网关会在第一顺位模型异常时全自动无缝重试后续模型。',
    keyFeatures: [
      '客户端无感知：自动处理 429 限流、500 上游宕机及长文本超时',
      '支持异构模型混搭：例如优先 Claude 3.7 Sonnet，失败时降级至 GPT-4o 或 DeepSeek-V3',
      '支持配合 ufreetokens/auto 作为最终保底项'
    ],
    codeExamples: [
      {
        title: '传入模型回退列表示例',
        lang: 'typescript',
        code: `const response = await openai.chat.completions.create({
  // 首选 Claude 3.7，超时或限流时自动切换 GPT-4o，再不行自动切换 DeepSeek
  model: [
    "anthropic/claude-3.7-sonnet",
    "openai/gpt-4o",
    "deepseek/deepseek-chat"
  ],
  messages: [{ role: "user", content: "分析这份财务报表的核心指标" }]
});`
      }
    ]
  },
  workspaces: {
    id: 'workspaces',
    category: 'Features',
    title: '企业级工作区与组织架构 (Workspaces)',
    subtitle: '为多团队、多业务线与多环境建立安全隔离的额度与密钥管控',
    badge: 'Enterprise',
    overview:
      'Workspaces 专为多组织多成员协作设计，支持创建独立的生产、预发与测试工作区。管理员可以为每个工作区分配独立的单月消费限额、单独绑定发票主体并分配不同的 RBAC 角色。',
    keyFeatures: [
      '环境硬隔离：研发环境与生产环境隔离不同的 API Key 与 Webhook',
      '消费配额软硬上限：超出软上限触发告警，超出硬上限自动熔断保护',
      '成员细粒度授权：Admin（全权）、Developer（只读账单与调用接口）、Viewer（仅看报表）'
    ]
  },
  'scim-group-mappings': {
    id: 'scim-group-mappings',
    category: 'Authentication',
    title: 'SCIM 协议组织架构自动同步',
    subtitle: '与 Okta、Azure AD (Entra ID) 无缝同步企业用户组与权限体系',
    badge: 'SSO & IAM',
    overview:
      '通过遵循 RFC 7644 标准的 SCIM 2.0 接口，企业的员工入职、转岗或离职状态将在数秒内同步至平台，无需人工手动逐个维护开发者账号。',
    keyFeatures: [
      '自动创建与禁用用户：HR 系统撤销员工时，其个人 API 密钥瞬时失效',
      '组映射规则：将企业 "Data-Science-Team" 组直接映射到拥有高算力配额的专属 Workspace',
      '支持 SAML 2.0 / OIDC 单点登录强制策略'
    ]
  },
  'ori-harness': {
    id: 'ori-harness',
    category: 'Ori Agent Suite',
    title: 'Ori Harness 智能体与代码评估套件',
    subtitle: '专为多步自主智能体打造的评测基准、沙箱环境与回放分析器',
    badge: 'Agentic AI',
    overview:
      'Ori Harness 是一套端到端的代码与决策执行套件。通过标准化的沙箱捕获大模型的每一次 Tool Call 与中间推理状态，评估在 SWE-bench、HumanEval 等真实工程场景中的任务解决率。',
    keyFeatures: [
      '毫秒级安全隔离沙箱容器，支持安全执行 Bash、Python 与 Git 操作',
      'Responses API 全链路回放，审查智能体的多步思维链与决策分叉',
      '交叉裁判机制：引入多模型共识评估代码修改的优雅性与安全性'
    ]
  },
  zdr: {
    id: 'zdr',
    category: 'Privacy',
    title: 'ZDR (Zero Data Retention) 零数据保留合规体系',
    subtitle: '针对金融、医疗与法律等高合规场景的绝对隐私保障',
    badge: 'Strict No-Log',
    overview:
      '开启 ZDR 后，请求将仅分发至与平台签署了法律约束力 No-Log Policy 的节点。所有 Prompt、输入上下文及生成的 Token 绝不落地存储，绝不作为任何模型微调训练素材。',
    keyFeatures: [
      '合规标准保障：满足 SOC2 Type II、HIPAA、GDPR 与 ISO 27001 审计要求',
      '预检接口支持：通过 GET /endpoints/zdr 实时拉取支持 ZDR 的最新模型节点白名单',
      '网关无盘内存转发：仅在传输层完成流式中继，生成结束后立刻释放内存缓冲区'
    ]
  },
  'prompt-caching': {
    id: 'prompt-caching',
    category: 'Best Practices',
    title: 'Prompt Caching 提示词缓存与费用减免',
    subtitle: '利用长上下文前缀复用技术，削减高达 90% 的重复 Token 计费',
    badge: 'Cost Savings',
    overview:
      '对于代码仓库分析、长篇法律合同或具有固定长 System Prompt 的场景，底层硬件集群会缓存已计算的 KV Cache。uFreeTokens 原生支持 Anthropic 与 DeepSeek 的提示词缓存，并直接将减免比例反馈至账单。',
    keyFeatures: [
      'Anthropic Claude 3.7 / 3.5：命中缓存的输入 Token 单价仅为原价的 10%',
      'DeepSeek-V3 / R1：上下文缓存命中 Token 价格极低至 $0.014 / M Tokens',
      '响应头返回 router_cache_hit 与 tokens_cached 精确统计'
    ]
  },
  mcp: {
    id: 'mcp',
    category: 'Developer Tools',
    title: 'Model Context Protocol (MCP) 统一扩展协议',
    subtitle: '将本地与远程数据源、工具与提示词无缝连接至任意大模型',
    badge: 'Standard Protocol',
    overview:
      'MCP 是由 Anthropic 与开源社区共同推动的通用上下文与工具协议。通过 uFreeTokens，开发者可以将任何遵循 MCP 协议的服务端（如 GitHub、Postgres、Slack、本地文件系统）作为 Tools 直接注册至模型调用中。',
    keyFeatures: [
      '统一客户端接入：一键打通 Claude Desktop、Cursor、Cline 与自研 Agent',
      '双向标准：支持 Tools、Resources 与 Prompts 三大核心原语',
      '企业内网网关穿透与安全审计日志'
    ]
  }
};

export function getDocArticle(id: string): DocContentArticle {
  if (DOCS_ARTICLES[id]) return DOCS_ARTICLES[id];
  const baseId = id.split(':')[0];
  if (DOCS_ARTICLES[baseId]) return DOCS_ARTICLES[baseId];

  // Capitalized title
  const formattedTitle = id
    .split(/[-_:]/)
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(' ');

  return {
    id,
    category: 'Documentation Guide',
    title: formattedTitle,
    subtitle: `uFreeTokens 官方开发规范与配置实践 (${formattedTitle})`,
    badge: 'Guide',
    overview: `关于 ${formattedTitle} 的核心架构设计、参数约束与生产级最佳实践。原生兼容 OpenAI SDK 标准请求头与流式响应机制，提供企业级高可用保障。`,
    keyFeatures: [
      '遵循标准 OpenAPI 3.1 规范，零迁移成本无缝兼容现有工程',
      '支持企业级 RBAC 细粒度权限控制与组织架构同步',
      '集成多模型自动故障转移 (Auto Fallback) 与上下文缓存优惠'
    ],
    codeExamples: [
      {
        title: '标准模型调用示例 (TypeScript)',
        lang: 'typescript',
        code: `import OpenAI from "openai";

const openai = new OpenAI({
  baseURL: "https://ufreetokens.com/api/v1",
  apiKey: process.env.UFREETOKENS_API_KEY,
});

const completion = await openai.chat.completions.create({
  model: "ufreetokens/auto",
  messages: [{ role: "user", content: "Demonstrate ${formattedTitle} integration." }],
});
console.log(completion.choices[0].message.content);`
      }
    ]
  };
}
