import React from 'react';
import {
  Compass,
  Rocket,
  FileText,
  Package,
  Layers,
  HelpCircle,
  Bug,
  GitFork,
  RefreshCw,
  Crosshair,
  Lock,
  Gem,
  Bot,
  Briefcase,
  Monitor,
  Server,
  Plug,
  Sparkles,
  FolderKanban,
  Bell,
  Folder,
  Sliders,
  Tag,
  Archive,
  Code,
  ArrowLeftRight,
  Shield,
  TrendingUp,
  ShieldCheck,
  Info,
  Radio,
  Fingerprint,
  Key,
  KeyRound,
  Users,
  Terminal,
  FileCheck,
  Cpu,
  FileEdit,
  MessageSquare,
  History,
  Wrench,
  CreditCard,
  Boxes,
  Database,
  MapPin,
  Globe,
  Gauge,
  Lightbulb,
  Cloud,
  Puzzle,
  Star
} from 'lucide-react';

export interface DocSubItem {
  id: string;
  title: string;
  badge?: string;
  endpointId?: string;
}

export interface DocItem {
  id: string;
  title: string;
  icon: React.ComponentType<{ className?: string }>;
  isQuickstart?: boolean;
  hasChevron?: boolean;
  badge?: string;
  endpointId?: string;
  subItems?: DocSubItem[];
  description?: string;
  category?: string;
}

export interface DocSection {
  id: string;
  title: string;
  icon?: React.ComponentType<{ className?: string }>;
  isOriHeader?: boolean;
  items: DocItem[];
}

export const DOCS_SECTIONS: DocSection[] = [
  {
    id: 'general',
    title: '',
    items: [
      {
        id: 'overview',
        title: 'Overview',
        icon: Compass,
        description: 'uFreeTokens 统一网关架构概览、核心能力与全球多节点拓扑结构。'
      },
      {
        id: 'quickstart',
        title: 'Quickstart',
        icon: Rocket,
        isQuickstart: true,
        description: '3 分钟快速接入：获取 API Key、设置 Base URL 并发起首个模型调用。'
      },
      {
        id: 'principles',
        title: 'Principles',
        icon: FileText,
        description: '开放、透明、中立与隐私第一的设计原则。'
      },
      {
        id: 'models',
        title: 'Models',
        icon: Package,
        endpointId: 'get-embeddings',
        description: '已支持的 300+ 前沿商用与开源大模型分类列表及定价速查。'
      },
      {
        id: 'multimodal',
        title: 'Multimodal',
        icon: Layers,
        hasChevron: true,
        description: '图像视觉理解、音频输入及 PDF 跨模态文档解析规范。',
        subItems: [
          { id: 'multimodal-vision', title: 'Vision (图片分析)' },
          { id: 'multimodal-audio', title: 'Audio Input (音频流)' },
          { id: 'multimodal-docs', title: 'PDF & Documents (长文档)' }
        ]
      },
      {
        id: 'faq',
        title: 'FAQ',
        icon: HelpCircle,
        description: '常见问题解答：计费周期、并发额度、模型版本迁移指南。'
      },
      {
        id: 'report-feedback',
        title: 'Report Feedback',
        icon: Bug,
        description: '提交 Bug 报告、性能问题或请求新增模型厂商节点。'
      }
    ]
  },
  {
    id: 'models-routing',
    title: 'Models & Routing',
    icon: GitFork,
    items: [
      {
        id: 'model-fallbacks',
        title: 'Model Fallbacks',
        icon: RefreshCw,
        description: '多模型智能容灾降级：当首选模型超时或限流时自动毫秒级切换。'
      },
      {
        id: 'provider-selection',
        title: 'Provider Selection',
        icon: GitFork,
        endpointId: 'get-model-endpoints',
        description: '自定义选择推理供应商（Together、Groq、DeepInfra、Azure、Bedrock 等）。'
      },
      {
        id: 'auto-exacto',
        title: 'Auto Exacto',
        icon: Crosshair,
        description: '精准模型自动路由引擎，自动平衡成本、时延与智商上限。'
      },
      {
        id: 'private-models',
        title: 'Private Models',
        icon: Lock,
        description: '接入私有微调权重与企业 VPC 内网专属部署的模型节点。'
      },
      {
        id: 'model-variants',
        title: 'Model Variants',
        icon: Gem,
        hasChevron: true,
        description: '量化精度版本 (FP8 / INT4 / BF16) 与超长上下文特殊变体。',
        subItems: [
          { id: 'variants-free', title: ':free 免额度变体' },
          { id: 'variants-nitro', title: ':nitro 极致低时延集群' },
          { id: 'variants-extended', title: ':extended 1M+ 上下文' }
        ]
      },
      {
        id: 'routers',
        title: 'Routers',
        icon: Bot,
        hasChevron: true,
        description: '自适应动态路由器：ufreetokens/auto 及定制化权重路由。',
        subItems: [
          { id: 'routers-auto', title: 'ufreetokens/auto 全自动路由' },
          { id: 'routers-fastest', title: 'ufreetokens/fastest 最快首字时延' },
          { id: 'routers-cheapest', title: 'ufreetokens/cheapest 极低成本' }
        ]
      }
    ]
  },
  {
    id: 'tool-calling',
    title: 'Tool Calling',
    icon: Briefcase,
    items: [
      {
        id: 'client-tools',
        title: 'Client Tools',
        icon: Monitor,
        description: '客户端声明函数并在接收到 tool_calls 后执行返回结果的标准闭环。'
      },
      {
        id: 'server-tools',
        title: 'Server Tools',
        icon: Server,
        hasChevron: true,
        description: '服务端免部署工具（实时联网 Google Search、代码沙箱执行器等）。',
        subItems: [
          { id: 'server-websearch', title: 'Web Search 联网检索' },
          { id: 'server-code-eval', title: 'Python 沙箱执行' }
        ]
      },
      {
        id: 'plugins',
        title: 'Plugins',
        icon: Plug,
        hasChevron: true,
        description: '生态级工具插件标准与 MCP (Model Context Protocol) 扩展协议。',
        subItems: [
          { id: 'plugins-mcp', title: 'MCP 连接器' },
          { id: 'plugins-rag', title: '向量检索增强插件' }
        ]
      }
    ]
  },
  {
    id: 'features',
    title: 'Features',
    icon: Sparkles,
    items: [
      {
        id: 'workspaces',
        title: 'Workspaces',
        icon: FolderKanban,
        hasChevron: true,
        description: '多团队协作工作区：成员权限隔离、独立 API 密钥与独立用量配额。',
        subItems: [
          { id: 'workspaces-teams', title: '团队配额与成员' },
          { id: 'workspaces-projects', title: '环境与项目隔离' }
        ]
      },
      {
        id: 'notifications',
        title: 'Notifications',
        icon: Bell,
        description: '额度告警 Webhook、服务波动即时告警与邮件推送。'
      },
      {
        id: 'files',
        title: 'Files',
        icon: Folder,
        badge: 'Beta',
        endpointId: 'get-files',
        description: '文件上传管理服务：支持超大 PDF、代码仓库压缩包持久化存储。'
      },
      {
        id: 'containers',
        title: 'Containers',
        icon: Package,
        badge: 'Beta',
        description: '用于执行 Agent 复杂代码测试的隔离容器沙箱。'
      },
      {
        id: 'presets',
        title: 'Presets',
        icon: Sliders,
        description: '保存预配置的 System Prompt、温度系数与模型超参数模板。'
      },
      {
        id: 'custom-classifiers',
        title: 'Custom Classifiers',
        icon: Tag,
        description: '前置请求分类器，自动拦截危险意图或重定向专业模型。'
      },
      {
        id: 'response-caching',
        title: 'Response Caching',
        icon: Archive,
        description: '网关层 HTTP 响应缓存，降低相同请求的重复推理开销。'
      },
      {
        id: 'structured-outputs',
        title: 'Structured Outputs',
        icon: Code,
        description: '基于 JSON Schema 严格保证模型输出符合预定数据格式。'
      },
      {
        id: 'message-transforms',
        title: 'Message Transforms',
        icon: ArrowLeftRight,
        description: '自动清洗上游特定格式的特殊标记与上下文剪裁。'
      },
      {
        id: 'zero-completion-insurance',
        title: 'Zero Completion Insurance',
        icon: Shield,
        description: '因上游中断导致生成 0 Token 时自动全额退款保障机制。'
      },
      {
        id: 'app-attribution',
        title: 'App Attribution',
        icon: TrendingUp,
        description: 'HTTP-Referer 与 X-Title 应用归属统计与生态排行展示。'
      },
      {
        id: 'guardrails',
        title: 'Guardrails',
        icon: ShieldCheck,
        hasChevron: true,
        endpointId: 'get-guardrails',
        description: '企业级安全护栏：敏感词检测、PII 隐私脱敏与越狱防御。',
        subItems: [
          { id: 'guardrails-pii', title: 'PII 个人隐私脱敏' },
          { id: 'guardrails-safety', title: '内容安全审核过滤' }
        ]
      },
      {
        id: 'service-tiers',
        title: 'Service Tiers',
        icon: Layers,
        description: '标准共享层、企业专用吞吐保障层与专属私有集群服务。'
      },
      {
        id: 'router-metadata',
        title: 'Router Metadata',
        icon: Info,
        endpointId: 'get-generation',
        description: '每次请求包含的真实提供商、缓存命中率及精确纳秒级耗时。'
      },
      {
        id: 'broadcast',
        title: 'Broadcast',
        icon: Radio,
        hasChevron: true,
        description: '单次请求并发广播给多个模型，并行对比或多数票表决。',
        subItems: [
          { id: 'broadcast-parallel', title: '并行多模型推理' },
          { id: 'broadcast-consensus', title: '共识算法与评判' }
        ]
      }
    ]
  },
  {
    id: 'authentication',
    title: 'Authentication',
    icon: Shield,
    items: [
      {
        id: 'oauth',
        title: 'OAuth',
        icon: ShieldCheck,
        description: '支持第三方应用通过 OAuth 2.0 PKCE 流程授权用户凭据。'
      },
      {
        id: 'workload-identity',
        title: 'Workload Identity',
        icon: Fingerprint,
        description: 'AWS / GCP / K8s 工作负载身份无密钥（Keyless）直接认证。'
      },
      {
        id: 'management-api-keys',
        title: 'Management API Keys',
        icon: Key,
        description: '用于自动化运维、创建子密钥与查询账单的管理级 API 密钥。'
      },
      {
        id: 'byok',
        title: 'BYOK',
        icon: KeyRound,
        description: 'Bring Your Own Key：自带 OpenAI / Anthropic 密钥享受平台路由。'
      },
      {
        id: 'sso',
        title: 'Single Sign-On (SSO)',
        icon: Shield,
        description: 'SAML 2.0 与 OIDC 企业单点登录（Okta、Google Workspace 等）。'
      },
      {
        id: 'scim-group-mappings',
        title: 'SCIM Group Mappings',
        icon: Users,
        description: '通过 SCIM 协议自动同步企业组织架构与用户组成员权限。'
      }
    ]
  },
  {
    id: 'ori',
    title: 'Ori',
    icon: Terminal,
    isOriHeader: true,
    items: [
      {
        id: 'ori-eval',
        title: 'Ori Eval',
        icon: FileCheck,
        endpointId: 'post-responses',
        description: '智能体能力基准评测框架：自动化度量多步规划与工具调用成功率。'
      },
      {
        id: 'ori-harness',
        title: 'Ori Harness',
        icon: Cpu,
        description: '为复杂 Coding Agent 提供沙箱环境、代码评估及回放工具链。'
      },
      {
        id: 'file-writing',
        title: 'File Writing',
        icon: FileEdit,
        description: '智能体安全文件修改与局部代码 Diff 补丁应用协议。'
      },
      {
        id: 'ori-configuration',
        title: 'Ori Configuration',
        icon: Sliders,
        description: 'ori.config.json 配置文件参数规范与环境变量覆盖机制。'
      },
      {
        id: 'chatting-with-interns',
        title: 'Chatting with Interns',
        icon: MessageSquare,
        description: '人机协同监督模式：在 Agent 关键执行节点前请求人工确认。'
      },
      {
        id: 'vault-secrets-for-interns',
        title: 'Vault Secrets for Interns',
        icon: KeyRound,
        description: '临时敏感凭据安全注入与执行后自动吊销沙箱凭据机制。'
      },
      {
        id: 'changelog',
        title: 'Changelog',
        icon: History,
        description: 'uFreeTokens 平台 API 版本演进记录、更新日志与弃用说明。'
      }
    ]
  },
  {
    id: 'developer-tools',
    title: 'Developer Tools',
    icon: Wrench,
    items: [
      {
        id: 'mcp',
        title: 'MCP',
        icon: Plug,
        description: 'Model Context Protocol 官方客户端与服务端生态集成方案。'
      },
      {
        id: 'terraform-provider',
        title: 'Terraform Provider',
        icon: Layers,
        description: '使用 IaC 基础设施即代码自动化管理密钥、预算与路由策略。'
      },
      {
        id: 'stripe-projects',
        title: 'Stripe Projects',
        icon: CreditCard,
        description: '按项目绑定独立信用卡与自动充值阈值配置指南。'
      },
      {
        id: 'batch',
        title: 'Batch',
        icon: Boxes,
        description: '非实时异步批量任务处理，享有 50% 成本折扣。'
      }
    ]
  },
  {
    id: 'privacy',
    title: 'Privacy',
    icon: Shield,
    items: [
      {
        id: 'data-collection',
        title: 'Data Collection',
        icon: Database,
        description: '平台数据收集声明：提示词与生成结果不留存政策。'
      },
      {
        id: 'provider-logging',
        title: 'Provider Logging',
        icon: FileText,
        description: '各大底层模型提供商数据保留条款透明公示。'
      },
      {
        id: 'zdr',
        title: 'ZDR',
        icon: ShieldCheck,
        endpointId: 'get-zdr-preview',
        description: '零数据保留（Zero Data Retention）强制合规通道。'
      },
      {
        id: 'input-output-logging',
        title: 'Input & Output Logging',
        icon: FileText,
        description: '控制台日志开关与本地端到端加密存储。'
      },
      {
        id: 'in-region-routing',
        title: 'In-Region Routing',
        icon: MapPin,
        description: '地理围栏合规：强制请求仅在欧盟 (EU) 或美国 (US) 境内处理。'
      },
      {
        id: 'sovereign-ai',
        title: 'Sovereign AI',
        icon: Globe,
        description: '数据主权保护：支持主权云（Sovereign Cloud）专用节点合规。'
      }
    ]
  },
  {
    id: 'best-practices',
    title: 'Best Practices',
    icon: Lightbulb,
    items: [
      {
        id: 'latency-and-performance',
        title: 'Latency and Performance',
        icon: Gauge,
        description: '毫秒级低时延调优最佳实践：TTFT 优化与就近路由。'
      },
      {
        id: 'prompt-caching',
        title: 'Prompt Caching',
        icon: Database,
        description: '利用 Anthropic 与 DeepSeek 提示词缓存削减 90% 重复 Token 费用。'
      },
      {
        id: 'uptime-optimization',
        title: 'Uptime Optimization',
        icon: TrendingUp,
        description: '实现 99.999% 高可用架构设计：多供应商重试与指数避退。'
      },
      {
        id: 'reasoning-tokens',
        title: 'Reasoning Tokens',
        icon: Lightbulb,
        description: '深度思考模型（DeepSeek R1、o3-mini）思维链 Token 计费与截断控制。'
      }
    ]
  },
  {
    id: 'community',
    title: 'Community',
    icon: Users,
    items: [
      { id: 'for-providers', title: 'For Providers', icon: Cloud },
      { id: 'frameworks-overview', title: 'Frameworks and Integrations Overview', icon: Puzzle },
      { id: 'awesome-ufreetokens', title: 'Awesome uFreeTokens', icon: Star },
      { id: 'effect-ai-sdk', title: 'Effect AI SDK', icon: Puzzle },
      { id: 'arize-ax', title: 'Arize AX', icon: Puzzle },
      { id: 'langchain', title: 'LangChain', icon: Puzzle },
      { id: 'livekit', title: 'LiveKit', icon: Puzzle },
      { id: 'langfuse', title: 'Langfuse', icon: Puzzle },
      { id: 'mastra', title: 'Mastra', icon: Puzzle },
      { id: 'openai-sdk', title: 'OpenAI SDK', icon: Puzzle },
      { id: 'anthropic-agent-sdk', title: 'Anthropic Agent SDK', icon: Puzzle },
      { id: 'pydantic-ai', title: 'PydanticAI', icon: Puzzle },
      { id: 'render', title: 'Render', icon: Puzzle },
      { id: 'replit', title: 'Replit', icon: Puzzle },
      { id: 'tanstack-ai', title: 'TanStack AI', icon: Puzzle },
      { id: 'vercel-ai-sdk', title: 'Vercel AI SDK', icon: Puzzle }
    ]
  }
];
