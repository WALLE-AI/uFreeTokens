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
}

export const CheckboxGroup: React.FC<CheckboxGroupProps> = ({
  items,
  selectedItems = [],
  onToggle,
  showMore = true,
}) => {
  const [expanded, setExpanded] = useState(!showMore);
  const displayItems = expanded ? items : items.slice(0, 3);

  return (
    <div className="space-y-2 pl-0.5">
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
      {showMore && items.length > 3 && (
        <button
          type="button"
          onClick={() => setExpanded(!expanded)}
          className="text-gray-400 hover:text-purple-600 text-xs font-normal pt-0.5 block transition-colors cursor-pointer"
        >
          {expanded ? '收起' : '更多...'}
        </button>
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
  labelMin?: string;
  labelMax?: string;
  displayValue?: string;
}

export const SliderControl: React.FC<SliderControlProps> = ({
  min = 0,
  max = 100,
  val,
  onChange,
  ticks = [],
  labelMin,
  labelMax,
  displayValue,
}) => {
  return (
    <div className="px-1 py-1 space-y-1.5">
      {displayValue && (
        <div className="text-right text-[11px] font-semibold text-purple-600">{displayValue}</div>
      )}
      <input
        type="range"
        min={min}
        max={max}
        value={val}
        onChange={(e) => onChange(Number(e.target.value))}
        className="purple-track"
      />
      {ticks.length > 0 && (
        <div>
          <div className="flex justify-between px-0.5 text-[10px] text-gray-300">
            {ticks.map((_, i) => (
              <span key={i}>|</span>
            ))}
          </div>
          <div className="flex justify-between text-[11px] text-gray-500 font-medium mt-0.5">
            {ticks.map((t) => (
              <span key={t}>{t}</span>
            ))}
          </div>
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
}

export const Sidebar: React.FC<SidebarProps> = ({
  filters,
  onFilterChange,
  onResetFilters,
  totalFilteredCount,
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
          筛选条件 <span className="text-purple-600 font-bold ml-1">({totalFilteredCount})</span>
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
        <div className="space-y-4 pt-1">
          <div>
            <div className="flex items-center justify-between text-xs font-semibold text-gray-700 mb-1">
              <div className="flex items-center space-x-1">
                <BarChart2 className="w-3.5 h-3.5 text-gray-500" />
                <span>Intelligence Index</span>
              </div>
              <Info className="w-3 h-3 text-gray-400" />
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
              <Info className="w-3 h-3 text-gray-400" />
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
              <Info className="w-3 h-3 text-gray-400" />
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
        <div className="space-y-3.5 pt-1">
          <div>
            <div className="flex items-center justify-between text-xs text-gray-700 font-semibold mb-1">
              <div className="flex items-center space-x-1">
                <Code2 className="w-3.5 h-3.5 text-gray-500" />
                <span>Code Categories</span>
              </div>
              <Info className="w-3 h-3 text-gray-400" />
            </div>
            <IndexRangeSlider minVal="0" maxVal="1,387" />
          </div>

          <div>
            <div className="flex items-center justify-between text-xs text-gray-700 font-semibold mb-1">
              <div className="flex items-center space-x-1">
                <Layout className="w-3.5 h-3.5 text-gray-500" />
                <span>UI Component</span>
              </div>
              <Info className="w-3 h-3 text-gray-400" />
            </div>
            <IndexRangeSlider minVal="0" maxVal="1,389" />
          </div>

          <div>
            <div className="flex items-center justify-between text-xs text-gray-700 font-semibold mb-1">
              <div className="flex items-center space-x-1">
                <Gamepad2 className="w-3.5 h-3.5 text-gray-500" />
                <span>Game Development</span>
              </div>
              <Info className="w-3 h-3 text-gray-400" />
            </div>
            <IndexRangeSlider minVal="0" maxVal="1,413" />
          </div>

          <div>
            <div className="flex items-center justify-between text-xs text-gray-700 font-semibold mb-1">
              <div className="flex items-center space-x-1">
                <LineChart className="w-3.5 h-3.5 text-gray-500" />
                <span>Data Visualization</span>
              </div>
              <Info className="w-3 h-3 text-gray-400" />
            </div>
            <IndexRangeSlider minVal="0" maxVal="1,366" />
          </div>

          <div>
            <div className="flex items-center justify-between text-xs text-gray-700 font-semibold mb-1">
              <div className="flex items-center space-x-1">
                <Box className="w-3.5 h-3.5 text-gray-500" />
                <span>3D</span>
              </div>
              <Info className="w-3 h-3 text-gray-400" />
            </div>
            <IndexRangeSlider minVal="0" maxVal="1,432" />
          </div>

          <div>
            <div className="flex items-center justify-between text-xs text-gray-700 font-semibold mb-1">
              <div className="flex items-center space-x-1">
                <ImageIcon className="w-3.5 h-3.5 text-gray-500" />
                <span>Image</span>
              </div>
              <Info className="w-3 h-3 text-gray-400" />
            </div>
            <IndexRangeSlider minVal="0" maxVal="1,385" />
          </div>

          <div>
            <div className="flex items-center justify-between text-xs text-gray-700 font-semibold mb-1">
              <div className="flex items-center space-x-1">
                <Video className="w-3.5 h-3.5 text-gray-500" />
                <span>Video</span>
              </div>
              <Info className="w-3 h-3 text-gray-400" />
            </div>
            <IndexRangeSlider minVal="0" maxVal="2,000" />
          </div>

          <div>
            <div className="flex items-center justify-between text-xs text-gray-700 font-semibold mb-1">
              <div className="flex items-center space-x-1">
                <Feather className="w-3.5 h-3.5 text-gray-500" />
                <span>SVG</span>
              </div>
              <Info className="w-3 h-3 text-gray-400" />
            </div>
            <IndexRangeSlider minVal="0" maxVal="1,352" />
          </div>
        </div>
      </SidebarSection>

      {/* 17. Providers */}
      <SidebarSection title="Providers" icon={<Landmark className="w-4 h-4" />}>
        <CheckboxGroup
          items={['AI21', 'AionLabs', 'AkashML', 'anthropic', 'deepseek', 'google', 'inference.net', 'meta', 'openai', 'sakana', 'union']}
          selectedItems={filters.selectedProviders}
          onToggle={handleToggleProvider}
          showMore={true}
        />
      </SidebarSection>

      {/* 18. Model authors */}
      <SidebarSection title="Model authors" icon={<UserIcon className="w-4 h-4" />}>
        <CheckboxGroup
          items={['aion-labs', 'alibaba', 'amazon', 'anthropic', 'deepseek', 'google', 'meta', 'openai', 'sakana', 'union-labs']}
          selectedItems={filters.selectedAuthors}
          onToggle={handleToggleAuthor}
          showMore={true}
        />
      </SidebarSection>
    </aside>
  );
};
