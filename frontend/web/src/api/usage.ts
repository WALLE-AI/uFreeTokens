import { request } from './client';

export interface UsageWallet {
  cashBalanceMicro: number;
  bonusBalanceMicro: number;
  frozenMicro: number;
}

export interface UsageTotals {
  totalRequests: number;
  totalInputTokens: number;
  totalOutputTokens: number;
  totalChargedAmountMicro: number;
}

export interface UsageSnapshot {
  wallet: UsageWallet;
  usage: UsageTotals;
}

interface RawUsageResponse {
  wallet: {
    cash_balance_micro: number;
    bonus_balance_micro: number;
    frozen_micro: number;
  };
  usage: {
    total_requests: number;
    total_input_tokens: number;
    total_output_tokens: number;
    total_charged_amount_micro: number;
  };
}

// getUsage 调用 GET /v1/usage（需要 API Key）：钱包余额 + 累计用量的自助
// 查询快照，全是微元/token 整数——不在这里转换成"元"，展示层用
// microToDisplay。
export async function getUsage(apiKey: string): Promise<UsageSnapshot> {
  const raw = await request<RawUsageResponse>('/v1/usage', { apiKey });
  return {
    wallet: {
      cashBalanceMicro: raw.wallet.cash_balance_micro,
      bonusBalanceMicro: raw.wallet.bonus_balance_micro,
      frozenMicro: raw.wallet.frozen_micro,
    },
    usage: {
      totalRequests: raw.usage.total_requests,
      totalInputTokens: raw.usage.total_input_tokens,
      totalOutputTokens: raw.usage.total_output_tokens,
      totalChargedAmountMicro: raw.usage.total_charged_amount_micro,
    },
  };
}

// microToDisplay 把微元金额转成带货币符号的人类可读字符串。技术方案规定
// 对外售价统一 CNY（1 元 = 1,000,000 微元），默认按 CNY 展示；预留 USD 只是
// 为了兼容个别历史文案，不代表后端真的支持多币种结算。
export function microToDisplay(micro: number, currency: 'CNY' | 'USD' = 'CNY'): string {
  const symbol = currency === 'CNY' ? '¥' : '$';
  const amount = micro / 1_000_000;
  return `${symbol}${amount.toFixed(4)}`;
}
