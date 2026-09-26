export type ModalityType = 'text' | 'image' | 'file' | 'audio' | 'video';

export interface ModelScores {
  intelligenceIndex: number;
  codingIndex: number;
  agenticIndex: number;
  designArena?: {
    codeCategories?: number;
    uiComponent?: number;
    gameDev?: number;
    dataViz?: number;
    threeD?: number;
    image?: number;
    video?: number;
    svg?: number;
  };
}

export interface Model {
  id: string;
  name: string;
  provider: string;
  providerDisplay: string;
  author: string;
  series: 'GPT' | 'Claude' | 'Gemini' | 'DeepSeek' | 'Meta' | 'Mistral' | 'Sakana' | 'Other';
  description: string;
  iconBg: string;
  badge?: string | null;
  badgeColor?: string;
  date: string;
  releaseDate: string;
  contextTokens: number;
  contextDisplay: string | null;
  maxOutputTokens: number;
  inputPricePerM: number;
  outputPricePerM: number;
  inputPriceDisplay: string;
  outputPriceDisplay: string | null;
  isHourly?: boolean;
  tokensDisplay?: string | null;
  modalities: ModalityType[];
  category: string;
  tags: string[];
  variants: ('standard' | 'free' | 'extended' | 'thinking' | 'batch')[];
  hasDiscount?: boolean;
  discountPercent?: number;
  distillable: boolean;
  zeroDataRetention: boolean;
  inRegionRouting: ('EU' | 'US')[];
  supportedParameters: string[];
  toolCallingCapability: number; // 0 to 100%
  modelAgeMonths: number;
  isDeprecated?: boolean;
  scores: ModelScores;
}

export interface FilterState {
  searchQuery: string;
  selectedModalities: ModalityType[];
  hasDiscountOnly: boolean;
  minContextLength: number; // in thousands (e.g. 0 to 1000K)
  maxPromptPrice: number; // in $ (e.g. 0 to 15, where 15 is $10+)
  maxOutputPrice: number; // in $ (e.g. 0 to 35)
  selectedSeries: string[];
  selectedCategories: string[];
  selectedParameters: string[];
  distillable: 'any' | 'yes' | 'no';
  zeroDataRetentionOnly: boolean;
  selectedRegions: ('EU' | 'US')[];
  maxModelAgeMonths: number;
  minToolCalling: number;
  showDeprecated: boolean;
  selectedProviders: string[];
  selectedAuthors: string[];
  selectedVariant: string;
  selectedPrimaryTag: string;
  pinnedModelIds: string[];
  minIntelligenceIndex: number;
  minCodingIndex: number;
  minAgenticIndex: number;
}

export type SortOption =
  | 'newest'
  | 'price-asc'
  | 'price-desc'
  | 'context-desc'
  | 'intelligence-desc'
  | 'popular';

export type ViewMode = 'list' | 'table';
