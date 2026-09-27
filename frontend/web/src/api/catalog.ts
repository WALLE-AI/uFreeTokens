import { request } from './client';

export interface CatalogSellPriceComponent {
  meter: string;
  unit: string;
  unitPrice: number;
}

export interface CatalogSellPrice {
  currency: string;
  components: CatalogSellPriceComponent[];
}

// CatalogModel 是 GET /v1/catalog（免鉴权公开目录，迭代5/6）单条模型的
// 前端形状。displayName/description/providerDisplay/tags/scores 都是运营
// 录入的可选元数据，缺失时是 undefined，不是空字符串——调用方要能区分
// "没录入过"和"录入了空值"。
export interface CatalogModel {
  name: string;
  family: string;
  type: string;
  contextWindow: number;
  maxOutput: number;
  capabilities: string[];
  sellPrice: CatalogSellPrice | null;
  displayName?: string;
  description?: string;
  providerDisplay?: string;
  tags?: string[];
  scores?: Record<string, number>;
  status: 'active' | 'deprecated';
}

interface RawSellPriceComponent {
  meter: string;
  unit: string;
  unit_price: string;
}

interface RawSellPrice {
  currency: string;
  components: RawSellPriceComponent[];
}

interface RawCatalogModel {
  name: string;
  family: string;
  type: string;
  context_window: number;
  max_output: number;
  capabilities: string[];
  sell_price?: RawSellPrice | null;
  display_name?: string;
  description?: string;
  provider_display?: string;
  tags?: string[];
  scores?: Record<string, number>;
  status: 'active' | 'deprecated';
}

function mapModel(raw: RawCatalogModel): CatalogModel {
  return {
    name: raw.name,
    family: raw.family,
    type: raw.type,
    contextWindow: raw.context_window,
    maxOutput: raw.max_output,
    capabilities: raw.capabilities ?? [],
    sellPrice: raw.sell_price
      ? {
          currency: raw.sell_price.currency,
          components: raw.sell_price.components.map((c) => ({
            meter: c.meter,
            unit: c.unit,
            unitPrice: Number(c.unit_price),
          })),
        }
      : null,
    displayName: raw.display_name,
    description: raw.description,
    providerDisplay: raw.provider_display,
    tags: raw.tags,
    scores: raw.scores,
    status: raw.status,
  };
}

// listCatalog 调用 GET /v1/catalog——不需要 API Key，匿名访客也能看到真实的
// 模型库（技术方案迭代6：模型库改为以这个接口为主数据源）。
export async function listCatalog(): Promise<CatalogModel[]> {
  const raw = await request<{ object: string; data: RawCatalogModel[] }>('/v1/catalog');
  return raw.data.map(mapModel);
}
