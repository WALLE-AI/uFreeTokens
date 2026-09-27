import React, { useState } from 'react';
import {
  LogIn,
  Gift,
  Cpu,
  DollarSign,
  Boxes,
  Tag,
  Code2,
  FlaskConical,
  ShieldCheck,
  Globe,
  Calendar,
  Wrench,
  Archive,
  AreaChart,
  PenTool,
  Landmark,
  User as UserIcon,
  ChevronUp,
  ChevronDown,
  Info,
  RotateCcw,
  BarChart2,
  Layout,
  Gamepad2,
  LineChart,
  Box,
  Image as ImageIcon,
  Video,
  Feather
} from 'lucide-react';
import { FilterState, ModalityType } from '../types';

interface SidebarSectionProps {
  title: string;
  icon?: React.ReactNode;
  hasInfo?: boolean;
  infoText?: string;
  defaultOpen?: boolean;
  children: React.ReactNode;
}

export const SidebarSection: React.FC<SidebarSectionProps> = ({
  title,
  icon,
  hasInfo = false,
  infoText,
  defaultOpen = true,
  children,
}) => {
  const [isOpen, setIsOpen] = useState(defaultOpen);

  return (
    <div className="border-b border-gray-100 py-3 text-xs">
      <div
        className="flex items-center justify-between cursor-pointer text-gray-800 font-medium hover:text-black mb-2 select-none group"
        onClick={() => setIsOpen(!isOpen)}
      >
        <div className="flex items-center space-x-2">
          {icon && <span className="w-4 h-4 text-gray-500 group-hover:text-purple-600 transition-colors flex items-center justify-center">{icon}</span>}
          <span className="text-[12px] font-semibold text-gray-800 group-hover:text-black">{title}</span>
          {hasInfo && (
            <span title={infoText || title} className="text-gray-400 hover:text-gray-600">
              <Info className="w-3.5 h-3.5" />
            </span>
          )}
        </div>
        <span className="text-gray-400 group-hover:text-gray-600">
          {isOpen ? <ChevronUp className="w-3.5 h-3.5" /> : <ChevronDown className="w-3.5 h-3.5" />}
        </span>
      </div>
      {isOpen && <div className="mt-2 space-y-2">{children}</div>}
    </div>
  );
};

interface CheckboxGroupProps {
  items: string[];
  selectedItems?: string[];
  onToggle: (item: string) => void;
  showMore?: boolean;
  // searchable：候选项超过 8 个时自动带一个前缀搜索框（技术方案 E7）——
  // Providers/Model authors 这类会随目录增长的长列表用得上，其余固定的
  // 短列表不需要。
  searchable?: boolean;
}

export const CheckboxGroup: React.FC<CheckboxGroupProps> = ({
  items,
  selectedItems = [],
  onToggle,
  showMore = true,
  searchable = false,
}) => {
  const [expanded, setExpanded] = useState(!showMore);
  const [query, setQuery] = useState('');

  const searchActive = searchable && items.length > 8 && query.trim().length > 0;
  const visibleItems = searchActive
    ? items.filter((i) => i.toLowerCase().includes(query.trim().toLowerCase()))
    : items;
  const displayItems = expanded || searchActive ? visibleItems : visibleItems.slice(0, 3);
  // 折叠状态下，已选中但被折叠隐藏的项不给任何提示会让用户以为筛选没生效
  // （技术方案 E5）——在"更多..."按钮上报个数，而不是静默隐藏。
  const hiddenSelectedCount =
    !expanded && !searchActive
      ? selectedItems.filter((s) => !visibleItems.slice(0, 3).includes(s)).length
      : 0;

  return (
    <div className="space-y-2 pl-0.5">
      {searchable && items.length > 8 && (
        <input
          type="text"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="搜索…"
          className="w-full text-xs border border-gray-200 rounded px-2 py-1 focus:outline-none focus:ring-1 focus:ring-purple-400 focus:border-purple-400"
        />
      )}
      {displayItems.map((item) => {
        const isChecked = selectedItems.includes(item);
        return (
          <label
            key={item}
            className="flex items-center space-x-2.5 text-gray-600 hover:text-black cursor-pointer text-xs select-none"
          >
            <input
              type="checkbox"
              checked={isChecked}
              onChange={() => onToggle(item)}
              className="rounded border-gray-300 text-purple-600 focus:ring-purple-500 w-3.5 h-3.5 cursor-pointer accent-purple-600"
            />
            <span className={isChecked ? 'text-purple-900 font-medium' : ''}>{item}</span>
          </label>
        );
      })}
      {showMore && !searchActive && items.length > 3 && (
        <button
          type="button"
          onClick={() => setExpanded(!expanded)}
          className="text-gray-400 hover:text-purple-600 text-xs font-normal pt-0.5 block transition-colors cursor-pointer"
        >
          {expanded
            ? '收起'
            : hiddenSelectedCount > 0
            ? `更多...（${hiddenSelectedCount} 项已选）`
            : '更多...'}
        </button>
      )}
      {searchActive && visibleItems.length === 0 && (
        <p className="text-gray-400 text-xs pt-0.5">没有匹配结果</p>
      )}
    </div>
  );
};

interface SliderControlProps {
  min?: number;
  max?: number;
  val: number;
  onChange: (v: number) => void;
  ticks?: string[];
  // tickValues：每个 tick 文案对应的真实数值，用来算它在滑块上的准确位置。
  // 不传时按 ticks 在 [min,max] 里均匀分布（等价于旧行为）。
  tickValues?: number[];
  // scaleExponent > 1 时滑块用幂函数映射（value = min + (max-min)*(pos/POS_MAX)^exponent），
  // 让低值区间占据更多滑块行程——避免 context length/价格这类高度集中在
  // 低端的数据只能挤在最左侧几像素内调节（技术方案 E3）。tick 位置用同一套
  // 映射反算，天然和滑块手柄对齐（顺带修好 E2 的刻度错位问题）。
  scaleExponent?: number;
  labelMin?: string;
  labelMax?: string;
  displayValue?: string;
}

const SLIDER_POS_MAX = 1000;

export const SliderControl: React.FC<SliderControlProps> = ({
  min = 0,
  max = 100,
  val,
  onChange,
  ticks = [],
  tickValues,
  scaleExponent = 1,
  labelMin,
  labelMax,
  displayValue,
}) => {
  const scaled = scaleExponent !== 1;
  const valueToPos = (v: number) => {
    if (!scaled) return v;
    const t = Math.max((v - min) / (max - min), 0);
    return Math.pow(t, 1 / scaleExponent) * SLIDER_POS_MAX;
  };
  const posToValue = (p: number) => {
    if (!scaled) return p;
    return min + (max - min) * Math.pow(p / SLIDER_POS_MAX, scaleExponent);
  };

  const resolvedTickValues =
    tickValues ??
    ticks.map((_, i) => (ticks.length > 1 ? min + ((max - min) * i) / (ticks.length - 1) : min));

  return (
    <div className="px-1 py-1 space-y-1.5">
      {displayValue && (
        <div className="text-right text-[11px] font-semibold text-purple-600">{displayValue}</div>
      )}
      <input
        type="range"
        min={scaled ? 0 : min}
        max={scaled ? SLIDER_POS_MAX : max}
        value={scaled ? valueToPos(val) : val}
        onChange={(e) => onChange(scaled ? Math.round(posToValue(Number(e.target.value))) : Number(e.target.value))}
        className="purple-track"
      />
      {ticks.length > 0 && (
        <div className="relative h-3.5 mt-0.5">
          {ticks.map((t, i) => {
            const tv = resolvedTickValues[i];
            const pct = scaled
              ? (valueToPos(tv) / SLIDER_POS_MAX) * 100
              : ((tv - min) / (max - min || 1)) * 100;
            const align = i === 0 ? 'left-0' : i === ticks.length - 1 ? 'right-0' : undefined;
            return (
              <span
                key={t}
                style={align ? undefined : { left: `${pct}%`, transform: 'translateX(-50%)' }}
                className={`absolute text-[11px] text-gray-500 font-medium whitespace-nowrap ${align ?? ''}`}
              >
                {t}
              </span>
            );
          })}
        </div>
      )}
      {(labelMin || labelMax) && (
        <div className="flex justify-between text-[11px] text-gray-500 font-medium">
          <span>{labelMin}</span>
          <span>{labelMax}</span>
        </div>
      )}
    </div>
  );
};

interface IndexRangeSliderProps {
  minVal: string | number;
  maxVal: string | number;
  currentVal?: number;
  onChange?: (v: number) => void;
}

export const IndexRangeSlider: React.FC<IndexRangeSliderProps> = ({
  minVal,
  maxVal,
  currentVal,
  onChange,
}) => {
  const numericMax = typeof maxVal === 'string' ? parseInt(maxVal.replace(/,/g, ''), 10) || 100 : maxVal;
  const numericMin = typeof minVal === 'string' ? parseInt(minVal.replace(/,/g, ''), 10) || 0 : minVal;
  const [val, setVal] = useState(currentVal ?? numericMax);

  const handleChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const num = Number(e.target.value);
    setVal(num);
    if (onChange) onChange(num);
  };

  return (
    <div className="px-1 space-y-1">
      <div className="flex justify-between text-xs text-gray-500 font-medium mb-1">
        <span>{minVal}</span>
        <span className="text-purple-700 font-semibold">{val.toLocaleString()}</span>
      </div>
      <input
        type="range"
        min={numericMin}
        max={numericMax}
        value={val}
        onChange={handleChange}
        className="purple-track"
      />
    </div>
  );
};

interface SidebarProps {
  filters: FilterState;
  onFilterChange: (newFilters: Partial<FilterState>) => void;
  onResetFilters: () => void;
  totalFilteredCount: number;
  // Providers/Model authors 候选列表，从真实加载到的模型动态派生（技术
  // 方案 A2），不再是组件内部硬编码的字符串数组。
  availableProviders: string[];
  availableAuthors: string[];
}

export const Sidebar: React.FC<SidebarProps> = ({
  filters,
  onFilterChange,
  onResetFilters,
  totalFilteredCount,
  availableProviders,
  availableAuthors,
}) => {
  const handleToggleModality = (rawItem: string) => {
    const map: Record<string, ModalityType> = {
      Text: 'text',
      Image: 'image',
      File: 'file',
      Audio: 'audio',
      Video: 'video',
    };
    const mod = map[rawItem];
    if (!mod) return;

    const current = filters.selectedModalities;
    const next = current.includes(mod)
      ? current.filter((m) => m !== mod)
      : [...current, mod];
    onFilterChange({ selectedModalities: next });
  };

  const handleToggleSeries = (s: string) => {
    const current = filters.selectedSeries;
    const next = current.includes(s)
      ? current.filter((x) => x !== s)
      : [...current, s];
    onFilterChange({ selectedSeries: next });
  };

  const handleToggleCategory = (c: string) => {
    const current = filters.selectedCategories;
    const next = current.includes(c)
      ? current.filter((x) => x !== c)
      : [...current, c];
    onFilterChange({ selectedCategories: next });
  };

  const handleToggleParam = (p: string) => {
    const current = filters.selectedParameters;
    const next = current.includes(p)
      ? current.filter((x) => x !== p)
      : [...current, p];
    onFilterChange({ selectedParameters: next });
  };

  const handleToggleProvider = (prov: string) => {
    const current = filters.selectedProviders;
    const next = current.includes(prov)
      ? current.filter((x) => x !== prov)
      : [...current, prov];
    onFilterChange({ selectedProviders: next });
  };

  const handleToggleAuthor = (auth: string) => {
    const current = filters.selectedAuthors;
    const next = current.includes(auth)
      ? current.filter((x) => x !== auth)
      : [...current, auth];
    onFilterChange({ selectedAuthors: next });
  };

  const handleToggleRegion = (reg: 'EU' | 'US') => {
    const current = filters.selectedRegions;
    const next = current.includes(reg)
      ? current.filter((r) => r !== reg)
      : [...current, reg];
    onFilterChange({ selectedRegions: next });
  };

  return (
    <aside className="w-64 border-r border-gray-200 p-4 h-[calc(100vh-3rem)] overflow-y-auto shrink-0 select-none bg-white">
      {/* Top action row */}
      <div className="flex items-center justify-between pb-3 border-b border-gray-100">
        <span className="text-xs font-semibold text-gray-700">
          匹配模型 <span className="text-purple-600 font-bold ml-1">({totalFilteredCount})</span>
        </span>
        <button
          onClick={onResetFilters}
          className="flex items-center space-x-1 text-xs text-gray-400 hover:text-purple-600 transition-colors"
          title="重置所有筛选"
        >
          <RotateCcw className="w-3 h-3" />
          <span>重置</span>
        </button>
      </div>

      {/* 1. Input modalities */}
      <SidebarSection title="Input modalities" icon={<LogIn className="w-4 h-4" />}>
        <CheckboxGroup
          items={['Text', 'Image', 'File', 'Audio', 'Video']}
          selectedItems={filters.selectedModalities.map(
            (m) => m.charAt(0).toUpperCase() + m.slice(1)
          )}
          onToggle={handleToggleModality}
          showMore={false}
        />
      </SidebarSection>

      {/* 2. Discounted */}
      <SidebarSection title="Discounted" icon={<Gift className="w-4 h-4" />}>
        <label className="flex items-center space-x-2.5 text-gray-600 hover:text-black cursor-pointer text-xs">
          <input
            type="checkbox"
            checked={filters.hasDiscountOnly}
            onChange={(e) => onFilterChange({ hasDiscountOnly: e.target.checked })}
            className="rounded border-gray-300 text-purple-600 focus:ring-purple-500 w-3.5 h-3.5 accent-purple-600"
          />
          <span className={filters.hasDiscountOnly ? 'text-purple-900 font-medium' : ''}>
            Has a discount
          </span>
        </label>
      </SidebarSection>

      {/* 3. Context length */}
      <SidebarSection title="Context length" icon={<Cpu className="w-4 h-4" />}>
        <SliderControl
          min={0}
          max={1000}
          val={filters.minContextLength}
          onChange={(v) => onFilterChange({ minContextLength: v })}
          ticks={['4K', '64K', '1M']}
          tickValues={[4, 64, 1000]}
          scaleExponent={3}
          displayValue={filters.minContextLength > 0 ? `≥ ${filters.minContextLength}K tokens` : undefined}
        />
      </SidebarSection>

      {/* 4. Prompt pricing */}
      <SidebarSection title="Prompt pricing" icon={<DollarSign className="w-4 h-4" />}>
        <SliderControl
          min={0}
          max={15}
          val={filters.maxPromptPrice}
          onChange={(v) => onFilterChange({ maxPromptPrice: v })}
          ticks={['FREE', '$0.50', '$10+']}
          tickValues={[0, 0.5, 10]}
          scaleExponent={3}
          displayValue={filters.maxPromptPrice >= 15 ? '不限价格' : `≤ $${filters.maxPromptPrice}/M`}
        />
      </SidebarSection>

      {/* 5. Series */}
      <SidebarSection title="Series" icon={<Boxes className="w-4 h-4" />}>
        <CheckboxGroup
          items={['GPT', 'Claude', 'Gemini', 'DeepSeek', 'Meta', 'Sakana', 'Other']}
          selectedItems={filters.selectedSeries}
          onToggle={handleToggleSeries}
          showMore={true}
        />
      </SidebarSection>

      {/* 6. Categories */}
      <SidebarSection title="Categories" icon={<Tag className="w-4 h-4" />}>
        <CheckboxGroup
          items={['Programming', 'Roleplay', 'Marketing', 'Reasoning', 'Multimodal', 'Extraction', 'Audio']}
          selectedItems={filters.selectedCategories}
          onToggle={handleToggleCategory}
          showMore={true}
        />
      </SidebarSection>

      {/* 7. Supported parameters */}
      <SidebarSection title="Supported parameters" icon={<Code2 className="w-4 h-4" />}>
        <CheckboxGroup
          items={['tools', 'temperature', 'top_p', 'json_object', 'response_format']}
          selectedItems={filters.selectedParameters}
          onToggle={handleToggleParam}
          showMore={true}
        />
      </SidebarSection>

      {/* 8. Distillable */}
      <SidebarSection title="Distillable" icon={<FlaskConical className="w-4 h-4" />}>
        <div className="space-y-1.5 pl-0.5">
          {['any', 'yes', 'no'].map((mode) => (
            <label key={mode} className="flex items-center space-x-2 text-gray-600 hover:text-black cursor-pointer text-xs">
              <input
                type="radio"
                name="distillable"
                checked={filters.distillable === mode}
                onChange={() => onFilterChange({ distillable: mode as 'any' | 'yes' | 'no' })}
                className="text-purple-600 focus:ring-purple-500 accent-purple-600 w-3.5 h-3.5"
              />
              <span className="capitalize">{mode === 'any' ? '全部' : mode === 'yes' ? 'Yes' : 'No'}</span>
            </label>
          ))}
        </div>
        <p className="text-[10px] text-gray-400 pt-1">
          此项支持排除不满足条件的模型；下方其余"是否类"筛选仅支持"只看满足项"。
        </p>
      </SidebarSection>

      {/* 9. Zero data retention */}
      <SidebarSection title="Zero data retention" icon={<ShieldCheck className="w-4 h-4" />}>
        <label className="flex items-center space-x-2.5 text-gray-600 hover:text-black cursor-pointer text-xs">
          <input
            type="checkbox"
            checked={filters.zeroDataRetentionOnly}
            onChange={(e) => onFilterChange({ zeroDataRetentionOnly: e.target.checked })}
            className="rounded border-gray-300 text-purple-600 focus:ring-purple-500 w-3.5 h-3.5 accent-purple-600"
          />
          <span>Supported</span>
        </label>
      </SidebarSection>

      {/* 10. In-region routing */}
      <SidebarSection title="In-region routing" icon={<Globe className="w-4 h-4" />} hasInfo={true} infoText="选择数据流向支持的地理区域">
        <div className="space-y-2 pl-0.5">
          {(['EU', 'US'] as const).map((region) => (
            <label key={region} className="flex items-center space-x-2.5 text-gray-600 hover:text-black cursor-pointer text-xs">
              <input
                type="checkbox"
                checked={filters.selectedRegions.includes(region)}
                onChange={() => handleToggleRegion(region)}
                className="rounded border-gray-300 text-purple-600 focus:ring-purple-500 w-3.5 h-3.5 accent-purple-600"
              />
              <span>{region}</span>
            </label>
          ))}
        </div>
      </SidebarSection>

      {/* 11. Output pricing */}
      <SidebarSection title="Output pricing" icon={<DollarSign className="w-4 h-4" />}>
        <SliderControl
          min={0}
          max={60}
          val={filters.maxOutputPrice}
          onChange={(v) => onFilterChange({ maxOutputPrice: v })}
          ticks={['FREE', '$2', '$30+']}
          tickValues={[0, 2, 30]}
          scaleExponent={3}
          displayValue={filters.maxOutputPrice >= 60 ? '不限价格' : `≤ $${filters.maxOutputPrice}/M`}
        />
      </SidebarSection>

      {/* 12. Model age */}
      <SidebarSection title="Model age" icon={<Calendar className="w-4 h-4" />}>
        <SliderControl
          min={1}
          max={12}
          val={filters.maxModelAgeMonths}
          onChange={(v) => onFilterChange({ maxModelAgeMonths: v })}
          labelMin="New"
          labelMax="12+ mo"
          displayValue={filters.maxModelAgeMonths >= 12 ? '全部发布时间' : `≤ ${filters.maxModelAgeMonths} 个月内`}
        />
      </SidebarSection>

      {/* 13. Tool calling */}
      <SidebarSection title="Tool calling" icon={<Wrench className="w-4 h-4" />} hasInfo={true} infoText="工具调用评测支持度阈值">
        <SliderControl
          min={0}
          max={100}
          val={filters.minToolCalling}
          onChange={(v) => onFilterChange({ minToolCalling: v })}
          labelMin="0%"
          labelMax="100%"
          displayValue={filters.minToolCalling > 0 ? `≥ ${filters.minToolCalling}%` : undefined}
        />
      </SidebarSection>

      {/* 14. Inactive models */}
      <SidebarSection title="Inactive models" icon={<Archive className="w-4 h-4" />}>
        <label className="flex items-center space-x-2.5 text-gray-600 hover:text-black cursor-pointer text-xs">
          <input
            type="checkbox"
            checked={filters.showDeprecated}
            onChange={(e) => onFilterChange({ showDeprecated: e.target.checked })}
            className="rounded border-gray-300 text-purple-600 focus:ring-purple-500 w-3.5 h-3.5 accent-purple-600"
          />
          <span>Show deprecated</span>
        </label>
      </SidebarSection>

      {/* 15. Artificial Analysis Indexes */}
      <SidebarSection title="Artificial Analysis" icon={<AreaChart className="w-4 h-4" />}>
        <p className="text-[10px] text-gray-400 -mt-1">0–100 百分位评分，数值越高越强</p>
        <div className="space-y-4 pt-1">
          <div>
            <div className="flex items-center justify-between text-xs font-semibold text-gray-700 mb-1">
              <div className="flex items-center space-x-1">
                <BarChart2 className="w-3.5 h-3.5 text-gray-500" />
                <span>Intelligence Index</span>
              </div>
              <span title="综合智能水平评测得分（0–100，来自 Artificial Analysis 基准）"><Info className="w-3 h-3 text-gray-400" /></span>
            </div>
            <IndexRangeSlider
              minVal="0"
              maxVal="54"
              currentVal={filters.minIntelligenceIndex}
              onChange={(v) => onFilterChange({ minIntelligenceIndex: v })}
            />
          </div>

          <div>
            <div className="flex items-center justify-between text-xs font-semibold text-gray-700 mb-1">
              <div className="flex items-center space-x-1">
                <Code2 className="w-3.5 h-3.5 text-gray-500" />
                <span>Coding Index</span>
              </div>
              <span title="编程能力评测得分（0–100，来自 Artificial Analysis 基准）"><Info className="w-3 h-3 text-gray-400" /></span>
            </div>
            <IndexRangeSlider
              minVal="0"
              maxVal="82"
              currentVal={filters.minCodingIndex}
              onChange={(v) => onFilterChange({ minCodingIndex: v })}
            />
          </div>

          <div>
            <div className="flex items-center justify-between text-xs font-semibold text-gray-700 mb-1">
              <div className="flex items-center space-x-1">
                <Cpu className="w-3.5 h-3.5 text-gray-500" />
                <span>Agentic Index</span>
              </div>
              <span title="自主任务执行（Agent）能力评测得分（0–100，来自 Artificial Analysis 基准）"><Info className="w-3 h-3 text-gray-400" /></span>
            </div>
            <IndexRangeSlider
              minVal="0"
              maxVal="58"
              currentVal={filters.minAgenticIndex}
              onChange={(v) => onFilterChange({ minAgenticIndex: v })}
            />
          </div>
        </div>
      </SidebarSection>

      {/* 16. Design Arena */}
      <SidebarSection title="Design Arena" icon={<PenTool className="w-4 h-4" />}>
        <p className="text-[10px] text-gray-400 -mt-1">Elo 原始评分（非百分制，量级与上方指数不同）</p>
        <div className="space-y-3.5 pt-1">
          <div>
            <div className="flex items-center justify-between text-xs text-gray-700 font-semibold mb-1">
              <div className="flex items-center space-x-1">
                <Code2 className="w-3.5 h-3.5 text-gray-500" />
                <span>Code Categories</span>
              </div>
              <span title="代码生成类任务的 Design Arena Elo 评分（非百分制）"><Info className="w-3 h-3 text-gray-400" /></span>
            </div>
            <IndexRangeSlider
              minVal="0"
              maxVal="1,387"
              currentVal={filters.minDesignArenaCode}
              onChange={(v) => onFilterChange({ minDesignArenaCode: v })}
            />
          </div>

          <div>
            <div className="flex items-center justify-between text-xs text-gray-700 font-semibold mb-1">
              <div className="flex items-center space-x-1">
                <Layout className="w-3.5 h-3.5 text-gray-500" />
                <span>UI Component</span>
              </div>
              <span title="UI 组件生成类任务的 Design Arena Elo 评分（非百分制）"><Info className="w-3 h-3 text-gray-400" /></span>
            </div>
            <IndexRangeSlider
              minVal="0"
              maxVal="1,389"
              currentVal={filters.minDesignArenaUI}
              onChange={(v) => onFilterChange({ minDesignArenaUI: v })}
            />
          </div>

          <div>
            <div className="flex items-center justify-between text-xs text-gray-700 font-semibold mb-1">
              <div className="flex items-center space-x-1">
                <Gamepad2 className="w-3.5 h-3.5 text-gray-500" />
                <span>Game Development</span>
              </div>
              <span title="游戏开发类任务的 Design Arena Elo 评分（非百分制）"><Info className="w-3 h-3 text-gray-400" /></span>
            </div>
            <IndexRangeSlider
              minVal="0"
              maxVal="1,413"
              currentVal={filters.minDesignArenaGame}
              onChange={(v) => onFilterChange({ minDesignArenaGame: v })}
            />
          </div>

          <div>
            <div className="flex items-center justify-between text-xs text-gray-700 font-semibold mb-1">
              <div className="flex items-center space-x-1">
                <LineChart className="w-3.5 h-3.5 text-gray-500" />
                <span>Data Visualization</span>
              </div>
              <span title="数据可视化类任务的 Design Arena Elo 评分（非百分制）"><Info className="w-3 h-3 text-gray-400" /></span>
            </div>
            <IndexRangeSlider
              minVal="0"
              maxVal="1,366"
              currentVal={filters.minDesignArenaDataViz}
              onChange={(v) => onFilterChange({ minDesignArenaDataViz: v })}
            />
          </div>

          <div>
            <div className="flex items-center justify-between text-xs text-gray-700 font-semibold mb-1">
              <div className="flex items-center space-x-1">
                <Box className="w-3.5 h-3.5 text-gray-500" />
                <span>3D</span>
              </div>
              <span title="3D 内容生成类任务的 Design Arena Elo 评分（非百分制）"><Info className="w-3 h-3 text-gray-400" /></span>
            </div>
            <IndexRangeSlider
              minVal="0"
              maxVal="1,432"
              currentVal={filters.minDesignArena3D}
              onChange={(v) => onFilterChange({ minDesignArena3D: v })}
            />
          </div>

          <div>
            <div className="flex items-center justify-between text-xs text-gray-700 font-semibold mb-1">
              <div className="flex items-center space-x-1">
                <ImageIcon className="w-3.5 h-3.5 text-gray-500" />
                <span>Image</span>
              </div>
              <span title="图像生成类任务的 Design Arena Elo 评分（非百分制）"><Info className="w-3 h-3 text-gray-400" /></span>
            </div>
            <IndexRangeSlider
              minVal="0"
              maxVal="1,385"
              currentVal={filters.minDesignArenaImage}
              onChange={(v) => onFilterChange({ minDesignArenaImage: v })}
            />
          </div>

          <div>
            <div className="flex items-center justify-between text-xs text-gray-700 font-semibold mb-1">
              <div className="flex items-center space-x-1">
                <Video className="w-3.5 h-3.5 text-gray-500" />
                <span>Video</span>
              </div>
              <span title="视频生成类任务的 Design Arena Elo 评分（非百分制）"><Info className="w-3 h-3 text-gray-400" /></span>
            </div>
            <IndexRangeSlider
              minVal="0"
              maxVal="2,000"
              currentVal={filters.minDesignArenaVideo}
              onChange={(v) => onFilterChange({ minDesignArenaVideo: v })}
            />
          </div>

          <div>
            <div className="flex items-center justify-between text-xs text-gray-700 font-semibold mb-1">
              <div className="flex items-center space-x-1">
                <Feather className="w-3.5 h-3.5 text-gray-500" />
                <span>SVG</span>
              </div>
              <span title="SVG 矢量图生成类任务的 Design Arena Elo 评分（非百分制）"><Info className="w-3 h-3 text-gray-400" /></span>
            </div>
            <IndexRangeSlider
              minVal="0"
              maxVal="1,352"
              currentVal={filters.minDesignArenaSVG}
              onChange={(v) => onFilterChange({ minDesignArenaSVG: v })}
            />
          </div>
        </div>
      </SidebarSection>

      {/* 17. Providers */}
      <SidebarSection title="Providers" icon={<Landmark className="w-4 h-4" />}>
        <CheckboxGroup
          items={availableProviders}
          selectedItems={filters.selectedProviders}
          onToggle={handleToggleProvider}
          showMore={true}
          searchable={true}
        />
      </SidebarSection>

      {/* 18. Model authors */}
      <SidebarSection title="Model authors" icon={<UserIcon className="w-4 h-4" />}>
        <CheckboxGroup
          items={availableAuthors}
          selectedItems={filters.selectedAuthors}
          onToggle={handleToggleAuthor}
          showMore={true}
          searchable={true}
        />
      </SidebarSection>
    </aside>
  );
};
