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
  'price_source.create': '创建数据源',
  'price_source.update': '修改数据源',
  'price_source.run': '立即运行数据源',
  'agent.decision': '审批智能体提案',
  'agent_job.update': '修改智能作业',
  'agent_job.run': '立即运行智能作业',
  'upstream_offer.status': '处理优惠情报',
  'upstream_offer.adopt': '采用优惠情报',
  'model_alias.set': '设置榜单模型映射',
  'price_change.approve': '批准调价',
  'price_change.reject': '驳回调价',
  'listing.publish': '上架新模型',
  'listing.dismiss': '忽略待上架模型',
  'benchmark.create': '创建基准测试',
  'benchmark.update': '修改基准测试',
  'benchmark_run.create': '录入基准测试 run',
  'benchmark_run.publish': '发布基准测试 run',
  'benchmark_run.delete': '删除基准测试 run',
  'public_app_rule.create': '新增公开应用榜规则',
  'public_app_rule.delete': '删除公开应用榜规则',
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
  { value: 'price_source.', label: '数据源' },
  { value: 'upstream_offer.', label: '优惠雷达' },
  { value: 'model_alias.', label: '榜单模型映射' },
  { value: 'benchmark', label: '基准测试' },
  { value: 'public_app_rule.', label: '公开应用榜' },
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
  price_source: '数据源',
  upstream_offer: '优惠情报',
  model_alias: '榜单模型映射',
  fx_rate: '汇率',
  benchmark: '基准测试',
  benchmark_run: '基准测试 run',
  public_app_rule: '公开应用榜规则',
  agent_tool_call: '智能体提案',
  agent_job: '智能作业',
};

export function actionLabel(action: string): string {
  return ACTION_LABELS[action] ?? action;
}

export function targetLabel(targetType: string, targetId: string): string {
  // fx_rate 的 id 是币种对，model_alias 的 id 是 "命名空间:原始模型名"，都不是数字
  const plain = targetType === 'fx_rate' || targetType === 'model_alias' || targetType === 'agent_tool_call';
  return `${TARGET_LABELS[targetType] ?? targetType} ${plain ? targetId : `#${targetId}`}`;
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
    case 'benchmark':
      return `/benchmarks/${targetId}`;
    case 'public_app_rule':
      return '/public-apps';
    case 'price_change_request':
      return `/pricing/changes?id=${targetId}`;
    case 'pending_model_listing':
      return '/pricing/listings';
    case 'price_source':
    case 'fx_rate':
      return '/pricing/sources';
    case 'upstream_offer':
      return `/pricing/offers?status=all&id=${targetId}`;
    case 'model_alias': {
      // target_id = 命名空间:原始模型名（原始名本身可能含冒号，只按第一个冒号切）
      const i = targetId.indexOf(':');
      if (i < 0) return '/catalog/model-aliases';
      const qs = new URLSearchParams({ namespace: targetId.slice(0, i), q: targetId.slice(i + 1), status: 'all' });
      return `/catalog/model-aliases?${qs.toString()}`;
    }
    default:
      return null;
  }
}

export function actorLabel(e: Pick<AuditLogEntry, 'actor_name' | 'actor_id'>): string {
  if (e.actor_name) return e.actor_name;
  return e.actor_id ? `#${e.actor_id}` : '未知';
}
