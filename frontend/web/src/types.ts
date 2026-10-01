export type ModalityType = 'text' | 'image' | 'file' | 'audio' | 'video';

export interface ModelScores {
  intelligenceIndex: number;
  codingIndex: number;
  agenticIndex: number;
  // 以下是后端从公开评测榜单自动投影出来的成绩（GET /v1/catalog 的 scores，
  // snake_case 键，取该模型最好的变体）；没有成绩时为 undefined。
  // LMArena Elo 评分（约 1000–1600）。
  arenaText?: number;
  arenaChinese?: number;
  arenaCoding?: number;
  arenaWebdev?: number;
  arenaVision?: number;
  // 百分制（0–100）。
  gpqaDiamond?: number;
  sweBenchVerified?: number;
  hle?: number;
  terminalBench?: number;
  aiderPolyglot?: number;
  arcAgi2?: number;
  livebench?: number;
  // Epoch Capabilities Index（约 100–170）。
  epochEci?: number;
  opencompass?: number;
  superclue?: number;
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
  // isCallable 标记这个模型是否在已连接账户的 GET /v1/models 结果里真实存在——
  // 也就是这把 API Key 实际能调用它，而不是纯 mock 展示数据。未连接 Key 时
  // 始终是 undefined（见 App.tsx 的模型列表合并逻辑）。
  isCallable?: boolean;
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
  minDesignArenaCode: number;
  minDesignArenaUI: number;
  minDesignArenaGame: number;
  minDesignArenaDataViz: number;
  minDesignArena3D: number;
  minDesignArenaImage: number;
  minDesignArenaVideo: number;
  minDesignArenaSVG: number;
}

export type SortOption =
  | 'newest'
  | 'price-asc'
  | 'price-desc'
  | 'context-desc'
  | 'intelligence-desc'
  | 'popular';

export type ViewMode = 'grid' | 'list' | 'table';
