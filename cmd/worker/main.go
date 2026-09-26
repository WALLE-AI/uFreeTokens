// Command worker 是异步任务入口（技术方案 §7.11、§7.12、§7.13）：
//   - 回收过期未结算的预扣（网关崩溃留下的"孤儿" reservation）
//   - 保持 request_logs 的未来分区存在（表按天分区，不持续补分区插入会失败）
//   - 定期做内部一致性对账（钱包余额 vs 账本流水、账本 vs 请求日志）
//   - 每分钟跑一轮风控规则引擎（internal/risk：消费速率突增自动降级 tier，
//     同 IP 多账户聚集只上报不处置）
//
// 尚未实现：价格同步调度（§7.16，见 internal/pricesync 包文档）。账单级对账
// （internal/reconcile.CheckBillingDrift）已经有框架，但没有接进这个循环——
// 调用它需要一个真实的 BillingFetcher 实现和要扫描哪些 provider_account，
// 这两者目前都不存在（只有 mock fetcher 用于测试），接一个假的调用点不会
// 产生任何真实的对账能力。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/WALLE-AI/uFreeTokens/internal/config"
	"github.com/WALLE-AI/uFreeTokens/internal/observability"
	"github.com/WALLE-AI/uFreeTokens/internal/reconcile"
	"github.com/WALLE-AI/uFreeTokens/internal/reqlog"
	"github.com/WALLE-AI/uFreeTokens/internal/risk"
	"github.com/WALLE-AI/uFreeTokens/internal/store"
	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

// 各任务的调度周期与参数。当前写死在代码里；随着任务变多，可以像 cmd/gateway 那样
// 挪进 config.Config，现在为了不过度设计先保持简单。
const (
	reclaimInterval = time.Minute
	reclaimLimit    = 500 // 单次最多回收的过期预扣数，避免一次扫太多阻塞太久

	partitionInterval  = 6 * time.Hour
	partitionDaysAhead = 14 // 维持"未来 14 天分区已就绪"的缓冲，容忍 worker 短暂故障

	reconcileInterval = time.Hour
	reconcileLookback = time.Hour       // 每次对账检查最近 1 小时的账本 vs 请求日志
	reconcileBuffer   = 5 * time.Minute // 窗口结束时间往回退 5 分钟，避开异步写入延迟

	riskScanInterval = time.Minute // 与技术方案 §7.12"异常检测 worker 每分钟聚合"一致
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "worker: fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv("UFT_CONFIG"))
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger := observability.NewLogger(cfg.Log)
	logger.Info("starting worker")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pg, err := store.NewPostgres(ctx, cfg.Postgres)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pg.Close()

	rdb, err := store.NewRedis(ctx, cfg.Redis)
	if err != nil {
		return fmt.Errorf("connect redis: %w", err)
	}
	defer rdb.Close()

	walletSvc := wallet.New(pg)
	reconciler := reconcile.New(pg)
	riskEngine := risk.New(pg, risk.DefaultThresholds())

	logger.Info("worker dependencies connected, starting task loops")

	var wg sync.WaitGroup
	runTask(ctx, &wg, logger, "reclaim_expired_reservations", reclaimInterval, func(ctx context.Context) {
		reclaimExpiredReservations(ctx, logger, walletSvc)
	})
	runTask(ctx, &wg, logger, "ensure_request_logs_partitions", partitionInterval, func(ctx context.Context) {
		ensurePartitions(ctx, logger, pg)
	})
	runTask(ctx, &wg, logger, "reconcile", reconcileInterval, func(ctx context.Context) {
		runReconcile(ctx, logger, reconciler)
	})
	runTask(ctx, &wg, logger, "risk_scan", riskScanInterval, func(ctx context.Context) {
		runRiskScan(ctx, logger, riskEngine)
	})

	<-ctx.Done()
	logger.Info("shutdown signal received, waiting for task loops to finish")
	wg.Wait()
	logger.Info("worker shut down cleanly")
	return nil
}

// runTask 启动一个周期任务：启动时立即跑一次，此后每 interval 跑一次，直到 ctx 被取消。
// 单次任务执行的超时由 task 自己的 context 控制（这里给了一个不超过 interval 的
// 上限，避免某次任务卡住导致后续周期永远排不上）。
func runTask(ctx context.Context, wg *sync.WaitGroup, logger *slog.Logger, name string, interval time.Duration, task func(context.Context)) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		runOnce := func() {
			taskCtx, cancel := context.WithTimeout(ctx, interval)
			defer cancel()
			start := time.Now()
			task(taskCtx)
			logger.Debug("task finished", "task", name, "duration", time.Since(start))
		}

		runOnce()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runOnce()
			}
		}
	}()
}

func reclaimExpiredReservations(ctx context.Context, logger *slog.Logger, walletSvc *wallet.Service) {
	n, err := walletSvc.ReclaimExpired(ctx, reclaimLimit)
	if err != nil {
		logger.Error("reclaim expired reservations failed", "error", err)
		return
	}
	if n > 0 {
		logger.Info("reclaimed expired reservations", "count", n)
	}
}

func ensurePartitions(ctx context.Context, logger *slog.Logger, pg *pgxpool.Pool) {
	if err := reqlog.EnsureFuturePartitions(ctx, pg, partitionDaysAhead); err != nil {
		logger.Error("ensure request_logs partitions failed", "error", err)
		return
	}
	logger.Debug("request_logs partitions ensured", "days_ahead", partitionDaysAhead)
}

func runReconcile(ctx context.Context, logger *slog.Logger, reconciler *reconcile.Reconciler) {
	report, err := reconciler.Run(ctx, reconcileLookback, reconcileBuffer)
	if err != nil {
		logger.Error("reconcile run failed", "error", err)
		return
	}
	if report.Clean() {
		logger.Debug("reconcile: no discrepancies found")
		return
	}

	const maxLogged = 20
	for i, d := range report.WalletDiscrepancies {
		if i >= maxLogged {
			logger.Error("reconcile: additional wallet discrepancies omitted from log", "total", len(report.WalletDiscrepancies))
			break
		}
		logger.Error("reconcile: wallet discrepancy",
			"account_id", d.AccountID, "field", d.Field, "recorded", d.Recorded, "expected", d.Expected, "diff", d.Diff())
	}
	if report.LedgerVsLogs != nil && report.LedgerVsLogs.Diff() != 0 {
		logger.Error("reconcile: ledger vs request_logs mismatch",
			"window_start", report.LedgerVsLogs.WindowStart, "window_end", report.LedgerVsLogs.WindowEnd,
			"ledger_total", report.LedgerVsLogs.LedgerTotal, "request_logs_total", report.LedgerVsLogs.RequestLogsTotal,
			"diff", report.LedgerVsLogs.Diff())
	}
}

// runRiskScan 跑一轮 §7.12 风控规则引擎；命中的规则已经在 Scan 内部执行完
// 对应的 Action（比如降级 tier），这里只负责把结果记日志——真正的人工通知
// 渠道（邮件/Slack）没有接，见 internal/risk 包文档。
func runRiskScan(ctx context.Context, logger *slog.Logger, engine *risk.Engine) {
	findings, err := engine.Scan(ctx, time.Now())
	if err != nil {
		logger.Error("risk scan failed", "error", err)
		return
	}
	for _, f := range findings {
		logger.Warn("risk: rule matched",
			"rule", f.Rule, "account_id", f.AccountID, "ip", f.IP, "detail", f.Detail,
			"action", f.Action, "applied", f.Applied)
	}
}
