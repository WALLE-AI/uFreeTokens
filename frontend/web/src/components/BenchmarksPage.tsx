import React, { useState } from 'react';
import {
  Code2,
  ExternalLink,
  Bot,
  Image as ImageIcon,
  Sparkles,
  FlaskConical,
  Globe,
  ChevronRight,
  ShieldCheck,
  Zap,
  DollarSign,
  Clock,
  Award,
  Filter,
  ArrowUpDown,
  Search,
  Check,
  Copy,
  X,
  Play
} from 'lucide-react';
import { Model } from '../types';

interface BenchmarksPageProps {
  onSelectModel?: (model: Model) => void;
  onNavigateToModels: () => void;
  allModels?: Model[];
}

interface BenchmarkItem {
  id: string;
  category: 'agents' | 'media' | 'artifacts' | 'reasoning' | 'search';
  name: string;
  description: string;
  modelsCount: number;
  lastRun: string;
  quality?: {
    score: string;
    model: string;
    provider: string;
    iconBg?: string;
  };
  value: {
    price: string;
    model: string;
    provider: string;
    iconBg?: string;
  };
  speed: {
    time: string;
    model: string;
    provider: string;
    iconBg?: string;
  };
  samples?: {
    label: string;
    type: 'image' | 'video' | 'artifact';
    imgUrl: string;
  }[];
  leaderboard?: {
    rank: number;
    model: string;
    provider: string;
    quality: string;
    cost: string;
    speed: string;
    errorRate: string;
  }[];
}

export const BenchmarksPage: React.FC<BenchmarksPageProps> = ({
  onSelectModel,
  onNavigateToModels,
  allModels = [],
}) => {
  const [selectedCategory, setSelectedCategory] = useState<string>('all');
  const [activeBenchmarkModal, setActiveBenchmarkModal] = useState<BenchmarkItem | null>(null);
  const [isApiModalOpen, setIsApiModalOpen] = useState(false);
  const [copied, setCopied] = useState(false);
  const [newsletterEmail, setNewsletterEmail] = useState('gaojing850063636@gmail.com');
  const [subscribed, setSubscribed] = useState(false);

  // Benchmarks data mirroring the user-uploaded image
  const benchmarksData: BenchmarkItem[] = [
    // 1. Agents & tools
    {
      id: 't2-bench-airline',
      category: 'agents',
      name: 'T²-Bench Airline',
      description: '在严格业务规则与安全策略约束下，多轮会话客服智能体执行高精度工具调用任务。',
      modelsCount: 123,
      lastRun: '2026年9月17日',
      quality: {
        score: '80.6%',
        model: 'Gemini 3.7 Flash',
        provider: 'Google',
        iconBg: 'bg-blue-600 text-white',
      },
      value: {
        price: '$0.016',
        model: 'Gemma 4 31B',
        provider: 'Google',
        iconBg: 'bg-indigo-600 text-white',
      },
      speed: {
        time: '1.7m',
        model: 'Claude Opus 4.7',
        provider: 'Anthropic',
        iconBg: 'bg-amber-700 text-white',
      },
      leaderboard: [
        { rank: 1, model: 'Gemini 3.7 Flash', provider: 'Google', quality: '80.6%', cost: '$0.024', speed: '2.1m', errorRate: '0.04%' },
        { rank: 2, model: 'Claude Opus 4.7', provider: 'Anthropic', quality: '79.8%', cost: '$0.085', speed: '1.7m', errorRate: '0.08%' },
        { rank: 3, model: 'DeepSeek V4.1 Flash', provider: 'DeepSeek', quality: '78.4%', cost: '$0.018', speed: '1.9m', errorRate: '0.12%' },
        { rank: 4, model: 'Gemma 4 31B', provider: 'Google', quality: '75.2%', cost: '$0.016', speed: '2.4m', errorRate: '0.19%' },
        { rank: 5, model: 'GPT-5.5 Mini', provider: 'OpenAI', quality: '74.9%', cost: '$0.032', speed: '1.8m', errorRate: '0.15%' },
      ],
    },

    // 2. Media
    {
      id: 'image-bench',
      category: 'media',
      name: 'Image',
      description: '专为挑战生成边界而定制的高难提示词：精确景深、三维朝向、实体计数与画框内文字排版渲染。',
      modelsCount: 44,
      lastRun: '2026年9月16日',
      samples: [
        { label: '古典油画静物', type: 'image', imgUrl: 'https://images.unsplash.com/photo-1579783900882-c0d3dad7b119?w=120&auto=format&fit=crop&q=80' },
        { label: '多重景深几何', type: 'image', imgUrl: 'https://images.unsplash.com/photo-1541701494587-cb58502866ab?w=120&auto=format&fit=crop&q=80' },
        { label: '微距微观雕刻', type: 'image', imgUrl: 'https://images.unsplash.com/photo-1509198397868-475647b2a1e5?w=120&auto=format&fit=crop&q=80' },
      ],
      value: {
        price: '$0.010',
        model: 'Meta: Muse Image',
        provider: 'Meta',
        iconBg: 'bg-blue-700 text-white',
      },
      speed: {
        time: '4s',
        model: 'Google: Nano Ban...',
        provider: 'Google',
        iconBg: 'bg-emerald-600 text-white',
      },
      leaderboard: [
        { rank: 1, model: 'Google: Nano Ban...', provider: 'Google', quality: '92.4%', cost: '$0.018', speed: '4s', errorRate: '0.01%' },
        { rank: 2, model: 'Meta: Muse Image', provider: 'Meta', quality: '90.1%', cost: '$0.010', speed: '6s', errorRate: '0.02%' },
        { rank: 3, model: 'Flux 1.1 Pro', provider: 'Black Forest Labs', quality: '89.5%', cost: '$0.040', speed: '8s', errorRate: '0.03%' },
      ],
    },
    {
      id: 'video-bench',
      category: 'media',
      name: 'Video',
      description: '统一在全模型范围内对严格固定 6 秒时长与高清晰度分辨率的视频连续帧生成评测。',
      modelsCount: 24,
      lastRun: '2026年9月15日',
      samples: [
        { label: '街景光影追踪', type: 'video', imgUrl: 'https://images.unsplash.com/photo-1508739773434-c26b3d09e071?w=120&auto=format&fit=crop&q=80' },
        { label: '流体粒子运动', type: 'video', imgUrl: 'https://images.unsplash.com/photo-1518709268805-4e9042af9f23?w=120&auto=format&fit=crop&q=80' },
        { label: '高速摄影运镜', type: 'video', imgUrl: 'https://images.unsplash.com/photo-1451187580459-43490279c0fa?w=120&auto=format&fit=crop&q=80' },
      ],
      value: {
        price: '$0.24',
        model: 'ByteDance: Seeda...',
        provider: 'ByteDance',
        iconBg: 'bg-cyan-600 text-white',
      },
      speed: {
        time: '56s',
        model: 'MiniMax: H3 Max',
        provider: 'MiniMax',
        iconBg: 'bg-rose-600 text-white',
      },
      leaderboard: [
        { rank: 1, model: 'ByteDance: Seeda...', provider: 'ByteDance', quality: '88.3%', cost: '$0.24', speed: '62s', errorRate: '0.05%' },
        { rank: 2, model: 'MiniMax: H3 Max', provider: 'MiniMax', quality: '86.7%', cost: '$0.35', speed: '56s', errorRate: '0.06%' },
        { rank: 3, model: 'Runway Gen-3 Alpha', provider: 'Runway', quality: '85.4%', cost: '$0.48', speed: '74s', errorRate: '0.04%' },
      ],
    },
    {
      id: 'memes-bench',
      category: 'media',
      name: 'Memes',
      description: '以图像或动画微短片形式自动创意生成网络模因表情，基于幽默命题契合度与视觉笑点打分。',
      modelsCount: 57,
      lastRun: '2026年9月16日',
      samples: [
        { label: '极客技术谐音', type: 'image', imgUrl: 'https://images.unsplash.com/photo-1534447677768-be436bb09401?w=120&auto=format&fit=crop&q=80' },
        { label: '经典戏剧反转', type: 'image', imgUrl: 'https://images.unsplash.com/photo-1514888286974-6c03e2ca1dba?w=120&auto=format&fit=crop&q=80' },
        { label: '超现实萌宠插图', type: 'image', imgUrl: 'https://images.unsplash.com/photo-1543466835-00a7907e9de1?w=120&auto=format&fit=crop&q=80' },
      ],
      value: {
        price: '$0.010',
        model: 'Meta: Muse Image',
        provider: 'Meta',
        iconBg: 'bg-blue-700 text-white',
      },
      speed: {
        time: '7s',
        model: 'Black Forest Labs: ...',
        provider: 'Black Forest Labs',
        iconBg: 'bg-gray-900 text-white',
      },
      leaderboard: [
        { rank: 1, model: 'Meta: Muse Image', provider: 'Meta', quality: '94.2%', cost: '$0.010', speed: '9s', errorRate: '0.01%' },
        { rank: 2, model: 'Black Forest Labs: ...', provider: 'Black Forest Labs', quality: '91.8%', cost: '$0.025', speed: '7s', errorRate: '0.02%' },
      ],
    },

    // 3. Artifact generation
    {
      id: 'sketch-bench',
      category: 'artifacts',
      name: 'Sketch',
      description: '文本纯模型基于文字提示直接输出矢量绘制代码（SVG/Canvas），成果经由图像视觉基准自动打分。',
      modelsCount: 204,
      lastRun: '2026年9月17日',
      samples: [
        { label: '矢量机械装配图', type: 'artifact', imgUrl: 'https://images.unsplash.com/photo-1618005182384-a83a8bd57fbe?w=120&auto=format&fit=crop&q=80' },
        { label: '几何渐变徽标', type: 'artifact', imgUrl: 'https://images.unsplash.com/photo-1600585154340-be6161a56a0c?w=120&auto=format&fit=crop&q=80' },
        { label: '极简建筑线稿', type: 'artifact', imgUrl: 'https://images.unsplash.com/photo-1513694203232-719a280e022f?w=120&auto=format&fit=crop&q=80' },
      ],
      value: {
        price: '$0.000032',
        model: 'Mistral: Mistral Ne...',
        provider: 'Mistral',
        iconBg: 'bg-amber-600 text-white',
      },
      speed: {
        time: '2s',
        model: 'Tencent: Hy-MT2-...',
        provider: 'Tencent',
        iconBg: 'bg-blue-500 text-white',
      },
      leaderboard: [
        { rank: 1, model: 'Claude Opus 4.7', provider: 'Anthropic', quality: '96.2%', cost: '$0.015', speed: '3s', errorRate: '0.01%' },
        { rank: 2, model: 'Mistral: Mistral Ne...', provider: 'Mistral', quality: '89.4%', cost: '$0.000032', speed: '4s', errorRate: '0.05%' },
        { rank: 3, model: 'Tencent: Hy-MT2-...', provider: 'Tencent', quality: '88.1%', cost: '$0.000085', speed: '2s', errorRate: '0.04%' },
      ],
    },
    {
      id: 'games-bench',
      category: 'artifacts',
      name: 'Games',
      description: '大语言模型根据单次任务简报（One-Shot Prompt）一次性直接生成完整可交互、零报错的 HTML5/JS 游戏。',
      modelsCount: 29,
      lastRun: '2026年9月16日',
      samples: [
        { label: '像素地牢探险', type: 'artifact', imgUrl: 'https://images.unsplash.com/photo-1550745165-9bc0b252726f?w=120&auto=format&fit=crop&q=80' },
        { label: '复古打砖块', type: 'artifact', imgUrl: 'https://images.unsplash.com/photo-1551103782-8ab07afd45c1?w=120&auto=format&fit=crop&q=80' },
        { label: '物理重力球', type: 'artifact', imgUrl: 'https://images.unsplash.com/photo-1511512578047-dfb367046420?w=120&auto=format&fit=crop&q=80' },
      ],
      value: {
        price: '$0.003',
        model: 'OpenAI: gpt-oss-...',
        provider: 'OpenAI',
        iconBg: 'bg-emerald-700 text-white',
      },
      speed: {
        time: '41s',
        model: 'Google: Gemini 3...',
        provider: 'Google',
        iconBg: 'bg-blue-600 text-white',
      },
      leaderboard: [
        { rank: 1, model: 'Claude Opus 4.7', provider: 'Anthropic', quality: '95.5%', cost: '$0.045', speed: '48s', errorRate: '0.00%' },
        { rank: 2, model: 'Google: Gemini 3...', provider: 'Google', quality: '93.0%', cost: '$0.012', speed: '41s', errorRate: '0.02%' },
        { rank: 3, model: 'OpenAI: gpt-oss-...', provider: 'OpenAI', quality: '89.7%', cost: '$0.003', speed: '55s', errorRate: '0.04%' },
      ],
    },

    // 4. Reasoning
    {
      id: 'gpqa-diamond',
      category: 'reasoning',
      name: 'GPQA Diamond',
      description: '抗网络信息检索的研究生级别顶尖科学难题，必须依赖深度自洽逻辑推演方可求解。',
      modelsCount: 136,
      lastRun: '2026年9月17日',
      quality: {
        score: '94.6%',
        model: 'Fugu Ultra',
        provider: 'Fugu Labs',
        iconBg: 'bg-rose-500 text-white',
      },
      value: {
        price: '$0.008',
        model: 'Auto Router',
        provider: 'uFreeTokens',
        iconBg: 'bg-purple-600 text-white',
      },
      speed: {
        time: '31s',
        model: 'Claude Fable 5.1',
        provider: 'Anthropic',
        iconBg: 'bg-amber-800 text-white',
      },
      leaderboard: [
        { rank: 1, model: 'Fugu Ultra', provider: 'Fugu Labs', quality: '94.6%', cost: '$0.042', speed: '38s', errorRate: '0.01%' },
        { rank: 2, model: 'DeepSeek R1 / V4.1', provider: 'DeepSeek', quality: '93.8%', cost: '$0.009', speed: '34s', errorRate: '0.02%' },
        { rank: 3, model: 'Claude Fable 5.1', provider: 'Anthropic', quality: '92.9%', cost: '$0.055', speed: '31s', errorRate: '0.01%' },
        { rank: 4, model: 'Auto Router', provider: 'uFreeTokens', quality: '93.5%', cost: '$0.008', speed: '33s', errorRate: '0.02%' },
        { rank: 5, model: 'o3-mini High', provider: 'OpenAI', quality: '91.2%', cost: '$0.015', speed: '36s', errorRate: '0.03%' },
      ],
    },

    // 5. Search
    {
      id: 'browse-comp',
      category: 'search',
      name: 'BrowseComp',
      description: '抓取实时互联网中极度隐蔽的冷门事实，依据多步自主调研工具链路及抗死锁能力综合评分。',
      modelsCount: 4,
      lastRun: '2026年8月18日',
      quality: {
        score: '89.0%',
        model: 'Perplexity / Claude Opus 5 · high',
        provider: 'Perplexity',
        iconBg: 'bg-teal-700 text-white',
      },
      value: {
        price: '$0.99',
        model: 'Perplexity / Claude Opus 5 · high',
        provider: 'Perplexity',
        iconBg: 'bg-teal-700 text-white',
      },
      speed: {
        time: '1.9m',
        model: 'Perplexity / Claude Opus 5 · high',
        provider: 'Perplexity',
        iconBg: 'bg-teal-700 text-white',
      },
      leaderboard: [
        { rank: 1, model: 'Claude Opus 5 · high', provider: 'Perplexity', quality: '89.0%', cost: '$0.99', speed: '1.9m', errorRate: '0.02%' },
        { rank: 2, model: 'GPT-5.6 Sol · high', provider: 'Perplexity', quality: '85.5%', cost: '$0.75', speed: '2.1m', errorRate: '0.04%' },
        { rank: 3, model: 'DeepSeek Search Online', provider: 'DeepSeek', quality: '84.0%', cost: '$0.15', speed: '1.4m', errorRate: '0.05%' },
      ],
    },
    {
      id: 'deepsearch-qa',
      category: 'search',
      name: 'DeepSearchQA',
      description: '目标答案为完整列表项的聚合型提问，评估零遗漏穷尽检索召回率与零幻觉填充表现。',
      modelsCount: 4,
      lastRun: '2026年8月18日',
      quality: {
        score: '77.0%',
        model: 'Parallel / Claude Opus 5 · high',
        provider: 'Parallel',
        iconBg: 'bg-indigo-700 text-white',
      },
      value: {
        price: '$0.10',
        model: 'Perplexity / GPT-5.6 Luna · xhigh',
        provider: 'Perplexity',
        iconBg: 'bg-teal-700 text-white',
      },
      speed: {
        time: '1.6m',
        model: 'Perplexity / GPT-5.6 Luna · xhigh',
        provider: 'Perplexity',
        iconBg: 'bg-teal-700 text-white',
      },
      leaderboard: [
        { rank: 1, model: 'Claude Opus 5 · high', provider: 'Parallel', quality: '77.0%', cost: '$0.42', speed: '1.8m', errorRate: '0.03%' },
        { rank: 2, model: 'GPT-5.6 Luna · xhigh', provider: 'Perplexity', quality: '75.2%', cost: '$0.10', speed: '1.6m', errorRate: '0.02%' },
      ],
    },
    {
      id: 'hle-search',
      category: 'search',
      name: 'HLE',
      description: '人类最后考试（Humanity\'s Last Exam）联网搜索挑战版：专家级跨学科命题结合实时网页检索作答。',
      modelsCount: 2,
      lastRun: '2026年8月17日',
      quality: {
        score: '77.4%',
        model: 'Perplexity / Claude Opus 5 · high',
        provider: 'Perplexity',
        iconBg: 'bg-teal-700 text-white',
      },
      value: {
        price: '$0.16',
        model: 'Perplexity / Claude Opus 5 · high',
        provider: 'Perplexity',
        iconBg: 'bg-teal-700 text-white',
      },
      speed: {
        time: '48s',
        model: 'Perplexity / Claude Opus 5 · high',
        provider: 'Perplexity',
        iconBg: 'bg-teal-700 text-white',
      },
      leaderboard: [
        { rank: 1, model: 'Claude Opus 5 · high', provider: 'Perplexity', quality: '77.4%', cost: '$0.16', speed: '48s', errorRate: '0.01%' },
        { rank: 2, model: 'GPT-5.6 Luna · xhigh', provider: 'Perplexity', quality: '73.1%', cost: '$0.14', speed: '52s', errorRate: '0.03%' },
      ],
    },
    {
      id: 'wide-search',
      category: 'search',
      name: 'WideSearch',
      description: '全量填充复杂多维数据表格，依据每一格提取答案的准确度与部分匹配度综合梯级评分。',
      modelsCount: 4,
      lastRun: '2026年8月18日',
      quality: {
        score: '84.0%',
        model: 'Perplexity / GPT-5.6 Sol · high',
        provider: 'Perplexity',
        iconBg: 'bg-teal-700 text-white',
      },
      value: {
        price: '$0.063',
        model: 'Perplexity / GPT-5.6 Luna · xhigh',
        provider: 'Perplexity',
        iconBg: 'bg-teal-700 text-white',
      },
      speed: {
        time: '1.9m',
        model: 'Perplexity / GPT-5.6 Sol · high',
        provider: 'Perplexity',
        iconBg: 'bg-teal-700 text-white',
      },
      leaderboard: [
        { rank: 1, model: 'GPT-5.6 Sol · high', provider: 'Perplexity', quality: '84.0%', cost: '$0.082', speed: '1.9m', errorRate: '0.02%' },
        { rank: 2, model: 'GPT-5.6 Luna · xhigh', provider: 'Perplexity', quality: '81.6%', cost: '$0.063', speed: '2.0m', errorRate: '0.03%' },
      ],
    },
  ];

  const categoryTabs = [
    { id: 'all', label: '全部' },
    { id: 'agents', label: 'Agents 智能体', icon: <Bot className="w-3.5 h-3.5" /> },
    { id: 'media', label: 'Media 多媒体', icon: <ImageIcon className="w-3.5 h-3.5" /> },
    { id: 'artifacts', label: 'Artifacts 工件生成', icon: <Sparkles className="w-3.5 h-3.5" /> },
    { id: 'reasoning', label: 'Reasoning 逻辑推演', icon: <FlaskConical className="w-3.5 h-3.5" /> },
    { id: 'search', label: 'Search 联网检索', icon: <Globe className="w-3.5 h-3.5" /> },
  ];

  const filteredBenchmarks =
    selectedCategory === 'all'
      ? benchmarksData
      : benchmarksData.filter((b) => b.category === selectedCategory);

  // Group filtered benchmarks by category for section rendering
  const categoriesToRender =
    selectedCategory === 'all'
      ? ['agents', 'media', 'artifacts', 'reasoning', 'search']
      : [selectedCategory];

  const categoryMeta: Record<
    string,
    { title: string; icon: React.ReactNode; subtitle?: string }
  > = {
    agents: {
      title: 'Agents 与智能体工具',
      icon: <Bot className="w-4 h-4 text-emerald-600" />,
      subtitle: '评估模型在多步自主决策、API 工具集成及复杂业务环境中的表现。',
    },
    media: {
      title: 'Media 多媒体视觉与音频',
      icon: <ImageIcon className="w-4 h-4 text-amber-600" />,
      subtitle: '针对生成的图像、视频片段与语音，按照提示词遵从度与保真度打分，并按单次输出计算成本。',
    },
    artifacts: {
      title: 'Artifact 工件与应用生成',
      icon: <Sparkles className="w-4 h-4 text-blue-600" />,
      subtitle: '大语言模型挑战绘制复杂矢量图形、流程图及一次性端到端生成交互式可运行小游戏。',
    },
    reasoning: {
      title: 'Reasoning 逻辑推演',
      icon: <FlaskConical className="w-4 h-4 text-purple-600" />,
      subtitle: '聚焦高难度学术级抗检索推导、数学证明、形式化逻辑与深层归纳。',
    },
    search: {
      title: 'Search 联网检索与深度调研',
      icon: <Globe className="w-4 h-4 text-cyan-600" />,
      subtitle: '针对海量长尾事实检索、聚合型问答及实时多步骤网页分析能力评测。',
    },
  };

  const handleCopyCode = (text: string) => {
    navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div className="min-h-screen bg-white text-gray-900 flex flex-col font-sans select-text">
      {/* Top Main Benchmarks Header */}
      <div className="max-w-6xl mx-auto w-full px-4 sm:px-6 lg:px-8 pt-8 pb-4">
        <div className="flex flex-col md:flex-row md:items-start justify-between gap-6 pb-6 border-b border-gray-100">
          <div className="space-y-2 max-w-2xl">
            <h1 className="text-2xl sm:text-3xl font-bold text-gray-900 tracking-tight">
              基准测试
            </h1>
            <p className="text-xs text-gray-500 leading-relaxed">
              对可以在 uFreeTokens 请求中实际配置的各项参数（模型、服务提供商、搜索引擎及工具预算等）进行独立、可复现的标准化量化评测。每项得分均透明溯源至其背后的配置、成本与真实遥测数据。
            </p>
            <div className="text-[11px] text-gray-400 flex items-center space-x-2 pt-1 font-mono">
              <span className="font-semibold text-gray-700">11</span> 项基准评测
              <span>•</span>
              <span className="font-semibold text-gray-700">2,454,759</span> 次任务评测
              <span>•</span>
              <span>最近运行时间 2026年9月17日</span>
            </div>
          </div>

          {/* Right Benchmarks API Button Card */}
          <button
            onClick={() => setIsApiModalOpen(true)}
            className="flex items-center space-x-2.5 p-3 rounded-xl border border-gray-200 hover:border-purple-300 hover:bg-purple-50/40 transition-all text-left group shrink-0 bg-white shadow-xs cursor-pointer"
          >
            <div className="w-7 h-7 rounded-lg bg-gray-100 group-hover:bg-purple-100 text-gray-700 group-hover:text-purple-700 flex items-center justify-center font-mono text-xs font-bold transition-colors">
              {'</>'}
            </div>
            <div>
              <div className="text-xs font-bold text-gray-900 group-hover:text-purple-700 flex items-center space-x-1">
                <span>基准测试 API</span>
                <ExternalLink className="w-3 h-3 text-gray-400 group-hover:text-purple-600" />
              </div>
              <div className="text-[10px] text-gray-400 mt-0.5">
                通过 API 获取最新基准评测数据与元数据
              </div>
            </div>
          </button>
        </div>

        {/* Filter Pills */}
        <div className="flex flex-wrap items-center gap-2 pt-4 pb-2 text-xs">
          {categoryTabs.map((tab) => {
            const active = selectedCategory === tab.id;
            return (
              <button
                key={tab.id}
                onClick={() => setSelectedCategory(tab.id)}
                className={`flex items-center space-x-1.5 px-3 py-1.5 rounded-full border text-xs font-medium transition-colors cursor-pointer ${
                  active
                    ? 'bg-gray-900 text-white border-gray-900 shadow-xs'
                    : 'bg-white text-gray-600 border-gray-200 hover:bg-gray-50'
                }`}
              >
                {tab.icon && <span>{tab.icon}</span>}
                <span>{tab.label}</span>
              </button>
            );
          })}
        </div>
      </div>

      {/* Main Content: Categories & Benchmark Lists */}
      <div className="max-w-6xl mx-auto w-full px-4 sm:px-6 lg:px-8 py-4 space-y-10 flex-1">
        {categoriesToRender.map((catKey) => {
          const catBenchmarks = benchmarksData.filter((b) => b.category === catKey);
          if (catBenchmarks.length === 0) return null;
          const meta = categoryMeta[catKey];

          return (
            <section key={catKey} className="space-y-3">
              {/* Category Header */}
              <div className="space-y-1">
                <div className="flex items-center space-x-2">
                  {meta.icon}
                  <h2 className="text-sm sm:text-base font-bold text-gray-900">{meta.title}</h2>
                </div>
                {meta.subtitle && (
                  <p className="text-xs text-gray-500 leading-relaxed">{meta.subtitle}</p>
                )}
              </div>

              {/* Table Container */}
              <div className="border border-gray-200 rounded-xl overflow-hidden bg-white shadow-xs">
                <table className="w-full text-left text-xs border-collapse">
                  <thead>
                    <tr className="bg-gray-50/70 border-b border-gray-200 text-[10px] text-gray-400 font-semibold uppercase tracking-wider">
                      <th className="py-2.5 px-4 font-medium w-2/5">基准测试项</th>
                      {catKey === 'media' || catKey === 'artifacts' ? (
                        <th className="py-2.5 px-4 font-medium">
                          <span className="flex items-center space-x-1">
                            <span>生成示例</span>
                          </span>
                        </th>
                      ) : (
                        <th className="py-2.5 px-4 font-medium">
                          <span className="flex items-center space-x-1 text-emerald-700">
                            <Award className="w-3 h-3" />
                            <span>最高质量</span>
                          </span>
                        </th>
                      )}
                      <th className="py-2.5 px-4 font-medium">
                        <span className="flex items-center space-x-1 text-purple-700">
                          <DollarSign className="w-3 h-3" />
                          <span>最高性价比</span>
                        </span>
                      </th>
                      <th className="py-2.5 px-4 font-medium">
                        <span className="flex items-center space-x-1 text-amber-700">
                          <Zap className="w-3 h-3" />
                          <span>最快响应</span>
                        </span>
                      </th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-gray-100">
                    {catBenchmarks.map((bench) => (
                      <tr
                        key={bench.id}
                        onClick={() => setActiveBenchmarkModal(bench)}
                        className="hover:bg-purple-50/30 transition-colors group cursor-pointer"
                      >
                        {/* 1. Benchmark Name & Info */}
                        <td className="py-3.5 px-4 align-top">
                          <div className="space-y-1 pr-2">
                            <div className="font-bold text-gray-900 group-hover:text-purple-700 flex items-center space-x-1 transition-colors">
                              <span>{bench.name}</span>
                              <ChevronRight className="w-3.5 h-3.5 text-gray-400 group-hover:text-purple-600 transition-transform group-hover:translate-x-0.5" />
                            </div>
                            <p className="text-[11px] text-gray-500 leading-snug line-clamp-2">
                              {bench.description}
                            </p>
                            <div className="text-[10px] text-gray-400 font-mono pt-0.5">
                              {bench.modelsCount} 个评测模型
                              {bench.lastRun && ` • 最近运行于 ${bench.lastRun}`}
                            </div>
                          </div>
                        </td>

                        {/* 2. Samples OR Quality */}
                        {bench.samples ? (
                          <td className="py-3.5 px-4 align-top">
                            <div className="flex items-center space-x-1.5 pt-0.5">
                              {bench.samples.map((s, idx) => (
                                <div
                                  key={idx}
                                  className="w-10 h-10 rounded-md overflow-hidden bg-gray-100 border border-gray-200 shrink-0 relative group/img shadow-xs"
                                  title={s.label}
                                >
                                  <img
                                    src={s.imgUrl}
                                    alt={s.label}
                                    className="w-full h-full object-cover group-hover/img:scale-105 transition-transform"
                                  />
                                </div>
                              ))}
                            </div>
                          </td>
                        ) : (
                          <td className="py-3.5 px-4 align-top">
                            {bench.quality && (
                              <div className="space-y-1">
                                <div className="text-sm font-extrabold text-gray-900 font-mono">
                                  {bench.quality.score}
                                </div>
                                <div className="text-[10px] text-gray-500 flex items-center space-x-1 truncate max-w-[150px]">
                                  <span className="text-emerald-600">✦</span>
                                  <span className="truncate">{bench.quality.model}</span>
                                </div>
                              </div>
                            )}
                          </td>
                        )}

                        {/* 3. Value */}
                        <td className="py-3.5 px-4 align-top">
                          <div className="space-y-1">
                            <div className="text-sm font-extrabold text-gray-900 font-mono">
                              {bench.value.price}
                            </div>
                            <div className="text-[10px] text-gray-500 flex items-center space-x-1 truncate max-w-[160px]">
                              <span className="text-purple-600 font-mono text-[9px]">❖</span>
                              <span className="truncate">{bench.value.model}</span>
                            </div>
                          </div>
                        </td>

                        {/* 4. Speed */}
                        <td className="py-3.5 px-4 align-top">
                          <div className="space-y-1">
                            <div className="text-sm font-extrabold text-gray-900 font-mono">
                              {bench.speed.time}
                            </div>
                            <div className="text-[10px] text-gray-500 flex items-center space-x-1 truncate max-w-[160px]">
                              <span className="text-amber-600 font-mono text-[9px]">⚡</span>
                              <span className="truncate">{bench.speed.model}</span>
                            </div>
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </section>
          );
        })}

        {/* Footer Sub-links */}
        <div className="pt-4 pb-2 text-xs text-gray-500 text-center">
          如需查看基于调用量权重的同类模型视图，请参阅{' '}
          <button
            onClick={onNavigateToModels}
            className="text-purple-600 hover:text-purple-800 font-medium underline cursor-pointer"
          >
            模型排行榜
          </button>{' '}
          以及{' '}
          <button
            onClick={onNavigateToModels}
            className="text-purple-600 hover:text-purple-800 font-medium underline cursor-pointer"
          >
            完整模型列表
          </button>
          。
        </div>
      </div>

      {/* Interactive Leaderboard Detail Modal */}
      {activeBenchmarkModal && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/40 backdrop-blur-xs animate-in fade-in duration-100"
          onClick={() => setActiveBenchmarkModal(null)}
        >
          <div
            className="bg-white rounded-2xl shadow-2xl max-w-2xl w-full border border-gray-200 overflow-hidden flex flex-col max-h-[90vh] animate-in zoom-in-95 duration-150"
            onClick={(e) => e.stopPropagation()}
          >
            {/* Modal Header */}
            <div className="p-4 sm:p-5 border-b border-gray-200 flex items-start justify-between bg-gray-50/70">
              <div className="space-y-1">
                <div className="flex items-center space-x-2">
                  <span className="px-2 py-0.5 rounded bg-purple-100 text-purple-700 text-[10px] font-bold uppercase font-mono">
                    {activeBenchmarkModal.category}
                  </span>
                  <h3 className="text-base font-bold text-gray-900">
                    {activeBenchmarkModal.name} 基准评测排行榜
                  </h3>
                </div>
                <p className="text-xs text-gray-500">{activeBenchmarkModal.description}</p>
                <div className="text-[10px] text-gray-400 font-mono pt-0.5">
                  评测覆盖 {activeBenchmarkModal.modelsCount} 个模型 • 最近轮次运行于{' '}
                  {activeBenchmarkModal.lastRun}
                </div>
              </div>
              <button
                onClick={() => setActiveBenchmarkModal(null)}
                className="p-1 rounded-md text-gray-400 hover:text-gray-700 hover:bg-gray-200 transition-colors"
              >
                <X className="w-5 h-5" />
              </button>
            </div>

            {/* Modal Body: Top Leaderboard Table */}
            <div className="p-4 sm:p-5 overflow-y-auto space-y-4 text-xs">
              <div className="flex items-center justify-between">
                <span className="font-bold text-gray-900 text-xs">综合表现排名前列模型</span>
                <span className="text-[11px] text-gray-400 font-mono">支持点击模型跳转体验</span>
              </div>

              <div className="border border-gray-200 rounded-lg overflow-hidden">
                <table className="w-full text-left text-xs">
                  <thead className="bg-gray-50 text-[10px] text-gray-400 font-semibold uppercase">
                    <tr>
                      <th className="py-2 px-3">排名</th>
                      <th className="py-2 px-3">模型</th>
                      <th className="py-2 px-3">提供商</th>
                      <th className="py-2 px-3 text-right">准确率 / 得分</th>
                      <th className="py-2 px-3 text-right">单次成本</th>
                      <th className="py-2 px-3 text-right">响应耗时</th>
                      <th className="py-2 px-3 text-right">异常率</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-gray-100">
                    {(activeBenchmarkModal.leaderboard || []).map((row) => (
                      <tr
                        key={row.rank}
                        className="hover:bg-purple-50/40 transition-colors group cursor-pointer"
                        onClick={() => {
                          const matched = allModels.find(
                            (m) =>
                              m.name.toLowerCase().includes(row.model.toLowerCase()) ||
                              row.model.toLowerCase().includes(m.name.toLowerCase())
                          );
                          if (matched && onSelectModel) {
                            onSelectModel(matched);
                            setActiveBenchmarkModal(null);
                          }
                        }}
                      >
                        <td className="py-2.5 px-3 font-mono font-bold">
                          {row.rank === 1 ? (
                            <span className="w-5 h-5 rounded-full bg-amber-100 text-amber-800 inline-flex items-center justify-center text-[10px]">
                              1
                            </span>
                          ) : row.rank === 2 ? (
                            <span className="w-5 h-5 rounded-full bg-gray-200 text-gray-800 inline-flex items-center justify-center text-[10px]">
                              2
                            </span>
                          ) : row.rank === 3 ? (
                            <span className="w-5 h-5 rounded-full bg-amber-700/20 text-amber-900 inline-flex items-center justify-center text-[10px]">
                              3
                            </span>
                          ) : (
                            <span className="text-gray-400 pl-1">{row.rank}</span>
                          )}
                        </td>
                        <td className="py-2.5 px-3 font-semibold text-gray-900 group-hover:text-purple-700">
                          {row.model}
                        </td>
                        <td className="py-2.5 px-3 text-gray-500">{row.provider}</td>
                        <td className="py-2.5 px-3 text-right font-mono font-bold text-emerald-700">
                          {row.quality}
                        </td>
                        <td className="py-2.5 px-3 text-right font-mono text-purple-700 font-medium">
                          {row.cost}
                        </td>
                        <td className="py-2.5 px-3 text-right font-mono text-gray-600">
                          {row.speed}
                        </td>
                        <td className="py-2.5 px-3 text-right font-mono text-gray-400">
                          {row.errorRate}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>

              {/* Sample test prompt description */}
              <div className="p-3 bg-gray-50 rounded-lg border border-gray-200 space-y-1.5 text-gray-600 text-[11px]">
                <div className="font-bold text-gray-800">评测基准配置与真实遥测</div>
                <p>
                  所有得分均由 uFreeTokens 自动化评测集群发起，在标准 HTTP 请求头设置固定{' '}
                  <code className="bg-white px-1 py-0.5 rounded border text-[10px] font-mono">
                    temperature=0.0
                  </code>{' '}
                  以及相同的工具模式沙箱。点击对应模型可直接跳转查看详细技术规格与实时提供商列表。
                </p>
              </div>
            </div>

            {/* Modal Footer */}
            <div className="p-4 border-t border-gray-200 flex justify-between items-center bg-gray-50">
              <button
                onClick={() => {
                  onNavigateToModels();
                  setActiveBenchmarkModal(null);
                }}
                className="px-3 py-1.5 rounded-lg border border-gray-300 text-gray-700 hover:bg-white text-xs font-medium cursor-pointer"
              >
                在模型集市中检索此类模型
              </button>
              <button
                onClick={() => setActiveBenchmarkModal(null)}
                className="px-4 py-1.5 rounded-lg bg-purple-600 hover:bg-purple-700 text-white text-xs font-semibold shadow-xs cursor-pointer"
              >
                关闭
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Benchmarks API Documentation Modal */}
      {isApiModalOpen && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/40 backdrop-blur-xs"
          onClick={() => setIsApiModalOpen(false)}
        >
          <div
            className="bg-white rounded-2xl shadow-2xl max-w-xl w-full border border-gray-200 overflow-hidden"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="p-4 border-b border-gray-200 flex items-center justify-between">
              <div className="flex items-center space-x-2">
                <Code2 className="w-4 h-4 text-purple-600" />
                <h3 className="font-bold text-sm text-gray-900">uFreeTokens Benchmarks API</h3>
              </div>
              <button
                onClick={() => setIsApiModalOpen(false)}
                className="p-1 rounded text-gray-400 hover:text-gray-700"
              >
                <X className="w-4 h-4" />
              </button>
            </div>

            <div className="p-4 space-y-3 text-xs">
              <p className="text-gray-600 leading-relaxed">
                无需编写爬虫，直接调用我们的公共 API 实时拉取最新全维度基准测试得分与模型配置元数据：
              </p>

              <div className="bg-gray-900 text-gray-100 rounded-lg p-3 font-mono text-[11px] relative">
                <button
                  onClick={() =>
                    handleCopyCode(
                      `curl https://api.ufreetokens.ai/v1/benchmarks \\\n  -H "Authorization: Bearer $UFREETOKENS_API_KEY"`
                    )
                  }
                  className="absolute right-2 top-2 p-1 text-gray-400 hover:text-white rounded bg-gray-800"
                >
                  {copied ? <Check className="w-3.5 h-3.5 text-emerald-400" /> : <Copy className="w-3.5 h-3.5" />}
                </button>
                <pre className="overflow-x-auto text-emerald-400">
                  {`curl https://api.ufreetokens.ai/v1/benchmarks \\\n  -H "Authorization: Bearer $UFREETOKENS_API_KEY"`}
                </pre>
              </div>

              <div className="text-[11px] text-gray-500 pt-1">
                返回字段包含各模型在 <code className="text-purple-700 font-mono">T²-Bench</code>、
                <code className="text-purple-700 font-mono">GPQA Diamond</code>、
                <code className="text-purple-700 font-mono">BrowseComp</code> 等公开评测集上的精确数值、每百万
                Token 成本与 P50 延迟时间序列。
              </div>
            </div>

            <div className="p-3 bg-gray-50 border-t border-gray-200 flex justify-end">
              <button
                onClick={() => setIsApiModalOpen(false)}
                className="px-4 py-1.5 rounded-lg bg-gray-900 text-white text-xs font-medium cursor-pointer"
              >
                知道了
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Brand Footer */}
      <footer className="border-t border-gray-200 bg-white text-gray-600 text-xs mt-12">
        <div className="max-w-6xl mx-auto px-4 sm:px-6 lg:px-8 py-12">
          <div className="grid grid-cols-2 md:grid-cols-4 gap-8 mb-12">
            {/* Col 1: Brand */}
            <div className="col-span-2 md:col-span-1 space-y-3">
              <div className="flex items-center space-x-1.5 font-bold text-gray-900">
                <div className="w-4 h-4 rounded bg-purple-600 flex items-center justify-center text-white text-[10px]">
                  ▲
                </div>
                <span className="text-sm font-semibold tracking-tight">uFreeTokens</span>
              </div>
              <p className="text-[11px] text-gray-400 leading-relaxed">
                © 2026 uFreeTokens, Inc.
              </p>
            </div>

            {/* Col 2: Product */}
            <div className="space-y-2">
              <h4 className="font-semibold text-gray-900 text-xs">产品与服务</h4>
              <ul className="space-y-1.5 text-gray-500 text-[11px]">
                <li>
                  <button
                    onClick={onNavigateToModels}
                    className="hover:text-purple-600 transition-colors"
                  >
                    模型集市
                  </button>
                </li>
                <li>
                  <button
                    onClick={onNavigateToModels}
                    className="hover:text-purple-600 transition-colors"
                  >
                    排行榜
                  </button>
                </li>
                <li>
                  <span className="font-semibold text-purple-600">基准评测</span>
                </li>
                <li>
                  <a href="#" className="hover:text-purple-600 transition-colors">
                    生态应用
                  </a>
                </li>
                <li>
                  <a href="#" className="hover:text-purple-600 transition-colors">
                    发现精选
                  </a>
                </li>
                <li>
                  <a href="#" className="hover:text-purple-600 transition-colors">
                    定价方案
                  </a>
                </li>
              </ul>
            </div>

            {/* Col 3: Developer */}
            <div className="space-y-2">
              <h4 className="font-semibold text-gray-900 text-xs">开发者生态</h4>
              <ul className="space-y-1.5 text-gray-500 text-[11px]">
                <li>
                  <a href="#" className="hover:text-purple-600 transition-colors">
                    开发文档
                  </a>
                </li>
                <li>
                  <a href="#" className="hover:text-purple-600 transition-colors">
                    API 接口参考
                  </a>
                </li>
                <li>
                  <a href="#" className="hover:text-purple-600 transition-colors">
                    开发者开放平台
                  </a>
                </li>
                <li>
                  <a href="#" className="hover:text-purple-600 transition-colors">
                    服务可用状态
                  </a>
                </li>
              </ul>
            </div>

            {/* Col 4: Connect */}
            <div className="space-y-2">
              <h4 className="font-semibold text-gray-900 text-xs">社区与社交</h4>
              <ul className="space-y-1.5 text-gray-500 text-[11px]">
                <li>
                  <a href="#" className="hover:text-purple-600 transition-colors">
                    Discord 交流群
                  </a>
                </li>
                <li>
                  <a href="#" className="hover:text-purple-600 transition-colors">
                    GitHub 开源
                  </a>
                </li>
                <li>
                  <a href="#" className="hover:text-purple-600 transition-colors">
                    LinkedIn
                  </a>
                </li>
                <li>
                  <a href="#" className="hover:text-purple-600 transition-colors">
                    X (Twitter)
                  </a>
                </li>
              </ul>
            </div>
          </div>

          {/* Newsletter section */}
          <div className="pt-8 border-t border-gray-100 flex flex-col sm:flex-row items-start sm:items-center justify-between gap-4">
            <div className="space-y-1">
              <div className="font-semibold text-gray-900 text-xs">
                订阅 uFreeTokens 官方周刊
              </div>
              <p className="text-[11px] text-gray-400">
                掌握最新的模型使用趋势、版本更新及前沿研究报告，每周一期，无垃圾邮件。
              </p>
            </div>

            <div className="flex items-center space-x-2 w-full sm:w-auto">
              <input
                type="email"
                value={newsletterEmail}
                onChange={(e) => setNewsletterEmail(e.target.value)}
                placeholder="输入你的电子邮箱地址"
                className="px-3 py-1.5 border border-gray-300 rounded-lg text-xs w-full sm:w-64 focus:outline-none focus:border-purple-600"
              />
              <button
                onClick={() => setSubscribed(true)}
                className="px-4 py-1.5 bg-purple-600 hover:bg-purple-700 text-white font-semibold rounded-lg text-xs shrink-0 transition-colors cursor-pointer"
              >
                {subscribed ? '已订阅 ✓' : '订阅'}
              </button>
            </div>
          </div>
        </div>
      </footer>
    </div>
  );
};
