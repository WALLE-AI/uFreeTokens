import type { AuditLogEntry } from '../types';

// 审计日志的人话描述与跳转（UI_DESIGN.md §5.7）。action 与 target_type 的取值
// 以 internal/app 里 recordAudit 的调用为准，新增审计点时在这里补一行。

export const ACTION_LABELS: Record<string, string> = {
  'account.create': '创建账户',
  'account.update': '修改账户',
  'api_key.create': '创建 API 密钥',
  'api_key.revoke': '吊销 API 密钥',
  'wallet.adjust': '人工调账',
  'wallet.credit_grant': '发放赠送余额',
  'provider.create': '创建供应商',
  'provider.update': '修改供应商',
  'provider_account.create': '创建上游账号',
  'provider_account.update': '修改上游账号',
  'provider_key.add': '添加上游密钥',
  'provider_key.update': '修改上游密钥',
  'provider_key.revoke': '吊销上游密钥',
  'virtual_model.create': '创建虚拟模型',
  'virtual_model.update': '修改虚拟模型',
  'virtual_model_metadata.set': '更新展示元数据',
  'channel.create': '创建渠道',
  'channel.update': '修改渠道',
  'sell_price.set': '发布售价',
  'cost_price.set': '发布成本价',
  'fx_rate.set': '设置汇率',
  'price_source.create': '创建价格源',
  'price_source.update': '修改价格源',
  'price_change.approve': '批准调价',
  'price_change.reject': '驳回调价',
  'listing.publish': '上架新模型',
  'listing.dismiss': '忽略待上架模型',
};

// 动作前缀分组，供审计页的"动作类型"筛选使用（后端 action 参数是前缀匹配）
export const ACTION_GROUPS: Array<{ value: string; label: string }> = [
  { value: 'price_change.', label: '调价审批' },
  { value: 'wallet.', label: '资金' },
  { value: 'sell_price.', label: '售价' },
  { value: 'cost_price.', label: '成本价' },
  { value: 'channel.', label: '渠道' },
  { value: 'virtual_model', label: '虚拟模型' },
  { value: 'provider', label: '供应商 / 账号 / 密钥' },
  { value: 'listing.', label: '待上架' },
  { value: 'account.', label: '账户' },
  { value: 'api_key.', label: 'API 密钥' },
  { value: 'fx_rate.', label: '汇率' },
  { value: 'price_source.', label: '价格源' },
];

export const TARGET_LABELS: Record<string, string> = {
  account: '账户',
  api_key: 'API 密钥',
  provider: '供应商',
  provider_account: '上游账号',
  provider_key: '上游密钥',
  virtual_model: '虚拟模型',
  channel: '渠道',
  price_change_request: '调价申请',
  pending_model_listing: '待上架模型',
  price_source: '价格源',
  fx_rate: '汇率',
};

export function actionLabel(action: string): string {
  return ACTION_LABELS[action] ?? action;
}

export function targetLabel(targetType: string, targetId: string): string {
  return `${TARGET_LABELS[targetType] ?? targetType} ${targetType === 'fx_rate' ? targetId : `#${targetId}`}`;
}

// targetHref：审计对象在后台里的详情页；没有对应页面时返回 null。
export function targetHref(targetType: string, targetId: string): string | null {
  switch (targetType) {
    case 'account':
      return `/accounts/${targetId}`;
    case 'provider':
      return `/providers/${targetId}`;
    case 'virtual_model':
      return `/models/${targetId}`;
    case 'channel':
      return `/channels/${targetId}`;
    case 'price_change_request':
      return `/pricing/changes?id=${targetId}`;
    case 'pending_model_listing':
      return '/pricing/listings';
    case 'price_source':
    case 'fx_rate':
      return '/pricing/sources';
    default:
      return null;
  }
}

export function actorLabel(e: Pick<AuditLogEntry, 'actor_name' | 'actor_id'>): string {
  if (e.actor_name) return e.actor_name;
  return e.actor_id ? `#${e.actor_id}` : '未知';
}
