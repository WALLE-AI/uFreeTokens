package app

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/WALLE-AI/uFreeTokens/internal/auth"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
)

// usageWallet 是钱包余额的只读快照，单位统一是"微元"（1 元 = 1,000,000，
// 见 migrations/00006_billing_wallet.sql），和 internal/pricing、
// internal/wallet 全程一致，不在这里转换成"元"——转换成人类可读的"元"是
// 前端展示层的事，后端接口保持和其它计费相关字段同样的整数微元单位，避免
// 引入浮点误差。
type usageWallet struct {
	CashBalanceMicro  int64 `json:"cash_balance_micro"`
	BonusBalanceMicro int64 `json:"bonus_balance_micro"`
	FrozenMicro       int64 `json:"frozen_micro"`
}

// usageTotals 是该账户全部成功请求的累计用量——目前是全量聚合（没有时间
// 窗口/分页），账户量级到了需要分页/按天聚合的程度时应该改成走
// internal/chsync 同步出去的分析型存储查，不应该在这条热路径的同一张
// request_logs 主表上做无限增长的全表聚合；这个接口本身也是给 test_web
// 手工联调用的自助查询，不是设计给生产控制台的用量报表。
type usageTotals struct {
	TotalRequests           int64 `json:"total_requests"`
	TotalInputTokens        int64 `json:"total_input_tokens"`
	TotalOutputTokens       int64 `json:"total_output_tokens"`
	TotalChargedAmountMicro int64 `json:"total_charged_amount_micro"`
}

// usageHandler 是"我（这把 API Key 对应的账户）到现在一共花了多少钱、用了
// 多少 token"这个自助查询的 HTTP 入口——钱包余额直接查 wallets 表，累计用量
// 从 request_logs 按 account_id 聚合（status='success'，和
// internal/reconcile.CheckLedgerVsRequestLogs 用的同一个"成功请求"口径）。
// 只读，不写任何表。
func usageHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.FromContext(r.Context())
		if !ok {
			httpx.WriteError(w, r, http.StatusUnauthorized, "invalid_api_key", "Invalid API key.")
			return
		}

		var wallet usageWallet
		if err := pool.QueryRow(r.Context(),
			`SELECT cash_balance, bonus_balance, frozen FROM wallets WHERE account_id = $1`,
			principal.AccountID,
		).Scan(&wallet.CashBalanceMicro, &wallet.BonusBalanceMicro, &wallet.FrozenMicro); err != nil {
			httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Failed to load wallet.")
			return
		}

		var totals usageTotals
		if err := pool.QueryRow(r.Context(),
			`SELECT COUNT(*), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0), COALESCE(SUM(charged_amount), 0)
			 FROM request_logs WHERE account_id = $1 AND status = 'success'`,
			principal.AccountID,
		).Scan(&totals.TotalRequests, &totals.TotalInputTokens, &totals.TotalOutputTokens, &totals.TotalChargedAmountMicro); err != nil {
			httpx.WriteError(w, r, http.StatusInternalServerError, "internal_error", "Failed to load usage totals.")
			return
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]any{"wallet": wallet, "usage": totals})
	}
}
