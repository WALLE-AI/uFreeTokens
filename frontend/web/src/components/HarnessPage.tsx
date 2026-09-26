import React, { useState, useEffect } from 'react';
import {
  Terminal,
  Hash,
  Monitor,
  Plug,
  Code2,
  Sliders,
  GraduationCap,
  Mail,
  Copy,
  Check,
  ArrowRight,
  Sparkles,
  ExternalLink,
  ChevronRight,
  ShieldCheck,
  CheckCircle2,
  Cpu,
  Bot,
  Zap,
  Play,
  RotateCcw,
  Download,
  Share2,
  Layers,
  ArrowUpRight
} from 'lucide-react';

interface HarnessPageProps {
  onNavigateToModels: () => void;
  onNavigateToBenchmarks: () => void;
}

type HarnessTab = 'claude' | 'codex' | 'hermes' | 'opencode' | 'pi' | 'grok' | 'prime-agent';

interface HarnessInfo {
  id: HarnessTab;
  name: string;
  prefix: string;
  command: string;
  agentName: string;
  defaultModel: string;
  authMessage: string;
  description: string;
}

const HARNESS_TABS: HarnessInfo[] = [
  {
    id: 'claude',
    name: 'claude',
    prefix: '✳',
    command: 'ori claude',
    agentName: '欢迎使用 Claude Code',
    defaultModel: 'anthropic/claude-opus-latest',
    authMessage: '认证状态: uFreeTokens · 已启用组织安全规则',
    description: '在 Anthropic Claude Code 智能体中无缝调用 uFreeTokens 汇聚的模型，并支持自定义 Token 配额。'
  },
  {
    id: 'codex',
    name: 'codex',
    prefix: '@',
    command: 'ori codex',
    agentName: '欢迎使用 OpenAI Codex CLI',
    defaultModel: 'openai/gpt-4.5-turbo',
    authMessage: '认证状态: uFreeTokens · 已验证零数据保留 (ZDR)',
    description: '运行 Codex 代码执行循环，支持跨服务提供商端点的全自动容灾回退。'
  },
  {
    id: 'hermes',
    name: 'hermes',
    prefix: '⚚',
    command: 'ori hermes',
    agentName: '欢迎使用 Hermes Agent',
    defaultModel: 'nousresearch/hermes-3-llama-3.1-405b',
    authMessage: '认证状态: uFreeTokens · 函数调用已深度优化',
    description: 'Nous Research 智能体，具备深度逻辑推理、函数调用与结构化 JSON 规范输出能力。'
  },
  {
    id: 'opencode',
    name: 'opencode',
    prefix: '⌗',
    command: 'ori opencode',
    agentName: '欢迎使用 OpenCode 代码解释器',
    defaultModel: 'deepseek/deepseek-coder-v2',
    authMessage: '认证状态: uFreeTokens · 本地沙箱隔离执行',
    description: '开源全自动代码解释器，支持完整的多文件编辑、测试验证与 Git 提交。'
  },
  {
    id: 'pi',
    name: 'pi',
    prefix: 'π',
    command: 'ori pi',
    agentName: '欢迎使用 Inflection Pi CLI',
    defaultModel: 'inflection/pi-3-agent',
    authMessage: '认证状态: uFreeTokens · 高情商与对话记忆调优',
    description: '个人陪伴型助理智能体，支持共情推理、长期对话记忆与跨工作区数据同步。'
  },
  {
    id: 'grok',
    name: 'grok',
    prefix: '⚡',
    command: 'ori grok',
    agentName: '欢迎使用 Grok 终端开发套件',
    defaultModel: 'x-ai/grok-2-1212',
    authMessage: '认证状态: uFreeTokens · 实时网络检索增强已启用',
    description: 'xAI Grok 终端深度集成，支持实时联网检索检索基准、代码生成与文件变更暂存。'
  },
  {
    id: 'prime-agent',
    name: 'prime-agent',
    prefix: '✦',
    command: 'ori prime-agent',
    agentName: '欢迎使用 Prime Agent 多智能体架构',
    defaultModel: 'meta-llama/llama-3.3-70b-instruct',
    authMessage: '认证状态: uFreeTokens · 自主子智能体集群就绪',
    description: '多智能体协同编排 CLI，支持复杂任务自顶向下拆解、并行执行与共识校验。'
  }
];

export const HarnessPage: React.FC<HarnessPageProps> = ({
  onNavigateToModels,
  onNavigateToBenchmarks
}) => {
  // Waitlist state
  const [targetEnvironments, setTargetEnvironments] = useState<string[]>(['Terminal']);
  const [waitlistEmail, setWaitlistEmail] = useState('');
  const [isWaitlistSubmitted, setIsWaitlistSubmitted] = useState(false);

  // Active section for sidebar tracking
  const [activeSidebarItem, setActiveSidebarItem] = useState<string>('harness');

  // Harness tab selection
  const [selectedHarness, setSelectedHarness] = useState<HarnessTab>('claude');

  // Copy command states
  const [copiedHarnessCmd, setCopiedHarnessCmd] = useState(false);
  const [copiedCodeCmd, setCopiedCodeCmd] = useState(false);
  const [copiedEvalCmd, setCopiedEvalCmd] = useState(false);

  // Interactive Ori Code terminal simulator state
  const [simModel, setSimModel] = useState('gemini-3-pro');
  const [customPrompt, setCustomPrompt] = useState('');
  const [terminalHistory, setTerminalHistory] = useState<Array<{ type: 'input' | 'output' | 'system'; text: string }>>([
    { type: 'input', text: '修复 auth.spec.ts 中偶发失败的重试测试用例' },
    { type: 'output', text: '✓ 已修补 auth.spec.ts · 所有测试已通过 (tests green)' },
    { type: 'input', text: '/model gemini-3-pro' },
    { type: 'output', text: '✓ 任务中途热切换至 gemini-3-pro，完整保留 128k 上下文' }
  ]);

  const toggleTargetEnv = (env: string) => {
    if (targetEnvironments.includes(env)) {
      if (targetEnvironments.length > 1) {
        setTargetEnvironments(targetEnvironments.filter((e) => e !== env));
      }
    } else {
      setTargetEnvironments([...targetEnvironments, env]);
    }
  };

  const handleWaitlistSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!waitlistEmail || !waitlistEmail.includes('@')) return;
    setIsWaitlistSubmitted(true);
  };

  const handleCopy = (text: string, type: 'harness' | 'code' | 'eval') => {
    navigator.clipboard.writeText(text);
    if (type === 'harness') {
      setCopiedHarnessCmd(true);
      setTimeout(() => setCopiedHarnessCmd(false), 2000);
    } else if (type === 'code') {
      setCopiedCodeCmd(true);
      setTimeout(() => setCopiedCodeCmd(false), 2000);
    } else {
      setCopiedEvalCmd(true);
      setTimeout(() => setCopiedEvalCmd(false), 2000);
    }
  };

  const handleRunTerminalSim = (e: React.FormEvent) => {
    e.preventDefault();
    if (!customPrompt.trim()) return;

    const trimmed = customPrompt.trim();
    if (trimmed.startsWith('/model ')) {
      const newM = trimmed.replace('/model ', '').trim();
      setSimModel(newM);
      setTerminalHistory((prev) => [
        ...prev,
        { type: 'input', text: trimmed },
        { type: 'output', text: `✓ switched mid-task to ${newM}, context intact (128k ctx retained)` }
      ]);
    } else {
      setTerminalHistory((prev) => [
        ...prev,
        { type: 'input', text: trimmed },
        { type: 'output', text: `✓ completed via ${simModel} (2.3s · 412 tokens)` }
      ]);
    }
    setCustomPrompt('');
  };

  // ScrollSpy listener to update sidebar active item
  useEffect(() => {
    const handleScroll = () => {
      const sections = ['harness', 'code', 'eval', 'intern', 'desktop', 'waitlist'];
      const scrollPosition = window.scrollY + 200;

      for (const section of sections) {
        const el = document.getElementById(section);
        if (el) {
          const top = el.offsetTop;
          const height = el.offsetHeight;
          if (scrollPosition >= top && scrollPosition < top + height) {
            setActiveSidebarItem(section);
            break;
          }
        }
      }
    };

    window.addEventListener('scroll', handleScroll);
    return () => window.removeEventListener('scroll', handleScroll);
  }, []);

  const scrollToSection = (id: string) => {
    setActiveSidebarItem(id);
    const element = document.getElementById(id);
    if (element) {
      element.scrollIntoView({ behavior: 'smooth', block: 'start' });
    }
  };

  const currentHarness = HARNESS_TABS.find((h) => h.id === selectedHarness) || HARNESS_TABS[0];

  return (
    <div className="min-h-screen bg-white text-gray-900 selection:bg-purple-100 selection:text-purple-900 relative">
      {/* Background ethereal fluid texture emulation */}
      <div className="fixed inset-0 pointer-events-none overflow-hidden z-0 opacity-40">
        <div className="absolute -top-40 left-1/2 -translate-x-1/2 w-[1000px] h-[700px] bg-gradient-to-b from-purple-100/70 via-indigo-50/50 to-transparent rounded-full blur-3xl" />
        <div className="absolute top-[600px] -left-40 w-[600px] h-[600px] bg-purple-50/60 rounded-full blur-3xl" />
        <div className="absolute top-[1200px] -right-40 w-[700px] h-[700px] bg-indigo-50/60 rounded-full blur-3xl" />
      </div>

      <div className="relative z-10 max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-10">
        {/* ========================================================================= */}
        {/* HERO SECTION: Sphere / Halo + Big Typography + Waitlist Card             */}
        {/* ========================================================================= */}
        <div className="relative pt-12 pb-24 text-center">
          {/* Central Halo / Sphere Particle Background Graphic */}
          <div className="absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 w-[480px] sm:w-[620px] h-[480px] sm:h-[620px] pointer-events-none select-none opacity-80">
            <svg viewBox="0 0 400 400" className="w-full h-full animate-[spin_120s_linear_infinite]">
              {/* Concentric subtle wireframe orbits */}
              <circle cx="200" cy="200" r="190" fill="none" stroke="#e2e8f0" strokeWidth="1" strokeDasharray="3 6" opacity="0.6" />
              <circle cx="200" cy="200" r="160" fill="none" stroke="#cbd5e1" strokeWidth="1" strokeDasharray="2 4" opacity="0.4" />
              <circle cx="200" cy="200" r="130" fill="none" stroke="#cbd5e1" strokeWidth="1" opacity="0.3" />
              <circle cx="200" cy="200" r="90" fill="none" stroke="#e2e8f0" strokeWidth="1" strokeDasharray="4 8" opacity="0.4" />

              {/* Scattered geometric particles (+, ◇, ◆, ·) */}
              <text x="195" y="15" fill="#9333ea" fontSize="14" opacity="0.7">✦</text>
              <text x="260" y="45" fill="#a855f7" fontSize="10" opacity="0.6">◇</text>
              <text x="340" y="90" fill="#64748b" fontSize="12" opacity="0.5">+</text>
              <text x="380" y="180" fill="#1e293b" fontSize="10" opacity="0.7">◆</text>
              <text x="365" y="240" fill="#9333ea" fontSize="12" opacity="0.6">✦</text>
              <text x="320" y="320" fill="#a855f7" fontSize="10" opacity="0.6">◇</text>
              <text x="220" y="385" fill="#1e293b" fontSize="10" opacity="0.5">◆</text>
              <text x="130" y="370" fill="#64748b" fontSize="12" opacity="0.5">+</text>
              <text x="50" y="310" fill="#9333ea" fontSize="12" opacity="0.7">✦</text>
              <text x="20" y="210" fill="#64748b" fontSize="10" opacity="0.5">◇</text>
              <text x="55" y="110" fill="#a855f7" fontSize="12" opacity="0.6">+</text>
              <text x="120" y="45" fill="#1e293b" fontSize="10" opacity="0.5">◆</text>

              {/* Inner cluster */}
              <text x="220" y="90" fill="#9333ea" fontSize="8" opacity="0.8">◆</text>
              <text x="290" y="150" fill="#64748b" fontSize="10" opacity="0.5">+</text>
              <text x="270" y="270" fill="#a855f7" fontSize="8" opacity="0.7">◇</text>
              <text x="130" y="270" fill="#9333ea" fontSize="10" opacity="0.6">✦</text>
              <text x="110" y="160" fill="#64748b" fontSize="8" opacity="0.5">+</text>
            </svg>
          </div>

          {/* Eyebrow badge */}
          <div className="inline-flex items-center gap-1.5 px-3 py-1 rounded-full bg-purple-50/80 border border-purple-200/80 text-purple-700 text-xs font-semibold tracking-wider uppercase mb-5 shadow-xs">
            <Sparkles className="w-3.5 h-3.5 text-purple-600" />
            <span>认识 ORI 智能体套件</span>
          </div>

          {/* Big Headline */}
          <h1 className="text-4xl sm:text-5xl md:text-6xl font-black tracking-tight text-gray-950 leading-[1.15] max-w-3xl mx-auto">
            在您日常开发的每一处， <br />
            <span className="bg-gradient-to-r from-purple-600 via-indigo-600 to-purple-800 bg-clip-text text-transparent">
              尽情使用任意顶尖模型。
            </span>
          </h1>

          {/* Subtitle */}
          <p className="mt-5 text-sm sm:text-base text-gray-600 max-w-xl mx-auto leading-relaxed">
            uFreeTokens 开发者原生工具矩阵：让任意模型无缝接入您的终端命令行、Slack 协作群组以及桌面工作台。
          </p>

          {/* Interactive "Where do you want to use Ori?" Card */}
          <div className="mt-8 max-w-md mx-auto bg-white/95 backdrop-blur-md border border-gray-200 rounded-2xl shadow-xl shadow-purple-500/5 p-5 text-left relative">
            <div className="font-semibold text-gray-900 text-sm text-center mb-3">
              您希望在哪里使用 Ori？
            </div>

            {/* Selection buttons */}
            <div className="grid grid-cols-3 gap-2 mb-4">
              {[
                { id: 'Terminal', icon: Terminal, label: '终端 (Terminal)' },
                { id: 'Slack', icon: Hash, label: 'Slack' },
                { id: 'Desktop', icon: Monitor, label: '桌面端' }
              ].map((item) => {
                const Icon = item.icon;
                const isSelected = targetEnvironments.includes(item.id);
                return (
                  <button
                    key={item.id}
                    type="button"
                    onClick={() => toggleTargetEnv(item.id)}
                    className={`flex items-center justify-center gap-1.5 py-2 px-3 rounded-xl text-xs font-medium border transition-all cursor-pointer ${
                      isSelected
                        ? 'bg-purple-50 border-purple-300 text-purple-700 shadow-xs'
                        : 'bg-gray-50/80 border-gray-200 text-gray-600 hover:bg-gray-100 hover:text-gray-900'
                    }`}
                  >
                    <Icon className="w-3.5 h-3.5" />
                    <span>{item.label}</span>
                  </button>
                );
              })}
            </div>

            {/* Email form */}
            {!isWaitlistSubmitted ? (
              <form onSubmit={handleWaitlistSubmit} className="flex flex-col sm:flex-row gap-2">
                <div className="relative flex-1">
                  <input
                    type="email"
                    required
                    placeholder="输入您的电子邮箱地址"
                    value={waitlistEmail}
                    onChange={(e) => setWaitlistEmail(e.target.value)}
                    className="w-full bg-gray-50 border border-gray-200 rounded-xl px-3.5 py-2 text-xs text-gray-900 placeholder-gray-400 focus:outline-none focus:border-purple-400 focus:bg-white transition-colors"
                  />
                </div>
                <button
                  type="submit"
                  className="bg-purple-600 hover:bg-purple-700 text-white font-medium text-xs rounded-xl px-4 py-2 flex items-center justify-center gap-1.5 shadow-sm shadow-purple-500/20 transition-colors whitespace-nowrap cursor-pointer"
                >
                  <span>加入候补名单</span>
                  <ArrowRight className="w-3.5 h-3.5" />
                </button>
              </form>
            ) : (
              <div className="p-3 bg-purple-50 border border-purple-200 rounded-xl flex items-center gap-2.5 text-xs text-purple-900 font-medium">
                <CheckCircle2 className="w-4 h-4 text-purple-600 shrink-0" />
                <div>
                  <span>您已加入候补名单（关注环境：</span>
                  <span className="font-bold">{targetEnvironments.join(', ')}</span>）！我们将第一时间向您发送开放通知。
                </div>
              </div>
            )}
          </div>
        </div>

        {/* ========================================================================= */}
        {/* MAIN BODY: Sticky Left Sidebar + Content Sections                        */}
        {/* ========================================================================= */}
        <div className="flex flex-col lg:flex-row gap-12 items-start relative">
          {/* Left Sticky Sidebar */}
          <aside className="w-full lg:w-48 shrink-0 lg:sticky lg:top-20 z-20">
            <nav className="flex lg:flex-col gap-1 p-1 bg-gray-50/80 border border-gray-200 rounded-xl backdrop-blur-xs overflow-x-auto lg:overflow-visible">
              {[
                { id: 'harness', label: 'Harness 套件', icon: Plug },
                { id: 'code', label: 'Code 智能体', icon: Code2 },
                { id: 'eval', label: 'Eval 基准评测', icon: Sliders },
                { id: 'intern', label: 'Intern 协作助手', icon: GraduationCap },
                { id: 'desktop', label: 'Desktop 桌面端', icon: Monitor },
                { id: 'waitlist', label: '加入候补', icon: Mail }
              ].map((item) => {
                const Icon = item.icon;
                const isActive = activeSidebarItem === item.id;
                return (
                  <button
                    key={item.id}
                    onClick={() => scrollToSection(item.id)}
                    className={`flex items-center gap-2.5 px-3 py-2 rounded-lg text-xs font-medium transition-all text-left whitespace-nowrap cursor-pointer ${
                      isActive
                        ? 'bg-purple-100 text-purple-700 font-semibold shadow-xs'
                        : 'text-gray-600 hover:text-gray-900 hover:bg-gray-100/70'
                    }`}
                  >
                    <Icon className={`w-3.5 h-3.5 ${isActive ? 'text-purple-600' : 'text-gray-400'}`} />
                    <span>{item.label}</span>
                  </button>
                );
              })}
            </nav>
          </aside>

          {/* Right Content Stream */}
          <main className="flex-1 min-w-0 space-y-28 pb-20">
            {/* ===================================================================== */}
            {/* SECTION 1: ORI <HARNESS> · CLI                                        */}
            {/* ===================================================================== */}
            <section id="harness" className="scroll-mt-24">
              {/* Eyebrow Tag */}
              <div className="text-xs font-mono font-bold text-purple-600 tracking-wider uppercase mb-2">
                ORI &lt;HARNESS&gt; · 命令行工具
              </div>

              {/* Title */}
              <h2 className="text-2xl sm:text-3xl font-extrabold text-gray-950 tracking-tight mb-3">
                在您最钟爱的智能体套件中使用任意模型
              </h2>

              {/* Description */}
              <p className="text-sm text-gray-600 leading-relaxed max-w-3xl mb-6">
                <code className="bg-gray-100 text-purple-700 font-mono text-xs px-1.5 py-0.5 rounded">ori claude</code>
                , <code className="bg-gray-100 text-purple-700 font-mono text-xs px-1.5 py-0.5 rounded">ori codex</code>
                , <code className="bg-gray-100 text-purple-700 font-mono text-xs px-1.5 py-0.5 rounded">ori hermes</code>
                等：通过 <code className="bg-gray-100 text-purple-700 font-mono text-xs px-1.5 py-0.5 rounded">ori</code> CLI 命令行启动您的编码智能体，随时接入 uFreeTokens 提供的全部前沿模型。无需复杂鉴权配置，完全兼容并遵循企业组织的安全策略与预算护栏。
              </p>

              {/* Action bar: Curl command + Explore button */}
              <div className="flex flex-col sm:flex-row items-stretch sm:items-center gap-3 mb-6">
                <div className="flex-1 bg-gray-50 border border-gray-200 rounded-xl px-3.5 py-2 flex items-center justify-between font-mono text-xs text-gray-800">
                  <div className="flex items-center gap-2 truncate">
                    <span className="text-gray-400 select-none">$</span>
                    <span className="truncate">curl -fsSL https://ufreetokens.com/labs/ori/install.sh | bash</span>
                  </div>
                  <button
                    onClick={() => handleCopy('curl -fsSL https://ufreetokens.com/labs/ori/install.sh | bash', 'harness')}
                    className="p-1 text-gray-400 hover:text-gray-800 transition-colors ml-2 cursor-pointer"
                    title="复制安装命令"
                  >
                    {copiedHarnessCmd ? <Check className="w-4 h-4 text-emerald-600" /> : <Copy className="w-4 h-4" />}
                  </button>
                </div>

                <button
                  onClick={() => scrollToSection('code')}
                  className="bg-white border border-gray-200 hover:bg-gray-50 text-gray-800 font-medium text-xs px-4 py-2 rounded-xl flex items-center justify-center gap-1.5 shadow-xs transition-colors cursor-pointer"
                >
                  <span>探索 Ori Harness 套件</span>
                  <ArrowRight className="w-3.5 h-3.5" />
                </button>
              </div>

              {/* Harness tabs */}
              <div className="flex flex-wrap items-center gap-2 mb-4">
                {HARNESS_TABS.map((tab) => {
                  const isSelected = selectedHarness === tab.id;
                  return (
                    <button
                      key={tab.id}
                      onClick={() => setSelectedHarness(tab.id)}
                      className={`px-3 py-1 rounded-lg text-xs font-mono font-medium transition-all flex items-center gap-1 cursor-pointer ${
                        isSelected
                          ? 'bg-purple-100 text-purple-700 border border-purple-300 shadow-xs'
                          : 'bg-white border border-gray-200 text-gray-600 hover:bg-gray-50 hover:text-gray-900'
                      }`}
                    >
                      <span className="text-purple-600">{tab.prefix}</span>
                      <span>{tab.name}</span>
                    </button>
                  );
                })}
              </div>

              {/* Terminal window mockup for selected Harness */}
              <div className="bg-white border border-gray-200 rounded-xl shadow-xs overflow-hidden font-mono text-xs">
                {/* Window top bar */}
                <div className="px-4 py-2.5 bg-gray-50/90 border-b border-gray-200 flex items-center justify-between select-none">
                  <div className="flex items-center gap-2">
                    <span className="w-2.5 h-2.5 rounded-full bg-gray-300 inline-block" />
                    <span className="w-2.5 h-2.5 rounded-full bg-gray-300 inline-block" />
                    <span className="w-2.5 h-2.5 rounded-full bg-gray-300 inline-block" />
                    <span className="text-[11px] text-gray-500 font-medium ml-2">
                      ori {currentHarness.name} · zsh
                    </span>
                  </div>
                  <span className="text-[10px] text-purple-600 font-medium uppercase">
                    ufreetokens harness v0.9.4
                  </span>
                </div>

                {/* Window body */}
                <div className="p-6 space-y-4 bg-white min-h-[220px]">
                  <div className="text-gray-900 font-semibold flex items-center gap-2">
                    <span className="text-purple-600">$</span>
                    <span>{currentHarness.command}</span>
                  </div>

                  {/* ASCII Welcome Box */}
                  <div className="border border-gray-200 rounded-lg p-4 bg-gray-50/50 max-w-md space-y-1.5 text-[11px]">
                    <div className="font-semibold text-gray-900 flex items-center gap-1.5">
                      <span className="text-purple-600 font-bold">{currentHarness.prefix}</span>
                      <span>{currentHarness.agentName}</span>
                    </div>
                    <div className="text-gray-600 flex items-center justify-between gap-4">
                      <span>模型 (model):</span>
                      <span className="font-semibold text-purple-700">{currentHarness.defaultModel}</span>
                    </div>
                    <div className="text-gray-500 text-[10px] pt-1 border-t border-gray-200/80">
                      {currentHarness.authMessage}
                    </div>
                  </div>

                  {/* Terminal Prompt with Blinking Cursor */}
                  <div className="flex items-center gap-1.5 text-gray-800 pt-2">
                    <span className="text-purple-600 font-bold">&gt;</span>
                    <span className="w-2 h-4 bg-purple-600 animate-pulse" />
                  </div>
                </div>

                {/* Footer caption */}
                <div className="px-4 py-2 bg-gray-50 border-t border-gray-200 text-[11px] text-gray-500 flex justify-between items-center">
                  <span>{currentHarness.description}</span>
                  <span className="text-purple-600 font-medium">就绪 (Ready)</span>
                </div>
              </div>
            </section>

            {/* ===================================================================== */}
            {/* SECTION 2: ORI CODE · CLI                                             */}
            {/* ===================================================================== */}
            <section id="code" className="scroll-mt-24">
              {/* Eyebrow Tag */}
              <div className="text-xs font-mono font-bold text-purple-600 tracking-wider uppercase mb-2">
                ORI CODE · 原生终端编码智能体
              </div>

              {/* Title */}
              <h2 className="text-2xl sm:text-3xl font-extrabold text-gray-950 tracking-tight mb-3">
                uFreeTokens 原生全自动终端编码助手
              </h2>

              {/* Description */}
              <p className="text-sm text-gray-600 leading-relaxed max-w-3xl mb-6">
                Ori Code 专为在终端优雅调度多模型而深度定制。提供原生沉浸式的终端编程体验，支持随时无损热切换模型与上下文延续。
              </p>

              {/* Action bar */}
              <div className="flex flex-col sm:flex-row items-stretch sm:items-center gap-3 mb-6">
                <div className="flex-1 bg-gray-50 border border-gray-200 rounded-xl px-3.5 py-2 flex items-center justify-between font-mono text-xs text-gray-800">
                  <div className="flex items-center gap-2 truncate">
                    <span className="text-gray-400 select-none">$</span>
                    <span className="truncate">curl -fsSL https://ufreetokens.com/labs/ori/install.sh | bash</span>
                  </div>
                  <button
                    onClick={() => handleCopy('curl -fsSL https://ufreetokens.com/labs/ori/install.sh | bash', 'code')}
                    className="p-1 text-gray-400 hover:text-gray-800 transition-colors ml-2 cursor-pointer"
                    title="复制安装命令"
                  >
                    {copiedCodeCmd ? <Check className="w-4 h-4 text-emerald-600" /> : <Copy className="w-4 h-4" />}
                  </button>
                </div>
              </div>

              {/* Terminal window mockup */}
              <div className="bg-white border border-gray-200 rounded-xl shadow-xs overflow-hidden font-mono text-xs">
                {/* Window top bar */}
                <div className="px-4 py-2.5 bg-gray-50/90 border-b border-gray-200 flex items-center justify-between select-none">
                  <div className="flex items-center gap-2">
                    <span className="w-2.5 h-2.5 rounded-full bg-gray-300 inline-block" />
                    <span className="w-2.5 h-2.5 rounded-full bg-gray-300 inline-block" />
                    <span className="w-2.5 h-2.5 rounded-full bg-gray-300 inline-block" />
                    <span className="text-[11px] text-gray-500 font-medium ml-2">
                      ori · zsh
                    </span>
                  </div>
                  <span className="text-[10px] text-gray-400 font-medium">
                    原生终端交互引擎
                  </span>
                </div>

                {/* Window body with dynamic interactive execution */}
                <div className="p-6 space-y-3 bg-white">
                  <div className="text-gray-900 font-semibold flex items-center gap-2">
                    <span className="text-purple-600">$</span>
                    <span>ori</span>
                  </div>

                  {/* ASCII Welcome Box */}
                  <div className="border border-gray-200 rounded-lg p-4 bg-gray-50/50 max-w-md space-y-1 text-[11px]">
                    <div className="font-semibold text-gray-900 flex items-center gap-1.5">
                      <span className="text-purple-600">🖵</span>
                      <span>欢迎使用 Ori 原生助手</span>
                    </div>
                    <div className="text-gray-600 flex items-center justify-between">
                      <span>模型 (model):</span>
                      <span className="font-semibold text-purple-700">ufreetokens/auto</span>
                    </div>
                    <div className="text-gray-500 text-[10px] pt-1 border-t border-gray-200/80">
                      认证: uFreeTokens · 无需繁琐配置 API Key
                    </div>
                  </div>

                  {/* Message exchange stream */}
                  <div className="space-y-2 pt-2 text-xs">
                    {terminalHistory.map((item, idx) => (
                      <div key={idx} className="flex items-start gap-2">
                        {item.type === 'input' ? (
                          <>
                            <span className="text-purple-600 font-bold">&gt;</span>
                            <span className="text-gray-900 font-medium">{item.text}</span>
                          </>
                        ) : (
                          <>
                            <span className="text-emerald-600 font-bold">{item.text}</span>
                          </>
                        )}
                      </div>
                    ))}
                  </div>

                  {/* Interactive prompt input for testing */}
                  <form onSubmit={handleRunTerminalSim} className="flex items-center gap-2 pt-2 border-t border-gray-100">
                    <span className="text-purple-600 font-bold">&gt;</span>
                    <input
                      type="text"
                      placeholder="输入提示词或命令，例如 '/model claude-3-7-sonnet' 进行测试..."
                      value={customPrompt}
                      onChange={(e) => setCustomPrompt(e.target.value)}
                      className="flex-1 bg-transparent text-gray-900 placeholder-gray-400 text-xs focus:outline-none"
                    />
                    <button
                      type="submit"
                      className="px-2.5 py-1 bg-purple-50 text-purple-700 hover:bg-purple-100 rounded text-[11px] font-medium border border-purple-200 transition-colors cursor-pointer"
                    >
                      运行
                    </button>
                  </form>

                  {/* Terminal status bar footer */}
                  <div className="flex items-center justify-between text-[11px] text-gray-500 pt-3 border-t border-gray-100">
                    <div className="flex items-center gap-2">
                      <span className="w-1.5 h-1.5 rounded-full bg-emerald-500" />
                      <span>{simModel} · 经由 uFreeTokens 路由</span>
                    </div>
                    <div className="font-mono text-gray-600">
                      12.4k tokens · $0.08
                    </div>
                  </div>
                </div>
              </div>
            </section>

            {/* ===================================================================== */}
            {/* SECTION 3: ORI EVAL · CLI                                             */}
            {/* ===================================================================== */}
            <section id="eval" className="scroll-mt-24">
              <div className="text-xs font-mono font-bold text-purple-600 tracking-wider uppercase mb-2">
                ORI EVAL · 智能模型基准评测
              </div>

              <h2 className="text-2xl sm:text-3xl font-extrabold text-gray-950 tracking-tight mb-3">
                为您的专属工程量身匹配最佳模型
              </h2>

              <p className="text-sm text-gray-600 leading-relaxed max-w-3xl mb-6">
                直接针对本地项目运行真实的 Prompt 提示词集与测试套件。在部署上线前，横向评测数十种前沿模型的生成质量、首字延迟（TTFT）与调用成本。
              </p>

              {/* Action bar */}
              <div className="flex flex-col sm:flex-row items-stretch sm:items-center gap-3 mb-6">
                <div className="flex-1 bg-gray-50 border border-gray-200 rounded-xl px-3.5 py-2 flex items-center justify-between font-mono text-xs text-gray-800">
                  <div className="flex items-center gap-2 truncate">
                    <span className="text-gray-400 select-none">$</span>
                    <span className="truncate">ori eval --dataset ./tests/prompts.jsonl --models claude-3-7-sonnet,gpt-4.5,deepseek-r1</span>
                  </div>
                  <button
                    onClick={() => handleCopy('ori eval --dataset ./tests/prompts.jsonl --models claude-3-7-sonnet,gpt-4.5,deepseek-r1', 'eval')}
                    className="p-1 text-gray-400 hover:text-gray-800 transition-colors ml-2 cursor-pointer"
                    title="复制安装命令"
                  >
                    {copiedEvalCmd ? <Check className="w-4 h-4 text-emerald-600" /> : <Copy className="w-4 h-4" />}
                  </button>
                </div>

                <button
                  onClick={onNavigateToBenchmarks}
                  className="bg-white border border-gray-200 hover:bg-gray-50 text-gray-800 font-medium text-xs px-4 py-2 rounded-xl flex items-center justify-center gap-1.5 shadow-xs transition-colors cursor-pointer"
                >
                  <span>查看基准排行榜</span>
                  <ArrowRight className="w-3.5 h-3.5" />
                </button>
              </div>

              {/* Eval Output Mockup */}
              <div className="bg-white border border-gray-200 rounded-xl p-5 shadow-xs">
                <div className="flex items-center justify-between border-b border-gray-100 pb-3 mb-4 text-xs">
                  <div className="font-semibold text-gray-900 flex items-center gap-2">
                    <Sliders className="w-4 h-4 text-purple-600" />
                    <span>评测矩阵实测结果 (250 个提示词样本)</span>
                  </div>
                  <span className="text-gray-400 font-mono">总运行耗时: 18.4s</span>
                </div>

                <div className="overflow-x-auto">
                  <table className="w-full text-xs text-left">
                    <thead>
                      <tr className="border-b border-gray-200 text-gray-500 font-semibold">
                        <th className="pb-2">模型 (Model)</th>
                        <th className="pb-2">通过率 (Pass Rate)</th>
                        <th className="pb-2">首字延迟 (TTFT)</th>
                        <th className="pb-2">生成速率 (Tokens/s)</th>
                        <th className="pb-2 text-right">单次测试成本 (Cost/Eval)</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-gray-100 font-mono">
                      <tr className="hover:bg-purple-50/30 transition-colors">
                        <td className="py-2.5 font-semibold text-gray-900 flex items-center gap-1.5">
                          <span className="w-2 h-2 rounded-full bg-emerald-500" />
                          <span>anthropic/claude-3-7-sonnet</span>
                        </td>
                        <td className="py-2.5 text-emerald-600 font-bold">96.4%</td>
                        <td className="py-2.5 text-gray-700">380ms</td>
                        <td className="py-2.5 text-gray-700">72 tok/s</td>
                        <td className="py-2.5 text-right text-gray-900">$0.34</td>
                      </tr>
                      <tr className="hover:bg-purple-50/30 transition-colors">
                        <td className="py-2.5 font-semibold text-gray-900 flex items-center gap-1.5">
                          <span className="w-2 h-2 rounded-full bg-blue-500" />
                          <span>openai/gpt-4.5</span>
                        </td>
                        <td className="py-2.5 text-emerald-600 font-bold">95.2%</td>
                        <td className="py-2.5 text-gray-700">420ms</td>
                        <td className="py-2.5 text-gray-700">58 tok/s</td>
                        <td className="py-2.5 text-right text-gray-900">$1.12</td>
                      </tr>
                      <tr className="hover:bg-purple-50/30 transition-colors">
                        <td className="py-2.5 font-semibold text-gray-900 flex items-center gap-1.5">
                          <span className="w-2 h-2 rounded-full bg-purple-500" />
                          <span>deepseek/deepseek-r1</span>
                        </td>
                        <td className="py-2.5 text-emerald-600 font-bold">94.8%</td>
                        <td className="py-2.5 text-gray-700">290ms</td>
                        <td className="py-2.5 text-gray-700">88 tok/s</td>
                        <td className="py-2.5 text-right text-emerald-600 font-bold">$0.06</td>
                      </tr>
                    </tbody>
                  </table>
                </div>
              </div>
            </section>

            {/* ===================================================================== */}
            {/* SECTION 4: ORI INTERN · AGENT                                         */}
            {/* ===================================================================== */}
            <section id="intern" className="scroll-mt-24">
              <div className="text-xs font-mono font-bold text-purple-600 tracking-wider uppercase mb-2">
                ORI INTERN · 全自主研发智能体
              </div>

              <h2 className="text-2xl sm:text-3xl font-extrabold text-gray-950 tracking-tight mb-3">
                全天候自主软件工程师助手
              </h2>

              <p className="text-sm text-gray-600 leading-relaxed max-w-3xl mb-6">
                指派长时运行的后台工程任务、自动拉取 PR 代码审查与 Issue 分流复现，具备自动化测试持续验证与人工介入审查机制。
              </p>

              <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                <div className="bg-white border border-gray-200 rounded-xl p-4 shadow-xs">
                  <div className="w-8 h-8 rounded-lg bg-purple-50 flex items-center justify-center text-purple-600 mb-3 font-semibold">
                    1
                  </div>
                  <h3 className="font-semibold text-gray-900 text-sm mb-1">Issue 自动分流排查</h3>
                  <p className="text-xs text-gray-500 leading-relaxed">
                    Ori 实时监听 GitHub 代码仓库，通过自动化无头浏览器与自动化脚本重现用户上报的 Bug，并构建最小复现用例。
                  </p>
                </div>

                <div className="bg-white border border-gray-200 rounded-xl p-4 shadow-xs">
                  <div className="w-8 h-8 rounded-lg bg-purple-50 flex items-center justify-center text-purple-600 mb-3 font-semibold">
                    2
                  </div>
                  <h3 className="font-semibold text-gray-900 text-sm mb-1">PR 自动起草与提交</h3>
                  <p className="text-xs text-gray-500 leading-relaxed">
                    自动生成符合规范的语义化 Git 提交、同步更新单元测试用例与 Changelog，发起已准备好合并的 Pull Request。
                  </p>
                </div>

                <div className="bg-white border border-gray-200 rounded-xl p-4 shadow-xs">
                  <div className="w-8 h-8 rounded-lg bg-purple-50 flex items-center justify-center text-purple-600 mb-3 font-semibold">
                    3
                  </div>
                  <h3 className="font-semibold text-gray-900 text-sm mb-1">企业级安全合规护栏</h3>
                  <p className="text-xs text-gray-500 leading-relaxed">
                    严格遵守组织级安全合规：零数据保留（ZDR）模型路由、调用预算阈值熔断，破坏性高危操作必须经过团队人员审批。
                  </p>
                </div>
              </div>
            </section>

            {/* ===================================================================== */}
            {/* SECTION 5: ORI DESKTOP · APP                                          */}
            {/* ===================================================================== */}
            <section id="desktop" className="scroll-mt-24">
              <div className="text-xs font-mono font-bold text-purple-600 tracking-wider uppercase mb-2">
                ORI DESKTOP · 桌面工作台
              </div>

              <h2 className="text-2xl sm:text-3xl font-extrabold text-gray-950 tracking-tight mb-3">
                在操作系统桌面随时随地调用任意模型
              </h2>

              <p className="text-sm text-gray-600 leading-relaxed max-w-3xl mb-6">
                使用全局快捷键（⌥ Space）瞬时唤起任何模型。无缝结合剪贴板内容、拖拽本地文件分析，无需离开当前编辑窗口即可调遣本地 Agent 工具。
              </p>

              <div className="bg-white border border-gray-200 rounded-xl p-6 shadow-xs flex flex-col sm:flex-row items-center justify-between gap-6">
                <div className="space-y-2">
                  <div className="flex items-center gap-2 text-xs font-semibold text-gray-900">
                    <Monitor className="w-4 h-4 text-purple-600" />
                    <span>下载 Ori 桌面客户端 (v1.2.0 Beta)</span>
                  </div>
                  <p className="text-xs text-gray-500">
                    通用二进制包，兼容 macOS (Apple Silicon 与 Intel 芯片)、Windows 11 以及 Linux AppImage。
                  </p>
                </div>

                <div className="flex items-center gap-2.5">
                  <button
                    onClick={() => alert('Ori 桌面版客户端下载中。启动后接入您的 API Key 即可使用。')}
                    className="bg-purple-600 hover:bg-purple-700 text-white font-medium text-xs px-4 py-2 rounded-xl flex items-center gap-1.5 shadow-sm shadow-purple-500/20 transition-colors cursor-pointer"
                  >
                    <Download className="w-3.5 h-3.5" />
                    <span>下载 macOS 版</span>
                  </button>
                  <button
                    onClick={() => scrollToSection('waitlist')}
                    className="bg-gray-50 hover:bg-gray-100 text-gray-700 font-medium text-xs px-3.5 py-2 rounded-xl border border-gray-200 transition-colors cursor-pointer"
                  >
                    其他操作系统
                  </button>
                </div>
              </div>
            </section>

            {/* ===================================================================== */}
            {/* SECTION 6: WAITLIST & COMMUNITY                                       */}
            {/* ===================================================================== */}
            <section id="waitlist" className="scroll-mt-24 pt-6">
              <div className="bg-gradient-to-br from-purple-50 via-indigo-50/50 to-white border border-purple-200/80 rounded-2xl p-8 text-center relative overflow-hidden shadow-xs">
                <div className="max-w-xl mx-auto space-y-4">
                  <div className="w-10 h-10 rounded-xl bg-purple-600 text-white flex items-center justify-center mx-auto shadow-md shadow-purple-500/20">
                    <Sparkles className="w-5 h-5" />
                  </div>
                  <h3 className="text-xl sm:text-2xl font-black text-gray-900">
                    获取 Ori 早期体验资格
                  </h3>
                  <p className="text-xs text-gray-600 leading-relaxed">
                    成为首批在终端、协作工具与工作流中享受全能多模型加持的开发者。
                  </p>

                  <form onSubmit={handleWaitlistSubmit} className="flex flex-col sm:flex-row gap-2 max-w-md mx-auto pt-2">
                    <input
                      type="email"
                      required
                      placeholder="输入您的电子邮箱地址"
                      value={waitlistEmail}
                      onChange={(e) => setWaitlistEmail(e.target.value)}
                      className="flex-1 bg-white border border-gray-200 rounded-xl px-3.5 py-2 text-xs text-gray-900 placeholder-gray-400 focus:outline-none focus:border-purple-400"
                    />
                    <button
                      type="submit"
                      className="bg-purple-600 hover:bg-purple-700 text-white font-medium text-xs rounded-xl px-5 py-2 transition-colors cursor-pointer whitespace-nowrap shadow-xs"
                    >
                      申请体验名额
                    </button>
                  </form>

                  <div className="flex items-center justify-center gap-6 pt-4 text-xs text-gray-500">
                    <button onClick={onNavigateToModels} className="hover:text-purple-600 transition-colors cursor-pointer">
                      浏览 100+ 热门模型 ➔
                    </button>
                    <button onClick={onNavigateToBenchmarks} className="hover:text-purple-600 transition-colors cursor-pointer">
                      性能基准评估矩阵 ➔
                    </button>
                  </div>
                </div>
              </div>
            </section>
          </main>
        </div>
      </div>
    </div>
  );
};
