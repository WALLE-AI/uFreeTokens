/**
 * @license
 * SPDX-License-Identifier: Apache-2.0
 */

import React, { useState, useMemo, useEffect } from 'react';
import {
  Columns,
  ChevronDown,
  Search,
  ArrowUpDown,
  Filter,
  Pin,
  Check,
  List,
  LayoutGrid,
  Table as TableIcon,
  Sparkles,
  RotateCcw,
  SlidersHorizontal,
  X
} from 'lucide-react';
import { INITIAL_MODELS, PRIMARY_TAGS, matchesPrimaryTag, synthesizeCallableModel, modelFromCatalog } from './data/models';
import { Model, FilterState, SortOption, ViewMode, ModalityType } from './types';
import { useApiKey } from './api/auth';
import { listModels } from './api/models';
import { listCatalog, CatalogModel } from './api/catalog';
import { Header } from './components/Header';
import { Sidebar } from './components/Sidebar';
import { ModelCard } from './components/ModelCard';
import { ModelGridCard } from './components/ModelGridCard';
import { ModelTable } from './components/ModelTable';
import { ModelDetailPage } from './components/ModelDetailPage';
import { BenchmarksPage } from './components/BenchmarksPage';
import { RankingsPage } from './components/RankingsPage';
import { HarnessPage } from './components/HarnessPage';
import { DocsPage } from './components/DocsPage';
import { PersonalDashboardPage } from './components/PersonalDashboardPage';
import { ModelCompareModal } from './components/ModelCompareModal';
import { PlaygroundModal } from './components/PlaygroundModal';
import { CommandPalette } from './components/CommandPalette';

const DEFAULT_FILTERS: FilterState = {
  searchQuery: '',
  selectedModalities: [],
  hasDiscountOnly: false,
  minContextLength: 0,
  maxPromptPrice: 15,
  maxOutputPrice: 60,
  selectedSeries: [],
  selectedCategories: [],
  selectedParameters: [],
  distillable: 'any',
  zeroDataRetentionOnly: false,
  selectedRegions: [],
  maxModelAgeMonths: 12,
  minToolCalling: 0,
  showDeprecated: false,
  selectedProviders: [],
  selectedAuthors: [],
  selectedVariant: '全部变体',
  selectedPrimaryTag: 'all',
  pinnedModelIds: ['deepseek/deepseek-pro'],
  minIntelligenceIndex: 0,
  minCodingIndex: 0,
  minAgenticIndex: 0,
  // Design Arena 是 Elo 风格原始分，和上面的 minIntelligenceIndex 等指数
  // 阈值用同一套"min"语义：默认 0 = 不筛选，拖高滑块才开始生效。
  minDesignArenaCode: 0,
  minDesignArenaUI: 0,
  minDesignArenaGame: 0,
  minDesignArenaDataViz: 0,
  minDesignArena3D: 0,
  minDesignArenaImage: 0,
  minDesignArenaVideo: 0,
  minDesignArenaSVG: 0,
};

export default function App() {
  const [filters, setFilters] = useState<FilterState>(DEFAULT_FILTERS);
  const [sortOption, setSortOption] = useState<SortOption>('newest');
  const [viewMode, setViewMode] = useState<ViewMode>('grid');
  const [showPinnedOnly, setShowPinnedOnly] = useState<boolean>(false);

  // Dropdown states
  const [showVariantsMenu, setShowVariantsMenu] = useState(false);
  const [showSortMenu, setShowSortMenu] = useState(false);
  const [showFilterPresetMenu, setShowFilterPresetMenu] = useState(false);
  const [filterPreset, setFilterPreset] = useState<'全部' | '免费模型' | '多模态' | '旗舰大模型' | '降价折扣'>('全部');

  // Modals & Panels
  const [activeModelForDetail, setActiveModelForDetail] = useState<Model | null>(null);
  const [activeModelForPlayground, setActiveModelForPlayground] = useState<Model | null>(null);
  const [compareModels, setCompareModels] = useState<Model[]>([
    INITIAL_MODELS[0],
    INITIAL_MODELS[1],
  ]);
  const [isCompareModalOpen, setIsCompareModalOpen] = useState(false);
  const [isCommandPaletteOpen, setIsCommandPaletteOpen] = useState(false);
  const [isMobileSidebarOpen, setIsMobileSidebarOpen] = useState(false);
  const [activeNav, setActiveNav] = useState('模型');
  const [personalDashboardTab, setPersonalDashboardTab] = useState<string>('api-keys');

  // 模型库以 GET /v1/catalog（免鉴权公开目录）为主数据源（技术方案迭代6），
  // 匿名访客也能看到真实数据；INITIAL_MODELS 只在目录接口暂不可用时兜底，
  // 以及给运营还没录入元数据的模型补展示层字段（描述、系列……），见
  // data/models.ts 的 modelFromCatalog。
  const [catalogModels, setCatalogModels] = useState<CatalogModel[] | null>(null);

  useEffect(() => {
    let cancelled = false;
    listCatalog()
      .then((models) => {
        if (!cancelled) setCatalogModels(models);
      })
      .catch(() => {
        if (!cancelled) setCatalogModels(null);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // 已连接 Key 时额外拉一次 GET /v1/models（需要鉴权），标记这把 Key 具体
  // 能调用哪些模型——和公开目录是两个不同维度："模型存在"（catalog）不等于
  // "这把 Key 能调用它"（models，比如 tier 限制或 allowed_models 白名单）。
  const apiKey = useApiKey();
  const [remoteModelIds, setRemoteModelIds] = useState<Set<string> | null>(null);

  useEffect(() => {
    if (!apiKey) {
      setRemoteModelIds(null);
      return;
    }
    let cancelled = false;
    listModels(apiKey)
      .then((models) => {
        if (!cancelled) setRemoteModelIds(new Set(models.map((m) => m.id)));
      })
      .catch(() => {
        if (!cancelled) setRemoteModelIds(null);
      });
    return () => {
      cancelled = true;
    };
  }, [apiKey]);

  const baseModels = useMemo<Model[]>(() => {
    const mockById = new Map(INITIAL_MODELS.map((m) => [m.id, m]));
    let models: Model[] = catalogModels
      ? catalogModels.map((cm) => modelFromCatalog(cm, mockById.get(cm.name)))
      : INITIAL_MODELS;

    if (remoteModelIds) {
      const knownIds = new Set(models.map((m) => m.id));
      models = models.map((m) => (remoteModelIds.has(m.id) ? { ...m, isCallable: true } : m));
      remoteModelIds.forEach((id) => {
        if (!knownIds.has(id)) models.push(synthesizeCallableModel(id));
      });
    }
    return models;
  }, [catalogModels, remoteModelIds]);

  // 左侧栏 Providers/Model authors 候选列表：从真实加载到的模型派生，而不是
  // 硬编码字符串数组——后端新增厂商时筛选项自动跟上（技术方案 A2）。
  const availableProviders = useMemo(
    () => Array.from(new Set(baseModels.map((m) => m.provider))).sort(),
    [baseModels]
  );
  const availableAuthors = useMemo(
    () => Array.from(new Set(baseModels.map((m) => m.author))).sort(),
    [baseModels]
  );

  // 模态过滤 Tag 栏上的计数——之前是 data/models.ts 里写死的 444/52/29……，
  // 和真实模型数据完全脱钩。现在用 matchesPrimaryTag 对 baseModels 实时统计，
  // 数据库清空后这里也会正确显示 0。
  const primaryTagCounts = useMemo(() => {
    const counts = new Map<string, number>();
    PRIMARY_TAGS.forEach((tag) => {
      counts.set(tag.id, baseModels.filter((m) => matchesPrimaryTag(m, tag.id)).length);
    });
    return counts;
  }, [baseModels]);

  // Keyboard shortcut ⌘K / Ctrl+K listener
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault();
        setIsCommandPaletteOpen((prev) => !prev);
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, []);

  const handleUpdateFilters = (newFilters: Partial<FilterState>) => {
    setFilters((prev) => ({ ...prev, ...newFilters }));
  };

  const handleResetFilters = () => {
    setFilters((prev) => ({
      ...DEFAULT_FILTERS,
      pinnedModelIds: prev.pinnedModelIds,
    }));
    setShowPinnedOnly(false);
    setFilterPreset('全部');
  };

  const handleTogglePin = (id: string) => {
    setFilters((prev) => {
      const isPinned = prev.pinnedModelIds.includes(id);
      return {
        ...prev,
        pinnedModelIds: isPinned
          ? prev.pinnedModelIds.filter((mId) => mId !== id)
          : [...prev.pinnedModelIds, id],
      };
    });
  };

  const handleToggleCompare = (model: Model) => {
    setCompareModels((prev) => {
      const exists = prev.some((m) => m.id === model.id);
      if (exists) {
        return prev.filter((m) => m.id !== model.id);
      }
      if (prev.length >= 4) {
        alert('最多支持同时对比 4 个模型');
        return prev;
      }
      return [...prev, model];
    });
  };

  // Filter and Sort Pipeline
  const filteredModels = useMemo(() => {
    return baseModels.filter((model) => {
      // Search query
      if (filters.searchQuery.trim()) {
        const q = filters.searchQuery.toLowerCase();
        const match =
          model.name.toLowerCase().includes(q) ||
          model.id.toLowerCase().includes(q) ||
          model.description.toLowerCase().includes(q) ||
          model.provider.toLowerCase().includes(q) ||
          model.author.toLowerCase().includes(q);
        if (!match) return false;
      }

      // Pinned only filter
      if (showPinnedOnly && !filters.pinnedModelIds.includes(model.id)) {
        return false;
      }

      // Primary tag bar
      if (!matchesPrimaryTag(model, filters.selectedPrimaryTag)) return false;

      // Modalities checklist
      if (filters.selectedModalities.length > 0) {
        const hasAll = filters.selectedModalities.every((m) => model.modalities.includes(m));
        if (!hasAll) return false;
      }

      // Has discount
      if (filters.hasDiscountOnly && !model.hasDiscount) {
        return false;
      }

      // Min context length (in thousands)
      if (filters.minContextLength > 0 && model.contextTokens < filters.minContextLength * 1000) {
        return false;
      }

      // Max prompt price
      if (filters.maxPromptPrice < 15 && model.inputPricePerM > filters.maxPromptPrice) {
        return false;
      }

      // Max output price
      if (filters.maxOutputPrice < 60 && model.outputPricePerM > filters.maxOutputPrice) {
        return false;
      }

      // Series
      if (filters.selectedSeries.length > 0 && !filters.selectedSeries.includes(model.series)) {
        return false;
      }

      // Categories
      if (filters.selectedCategories.length > 0 && !filters.selectedCategories.includes(model.category)) {
        return false;
      }

      // Supported parameters
      if (filters.selectedParameters.length > 0) {
        const hasAllParams = filters.selectedParameters.every((p) =>
          model.supportedParameters.includes(p)
        );
        if (!hasAllParams) return false;
      }

      // Distillable
      if (filters.distillable === 'yes' && !model.distillable) return false;
      if (filters.distillable === 'no' && model.distillable) return false;

      // Zero data retention
      if (filters.zeroDataRetentionOnly && !model.zeroDataRetention) return false;

      // In-region routing
      if (filters.selectedRegions.length > 0) {
        const matchRegion = filters.selectedRegions.some((r) => model.inRegionRouting.includes(r));
        if (!matchRegion) return false;
      }

      // Model age
      if (filters.maxModelAgeMonths < 12 && model.modelAgeMonths > filters.maxModelAgeMonths) {
        return false;
      }

      // Tool calling capability
      if (filters.minToolCalling > 0 && model.toolCallingCapability < filters.minToolCalling) {
        return false;
      }

      // Inactive / Deprecated
      if (!filters.showDeprecated && model.isDeprecated) {
        return false;
      }

      // Providers
      if (
        filters.selectedProviders.length > 0 &&
        !filters.selectedProviders.map((p) => p.toLowerCase()).includes(model.provider.toLowerCase())
      ) {
        return false;
      }

      // Authors
      if (
        filters.selectedAuthors.length > 0 &&
        !filters.selectedAuthors.map((a) => a.toLowerCase()).includes(model.author.toLowerCase())
      ) {
        return false;
      }

      // Variants
      if (filters.selectedVariant !== '全部变体') {
        const variantMap: Record<string, string> = {
          '标准版 (Standard)': 'standard',
          '免费版 (Free)': 'free',
          '扩展版 (Extended)': 'extended',
          '深度思考 (Thinking)': 'thinking',
          '批处理 (Batch)': 'batch',
        };
        const target = variantMap[filters.selectedVariant];
        if (target && !model.variants.includes(target as any)) {
          return false;
        }
      }

      // Presets
      if (filterPreset === '免费模型' && model.inputPricePerM > 0) return false;
      if (filterPreset === '多模态' && model.modalities.length <= 1) return false;
      if (filterPreset === '旗舰大模型' && model.scores.intelligenceIndex < 52) return false;
      if (filterPreset === '降价折扣' && !model.hasDiscount) return false;

      // Indices
      if (filters.minIntelligenceIndex > 0 && model.scores.intelligenceIndex < filters.minIntelligenceIndex) return false;
      if (filters.minCodingIndex > 0 && model.scores.codingIndex < filters.minCodingIndex) return false;
      if (filters.minAgenticIndex > 0 && model.scores.agenticIndex < filters.minAgenticIndex) return false;

      // Design Arena（Elo 原始分，和上面三个百分位指数不是同一套量级，见
      // Sidebar.tsx 的 E8 说明文案）
      const da = model.scores.designArena;
      if (filters.minDesignArenaCode > 0 && (da?.codeCategories ?? 0) < filters.minDesignArenaCode) return false;
      if (filters.minDesignArenaUI > 0 && (da?.uiComponent ?? 0) < filters.minDesignArenaUI) return false;
      if (filters.minDesignArenaGame > 0 && (da?.gameDev ?? 0) < filters.minDesignArenaGame) return false;
      if (filters.minDesignArenaDataViz > 0 && (da?.dataViz ?? 0) < filters.minDesignArenaDataViz) return false;
      if (filters.minDesignArena3D > 0 && (da?.threeD ?? 0) < filters.minDesignArena3D) return false;
      if (filters.minDesignArenaImage > 0 && (da?.image ?? 0) < filters.minDesignArenaImage) return false;
      if (filters.minDesignArenaVideo > 0 && (da?.video ?? 0) < filters.minDesignArenaVideo) return false;
      if (filters.minDesignArenaSVG > 0 && (da?.svg ?? 0) < filters.minDesignArenaSVG) return false;

      return true;
    }).sort((a, b) => {
      // Pinned models always stay at the very top
      const aPinned = filters.pinnedModelIds.includes(a.id);
      const bPinned = filters.pinnedModelIds.includes(b.id);
      if (aPinned && !bPinned) return -1;
      if (!aPinned && bPinned) return 1;

      if (sortOption === 'newest') {
        return new Date(b.releaseDate).getTime() - new Date(a.releaseDate).getTime();
      }
      if (sortOption === 'price-asc') {
        return a.inputPricePerM - b.inputPricePerM;
      }
      if (sortOption === 'price-desc') {
        return b.inputPricePerM - a.inputPricePerM;
      }
      if (sortOption === 'context-desc') {
        return b.contextTokens - a.contextTokens;
      }
      if (sortOption === 'intelligence-desc') {
        return b.scores.intelligenceIndex - a.scores.intelligenceIndex;
      }
      return 0;
    });
  }, [baseModels, filters, sortOption, showPinnedOnly, filterPreset]);

  return (
    <div className="min-h-screen flex flex-col bg-white text-gray-800 antialiased font-sans">
      {/* 1. 顶栏 Header */}
      <Header
        searchQuery={filters.searchQuery}
        onSearchChange={(q) => handleUpdateFilters({ searchQuery: q })}
        onOpenCommandPalette={() => setIsCommandPaletteOpen(true)}
        onToggleMobileSidebar={() => setIsMobileSidebarOpen(!isMobileSidebarOpen)}
        activeNav={activeNav}
        onSelectNav={(n) => {
          setActiveNav(n);
          setActiveModelForDetail(null);
        }}
        onOpenPersonalDashboard={(tab = 'api-keys') => {
          setPersonalDashboardTab(tab);
          setActiveNav('个人中心');
          setActiveModelForDetail(null);
          window.scrollTo({ top: 0, behavior: 'smooth' });
        }}
      />

      {activeNav === '个人中心' ? (
        <PersonalDashboardPage
          initialTab={personalDashboardTab}
          onBackToModels={() => {
            setActiveNav('模型');
            window.scrollTo({ top: 0, behavior: 'smooth' });
          }}
        />
      ) : activeNav === '基准测试' ? (
        <BenchmarksPage
          allModels={INITIAL_MODELS}
          onSelectModel={(m) => {
            setActiveModelForDetail(m);
            setActiveNav('模型');
            window.scrollTo({ top: 0, behavior: 'smooth' });
          }}
          onNavigateToModels={() => {
            setActiveNav('模型');
            setActiveModelForDetail(null);
            window.scrollTo({ top: 0, behavior: 'smooth' });
          }}
        />
      ) : activeNav === '排行榜' ? (
        <RankingsPage
          allModels={INITIAL_MODELS}
          onSelectModel={(m) => {
            setActiveModelForDetail(m);
            setActiveNav('模型');
            window.scrollTo({ top: 0, behavior: 'smooth' });
          }}
          onNavigateToBenchmarks={() => {
            setActiveNav('基准测试');
            setActiveModelForDetail(null);
            window.scrollTo({ top: 0, behavior: 'smooth' });
          }}
          onNavigateToModels={() => {
            setActiveNav('模型');
            setActiveModelForDetail(null);
            window.scrollTo({ top: 0, behavior: 'smooth' });
          }}
        />
      ) : activeNav === 'Harness' ? (
        <HarnessPage
          onNavigateToModels={() => {
            setActiveNav('模型');
            setActiveModelForDetail(null);
            window.scrollTo({ top: 0, behavior: 'smooth' });
          }}
          onNavigateToBenchmarks={() => {
            setActiveNav('基准测试');
            setActiveModelForDetail(null);
            window.scrollTo({ top: 0, behavior: 'smooth' });
          }}
        />
      ) : activeNav === '文档' ? (
        <DocsPage
          onBackToMarketplace={() => {
            setActiveNav('模型');
            setActiveModelForDetail(null);
            window.scrollTo({ top: 0, behavior: 'smooth' });
          }}
        />
      ) : activeModelForDetail ? (
        <ModelDetailPage
          model={activeModelForDetail}
          allModels={baseModels}
          onBack={() => setActiveModelForDetail(null)}
          onOpenPlayground={(m) => setActiveModelForPlayground(m)}
          onToggleCompare={handleToggleCompare}
          isInCompare={compareModels.some((item) => item.id === activeModelForDetail.id)}
          onSelectOtherModel={(m) => {
            setActiveModelForDetail(m);
            window.scrollTo({ top: 0, behavior: 'smooth' });
          }}
          onNavigateToBenchmarks={() => {
            setActiveNav('基准测试');
            setActiveModelForDetail(null);
            window.scrollTo({ top: 0, behavior: 'smooth' });
          }}
        />
      ) : (
        /* Main Container */
        <div className="flex flex-1 relative overflow-hidden">
        {/* 2. 左侧边栏 - 桌面端 */}
        <div className="hidden md:block">
          <Sidebar
            filters={filters}
            onFilterChange={handleUpdateFilters}
            onResetFilters={handleResetFilters}
            totalFilteredCount={filteredModels.length}
            availableProviders={availableProviders}
            availableAuthors={availableAuthors}
          />
        </div>

        {/* 移动端抽屉边栏 */}
        {isMobileSidebarOpen && (
          <div className="fixed inset-0 z-50 md:hidden flex">
            <div
              className="fixed inset-0 bg-black/40 backdrop-blur-xs"
              onClick={() => setIsMobileSidebarOpen(false)}
            />
            <div className="relative w-72 max-w-[85vw] bg-white h-full z-10 shadow-2xl flex flex-col">
              <div className="p-3 border-b border-gray-200 flex items-center justify-between">
                <span className="font-bold text-xs">筛选选项</span>
                <button
                  onClick={() => setIsMobileSidebarOpen(false)}
                  className="p-1 rounded text-gray-400 hover:text-gray-700"
                >
                  <X className="w-4 h-4" />
                </button>
              </div>
              <div className="flex-1 overflow-y-auto">
                <Sidebar
                  filters={filters}
                  onFilterChange={handleUpdateFilters}
                  onResetFilters={handleResetFilters}
                  totalFilteredCount={filteredModels.length}
                  availableProviders={availableProviders}
                  availableAuthors={availableAuthors}
                />
              </div>
            </div>
          </div>
        )}

        {/* 3. 主内容区 - 模型列表与控制台 */}
        <main
          className={`flex-1 px-4 sm:px-6 py-4 overflow-y-auto h-[calc(100vh-3rem)] ${
            viewMode === 'grid' ? '' : 'max-w-5xl'
          }`}
        >
          {/* 页面主标题 + 右上角操作 */}
          <div className="flex items-center justify-between mb-3">
            <div className="flex items-center space-x-2">
              <h1 className="text-lg font-bold text-gray-900 tracking-tight">
                模型库 (Models)
              </h1>
              <span className="px-2 py-0.5 rounded bg-gray-100 text-gray-600 text-[11px] font-mono">
                {filteredModels.length} 款
              </span>
            </div>

            <div className="flex items-center space-x-3 text-xs text-gray-500">
              <button
                onClick={() => setIsCompareModalOpen(true)}
                className={`flex items-center space-x-1 transition-colors px-2 py-1 rounded cursor-pointer ${
                  compareModels.length > 0
                    ? 'text-purple-700 bg-purple-50 font-medium hover:bg-purple-100'
                    : 'hover:text-black'
                }`}
              >
                <Columns className="w-3.5 h-3.5 text-purple-600" />
                <span>模型对比</span>
                {compareModels.length > 0 && (
                  <span className="bg-purple-600 text-white rounded-full px-1 text-[10px] leading-tight">
                    {compareModels.length}
                  </span>
                )}
              </button>

              <div className="relative group">
                <button className="flex items-center space-x-1 hover:text-black cursor-pointer">
                  <span>发现推荐</span>
                  <ChevronDown className="w-3 h-3" />
                </button>
                <div className="absolute right-0 top-6 w-44 bg-white border border-gray-200 rounded-lg shadow-lg py-1 z-30 hidden group-hover:block text-xs">
                  <button
                    onClick={() => setFilterPreset('免费模型')}
                    className="w-full text-left px-3 py-1.5 hover:bg-gray-50 text-gray-700"
                  >
                    全部免额度模型 (Free)
                  </button>
                  <button
                    onClick={() => setFilterPreset('多模态')}
                    className="w-full text-left px-3 py-1.5 hover:bg-gray-50 text-gray-700"
                  >
                    前沿视觉/多模态模型
                  </button>
                  <button
                    onClick={() => setFilterPreset('旗舰大模型')}
                    className="w-full text-left px-3 py-1.5 hover:bg-gray-50 text-gray-700"
                  >
                    行业顶尖推理与代码
                  </button>
                </div>
              </div>
            </div>
          </div>

          {/* 次级控制工具栏（搜索框、排序、变体选择菜单） */}
          <div className="flex flex-wrap items-center justify-between gap-2.5 mb-3 border-b border-gray-100 pb-2.5">
            {/* 列表专属实时搜索输入框 */}
            <div className="relative w-full sm:w-64">
              <Search className="w-3.5 h-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-gray-400" />
              <input
                type="text"
                value={filters.searchQuery}
                onChange={(e) => handleUpdateFilters({ searchQuery: e.target.value })}
                placeholder="搜索模型..."
                className="w-full bg-white border border-gray-200 rounded pl-8 pr-7 py-1 text-xs focus:outline-none focus:border-purple-400"
              />
              {filters.searchQuery && (
                <button
                  onClick={() => handleUpdateFilters({ searchQuery: '' })}
                  className="absolute right-2 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600"
                >
                  <X className="w-3 h-3" />
                </button>
              )}
            </div>

            <div className="flex flex-wrap items-center space-x-2 sm:space-x-3 text-xs text-gray-500">
              {/* 排序 Dropdown */}
              <div className="relative">
                <button
                  onClick={() => {
                    setShowSortMenu(!showSortMenu);
                    setShowFilterPresetMenu(false);
                    setShowVariantsMenu(false);
                  }}
                  className="flex items-center space-x-1 hover:text-black py-1 px-1.5 rounded hover:bg-gray-100 transition-colors"
                >
                  <ArrowUpDown className="w-3 h-3 text-gray-500" />
                  <span>
                    {sortOption === 'newest' && '最新发布'}
                    {sortOption === 'price-asc' && '价格从低到高'}
                    {sortOption === 'price-desc' && '价格从高到低'}
                    {sortOption === 'context-desc' && '上下文容量'}
                    {sortOption === 'intelligence-desc' && '智能评测'}
                  </span>
                  <ChevronDown className="w-3 h-3 text-gray-400" />
                </button>

                {showSortMenu && (
                  <div className="absolute right-0 top-7 w-36 bg-white border border-gray-200 rounded-lg shadow-xl py-1 z-50 text-xs">
                    {[
                      { id: 'newest', label: '最新发布' },
                      { id: 'price-asc', label: '价格从低到高' },
                      { id: 'price-desc', label: '价格从高到低' },
                      { id: 'context-desc', label: '上下文容量' },
                      { id: 'intelligence-desc', label: '智能指数优先' },
                    ].map((opt) => (
                      <button
                        key={opt.id}
                        onClick={() => {
                          setSortOption(opt.id as SortOption);
                          setShowSortMenu(false);
                        }}
                        className="w-full text-left flex items-center justify-between px-3 py-1.5 hover:bg-gray-50 text-gray-700"
                      >
                        <span>{opt.label}</span>
                        {sortOption === opt.id && (
                          <Check className="w-3 h-3 text-purple-600" />
                        )}
                      </button>
                    ))}
                  </div>
                )}
              </div>

              {/* 快捷过滤 Preset */}
              <div className="relative">
                <button
                  onClick={() => {
                    setShowFilterPresetMenu(!showFilterPresetMenu);
                    setShowSortMenu(false);
                    setShowVariantsMenu(false);
                  }}
                  className="flex items-center space-x-1 hover:text-black py-1 px-1.5 rounded hover:bg-gray-100 transition-colors"
                >
                  <Filter className="w-3 h-3 text-gray-500" />
                  <span>{filterPreset}</span>
                  <ChevronDown className="w-3 h-3 text-gray-400" />
                </button>

                {showFilterPresetMenu && (
                  <div className="absolute right-0 top-7 w-32 bg-white border border-gray-200 rounded-lg shadow-xl py-1 z-50 text-xs">
                    {(['全部', '免费模型', '多模态', '旗舰大模型', '降价折扣'] as const).map(
                      (preset) => (
                        <button
                          key={preset}
                          onClick={() => {
                            setFilterPreset(preset);
                            setShowFilterPresetMenu(false);
                          }}
                          className="w-full text-left flex items-center justify-between px-3 py-1.5 hover:bg-gray-50 text-gray-700"
                        >
                          <span>{preset}</span>
                          {filterPreset === preset && (
                            <Check className="w-3 h-3 text-purple-600" />
                          )}
                        </button>
                      )
                    )}
                  </div>
                )}
              </div>

              {/* 固定过滤按钮 */}
              <button
                onClick={() => setShowPinnedOnly(!showPinnedOnly)}
                className={`flex items-center space-x-1 py-1 px-1.5 rounded transition-colors ${
                  showPinnedOnly
                    ? 'text-purple-700 bg-purple-50 font-medium'
                    : 'hover:text-black'
                }`}
              >
                <Pin className="w-3 h-3" />
                <span>已固定</span>
                {filters.pinnedModelIds.length > 0 && (
                  <span className="text-[10px] text-gray-400 font-mono">
                    ({filters.pinnedModelIds.length})
                  </span>
                )}
              </button>

              {/* 变体 Dropdown 下拉组件 */}
              <div className="relative">
                <button
                  onClick={() => {
                    setShowVariantsMenu(!showVariantsMenu);
                    setShowSortMenu(false);
                    setShowFilterPresetMenu(false);
                  }}
                  className="flex items-center space-x-1 text-purple-600 bg-purple-50 px-2 py-0.5 rounded font-medium border border-purple-200 hover:bg-purple-100"
                >
                  <Check className="w-3 h-3" />
                  <span>{filters.selectedVariant}</span>
                  <ChevronDown className="w-3 h-3" />
                </button>

                {showVariantsMenu && (
                  <div className="absolute right-0 top-7 w-36 bg-white border border-gray-200 rounded shadow-lg py-1 z-50 text-xs">
                    {[
                      '全部变体',
                      '标准版 (Standard)',
                      '免费版 (Free)',
                      '扩展版 (Extended)',
                      '深度思考 (Thinking)',
                      '批处理 (Batch)',
                    ].map((name) => {
                      const isActive = filters.selectedVariant === name;
                      return (
                        <div
                          key={name}
                          onClick={() => {
                            handleUpdateFilters({ selectedVariant: name });
                            setShowVariantsMenu(false);
                          }}
                          className="flex items-center space-x-1.5 px-2.5 py-1 hover:bg-gray-50 cursor-pointer text-gray-700"
                        >
                          <span className="w-3">
                            {isActive && <Check className="w-3 h-3 text-purple-600" />}
                          </span>
                          <span>{name}</span>
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>

              {/* 视图切换 (网格 / 列表 / 表格) */}
              <div className="flex items-center border border-gray-200 rounded bg-white overflow-hidden">
                <button
                  onClick={() => setViewMode('grid')}
                  className={`p-1.5 transition-colors border-r border-gray-200 ${
                    viewMode === 'grid' ? 'bg-gray-100 text-black' : 'text-gray-400 hover:text-black'
                  }`}
                  title="网格视图"
                >
                  <LayoutGrid className="w-3 h-3" />
                </button>
                <button
                  onClick={() => setViewMode('list')}
                  className={`p-1.5 transition-colors ${
                    viewMode === 'list'
                      ? 'bg-gray-100 text-black border-r border-gray-200'
                      : 'text-gray-400 hover:text-black border-r border-gray-200'
                  }`}
                  title="列表视图"
                >
                  <List className="w-3 h-3" />
                </button>
                <button
                  onClick={() => setViewMode('table')}
                  className={`p-1.5 transition-colors ${
                    viewMode === 'table' ? 'bg-gray-100 text-black' : 'text-gray-400 hover:text-black'
                  }`}
                  title="表格视图"
                >
                  <TableIcon className="w-3 h-3" />
                </button>
              </div>
            </div>
          </div>

          {/* 模态过滤 Tag 栏 */}
          <div className="flex flex-wrap items-center gap-1.5 mb-4 text-xs select-none">
            {PRIMARY_TAGS.map((tag) => {
              const isSelected = filters.selectedPrimaryTag === tag.id;
              const count = primaryTagCounts.get(tag.id) ?? 0;
              return (
                <button
                  key={tag.id}
                  onClick={() => handleUpdateFilters({ selectedPrimaryTag: tag.id })}
                  className={`px-2 py-0.5 rounded text-xxs font-medium transition-colors cursor-pointer ${
                    isSelected
                      ? 'bg-purple-600 text-white'
                      : 'bg-gray-100 text-gray-600 hover:bg-gray-200'
                  }`}
                >
                  <span>{tag.title}</span>
                  <span className={`ml-1 ${isSelected ? 'text-purple-200' : 'text-gray-400'}`}>
                    {count}
                  </span>
                </button>
              );
            })}
          </div>

          {/* Active Filter Chips bar (if any filter is applied) */}
          {(filters.searchQuery ||
            filters.selectedModalities.length > 0 ||
            filters.hasDiscountOnly ||
            filters.minContextLength > 0 ||
            filters.maxPromptPrice < 15 ||
            filters.selectedSeries.length > 0 ||
            filters.selectedCategories.length > 0 ||
            filters.selectedProviders.length > 0 ||
            showPinnedOnly ||
            filterPreset !== '全部') && (
            <div className="flex flex-wrap items-center gap-1.5 mb-3.5 bg-gray-50 p-2 rounded-lg border border-gray-100 text-[11px]">
              <span className="text-gray-400 text-xxs">生效中筛选:</span>

              {filters.searchQuery && (
                <span className="bg-white border border-gray-200 px-1.5 py-0.5 rounded text-gray-700 flex items-center space-x-1">
                  <span>搜索: {filters.searchQuery}</span>
                  <button onClick={() => handleUpdateFilters({ searchQuery: '' })}>
                    <X className="w-2.5 h-2.5 text-gray-400 hover:text-gray-700" />
                  </button>
                </span>
              )}

              {showPinnedOnly && (
                <span className="bg-purple-50 border border-purple-200 px-1.5 py-0.5 rounded text-purple-700 flex items-center space-x-1">
                  <span>仅看已固定</span>
                  <button onClick={() => setShowPinnedOnly(false)}>
                    <X className="w-2.5 h-2.5 text-purple-400 hover:text-purple-700" />
                  </button>
                </span>
              )}

              {filterPreset !== '全部' && (
                <span className="bg-purple-50 border border-purple-200 px-1.5 py-0.5 rounded text-purple-700 flex items-center space-x-1">
                  <span>{filterPreset}</span>
                  <button onClick={() => setFilterPreset('全部')}>
                    <X className="w-2.5 h-2.5 text-purple-400 hover:text-purple-700" />
                  </button>
                </span>
              )}

              {filters.minContextLength > 0 && (
                <span className="bg-white border border-gray-200 px-1.5 py-0.5 rounded text-gray-700 flex items-center space-x-1">
                  <span>上下文 ≥ {filters.minContextLength}K</span>
                  <button onClick={() => handleUpdateFilters({ minContextLength: 0 })}>
                    <X className="w-2.5 h-2.5 text-gray-400" />
                  </button>
                </span>
              )}

              {filters.maxPromptPrice < 15 && (
                <span className="bg-white border border-gray-200 px-1.5 py-0.5 rounded text-gray-700 flex items-center space-x-1">
                  <span>输入单价 ≤ ${filters.maxPromptPrice}/M</span>
                  <button onClick={() => handleUpdateFilters({ maxPromptPrice: 15 })}>
                    <X className="w-2.5 h-2.5 text-gray-400" />
                  </button>
                </span>
              )}

              {filters.selectedModalities.map((m) => (
                <span key={m} className="bg-white border border-gray-200 px-1.5 py-0.5 rounded text-gray-700 flex items-center space-x-1 uppercase font-mono">
                  <span>{m}</span>
                  <button onClick={() => handleUpdateFilters({ selectedModalities: filters.selectedModalities.filter((x) => x !== m) })}>
                    <X className="w-2.5 h-2.5 text-gray-400" />
                  </button>
                </span>
              ))}

              <button
                onClick={handleResetFilters}
                className="text-purple-600 hover:text-purple-800 ml-auto font-medium text-xxs flex items-center space-x-1"
              >
                <RotateCcw className="w-2.5 h-2.5" />
                <span>清空全部条件</span>
              </button>
            </div>
          )}

          {/* 模型列表 / 表格渲染 */}
          {filteredModels.length === 0 ? (
            <div className="py-20 text-center text-gray-400 border border-dashed border-gray-200 rounded-lg">
              <Search className="w-8 h-8 mx-auto text-gray-300 stroke-1 mb-2" />
              <div className="text-xs font-medium text-gray-700">没有找到符合条件的模型</div>
              <div className="text-xxs text-gray-400 mt-1">请尝试放宽价格区间、模态或清除搜索关键字</div>
              <button
                onClick={handleResetFilters}
                className="mt-3 px-3 py-1 text-xs bg-purple-50 text-purple-700 rounded-md hover:bg-purple-100 font-medium transition-colors"
              >
                恢复默认筛选
              </button>
            </div>
          ) : viewMode === 'grid' ? (
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 2xl:grid-cols-5 gap-3.5">
              {filteredModels.map((m) => (
                <ModelGridCard
                  key={m.id}
                  model={m}
                  isPinned={filters.pinnedModelIds.includes(m.id)}
                  isInCompare={compareModels.some((item) => item.id === m.id)}
                  onTogglePin={handleTogglePin}
                  onToggleCompare={handleToggleCompare}
                  onSelectModel={(model) => setActiveModelForDetail(model)}
                  onOpenPlayground={(model) => setActiveModelForPlayground(model)}
                  onSelectProvider={(prov) =>
                    handleUpdateFilters({ selectedProviders: [prov] })
                  }
                />
              ))}
            </div>
          ) : viewMode === 'list' ? (
            <div className="divide-y divide-gray-100">
              {filteredModels.map((m) => (
                <ModelCard
                  key={m.id}
                  model={m}
                  isPinned={filters.pinnedModelIds.includes(m.id)}
                  isInCompare={compareModels.some((item) => item.id === m.id)}
                  onTogglePin={handleTogglePin}
                  onToggleCompare={handleToggleCompare}
                  onSelectModel={(model) => setActiveModelForDetail(model)}
                  onOpenPlayground={(model) => setActiveModelForPlayground(model)}
                  onSelectProvider={(prov) =>
                    handleUpdateFilters({ selectedProviders: [prov] })
                  }
                />
              ))}
            </div>
          ) : (
            <ModelTable
              models={filteredModels}
              pinnedModelIds={filters.pinnedModelIds}
              compareModels={compareModels}
              onTogglePin={handleTogglePin}
              onToggleCompare={handleToggleCompare}
              onSelectModel={(model) => setActiveModelForDetail(model)}
              onOpenPlayground={(model) => setActiveModelForPlayground(model)}
            />
          )}
        </main>
      </div>
      )}

      {/* Floating Compare Tray (bottom right) if models selected */}
      {compareModels.length > 0 && !isCompareModalOpen && (
        <div className="fixed bottom-4 right-4 z-40 bg-white border border-purple-200 rounded-xl shadow-xl p-3 flex items-center space-x-3 text-xs animate-in fade-in slide-in-from-bottom-2">
          <div className="flex -space-x-1.5 overflow-hidden">
            {compareModels.map((m) => (
              <div
                key={m.id}
                className={`w-6 h-6 rounded-full flex items-center justify-center text-[9px] border-2 border-white font-bold shadow-xs ${m.iconBg}`}
                title={m.name}
              >
                ▲
              </div>
            ))}
          </div>

          <div>
            <div className="font-semibold text-gray-900">
              已选 {compareModels.length} 个模型
            </div>
            <div className="text-[10px] text-gray-400">支持多维参数横向评测</div>
          </div>

          <button
            onClick={() => setIsCompareModalOpen(true)}
            className="px-3 py-1.5 bg-purple-600 hover:bg-purple-700 text-white font-medium rounded-lg text-xs shadow-xs transition-colors"
          >
            开始对比
          </button>
        </div>
      )}

      {/* Compare Modal */}
      {isCompareModalOpen && (
        <ModelCompareModal
          models={compareModels}
          onClose={() => setIsCompareModalOpen(false)}
          onRemoveModel={(id) => setCompareModels((prev) => prev.filter((m) => m.id !== id))}
          onClearAll={() => setCompareModels([])}
          onOpenPlayground={(m) => {
            setIsCompareModalOpen(false);
            setActiveModelForPlayground(m);
          }}
        />
      )}

      {/* Playground Modal */}
      {activeModelForPlayground && (
        <PlaygroundModal
          model={activeModelForPlayground}
          onClose={() => setActiveModelForPlayground(null)}
        />
      )}

      {/* Command Palette (⌘K) */}
      <CommandPalette
        isOpen={isCommandPaletteOpen}
        onClose={() => setIsCommandPaletteOpen(false)}
        models={baseModels}
        onSelectModel={(m) => setActiveModelForDetail(m)}
        onNavigate={(nav) => {
          setActiveNav(nav);
          setActiveModelForDetail(null);
          window.scrollTo({ top: 0, behavior: 'smooth' });
        }}
      />
    </div>
  );
}
