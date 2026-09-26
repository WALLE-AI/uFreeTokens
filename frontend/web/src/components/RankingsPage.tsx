import React, { useState, useEffect } from 'react';
import {
  BarChart3,
  TrendingUp,
  Zap,
  DollarSign,
  Globe,
  Code2,
  Maximize2,
  Wrench,
  Image as ImageIcon,
  Smartphone,
  HelpCircle,
  ChevronDown,
  ChevronUp,
  Search,
  ExternalLink,
  ArrowUpRight,
  ArrowDownRight,
  Filter,
  Layers,
  Sparkles,
  Info,
  Sliders,
  CheckCircle2,
  PieChart,
  FileText,
  Volume2,
  Mic,
  Split,
  Box
} from 'lucide-react';
import { Model } from '../types';

interface RankingsPageProps {
  allModels?: Model[];
  onSelectModel?: (model: Model) => void;
  onNavigateToBenchmarks?: () => void;
  onNavigateToModels?: () => void;
}

// Data structures
interface LeaderboardModel {
  rank: number;
  name: string;
  author: string;
  authorDisplay: string;
  tokens: string;
  change: string;
  changeType: 'up' | 'down' | 'neutral';
  color: string;
}

interface AuthorMarketShare {
  rank: number;
  author: string;
  share: string;
  tokens: string;
  percentNum: number;
  color: string;
}

interface BenchmarkScatterPoint {
  id: string;
  name: string;
  author: string;
  score: number;
  price: number;
  isPareto?: boolean;
}

interface FastestModel {
  rank: number;
  name: string;
  fastestOn: string;
  speed: number; // tok/s
  price: number; // $/M
}

interface TopApp {
  rank: number;
  name: string;
  tokens: string;
  category: string;
}

const TOP_10_MODELS: LeaderboardModel[] = [
  { rank: 1, name: 'GPT-5.6 Luna', author: 'openai', authorDisplay: 'openai', tokens: '15.8T tokens', change: '+12%', changeType: 'up', color: '#10b981' },
  { rank: 2, name: 'DeepSeek V4.1 Flash', author: 'deepseek', authorDisplay: 'deepseek', tokens: '11.8T tokens', change: '+9999%', changeType: 'up', color: '#3b82f6' },
  { rank: 3, name: 'Hy4 preview', author: 'tencent', authorDisplay: 'tencent', tokens: '11.6T tokens', change: '-39%', changeType: 'down', color: '#8b5cf6' },
  { rank: 4, name: 'GLM 5.3 Flash', author: 'z-ai', authorDisplay: 'z-ai', tokens: '11.4T tokens', change: '+7%', changeType: 'up', color: '#ec4899' },
  { rank: 5, name: 'DeepSeek V4 Flash 0731', author: 'deepseek', authorDisplay: 'deepseek', tokens: '10.6T tokens', change: '-14%', changeType: 'down', color: '#06b6d4' },
  { rank: 6, name: 'MiMo-V2.5', author: 'xiaomi', authorDisplay: 'xiaomi', tokens: '7.52T tokens', change: '+46%', changeType: 'up', color: '#f97316' },
  { rank: 7, name: 'Hy3', author: 'tencent', authorDisplay: 'tencent', tokens: '4.69T tokens', change: '+40%', changeType: 'up', color: '#a855f7' },
  { rank: 8, name: 'DeepSeek V4 Flash 0423', author: 'deepseek', authorDisplay: 'deepseek', tokens: '4.09T tokens', change: '+14%', changeType: 'up', color: '#0ea5e9' },
  { rank: 9, name: 'Nemotron 3 Ultra (free)', author: 'nvidia', authorDisplay: 'nvidia', tokens: '3.64T tokens', change: '+0%', changeType: 'neutral', color: '#84cc16' },
  { rank: 10, name: 'GLM 5.3', author: 'z-ai', authorDisplay: 'z-ai', tokens: '2.56T tokens', change: '-16%', changeType: 'down', color: '#f43f5e' }
];

const MORE_MODELS: LeaderboardModel[] = [
  { rank: 11, name: 'Claude Sonnet 4.6', author: 'anthropic', authorDisplay: 'anthropic', tokens: '2.41T tokens', change: '+19%', changeType: 'up', color: '#d97706' },
  { rank: 12, name: 'Gemini 3.1 Pro Preview', author: 'google', authorDisplay: 'google', tokens: '2.15T tokens', change: '+8%', changeType: 'up', color: '#2563eb' },
  { rank: 13, name: 'Claude Opus 5', author: 'anthropic', authorDisplay: 'anthropic', tokens: '1.98T tokens', change: '+24%', changeType: 'up', color: '#ea580c' },
  { rank: 14, name: 'Qwen3.8 Max', author: 'qwen', authorDisplay: 'qwen', tokens: '1.82T tokens', change: '+31%', changeType: 'up', color: '#059669' },
  { rank: 15, name: 'Llama 3.3 70B Instruct', author: 'meta', authorDisplay: 'meta', tokens: '1.65T tokens', change: '+5%', changeType: 'up', color: '#4f46e5' }
];

const MARKET_SHARE_AUTHORS: AuthorMarketShare[] = [
  { rank: 1, author: 'deepseek', share: '24.6%', tokens: '793M', percentNum: 24.6, color: '#3b82f6' },
  { rank: 2, author: 'google', share: '19.2%', tokens: '618M', percentNum: 19.2, color: '#10b981' },
  { rank: 3, author: 'openai', share: '18.3%', tokens: '591M', percentNum: 18.3, color: '#14b8a6' },
  { rank: 4, author: 'z-ai', share: '8.9%', tokens: '286M', percentNum: 8.9, color: '#ec4899' },
  { rank: 5, author: 'qwen', share: '7.0%', tokens: '227M', percentNum: 7.0, color: '#8b5cf6' },
  { rank: 6, author: 'tencent', share: '4.5%', tokens: '145M', percentNum: 4.5, color: '#f59e0b' },
  { rank: 7, author: 'anthropic', share: '3.0%', tokens: '97.5M', percentNum: 3.0, color: '#f97316' },
  { rank: 8, author: 'mistral', share: '2.6%', tokens: '83.8M', percentNum: 2.6, color: '#06b6d4' },
  { rank: 9, author: 'meta-llama', share: '2.3%', tokens: '73.3M', percentNum: 2.3, color: '#6366f1' },
  { rank: 10, author: '其他厂商', share: '9.6%', tokens: '309M', percentNum: 9.6, color: '#94a3b8' }
];

const SCATTER_BENCHMARKS: BenchmarkScatterPoint[] = [
  { id: '1', name: 'Claude Fable 5.1 (Adaptive)', author: 'anthropic', score: 53.4, price: 3.07, isPareto: true },
  { id: '2', name: 'Qwen3.8 Max', author: 'qwen', score: 53.4, price: 1.45, isPareto: true },
  { id: '3', name: 'GPT-4 Astra (max)', author: 'openai', score: 52.8, price: 2.50, isPareto: false },
  { id: '4', name: 'Claude Opus 5 (Adaptive)', author: 'anthropic', score: 50.7, price: 2.80, isPareto: false },
  { id: '5', name: 'Claude Fable 5', author: 'anthropic', score: 49.7, price: 2.10, isPareto: false },
  { id: '6', name: 'GPT-5.6 Sol (max)', author: 'openai', score: 47.1, price: 1.80, isPareto: false },
  { id: '7', name: 'Qwen3.8 Max (0902)', author: 'qwen', score: 45.4, price: 1.20, isPareto: true },
  { id: '8', name: 'GLM-5.3 (max)', author: 'z-ai', score: 44.9, price: 0.95, isPareto: true },
  { id: '9', name: 'Grok 4.4 (high)', author: 'x-ai', score: 44.4, price: 1.60, isPareto: false },
  { id: '10', name: 'Kimi K3 (max)', author: 'moonshotai', score: 43.8, price: 0.85, isPareto: true }
];

const FASTEST_MODELS: FastestModel[] = [
  { rank: 1, name: 'gpt-oss-120b', fastestOn: 'Cerebras', speed: 708, price: 0.35 },
  { rank: 2, name: 'gpt-oss-safeguard-20b', fastestOn: 'Groq', speed: 453, price: 0.07 },
  { rank: 3, name: 'gpt-oss-20b', fastestOn: 'Groq', speed: 288, price: 0.07 },
  { rank: 4, name: 'MiniMax M2.7', fastestOn: 'Groq', speed: 241, price: 0.40 },
  { rank: 5, name: 'GPT-5.6 Luna Pro', fastestOn: 'OpenAI', speed: 220, price: 0.40 },
  { rank: 6, name: 'Gemini 3.8 Flash', fastestOn: 'Google AI Studio', speed: 192, price: 1.35 },
  { rank: 7, name: 'Gemini 3.5 Flash', fastestOn: 'Google AI Studio', speed: 185, price: 1.10 },
  { rank: 8, name: 'GLM 5.2', fastestOn: 'Decart', speed: 184, price: 2.10 },
  { rank: 9, name: 'Gemini 3.7 Flash', fastestOn: 'Google AI Studio', speed: 176, price: 1.35 },
  { rank: 10, name: 'Mercury 2', fastestOn: 'Inception', speed: 171, price: 0.25 }
];

const TOP_APPS: TopApp[] = [
  { rank: 1, name: 'Hermes Agent', tokens: '1.78T tokens', category: '编程智能体' },
  { rank: 2, name: 'Claude Code', tokens: '994B tokens', category: '开发助手' },
  { rank: 3, name: 'Draco-cascade-bench', tokens: '841B tokens', category: '基准测试套件' },
  { rank: 4, name: 'Kilo Code', tokens: '583B tokens', category: 'IDE 助手' },
  { rank: 5, name: 'Cline', tokens: '439B tokens', category: '自主编程 Agent' },
  { rank: 6, name: 'omo', tokens: '389B tokens', category: '开发者工具' },
  { rank: 7, name: 'Codex', tokens: '369B tokens', category: '代码执行引擎' },
  { rank: 8, name: 'DeepSeek Harness', tokens: '194B tokens', category: '智能体测试平台' },
  { rank: 9, name: 'OpenClaw', tokens: '185B tokens', category: '浏览器智能体' }
];

// Cost per session data
const COST_SESSION_MODELS = [
  { name: 'Llama 3.3 70B Instruct', author: 'meta', turn1: 0.0001, turn2: 0.001, turn10: 0.008, turn50: 0.076 },
  { name: 'Ling 2.0 Flash', author: 'ling', turn1: 0.0002, turn2: 0.0015, turn10: 0.012, turn50: 0.095 },
  { name: 'MiMo-V2.5', author: 'xiaomi', turn1: 0.0003, turn2: 0.0021, turn10: 0.021, turn50: 0.15 },
  { name: 'Gemini 2.5 Flash Lite', author: 'google', turn1: 0.0004, turn2: 0.0027, turn10: 0.025, turn50: 0.18 },
  { name: 'gpt-oss-120b', author: 'openai', turn1: 0.0005, turn2: 0.0035, turn10: 0.031, turn50: 0.24 },
  { name: 'GLM 4.7 Flash', author: 'z-ai', turn1: 0.0005, turn2: 0.0041, turn10: 0.038, turn50: 0.31 },
  { name: 'Solar Pro 4', author: 'upstage', turn1: 0.0006, turn2: 0.0049, turn10: 0.045, turn50: 0.38 },
  { name: 'DeepSeek V4 Flash 0731', author: 'deepseek', turn1: 0.0007, turn2: 0.0054, turn10: 0.048, turn50: 0.42 },
  { name: 'Muse Spark 1.3 Contributor', author: 'muse', turn1: 0.0005, turn2: 0.0048, turn10: 0.200, turn50: 0.55 },
  { name: 'DeepSeek V4 Flash 0423', author: 'deepseek', turn1: 0.0008, turn2: 0.0061, turn10: 0.052, turn50: 0.48 }
];

export const RankingsPage: React.FC<RankingsPageProps> = ({
  allModels = [],
  onSelectModel,
  onNavigateToBenchmarks,
  onNavigateToModels
}) => {
  // Navigation tabs at top
  const [activeModalityTab, setActiveModalityTab] = useState<string>('Text');
  const modalityTabs = [
    { id: 'Text', label: '文本 (Text)', icon: FileText },
    { id: 'Image', label: '图像 (Image)', icon: ImageIcon },
    { id: 'Embeddings', label: '向量嵌入 (Embeddings)', icon: Layers },
    { id: 'Rerank', label: '重排 (Rerank)', icon: Split },
    { id: 'Video', label: '视频 (Video)', icon: Box },
    { id: 'Speech', label: '语音合成 (Speech)', icon: Mic },
    { id: 'Transcription', label: '语音识别 (Transcription)', icon: Volume2 },
    { id: 'Batch', label: '批量推理 (Batch)', icon: Sliders }
  ];

  // Active section for sidebar highlight
  const [activeSection, setActiveSection] = useState<string>('top-models');

  // Section states
  const [scaleType, setScaleType] = useState<'linear' | 'log'>('linear');
  const [showMoreLeaderboard, setShowMoreLeaderboard] = useState<boolean>(false);
  const [selectedTaskCategory, setSelectedTaskCategory] = useState<'Classification' | 'Code' | 'Agent' | 'Data'>('Classification');
  const [activeAgentTool, setActiveAgentTool] = useState<string>('Hermes Agent');
  const [showMoreCostModels, setShowMoreCostModels] = useState<boolean>(false);
  const [marketShareMode, setMarketShareMode] = useState<'absolute' | 'percentage'>('percentage');
  const [benchmarkSearch, setBenchmarkSearch] = useState<string>('');
  const [showPareto, setShowPareto] = useState<boolean>(true);
  const [languageMode, setLanguageMode] = useState<string>('英语');
  const [progLangMode, setProgLangMode] = useState<string>('Python');
  const [contextBucket, setContextBucket] = useState<string>('1k - 10k Tokens');

  const navItems = [
    { id: 'top-models', label: '热门模型趋势', icon: BarChart3 },
    { id: 'leaderboard', label: '模型排行榜', icon: TrendingUp },
    { id: 'task-models', label: '按任务分类排行', icon: Zap },
    { id: 'cost-session', label: '单会话成本', icon: DollarSign },
    { id: 'market-share', label: '市场份额', icon: PieChart },
    { id: 'benchmarks-sec', label: '基准测试', icon: Sliders },
    { id: 'fastest-models', label: '最快推理模型', icon: Zap },
    { id: 'languages-sec', label: '自然语言分布', icon: Globe },
    { id: 'programming-sec', label: '编程语言分布', icon: Code2 },
    { id: 'context-length', label: '上下文长度', icon: Maximize2 },
    { id: 'tool-calls', label: '工具调用', icon: Wrench },
    { id: 'images-sec', label: '图像处理量', icon: ImageIcon },
    { id: 'top-apps', label: '热门应用', icon: Smartphone },
    { id: 'measurement', label: '统计方法与说明', icon: HelpCircle }
  ];

  const scrollToSection = (id: string) => {
    setActiveSection(id);
    const element = document.getElementById(id);
    if (element) {
      const yOffset = -80;
      const y = element.getBoundingClientRect().top + window.pageYOffset + yOffset;
      window.scrollTo({ top: y, behavior: 'smooth' });
    }
  };

  // Track scroll position for active section
  useEffect(() => {
    const handleScroll = () => {
      const scrollPos = window.scrollY + 120;
      for (const item of navItems) {
        const el = document.getElementById(item.id);
        if (el) {
          const top = el.offsetTop;
          const height = el.offsetHeight;
          if (scrollPos >= top && scrollPos < top + height) {
            setActiveSection(item.id);
            break;
          }
        }
      }
    };
    window.addEventListener('scroll', handleScroll, { passive: true });
    return () => window.removeEventListener('scroll', handleScroll);
  }, []);

  const handleModelClick = (modelName: string) => {
    if (!onSelectModel) return;
    const cleanName = modelName.toLowerCase();
    const found = allModels.find(
      (m) =>
        m.name.toLowerCase().includes(cleanName) ||
        cleanName.includes(m.name.toLowerCase()) ||
        m.id.toLowerCase().includes(cleanName)
    );
    if (found) {
      onSelectModel(found);
    }
  };

  // Task category models mapping
  const taskCategoryRankings = {
    Classification: [
      { rank: 1, name: 'Claude Opus 5', author: 'anthropic', share: '8.5%', change: '+2.1' },
      { rank: 2, name: 'Gemini 3.1 Pro Preview', author: 'google', share: '6.9%', change: '+1.9' },
      { rank: 3, name: 'GPT-5.6 Sol', author: 'openai', share: '5.7%', change: '-0.3' },
      { rank: 4, name: 'Claude Sonnet 4.6', author: 'anthropic', share: '4.1%', change: '+0.9' },
      { rank: 5, name: 'Claude Sonnet 5', author: 'anthropic', share: '4.0%', change: '+0.3' },
      { rank: 6, name: 'Kimi K3', author: 'moonshotai', share: '3.6%', change: '+0.7' },
      { rank: 7, name: 'GPT-4 Astra', author: 'openai', share: '3.5%', change: '+3.5' },
      { rank: 8, name: 'GPT-5.6 Luna', author: 'openai', share: '3.4%', change: '+1.6' },
      { rank: 9, name: 'Gemini 3.7 Flash', author: 'google', share: '2.9%', change: '+2.6' },
      { rank: 10, name: 'Claude Opus 4.8', author: 'anthropic', share: '2.9%', change: '-1.7' }
    ],
    Code: [
      { rank: 1, name: 'Claude Sonnet 4.6', author: 'anthropic', share: '9.4%', change: '+1.8' },
      { rank: 2, name: 'GLM 5.3 Flash', author: 'z-ai', share: '8.2%', change: '+2.4' },
      { rank: 3, name: 'DeepSeek V4.1 Flash', author: 'deepseek', share: '7.8%', change: '+3.1' },
      { rank: 4, name: 'GPT-5.6 Luna', author: 'openai', share: '6.5%', change: '-0.4' },
      { rank: 5, name: 'Qwen3.8 Max', author: 'qwen', share: '5.9%', change: '+1.2' },
      { rank: 6, name: 'MiMo-V2.5', author: 'xiaomi', share: '4.6%', change: '+0.9' },
      { rank: 7, name: 'DeepSeek V4 Flash 0731', author: 'deepseek', share: '4.1%', change: '-0.8' },
      { rank: 8, name: 'Gemini 3.1 Pro Preview', author: 'google', share: '3.8%', change: '+0.5' },
      { rank: 9, name: 'Hy4 preview', author: 'tencent', share: '3.2%', change: '-1.2' },
      { rank: 10, name: 'Claude Opus 5', author: 'anthropic', share: '3.0%', change: '+0.7' }
    ],
    Agent: [
      { rank: 1, name: 'GPT-5.6 Luna', author: 'openai', share: '9.8%', change: '+2.5' },
      { rank: 2, name: 'Claude Sonnet 4.6', author: 'anthropic', share: '8.9%', change: '+1.4' },
      { rank: 3, name: 'Gemini 3.1 Pro Preview', author: 'google', share: '7.1%', change: '+0.8' },
      { rank: 4, name: 'DeepSeek V4.1 Flash', author: 'deepseek', share: '6.4%', change: '+4.2' },
      { rank: 5, name: 'Claude Opus 5', author: 'anthropic', share: '5.2%', change: '+1.1' },
      { rank: 6, name: 'GLM 5.3 Flash', author: 'z-ai', share: '4.8%', change: '+0.6' },
      { rank: 7, name: 'Hy4 preview', author: 'tencent', share: '3.9%', change: '-0.9' },
      { rank: 8, name: 'Qwen3.8 Max', author: 'qwen', share: '3.5%', change: '+0.3' },
      { rank: 9, name: 'MiMo-V2.5', author: 'xiaomi', share: '2.9%', change: '+0.5' },
      { rank: 10, name: 'Kimi K3', author: 'moonshotai', share: '2.4%', change: '+0.2' }
    ],
    Data: [
      { rank: 1, name: 'DeepSeek V4 Flash 0731', author: 'deepseek', share: '12.4%', change: '+3.1' },
      { rank: 2, name: 'GLM 5.3 Flash', author: 'z-ai', share: '10.8%', change: '+1.5' },
      { rank: 3, name: 'Gemini 2.5 Flash Lite', author: 'google', share: '9.5%', change: '+2.0' },
      { rank: 4, name: 'GPT-5.6 Luna', author: 'openai', share: '8.1%', change: '-0.5' },
      { rank: 5, name: 'DeepSeek V4.1 Flash', author: 'deepseek', share: '7.9%', change: '+1.8' },
      { rank: 6, name: 'Qwen3.8 Max', author: 'qwen', share: '6.2%', change: '+0.7' },
      { rank: 7, name: 'MiMo-V2.5', author: 'xiaomi', share: '5.1%', change: '+0.4' },
      { rank: 8, name: 'gpt-oss-120b', author: 'openai', share: '4.8%', change: '+1.1' },
      { rank: 9, name: 'Claude Sonnet 4.6', author: 'anthropic', share: '4.2%', change: '+0.3' },
      { rank: 10, name: 'Hy3', author: 'tencent', share: '3.8%', change: '-0.6' }
    ]
  };

  const filteredBenchmarks = SCATTER_BENCHMARKS.filter(
    (b) =>
      b.name.toLowerCase().includes(benchmarkSearch.toLowerCase()) ||
      b.author.toLowerCase().includes(benchmarkSearch.toLowerCase())
  );

  return (
    <div className="min-h-screen bg-white text-gray-900 font-sans selection:bg-purple-100">
      {/* Top sticky Modality / Feature filter pills */}
      <div className="sticky top-12 z-30 bg-white/95 backdrop-blur-md border-b border-gray-200 px-4 md:px-8 py-2.5 overflow-x-auto scrollbar-none">
        <div className="max-w-7xl mx-auto flex items-center gap-1.5 min-w-max">
          {modalityTabs.map((tab) => {
            const Icon = tab.icon;
            const isActive = activeModalityTab === tab.id;
            return (
              <button
                key={tab.id}
                onClick={() => setActiveModalityTab(tab.id)}
                className={`flex items-center gap-2 px-3.5 py-1.5 rounded-full text-xs font-medium transition-all ${
                  isActive
                    ? 'bg-gray-900 text-white shadow-xs'
                    : 'bg-gray-100 text-gray-600 hover:text-gray-900 hover:bg-gray-200'
                }`}
              >
                <Icon className="w-3.5 h-3.5" />
                <span>{tab.label}</span>
              </button>
            );
          })}
        </div>
      </div>

      {/* Main Container */}
      <div className="max-w-7xl mx-auto px-4 md:px-8 py-8">
        {/* Title Header */}
        <div className="mb-10">
          <h1 className="text-3xl md:text-4xl font-extrabold tracking-tight text-gray-900 mb-2">
            AI 模型排行榜
          </h1>
          <p className="text-sm md:text-base text-gray-600 max-w-4xl leading-relaxed">
            基于真实开发者调用的实时大模型排行榜。模型排名依据数百万开发者通过{' '}
            <span className="text-gray-900 font-semibold">uFreeTokens API</span> 实际处理的 Token 用量进行统计。{' '}
            <button
              onClick={onNavigateToModels}
              className="text-purple-600 hover:text-purple-800 font-medium underline inline-flex items-center gap-0.5 ml-1"
            >
              查看全部文本模型
              <ArrowUpRight className="w-3.5 h-3.5" />
            </button>
          </p>
          <div className="mt-3 flex items-center gap-2 text-xs text-gray-400 font-mono">
            <span className="inline-block w-2 h-2 rounded-full bg-emerald-500 animate-pulse" />
            <span>用量数据更新截至 2026年9月17日</span>
          </div>
        </div>

        {/* 2-Column Layout */}
        <div className="grid grid-cols-1 lg:grid-cols-12 gap-8 items-start">
          {/* Left Sticky Sidebar Nav */}
          <aside className="hidden lg:block lg:col-span-3 sticky top-28 space-y-1 bg-white border border-gray-200 rounded-2xl p-3 shadow-xs">
            <div className="px-3 py-2 text-xs font-semibold text-gray-400 uppercase tracking-wider">
              目录导航
            </div>
            <nav className="space-y-0.5 max-h-[calc(100vh-180px)] overflow-y-auto pr-1">
              {navItems.map((item) => {
                const Icon = item.icon;
                const isActive = activeSection === item.id;
                return (
                  <button
                    key={item.id}
                    onClick={() => scrollToSection(item.id)}
                    className={`w-full flex items-center gap-2.5 px-3 py-2 rounded-xl text-xs font-medium transition-all text-left ${
                      isActive
                        ? 'bg-purple-50 text-purple-700 border border-purple-200 font-semibold'
                        : 'text-gray-600 hover:text-gray-900 hover:bg-gray-50'
                    }`}
                  >
                    <Icon className={`w-3.5 h-3.5 shrink-0 ${isActive ? 'text-purple-600' : 'text-gray-400'}`} />
                    <span className="truncate">{item.label}</span>
                  </button>
                );
              })}
            </nav>
          </aside>

          {/* Right Main Content Stream */}
          <main className="lg:col-span-9 space-y-16">
            {/* SECTION 1: Top Models */}
            <section id="top-models" className="scroll-mt-32">
              <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-5 border-b border-gray-200 pb-4">
                <div>
                  <h2 className="text-xl font-bold text-gray-900 flex items-center gap-2">
                    <BarChart3 className="w-5 h-5 text-purple-600" />
                    热门模型趋势
                  </h2>
                  <p className="text-xs text-gray-500 mt-0.5">
                    uFreeTokens 平台各模型每周用量统计
                  </p>
                </div>
                <div className="flex items-center gap-1 bg-gray-100 border border-gray-200 p-1 rounded-lg self-start sm:self-auto">
                  <button
                    onClick={() => setScaleType('linear')}
                    className={`px-3 py-1 rounded-md text-xs font-medium transition-colors ${
                      scaleType === 'linear'
                        ? 'bg-white text-gray-900 shadow-xs'
                        : 'text-gray-500 hover:text-gray-900'
                    }`}
                  >
                    线性
                  </button>
                  <button
                    onClick={() => setScaleType('log')}
                    className={`px-3 py-1 rounded-md text-xs font-medium transition-colors ${
                      scaleType === 'log'
                        ? 'bg-white text-gray-900 shadow-xs'
                        : 'text-gray-500 hover:text-gray-900'
                    }`}
                  >
                    对数
                  </button>
                </div>
              </div>

              {/* Stacked Bar Timeline Chart */}
              <div className="bg-white border border-gray-200 rounded-2xl p-6 relative overflow-hidden shadow-xs">
                <div className="flex items-center justify-between text-xs text-gray-500 mb-3">
                  <span>每周处理 Token 量（Tokens/周）</span>
                  <span className="font-mono text-emerald-600 font-semibold">峰值：140T / 周</span>
                </div>

                {/* Chart Graphic */}
                <div className="h-64 w-full flex flex-col justify-end pt-4 pb-2 relative">
                  {/* Grid Lines */}
                  <div className="absolute inset-0 flex flex-col justify-between pointer-events-none opacity-40">
                    <div className="border-b border-dashed border-gray-300 w-full flex justify-end text-[10px] text-gray-400 pr-1">140T</div>
                    <div className="border-b border-dashed border-gray-300 w-full flex justify-end text-[10px] text-gray-400 pr-1">105T</div>
                    <div className="border-b border-dashed border-gray-300 w-full flex justify-end text-[10px] text-gray-400 pr-1">70T</div>
                    <div className="border-b border-dashed border-gray-300 w-full flex justify-end text-[10px] text-gray-400 pr-1">35T</div>
                    <div className="border-b border-gray-300 w-full flex justify-end text-[10px] text-gray-400 pr-1">0</div>
                  </div>

                  {/* Visual Stacked Bars */}
                  <div className="flex items-end justify-between h-48 gap-1.5 md:gap-2 z-10 px-2">
                    {[
                      { date: '9月22日', total: 42, m1: 15, m2: 8, m3: 7, m4: 5, m5: 7 },
                      { date: '10月20日', total: 54, m1: 18, m2: 12, m3: 9, m4: 6, m5: 9 },
                      { date: '11月17日', total: 68, m1: 22, m2: 15, m3: 11, m4: 8, m5: 12 },
                      { date: '12月15日', total: 81, m1: 25, m2: 19, m3: 14, m4: 9, m5: 14 },
                      { date: '1月12日', total: 95, m1: 28, m2: 24, m3: 17, m4: 11, m5: 15 },
                      { date: '2月09日', total: 110, m1: 30, m2: 29, m3: 20, m4: 13, m5: 18 },
                      { date: '3月09日', total: 121, m1: 32, m2: 34, m3: 22, m4: 15, m5: 18 },
                      { date: '4月06日', total: 128, m1: 33, m2: 38, m3: 24, m4: 15, m5: 18 },
                      { date: '5月04日', total: 134, m1: 34, m2: 41, m3: 25, m4: 16, m5: 18 },
                      { date: '6月01日', total: 137, m1: 35, m2: 43, m3: 26, m4: 16, m5: 17 },
                      { date: '7月06日', total: 139, m1: 35, m2: 45, m3: 26, m4: 16, m5: 17 },
                      { date: '8月24日', total: 142, m1: 36, m2: 47, m3: 26, m4: 16, m5: 17 }
                    ].map((col, idx) => (
                      <div key={idx} className="flex-1 flex flex-col items-center group relative h-full justify-end">
                        <div
                          className="w-full rounded-t-sm flex flex-col justify-end transition-all group-hover:brightness-125 overflow-hidden"
                          style={{ height: `${(col.total / 150) * 100}%` }}
                        >
                          <div className="bg-emerald-500 w-full" style={{ height: `${(col.m1 / col.total) * 100}%` }} />
                          <div className="bg-blue-500 w-full" style={{ height: `${(col.m2 / col.total) * 100}%` }} />
                          <div className="bg-purple-500 w-full" style={{ height: `${(col.m3 / col.total) * 100}%` }} />
                          <div className="bg-pink-500 w-full" style={{ height: `${(col.m4 / col.total) * 100}%` }} />
                          <div className="bg-slate-600 w-full" style={{ height: `${(col.m5 / col.total) * 100}%` }} />
                        </div>

                        {/* Tooltip on hover */}
                        <div className="opacity-0 group-hover:opacity-100 transition-opacity absolute bottom-full mb-2 z-30 bg-gray-900 border border-gray-800 text-gray-100 text-[11px] p-2.5 rounded-lg shadow-2xl pointer-events-none whitespace-nowrap min-w-36">
                          <div className="font-semibold text-white mb-1 border-b border-gray-800 pb-1">{col.date}, 2026</div>
                          <div className="text-emerald-400 flex justify-between gap-2"><span>GPT-5.6 Luna</span> <span>{col.m1}T</span></div>
                          <div className="text-blue-400 flex justify-between gap-2"><span>DeepSeek V4.1</span> <span>{col.m2}T</span></div>
                          <div className="text-purple-400 flex justify-between gap-2"><span>Hy4 preview</span> <span>{col.m3}T</span></div>
                          <div className="text-pink-400 flex justify-between gap-2"><span>GLM 5.3 Flash</span> <span>{col.m4}T</span></div>
                          <div className="text-gray-400 flex justify-between gap-2"><span>其他模型</span> <span>{col.m5}T</span></div>
                          <div className="font-bold text-white mt-1 pt-1 border-t border-gray-800 flex justify-between"><span>总计</span> <span>{col.total}T</span></div>
                        </div>

                        {/* Date tick */}
                        <span className="text-[10px] text-gray-400 mt-2 rotate-45 md:rotate-0 origin-top-left">
                          {col.date}
                        </span>
                      </div>
                    ))}
                  </div>
                </div>

                {/* Legend */}
                <div className="flex flex-wrap items-center justify-center gap-4 mt-8 pt-4 border-t border-gray-100 text-xs">
                  <div className="flex items-center gap-1.5"><span className="w-3 h-3 rounded-sm bg-emerald-500" /> <span className="text-gray-600">GPT-5.6 Luna</span></div>
                  <div className="flex items-center gap-1.5"><span className="w-3 h-3 rounded-sm bg-blue-500" /> <span className="text-gray-600">DeepSeek V4.1 Flash</span></div>
                  <div className="flex items-center gap-1.5"><span className="w-3 h-3 rounded-sm bg-purple-500" /> <span className="text-gray-600">Hy4 preview</span></div>
                  <div className="flex items-center gap-1.5"><span className="w-3 h-3 rounded-sm bg-pink-500" /> <span className="text-gray-600">GLM 5.3 Flash</span></div>
                  <div className="flex items-center gap-1.5"><span className="w-3 h-3 rounded-sm bg-gray-400" /> <span className="text-gray-500">其他模型</span></div>
                </div>
              </div>
            </section>

            {/* SECTION 2: LLM Leaderboard */}
            <section id="leaderboard" className="scroll-mt-32">
              <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-5 border-b border-gray-200 pb-4">
                <div>
                  <h2 className="text-xl font-bold text-gray-900 flex items-center gap-2">
                    <TrendingUp className="w-5 h-5 text-emerald-600" />
                    大模型排行榜
                  </h2>
                  <p className="text-xs text-gray-500 mt-0.5">
                    对比 uFreeTokens 平台上最受欢迎的模型用量与变化
                  </p>
                </div>
                <div className="flex items-center gap-2">
                  <div className="relative">
                    <select className="appearance-none bg-white border border-gray-200 rounded-lg px-3 py-1.5 pr-8 text-xs text-gray-700 font-medium hover:border-gray-300 cursor-pointer focus:outline-none focus:border-purple-400 shadow-xs">
                      <option>全部模型</option>
                      <option>仅开源模型</option>
                      <option>仅商业闭源模型</option>
                    </select>
                    <ChevronDown className="w-3.5 h-3.5 text-gray-400 absolute right-2.5 top-1/2 -translate-y-1/2 pointer-events-none" />
                  </div>
                  <div className="relative">
                    <select className="appearance-none bg-white border border-gray-200 rounded-lg px-3 py-1.5 pr-8 text-xs text-gray-700 font-medium hover:border-gray-300 cursor-pointer focus:outline-none focus:border-purple-400 shadow-xs">
                      <option>本周</option>
                      <option>近30天</option>
                      <option>历史全部</option>
                    </select>
                    <ChevronDown className="w-3.5 h-3.5 text-gray-400 absolute right-2.5 top-1/2 -translate-y-1/2 pointer-events-none" />
                  </div>
                </div>
              </div>

              {/* 2-Column Grid as in Image */}
              <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                {TOP_10_MODELS.map((item) => (
                  <div
                    key={item.rank}
                    onClick={() => handleModelClick(item.name)}
                    className="flex items-center justify-between p-3.5 bg-white border border-gray-200 rounded-xl hover:bg-purple-50/30 hover:border-purple-200 transition-all cursor-pointer group shadow-xs"
                  >
                    <div className="flex items-center gap-3.5 min-w-0">
                      <span className="w-6 text-center font-mono font-bold text-sm text-gray-400 group-hover:text-purple-600">
                        {item.rank}
                      </span>
                      <div className="min-w-0">
                        <div className="font-semibold text-sm text-gray-900 group-hover:text-purple-700 truncate">
                          {item.name}
                        </div>
                        <div className="text-xs text-gray-400 flex items-center gap-1.5">
                          <span>来自 {item.authorDisplay}</span>
                        </div>
                      </div>
                    </div>
                    <div className="text-right shrink-0">
                      <div className="text-xs font-semibold text-gray-800 font-mono">
                        {item.tokens}
                      </div>
                      <div
                        className={`text-[11px] font-medium flex items-center justify-end gap-0.5 ${
                          item.changeType === 'up'
                            ? 'text-emerald-600'
                            : item.changeType === 'down'
                            ? 'text-rose-600'
                            : 'text-gray-400'
                        }`}
                      >
                        {item.changeType === 'up' && <ArrowUpRight className="w-3 h-3" />}
                        {item.changeType === 'down' && <ArrowDownRight className="w-3 h-3" />}
                        {item.change}
                      </div>
                    </div>
                  </div>
                ))}
              </div>

              {/* Expandable more */}
              {showMoreLeaderboard && (
                <div className="grid grid-cols-1 md:grid-cols-2 gap-3 mt-3">
                  {MORE_MODELS.map((item) => (
                    <div
                      key={item.rank}
                      onClick={() => handleModelClick(item.name)}
                      className="flex items-center justify-between p-3.5 bg-white border border-gray-200 rounded-xl hover:bg-purple-50/30 hover:border-purple-200 transition-all cursor-pointer group shadow-xs"
                    >
                      <div className="flex items-center gap-3.5 min-w-0">
                        <span className="w-6 text-center font-mono font-bold text-sm text-gray-400 group-hover:text-purple-600">
                          {item.rank}
                        </span>
                        <div className="min-w-0">
                          <div className="font-semibold text-sm text-gray-900 group-hover:text-purple-700 truncate">
                            {item.name}
                          </div>
                          <div className="text-xs text-gray-400 flex items-center gap-1.5">
                            <span>来自 {item.authorDisplay}</span>
                          </div>
                        </div>
                      </div>
                      <div className="text-right shrink-0">
                        <div className="text-xs font-semibold text-gray-800 font-mono">
                          {item.tokens}
                        </div>
                        <div className="text-[11px] font-medium text-emerald-600 flex items-center justify-end gap-0.5">
                          <ArrowUpRight className="w-3 h-3" />
                          {item.change}
                        </div>
                      </div>
                    </div>
                  ))}
                </div>
              )}

              <div className="text-center mt-4">
                <button
                  onClick={() => setShowMoreLeaderboard(!showMoreLeaderboard)}
                  className="inline-flex items-center gap-1 text-xs font-medium text-purple-600 hover:text-purple-800 px-4 py-2 rounded-lg bg-white border border-gray-200 hover:bg-gray-50 shadow-xs transition-colors"
                >
                  {showMoreLeaderboard ? '收起' : '展开更多'}
                  {showMoreLeaderboard ? <ChevronUp className="w-3.5 h-3.5" /> : <ChevronDown className="w-3.5 h-3.5" />}
                </button>
              </div>
            </section>

            {/* SECTION 3: Top models by task */}
            <section id="task-models" className="scroll-mt-32">
              <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-5 border-b border-gray-200 pb-4">
                <div>
                  <h2 className="text-xl font-bold text-gray-900 flex items-center gap-2">
                    <Zap className="w-5 h-5 text-amber-500" />
                    按任务分类排行
                  </h2>
                  <p className="text-xs text-gray-500 mt-0.5">
                    按在 uFreeTokens 上的实际支出占比统计各细分任务领域的领先模型
                  </p>
                </div>
                <div className="relative">
                  <select className="appearance-none bg-white border border-gray-200 rounded-lg px-3 py-1.5 pr-8 text-xs text-gray-700 font-medium hover:border-gray-300 cursor-pointer focus:outline-none focus:border-purple-400 shadow-xs">
                    <option>支出占比</option>
                    <option>Token 消耗量</option>
                    <option>请求次数</option>
                  </select>
                  <ChevronDown className="w-3.5 h-3.5 text-gray-400 absolute right-2.5 top-1/2 -translate-y-1/2 pointer-events-none" />
                </div>
              </div>

              {/* Treemap Visualization Box */}
              <div className="bg-white border border-gray-200 rounded-2xl p-5 mb-5 shadow-xs">
                <div className="grid grid-cols-12 gap-2 h-44 md:h-52 w-full select-none text-xs font-medium">
                  {/* Classification block - 32.8% */}
                  <div
                    onClick={() => setSelectedTaskCategory('Classification')}
                    className={`col-span-12 md:col-span-4 rounded-xl p-3 flex flex-col justify-between cursor-pointer transition-all border ${
                      selectedTaskCategory === 'Classification'
                        ? 'bg-amber-50 border-amber-300 ring-2 ring-amber-400/30'
                        : 'bg-amber-50/50 border-amber-200 hover:bg-amber-100/60'
                    }`}
                  >
                    <div>
                      <div className="text-amber-900 font-bold text-sm">分类与对话 (Classification)</div>
                      <div className="text-amber-700 text-[11px]">占总支出 32.8%</div>
                    </div>
                    <div className="flex flex-wrap gap-1 text-[10px] text-amber-800">
                      <span className="bg-amber-100/80 px-1.5 py-0.5 rounded">角色扮演</span>
                      <span className="bg-amber-100/80 px-1.5 py-0.5 rounded">客服支持</span>
                      <span className="bg-amber-100/80 px-1.5 py-0.5 rounded">问答检索</span>
                    </div>
                  </div>

                  {/* Code block - 29.6% */}
                  <div
                    onClick={() => setSelectedTaskCategory('Code')}
                    className={`col-span-6 md:col-span-4 rounded-xl p-3 flex flex-col justify-between cursor-pointer transition-all border ${
                      selectedTaskCategory === 'Code'
                        ? 'bg-emerald-50 border-emerald-300 ring-2 ring-emerald-400/30'
                        : 'bg-emerald-50/50 border-emerald-200 hover:bg-emerald-100/60'
                    }`}
                  >
                    <div>
                      <div className="text-emerald-900 font-bold text-sm">代码开发 (Code)</div>
                      <div className="text-emerald-700 text-[11px]">占总支出 29.6%</div>
                    </div>
                    <div className="flex flex-wrap gap-1 text-[10px] text-emerald-800">
                      <span className="bg-emerald-100/80 px-1.5 py-0.5 rounded">代码生成</span>
                      <span className="bg-emerald-100/80 px-1.5 py-0.5 rounded">调试排错</span>
                      <span className="bg-emerald-100/80 px-1.5 py-0.5 rounded">代码审查</span>
                    </div>
                  </div>

                  {/* Agent & Data combined block */}
                  <div className="col-span-6 md:col-span-4 flex flex-col gap-2">
                    {/* Agent block - 28.9% */}
                    <div
                      onClick={() => setSelectedTaskCategory('Agent')}
                      className={`flex-1 rounded-xl p-2.5 flex flex-col justify-between cursor-pointer transition-all border ${
                        selectedTaskCategory === 'Agent'
                          ? 'bg-indigo-50 border-indigo-300 ring-2 ring-indigo-400/30'
                          : 'bg-indigo-50/50 border-indigo-200 hover:bg-indigo-100/60'
                      }`}
                    >
                      <div className="flex items-center justify-between">
                        <span className="text-indigo-900 font-bold">智能体 (Agent)</span>
                        <span className="text-indigo-700 text-[11px]">28.9%</span>
                      </div>
                      <div className="text-[10px] text-indigo-700 truncate">规划推理、工具调度</div>
                    </div>

                    {/* Data block - 9.6% */}
                    <div
                      onClick={() => setSelectedTaskCategory('Data')}
                      className={`h-16 rounded-xl p-2.5 flex items-center justify-between cursor-pointer transition-all border ${
                        selectedTaskCategory === 'Data'
                          ? 'bg-cyan-50 border-cyan-300 ring-2 ring-cyan-400/30'
                          : 'bg-cyan-50/50 border-cyan-200 hover:bg-cyan-100/60'
                      }`}
                    >
                      <span className="text-cyan-900 font-bold">数据处理 (Data)</span>
                      <span className="text-cyan-700 text-[11px]">9.6%</span>
                    </div>
                  </div>
                </div>

                {/* Interactive Pills */}
                <div className="flex flex-wrap items-center gap-2 mt-4 pt-3 border-t border-gray-100 text-xs">
                  <span className="text-gray-500 text-xs mr-2">任务筛选：</span>
                  {(['Classification', 'Code', 'Agent', 'Data'] as const).map((cat) => (
                    <button
                      key={cat}
                      onClick={() => setSelectedTaskCategory(cat)}
                      className={`px-3 py-1 rounded-full text-xs font-medium transition-all ${
                        selectedTaskCategory === cat
                          ? 'bg-purple-600 text-white shadow-xs'
                          : 'bg-gray-100 text-gray-600 hover:text-gray-900 hover:bg-gray-200'
                      }`}
                    >
                      {cat === 'Classification' ? '分类与对话' : cat === 'Code' ? '代码开发' : cat === 'Agent' ? '智能体 Agent' : '数据处理'} {cat === 'Classification' ? '32.8%' : cat === 'Code' ? '29.6%' : cat === 'Agent' ? '28.9%' : '9.6%'}
                    </button>
                  ))}
                </div>
              </div>

              {/* Detailed Category Table */}
              <div className="bg-white border border-gray-200 rounded-2xl overflow-hidden shadow-xs">
                <div className="px-5 py-3.5 bg-gray-50/80 border-b border-gray-200 flex items-center justify-between">
                  <div className="text-sm font-semibold text-gray-900">
                    {selectedTaskCategory === 'Classification' ? '分类与对话' : selectedTaskCategory === 'Code' ? '代码开发' : selectedTaskCategory === 'Agent' ? '智能体 Agent' : '数据处理'} <span className="text-gray-500 font-normal">支出占比领跑模型</span>
                  </div>
                  <span className="text-xs text-gray-400">每周更新</span>
                </div>
                <div className="divide-y divide-gray-100">
                  {taskCategoryRankings[selectedTaskCategory].map((row) => (
                    <div
                      key={row.rank}
                      onClick={() => handleModelClick(row.name)}
                      className="px-5 py-3 flex items-center justify-between hover:bg-purple-50/30 transition-colors cursor-pointer"
                    >
                      <div className="flex items-center gap-3">
                        <span className="w-5 text-center font-mono font-bold text-xs text-gray-400">
                          {row.rank}
                        </span>
                        <div>
                          <div className="text-sm font-medium text-gray-900 hover:text-purple-700">
                            {row.name}
                          </div>
                          <div className="text-xs text-gray-400">来自 {row.author}</div>
                        </div>
                      </div>
                      <div className="text-right">
                        <span className="font-mono text-sm font-semibold text-gray-800">{row.share}</span>
                        <div className="text-[11px] text-emerald-600">
                          {row.change.startsWith('+') ? row.change : row.change}
                        </div>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            </section>

            {/* SECTION 4: Cost per session */}
            <section id="cost-session" className="scroll-mt-32">
              <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-5 border-b border-gray-200 pb-4">
                <div>
                  <h2 className="text-xl font-bold text-gray-900 flex items-center gap-2">
                    <DollarSign className="w-5 h-5 text-emerald-600" />
                    单次会话成本
                  </h2>
                  <p className="text-xs text-gray-500 mt-0.5">
                    根据会话交互轮次（付费调用），统计编码智能体（Coding Agent）的典型会话成本
                  </p>
                </div>
                <div className="relative">
                  <select className="appearance-none bg-white border border-gray-200 rounded-lg px-3 py-1.5 pr-8 text-xs text-gray-700 font-medium hover:border-gray-300 cursor-pointer focus:outline-none focus:border-purple-400 shadow-xs">
                    <option>成本：由低到高</option>
                    <option>成本：由高到低</option>
                  </select>
                  <ChevronDown className="w-3.5 h-3.5 text-gray-400 absolute right-2.5 top-1/2 -translate-y-1/2 pointer-events-none" />
                </div>
              </div>

              {/* Agent switcher tabs */}
              <div className="flex items-center gap-2 mb-4 overflow-x-auto pb-1">
                {['Hermes Agent', 'Claude Code', 'Kilo Code', 'Codex'].map((agent) => (
                  <button
                    key={agent}
                    onClick={() => setActiveAgentTool(agent)}
                    className={`px-3 py-1.5 rounded-lg text-xs font-medium whitespace-nowrap transition-colors ${
                      activeAgentTool === agent
                        ? 'bg-purple-600 text-white shadow-xs'
                        : 'bg-white border border-gray-200 text-gray-600 hover:text-gray-900 hover:bg-gray-50 shadow-xs'
                    }`}
                  >
                    {agent}
                  </button>
                ))}
              </div>

              {/* Matrix Table with Log Scale Dots */}
              <div className="bg-white border border-gray-200 rounded-2xl overflow-x-auto shadow-xs">
                <div className="min-w-[640px]">
                  {/* Table Header */}
                  <div className="grid grid-cols-12 px-5 py-3 border-b border-gray-200 text-xs font-semibold text-gray-500 bg-gray-50/80">
                    <div className="col-span-4">模型</div>
                    <div className="col-span-2 text-center">1 轮交互</div>
                    <div className="col-span-2 text-center">2-9 轮交互</div>
                    <div className="col-span-2 text-center">10-49 轮交互</div>
                    <div className="col-span-2 text-center">50+ 轮交互</div>
                  </div>

                  {/* Rows */}
                  <div className="divide-y divide-gray-100 text-xs">
                    {(showMoreCostModels ? COST_SESSION_MODELS : COST_SESSION_MODELS.slice(0, 7)).map((m, idx) => (
                      <div
                        key={idx}
                        onClick={() => handleModelClick(m.name)}
                        className="grid grid-cols-12 px-5 py-3 items-center hover:bg-purple-50/30 transition-colors cursor-pointer"
                      >
                        <div className="col-span-4">
                          <div className="font-semibold text-gray-900 hover:text-purple-700 truncate">{m.name}</div>
                          <div className="text-[11px] text-gray-400">来自 {m.author}</div>
                        </div>
                        <div className="col-span-2 text-center font-mono text-emerald-600 font-medium">
                          ${m.turn1.toFixed(4)}
                        </div>
                        <div className="col-span-2 text-center font-mono text-emerald-600 font-medium">
                          ${m.turn2.toFixed(4)}
                        </div>
                        <div className="col-span-2 text-center font-mono text-amber-600 font-medium">
                          ${m.turn10.toFixed(3)}
                        </div>
                        <div className="col-span-2 text-center font-mono text-rose-600 font-semibold">
                          ${m.turn50.toFixed(2)}
                        </div>
                      </div>
                    ))}
                  </div>

                  {/* Expand button */}
                  <div className="p-3 text-center border-t border-gray-100">
                    <button
                      onClick={() => setShowMoreCostModels(!showMoreCostModels)}
                      className="text-xs text-purple-600 hover:text-purple-800 font-medium inline-flex items-center gap-1"
                    >
                      {showMoreCostModels ? '收起部分' : '显示更多模型'}
                      {showMoreCostModels ? <ChevronUp className="w-3.5 h-3.5" /> : <ChevronDown className="w-3.5 h-3.5" />}
                    </button>
                  </div>
                </div>
              </div>
            </section>

            {/* SECTION 5: Market Share */}
            <section id="market-share" className="scroll-mt-32">
              <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-5 border-b border-gray-200 pb-4">
                <div>
                  <h2 className="text-xl font-bold text-gray-900 flex items-center gap-2">
                    <PieChart className="w-5 h-5 text-indigo-600" />
                    提供商市场份额
                  </h2>
                  <p className="text-xs text-gray-500 mt-0.5">
                    对比各模型厂商在 uFreeTokens 平台上的文本请求量份额
                  </p>
                </div>
                <div className="flex items-center gap-1 bg-gray-100 border border-gray-200 p-1 rounded-lg">
                  <button
                    onClick={() => setMarketShareMode('absolute')}
                    className={`px-3 py-1 rounded-md text-xs font-medium transition-colors ${
                      marketShareMode === 'absolute' ? 'bg-white text-gray-900 shadow-xs' : 'text-gray-500 hover:text-gray-900'
                    }`}
                  >
                    绝对数值
                  </button>
                  <button
                    onClick={() => setMarketShareMode('percentage')}
                    className={`px-3 py-1 rounded-md text-xs font-medium transition-colors ${
                      marketShareMode === 'percentage' ? 'bg-white text-gray-900 shadow-xs' : 'text-gray-500 hover:text-gray-900'
                    }`}
                  >
                    百分比
                  </button>
                </div>
              </div>

              {/* Visual 100% Horizontal Proportion Bar */}
              <div className="bg-white border border-gray-200 rounded-2xl p-6 shadow-xs">
                <div className="text-xs text-gray-500 mb-2 flex justify-between">
                  <span>厂商分布（月活跃请求量）</span>
                  <span className="font-mono text-gray-900 font-semibold">总量：约 32亿 次/月</span>
                </div>

                <div className="h-6 w-full rounded-lg overflow-hidden flex shadow-inner">
                  {MARKET_SHARE_AUTHORS.map((author) => (
                    <div
                      key={author.author}
                      style={{ width: `${author.percentNum}%`, backgroundColor: author.color }}
                      title={`${author.author}: ${author.share}`}
                      className="h-full hover:opacity-80 transition-opacity cursor-pointer relative group"
                    />
                  ))}
                </div>

                {/* 2-Column Author breakdown list */}
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 mt-6 pt-4 border-t border-gray-100">
                  {MARKET_SHARE_AUTHORS.map((item) => (
                    <div
                      key={item.author}
                      className="flex items-center justify-between p-2.5 rounded-lg bg-gray-50/70 border border-gray-200"
                    >
                      <div className="flex items-center gap-2.5">
                        <span className="w-3 h-3 rounded-full shrink-0" style={{ backgroundColor: item.color }} />
                        <span className="text-xs font-semibold text-gray-900">{item.author}</span>
                      </div>
                      <div className="text-right">
                        <span className="text-xs font-mono font-bold text-gray-800">{item.share}</span>
                        <span className="text-[11px] text-gray-500 ml-2">({item.tokens})</span>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            </section>

            {/* SECTION 6: Benchmarks */}
            <section id="benchmarks-sec" className="scroll-mt-32">
              <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-5 border-b border-gray-200 pb-4">
                <div>
                  <h2 className="text-xl font-bold text-gray-900 flex items-center gap-2">
                    <Sliders className="w-5 h-5 text-purple-600" />
                    基准跑分评估
                  </h2>
                  <p className="text-xs text-gray-500 mt-0.5">
                    基于综合智能评估指数评测 uFreeTokens 平台热门模型 |{' '}
                    <button
                      onClick={onNavigateToBenchmarks}
                      className="text-purple-600 hover:text-purple-800 underline font-medium"
                    >
                      查看完整基准测试
                    </button>
                  </p>
                </div>
                <div className="flex items-center gap-3">
                  <label className="flex items-center gap-2 text-xs text-gray-600 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={showPareto}
                      onChange={(e) => setShowPareto(e.target.checked)}
                      className="rounded bg-white border-gray-300 text-purple-600 focus:ring-0"
                    />
                    <span>显示帕累托前沿 (Pareto)</span>
                  </label>
                  <div className="relative">
                    <Search className="w-3.5 h-3.5 text-gray-400 absolute left-2.5 top-1/2 -translate-y-1/2" />
                    <input
                      type="text"
                      placeholder="搜索模型..."
                      value={benchmarkSearch}
                      onChange={(e) => setBenchmarkSearch(e.target.value)}
                      className="bg-white border border-gray-200 rounded-lg pl-8 pr-3 py-1.5 text-xs text-gray-800 placeholder-gray-400 focus:outline-none focus:border-purple-500 shadow-xs"
                    />
                  </div>
                </div>
              </div>

              {/* Scatter Plot Simulation */}
              <div className="bg-white border border-gray-200 rounded-2xl p-6 shadow-xs">
                <div className="flex justify-between text-xs text-gray-500 mb-2">
                  <span>综合智能指数 vs. 加权平均输入单价 ($/100万 tokens)</span>
                  <span className="text-[11px] text-purple-600 font-medium">帕累托前沿线：最优性价比分布</span>
                </div>

                {/* Visual SVG plot */}
                <div className="relative h-60 w-full bg-gray-50/80 border border-gray-200 rounded-xl p-4 overflow-hidden">
                  <svg className="w-full h-full" viewBox="0 0 500 200">
                    {/* Grid lines */}
                    <line x1="40" y1="20" x2="480" y2="20" stroke="#e2e8f0" strokeDasharray="3 3" />
                    <line x1="40" y1="70" x2="480" y2="70" stroke="#e2e8f0" strokeDasharray="3 3" />
                    <line x1="40" y1="120" x2="480" y2="120" stroke="#e2e8f0" strokeDasharray="3 3" />
                    <line x1="40" y1="170" x2="480" y2="170" stroke="#cbd5e1" />

                    {/* Y Axis Labels */}
                    <text x="10" y="25" fill="#64748b" fontSize="10">54</text>
                    <text x="10" y="75" fill="#64748b" fontSize="10">48</text>
                    <text x="10" y="125" fill="#64748b" fontSize="10">42</text>
                    <text x="10" y="175" fill="#64748b" fontSize="10">36</text>

                    {/* X Axis Labels */}
                    <text x="50" y="192" fill="#64748b" fontSize="10">$0.80</text>
                    <text x="180" y="192" fill="#64748b" fontSize="10">$1.50</text>
                    <text x="320" y="192" fill="#64748b" fontSize="10">$2.30</text>
                    <text x="450" y="192" fill="#64748b" fontSize="10">$3.07</text>

                    {/* Pareto Curve line */}
                    {showPareto && (
                      <path
                        d="M 60 40 Q 180 30 350 25 T 470 20"
                        fill="none"
                        stroke="#9333ea"
                        strokeWidth="2"
                        strokeDasharray="4 4"
                      />
                    )}

                    {/* Scatter points */}
                    {filteredBenchmarks.map((pt, i) => {
                      // Normalize coordinates
                      const cx = 50 + ((pt.price - 0.8) / 2.3) * 400;
                      const cy = 170 - ((pt.score - 36) / 20) * 150;
                      return (
                        <g key={pt.id} className="cursor-pointer group">
                          <circle
                            cx={cx}
                            cy={cy}
                            r={pt.isPareto ? 6 : 4.5}
                            fill={pt.isPareto ? '#9333ea' : '#94a3b8'}
                            stroke="#ffffff"
                            strokeWidth="2"
                            className="transition-transform group-hover:scale-125"
                          />
                          <text
                            x={cx + 8}
                            y={cy + 3}
                            fill="#334155"
                            fontSize="9"
                            className="pointer-events-none opacity-80 group-hover:opacity-100 font-medium"
                          >
                            {pt.name.split(' ')[0]} ({pt.score})
                          </text>
                        </g>
                      );
                    })}
                  </svg>
                </div>

                {/* Ranked List below */}
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-2 mt-4">
                  {filteredBenchmarks.slice(0, 10).map((m, idx) => (
                    <div
                      key={m.id}
                      onClick={() => handleModelClick(m.name)}
                      className="flex items-center justify-between p-2.5 bg-gray-50/70 rounded-lg border border-gray-200 hover:bg-purple-50/30 cursor-pointer transition-colors"
                    >
                      <div className="flex items-center gap-2">
                        <span className="font-mono text-xs text-gray-400">{idx + 1}.</span>
                        <div>
                          <div className="text-xs font-semibold text-gray-900">{m.name}</div>
                          <div className="text-[10px] text-gray-500">来自 {m.author}</div>
                        </div>
                      </div>
                      <div className="text-right">
                        <div className="text-xs font-bold font-mono text-purple-600">{m.score}</div>
                        <div className="text-[10px] text-gray-400">${m.price}/M</div>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            </section>

            {/* SECTION 7: Fastest models */}
            <section id="fastest-models" className="scroll-mt-32">
              <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-5 border-b border-gray-200 pb-4">
                <div>
                  <h2 className="text-xl font-bold text-gray-900 flex items-center gap-2">
                    <Zap className="w-5 h-5 text-amber-500" />
                    最快推理速度
                  </h2>
                  <p className="text-xs text-gray-500 mt-0.5">
                    对比各模型及服务提供商在 uFreeTokens 上的实际响应与吞吐性能
                  </p>
                </div>
                <div className="relative">
                  <select className="appearance-none bg-white border border-gray-200 rounded-lg px-3 py-1.5 pr-8 text-xs text-gray-700 font-medium hover:border-gray-300 cursor-pointer focus:outline-none focus:border-purple-400 shadow-xs">
                    <option>最高吞吐量</option>
                    <option>最低延迟</option>
                    <option>性价比与速度比</option>
                  </select>
                  <ChevronDown className="w-3.5 h-3.5 text-gray-400 absolute right-2.5 top-1/2 -translate-y-1/2 pointer-events-none" />
                </div>
              </div>

              <div className="bg-white border border-gray-200 rounded-2xl overflow-hidden shadow-xs">
                <div className="divide-y divide-gray-100">
                  {FASTEST_MODELS.map((item) => (
                    <div
                      key={item.rank}
                      onClick={() => handleModelClick(item.name)}
                      className="p-4 flex items-center justify-between hover:bg-purple-50/30 transition-colors cursor-pointer"
                    >
                      <div className="flex items-center gap-4">
                        <span className="font-mono font-bold text-sm text-gray-400 w-5 text-center">
                          {item.rank}
                        </span>
                        <div>
                          <div className="text-sm font-semibold text-gray-900 hover:text-purple-700">
                            {item.name}
                          </div>
                          <div className="text-xs text-gray-400 mt-0.5">
                            最快渠道：<span className="text-purple-600 font-medium">{item.fastestOn}</span>
                          </div>
                        </div>
                      </div>
                      <div className="text-right">
                        <div className="text-sm font-mono font-bold text-amber-600">
                          {item.speed} tok/s
                        </div>
                        <div className="text-xs text-gray-400 font-mono">
                          ${item.price.toFixed(2)}/M
                        </div>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            </section>

            {/* SECTION 8: Languages */}
            <section id="languages-sec" className="scroll-mt-32">
              <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-5 border-b border-gray-200 pb-4">
                <div>
                  <h2 className="text-xl font-bold text-gray-900 flex items-center gap-2">
                    <Globe className="w-5 h-5 text-emerald-600" />
                    多语言表现
                  </h2>
                  <p className="text-xs text-gray-500 mt-0.5">
                    对比不同自然语言语种在 uFreeTokens 上的模型用量
                  </p>
                </div>
                <div className="flex items-center gap-2">
                  <div className="relative">
                    <select
                      value={languageMode}
                      onChange={(e) => setLanguageMode(e.target.value)}
                      className="appearance-none bg-white border border-gray-200 rounded-lg px-3 py-1.5 pr-8 text-xs text-gray-700 font-medium hover:border-gray-300 cursor-pointer focus:outline-none focus:border-purple-400 shadow-xs"
                    >
                      <option>英语</option>
                      <option>中文</option>
                      <option>西班牙语</option>
                      <option>日语</option>
                      <option>德语</option>
                    </select>
                    <ChevronDown className="w-3.5 h-3.5 text-gray-400 absolute right-2.5 top-1/2 -translate-y-1/2 pointer-events-none" />
                  </div>
                </div>
              </div>

              <div className="bg-white border border-gray-200 rounded-2xl p-5 shadow-xs">
                <div className="text-xs font-semibold text-gray-500 mb-3">
                  {languageMode} 领先模型排行榜（按每周 Token 量统计）
                </div>
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                  {[
                    { rank: 1, name: 'DeepSeek V4.1 Flash', tokens: '1.98T', share: '10.2%' },
                    { rank: 2, name: 'GLM 5.3 Flash', tokens: '1.88T', share: '9.7%' },
                    { rank: 3, name: 'Hy4 preview', tokens: '1.53T', share: '7.9%' },
                    { rank: 4, name: 'DeepSeek V4 Flash 0731', tokens: '1.33T', share: '6.9%' },
                    { rank: 5, name: 'GPT-5.6 Luna', tokens: '1.28T', share: '6.6%' },
                    { rank: 6, name: 'Union Alpha', tokens: '961B', share: '5.0%' },
                    { rank: 7, name: 'MiMo-V2.5', tokens: '948B', share: '4.9%' },
                    { rank: 8, name: 'GPT-4 Astra', tokens: '900B', share: '4.7%' },
                    { rank: 9, name: 'Hy3', tokens: '745B', share: '3.9%' },
                    { rank: 10, name: '其他模型', tokens: '7.77T', share: '40.2%' }
                  ].map((row) => (
                    <div key={row.rank} className="flex items-center justify-between p-2.5 bg-gray-50/70 rounded-lg border border-gray-200 hover:bg-purple-50/30 transition-colors">
                      <div className="flex items-center gap-2">
                        <span className="w-5 text-center font-mono text-xs text-gray-400">{row.rank}</span>
                        <span className="text-xs font-medium text-gray-900">{row.name}</span>
                      </div>
                      <div className="text-right">
                        <span className="font-mono text-xs font-bold text-gray-800">{row.tokens}</span>
                        <span className="text-[11px] text-gray-500 ml-1.5">({row.share})</span>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            </section>

            {/* SECTION 9: Programming */}
            <section id="programming-sec" className="scroll-mt-32">
              <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-5 border-b border-gray-200 pb-4">
                <div>
                  <h2 className="text-xl font-bold text-gray-900 flex items-center gap-2">
                    <Code2 className="w-5 h-5 text-pink-600" />
                    编程语言生态
                  </h2>
                  <p className="text-xs text-gray-500 mt-0.5">
                    对比各编程语言在 uFreeTokens 上的模型调用偏好
                  </p>
                </div>
                <div className="relative">
                  <select
                    value={progLangMode}
                    onChange={(e) => setProgLangMode(e.target.value)}
                    className="appearance-none bg-white border border-gray-200 rounded-lg px-3 py-1.5 pr-8 text-xs text-gray-700 font-medium hover:border-gray-300 cursor-pointer focus:outline-none focus:border-purple-400 shadow-xs"
                  >
                    <option>Python</option>
                    <option>TypeScript / JS</option>
                    <option>Rust</option>
                    <option>Go</option>
                    <option>C++</option>
                  </select>
                  <ChevronDown className="w-3.5 h-3.5 text-gray-400 absolute right-2.5 top-1/2 -translate-y-1/2 pointer-events-none" />
                </div>
              </div>

              <div className="bg-white border border-gray-200 rounded-2xl p-5 shadow-xs">
                <div className="text-xs font-semibold text-gray-500 mb-3">
                  {progLangMode} 编程场景领先模型（按处理 Token 统计）
                </div>
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                  {[
                    { rank: 1, name: 'GLM 5.3 Flash', tokens: '506B', share: '12.1%' },
                    { rank: 2, name: 'DeepSeek V4.1 Flash', tokens: '382B', share: '9.1%' },
                    { rank: 3, name: 'DeepSeek V4 Flash 0731', tokens: '307B', share: '7.3%' },
                    { rank: 4, name: 'Hy4 preview', tokens: '252B', share: '6.0%' },
                    { rank: 5, name: 'GPT-5.6 Luna', tokens: '245B', share: '5.8%' },
                    { rank: 6, name: 'Hy3', tokens: '178B', share: '4.2%' },
                    { rank: 7, name: 'MiMo-V2.5', tokens: '174B', share: '4.2%' },
                    { rank: 8, name: 'Union Alpha', tokens: '158B', share: '3.8%' },
                    { rank: 9, name: 'GPT-4 Astra', tokens: '152B', share: '3.6%' },
                    { rank: 10, name: '其他模型', tokens: '1.84T', share: '43.8%' }
                  ].map((row) => (
                    <div key={row.rank} className="flex items-center justify-between p-2.5 bg-gray-50/70 rounded-lg border border-gray-200 hover:bg-purple-50/30 transition-colors">
                      <div className="flex items-center gap-2">
                        <span className="w-5 text-center font-mono text-xs text-gray-400">{row.rank}</span>
                        <span className="text-xs font-medium text-gray-900">{row.name}</span>
                      </div>
                      <div className="text-right">
                        <span className="font-mono text-xs font-bold text-pink-600">{row.tokens}</span>
                        <span className="text-[11px] text-gray-500 ml-1.5">({row.share})</span>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            </section>

            {/* SECTION 10: Context Length */}
            <section id="context-length" className="scroll-mt-32">
              <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-5 border-b border-gray-200 pb-4">
                <div>
                  <h2 className="text-xl font-bold text-gray-900 flex items-center gap-2">
                    <Maximize2 className="w-5 h-5 text-cyan-600" />
                    上下文窗口长度
                  </h2>
                  <p className="text-xs text-gray-500 mt-0.5">
                    根据 Prompt 与补全长度区间统计 uFreeTokens 上的请求量
                  </p>
                </div>
                <div className="relative">
                  <select
                    value={contextBucket}
                    onChange={(e) => setContextBucket(e.target.value)}
                    className="appearance-none bg-white border border-gray-200 rounded-lg px-3 py-1.5 pr-8 text-xs text-gray-700 font-medium hover:border-gray-300 cursor-pointer focus:outline-none focus:border-purple-400 shadow-xs"
                  >
                    <option>1k - 10k tokens</option>
                    <option>10k - 50k tokens</option>
                    <option>50k - 200k tokens</option>
                    <option>200k+ tokens</option>
                  </select>
                  <ChevronDown className="w-3.5 h-3.5 text-gray-400 absolute right-2.5 top-1/2 -translate-y-1/2 pointer-events-none" />
                </div>
              </div>

              <div className="bg-white border border-gray-200 rounded-2xl p-5 shadow-xs">
                <div className="text-xs font-semibold text-gray-500 mb-3">
                  {contextBucket} 上下文区间请求量领先模型
                </div>
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                  {[
                    { rank: 1, name: 'DeepSeek V4 Flash 0731', requests: '171M', share: '11.2%' },
                    { rank: 2, name: 'DeepSeek V4 Flash 0423', requests: '160M', share: '10.5%' },
                    { rank: 3, name: 'Gemini 2.5 Flash Lite', requests: '75.9M', share: '5.0%' },
                    { rank: 4, name: 'GPT-5.6 Luna', requests: '75.7M', share: '5.0%' },
                    { rank: 5, name: 'GLM 5.3 Flash', requests: '75.6M', share: '5.0%' },
                    { rank: 6, name: 'gpt-oss-120b', requests: '65.9M', share: '4.3%' },
                    { rank: 7, name: 'Gemini 3.5 Flash Lite', requests: '45.1M', share: '3.0%' },
                    { rank: 8, name: 'Gemini 2.5 Flash', requests: '44.6M', share: '2.9%' },
                    { rank: 9, name: 'DeepSeek V4.1 Flash', requests: '43.3M', share: '2.8%' },
                    { rank: 10, name: '其他模型', requests: '766M', share: '50.3%' }
                  ].map((row) => (
                    <div key={row.rank} className="flex items-center justify-between p-2.5 bg-gray-50/70 rounded-lg border border-gray-200 hover:bg-purple-50/30 transition-colors">
                      <div className="flex items-center gap-2">
                        <span className="w-5 text-center font-mono text-xs text-gray-400">{row.rank}</span>
                        <span className="text-xs font-medium text-gray-900">{row.name}</span>
                      </div>
                      <div className="text-right">
                        <span className="font-mono text-xs font-bold text-cyan-600">{row.requests}</span>
                        <span className="text-[11px] text-gray-500 ml-1.5">({row.share})</span>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            </section>

            {/* SECTION 11: Tool Calls */}
            <section id="tool-calls" className="scroll-mt-32">
              <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-5 border-b border-gray-200 pb-4">
                <div>
                  <h2 className="text-xl font-bold text-gray-900 flex items-center gap-2">
                    <Wrench className="w-5 h-5 text-orange-600" />
                    工具调用 (Tool Calls)
                  </h2>
                  <p className="text-xs text-gray-500 mt-0.5">
                    各模型在 uFreeTokens 上的函数与工具调用次数
                  </p>
                </div>
              </div>

              <div className="bg-white border border-gray-200 rounded-2xl p-5 shadow-xs">
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                  {[
                    { rank: 1, name: 'GPT-5.6 Luna', calls: '90.1M', share: '12.0%' },
                    { rank: 2, name: 'GLM 5.3 Flash', calls: '83.7M', share: '11.2%' },
                    { rank: 3, name: 'DeepSeek V4.1 Flash', calls: '61.0M', share: '8.1%' },
                    { rank: 4, name: 'Hy4 preview', calls: '59.1M', share: '7.9%' },
                    { rank: 5, name: 'DeepSeek V4 Flash 0731', calls: '54.1M', share: '7.2%' },
                    { rank: 6, name: 'MiMo-V2.5', calls: '42.7M', share: '5.7%' },
                    { rank: 7, name: 'Hy3', calls: '34.1M', share: '4.6%' },
                    { rank: 8, name: 'DeepSeek V4 Flash 0423', calls: '24.6M', share: '3.3%' },
                    { rank: 9, name: 'GLM 5.3', calls: '15.6M', share: '2.1%' },
                    { rank: 10, name: '其他模型', calls: '283M', share: '37.8%' }
                  ].map((row) => (
                    <div key={row.rank} className="flex items-center justify-between p-2.5 bg-gray-50/70 rounded-lg border border-gray-200 hover:bg-purple-50/30 transition-colors">
                      <div className="flex items-center gap-2">
                        <span className="w-5 text-center font-mono text-xs text-gray-400">{row.rank}</span>
                        <span className="text-xs font-medium text-gray-900">{row.name}</span>
                      </div>
                      <div className="text-right">
                        <span className="font-mono text-xs font-bold text-orange-600">{row.calls}</span>
                        <span className="text-[11px] text-gray-500 ml-1.5">({row.share})</span>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            </section>

            {/* SECTION 12: Images */}
            <section id="images-sec" className="scroll-mt-32">
              <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-5 border-b border-gray-200 pb-4">
                <div>
                  <h2 className="text-xl font-bold text-gray-900 flex items-center gap-2">
                    <ImageIcon className="w-5 h-5 text-teal-600" />
                    多模态图像处理
                  </h2>
                  <p className="text-xs text-gray-500 mt-0.5">
                    uFreeTokens 平台上各视觉大模型处理的图像总量
                  </p>
                </div>
              </div>

              <div className="bg-white border border-gray-200 rounded-2xl p-5 shadow-xs">
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                  {[
                    { rank: 1, name: 'Gemini 2.5 Flash Lite', images: '137M', share: '18.6%' },
                    { rank: 2, name: 'GLM 5.3 Flash', images: '75.0M', share: '10.1%' },
                    { rank: 3, name: 'GPT-5.6 Luna', images: '52.0M', share: '7.0%' },
                    { rank: 4, name: 'DeepSeek V4.1 Flash', images: '44.0M', share: '6.0%' },
                    { rank: 5, name: 'MiMo-V2.5', images: '27.6M', share: '3.8%' },
                    { rank: 6, name: 'Gemini 3.1 Flash Lite', images: '22.6M', share: '3.1%' },
                    { rank: 7, name: 'Gemini 3.8 Flash', images: '22.5M', share: '3.1%' },
                    { rank: 8, name: 'Gemini 3.5 Flash', images: '22.0M', share: '3.0%' },
                    { rank: 9, name: 'Qwen3.7 Flash', images: '20.0M', share: '2.7%' },
                    { rank: 10, name: '其他模型', images: '307M', share: '42.1%' }
                  ].map((row) => (
                    <div key={row.rank} className="flex items-center justify-between p-2.5 bg-gray-50/70 rounded-lg border border-gray-200 hover:bg-purple-50/30 transition-colors">
                      <div className="flex items-center gap-2">
                        <span className="w-5 text-center font-mono text-xs text-gray-400">{row.rank}</span>
                        <span className="text-xs font-medium text-gray-900">{row.name}</span>
                      </div>
                      <div className="text-right">
                        <span className="font-mono text-xs font-bold text-teal-600">{row.images}</span>
                        <span className="text-[11px] text-gray-500 ml-1.5">({row.share})</span>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            </section>

            {/* SECTION 13: Top Apps */}
            <section id="top-apps" className="scroll-mt-32">
              <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-5 border-b border-gray-200 pb-4">
                <div>
                  <h2 className="text-xl font-bold text-gray-900 flex items-center gap-2">
                    <Smartphone className="w-5 h-5 text-purple-600" />
                    热门应用与客户端
                  </h2>
                  <p className="text-xs text-gray-500 mt-0.5">
                    已授权追踪调用量的主流 AI 应用与智能体工具排行
                  </p>
                </div>
                <div className="flex items-center gap-2">
                  <div className="relative">
                    <select className="appearance-none bg-white border border-gray-200 rounded-lg px-3 py-1.5 pr-8 text-xs text-gray-700 font-medium hover:border-gray-300 cursor-pointer focus:outline-none focus:border-purple-400 shadow-xs">
                      <option>今日</option>
                      <option>本周</option>
                      <option>本月</option>
                    </select>
                    <ChevronDown className="w-3.5 h-3.5 text-gray-400 absolute right-2.5 top-1/2 -translate-y-1/2 pointer-events-none" />
                  </div>
                  <button className="px-3 py-1.5 rounded-lg text-xs font-medium bg-purple-600 hover:bg-purple-700 text-white transition-colors">
                    浏览全部应用
                  </button>
                </div>
              </div>

              <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                {TOP_APPS.map((app) => (
                  <div
                    key={app.rank}
                    className="flex items-center justify-between p-3.5 bg-white border border-gray-200 rounded-xl hover:bg-purple-50/30 transition-colors shadow-xs"
                  >
                    <div className="flex items-center gap-3">
                      <span className="font-mono font-bold text-sm text-gray-400 w-5 text-center">
                        {app.rank}
                      </span>
                      <div>
                        <div className="text-sm font-semibold text-gray-900">{app.name}</div>
                        <div className="text-xs text-gray-500">{app.category}</div>
                      </div>
                    </div>
                    <div className="text-right">
                      <span className="text-xs font-mono font-bold text-purple-600">{app.tokens}</span>
                    </div>
                  </div>
                ))}
              </div>
            </section>

            {/* SECTION 14: How these rankings are measured */}
            <section id="measurement" className="scroll-mt-32">
              <div className="mb-5 border-b border-gray-200 pb-4">
                <h2 className="text-xl font-bold text-gray-900 flex items-center gap-2">
                  <HelpCircle className="w-5 h-5 text-gray-400" />
                  榜单统计方法与规则
                </h2>
              </div>

              <div className="bg-white border border-gray-200 rounded-2xl p-6 space-y-6 text-xs leading-relaxed text-gray-700 shadow-xs">
                <div>
                  <h3 className="text-sm font-bold text-gray-900 mb-1.5 flex items-center gap-2">
                    <CheckCircle2 className="w-4 h-4 text-purple-600" />
                    统计指标与范围
                  </h3>
                  <p className="text-gray-500">
                    排行榜数据直接基于全球开发者及终端应用通过 uFreeTokens API 网关实际处理的 Token 量、请求次数及支出费用生成。所有统计均已剔除开发测试、预发沙盒及内部健康度探测端点数据。
                  </p>
                </div>

                <div>
                  <h3 className="text-sm font-bold text-gray-900 mb-1.5 flex items-center gap-2">
                    <CheckCircle2 className="w-4 h-4 text-emerald-600" />
                    采样周期与更新频率
                  </h3>
                  <p className="text-gray-500">
                    所有排行榜与用量趋势图表每 60 分钟自动重新计算一次，采用滚动 7 天和 30 天聚合窗口。历史图表数据点均通过计费系统账单明细交叉校验。
                  </p>
                </div>

                <div>
                  <h3 className="text-sm font-bold text-gray-900 mb-1.5 flex items-center gap-2">
                    <Info className="w-4 h-4 text-amber-500" />
                    榜单客观性声明
                  </h3>
                  <p className="text-gray-500">
                    实际调用热度与流行度并不等同于特定合成基准测试的跑分能力。选型时建议综合考量模型的性价比、上下文窗口、首字延迟保证以及在特定细分领域的适配表现。
                  </p>
                </div>
              </div>
            </section>
          </main>
        </div>
      </div>

      {/* Footer */}
      <footer className="mt-24 border-t border-gray-200 bg-gray-50 py-12 px-4 md:px-8 text-xs text-gray-500">
        <div className="max-w-7xl mx-auto flex flex-col md:flex-row items-center justify-between gap-6">
          <div className="flex items-center gap-2">
            <span className="font-bold text-gray-900 text-sm">uFreeTokens 排行榜</span>
            <span className="text-gray-300">|</span>
            <span>真实开发者调用遥测数据</span>
          </div>
          <div className="flex flex-wrap items-center gap-6">
            <button onClick={onNavigateToModels} className="hover:text-gray-900">模型列表</button>
            <button onClick={onNavigateToBenchmarks} className="hover:text-gray-900">基准测试</button>
            <a href="#top-models" className="hover:text-gray-900">热门模型</a>
            <a href="#cost-session" className="hover:text-gray-900">成本矩阵</a>
            <span className="text-gray-400">© 2026 uFreeTokens. 保留所有权利.</span>
          </div>
        </div>
      </footer>
    </div>
  );
};
