import { request } from './client';

export interface RemoteModel {
  id: string;
  object: string;
  created?: number;
  owned_by?: string;
}

interface ListModelsResponse {
  object: string;
  data: RemoteModel[];
}

// listModels 调用 GET /v1/models（需要 API Key），返回该账户当前能看到、能
// 调用的虚拟模型 id 列表——真实数据，不是 data/models.ts 里的 mock 目录。
export async function listModels(apiKey: string): Promise<RemoteModel[]> {
  const res = await request<ListModelsResponse>('/v1/models', { apiKey });
  return res.data;
}
