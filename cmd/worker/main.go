// Command worker 是异步任务入口（技术方案 §7.11、§7.12、§7.13）：
//   - 回收过期未结算的预扣（网关崩溃留下的"孤儿" reservation）
//   - 保持 request_logs 的未来分区存在（表按天分区），兜底分区有数据时告警；
//     可选地按 UFT_REQLOG_RETENTION_DAYS 删除过期分区（默认不删除）
//   - 每 5 分钟把最近 2 小时的请求日志汇总进 usage_hourly（运营后台长时间窗统计用）
//   - 每 30 分钟把今天、昨天的 usage_hourly 物化进公开排行榜的日表（public_*_daily），
//     每天重建一次最近 90 天（账户的"不计入公开榜单"标记变更后，历史随之修正）
//   - 每 30 秒把网关推到 Redis 的熔断/冷却事件落到 channel_health_events
//   - 每分钟调度一次外部数据采集（internal/datasync）：价格来源（OpenRouter / models.dev /
//     LiteLLM / 定价页）、上游优惠情报、公开评测榜单（LMArena / Epoch AI ...），见
//     docs/外部数据采集模块（价格情报与评测榜单）技术方案.md；每个来源的每次执行记进
//     data_source_runs，过期的优惠情报每 10 分钟置为 expired
//   - 每个任务的每次执行记进 job_runs
//   - 定期做内部一致性对账（钱包余额 vs 账本流水、账本 vs 请求日志）
//   - 每分钟跑一轮风控规则引擎（internal/risk：消费速率突增自动降级 tier，
//     同 IP 多账户聚集只上报不处置）
//
// 尚未实现：账单级对账
// （internal/reconcile.CheckBillingDrift）已经有框架，但没有接进这个循环——
// 调用它需要一个真实的 BillingFetcher 实现和要扫描哪些 provider_account，
// 这两者目前都不存在（只有 mock fetcher 用于测试），接一个假的调用点不会
// 产生任何真实的对账能力。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/benchsync"
	"github.com/WALLE-AI/uFreeTokens/internal/config"
	"github.com/WALLE-AI/uFreeTokens/internal/datasync"
	"github.com/WALLE-AI/uFreeTokens/internal/health"
	"github.com/WALLE-AI/uFreeTokens/internal/llm"
	"github.com/WALLE-AI/uFreeTokens/internal/observability"
	"github.com/WALLE-AI/uFreeTokens/internal/offers"
	"github.com/WALLE-AI/uFreeTokens/internal/pricesync"
	"github.com/WALLE-AI/uFreeTokens/internal/rankings"
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

	rollupInterval = 5 * time.Minute
	rollupLookback = 2 * time.Hour // 每次重算最近 2 小时，迟到的日志也会被补进汇总
	// usage_hourly 为空时（首次部署）一次性回填的天数，按天分批。
	rollupBackfillDays = 90

	retentionInterval = 24 * time.Hour

	healthEventsInterval = 30 * time.Second // 网关熔断/冷却事件从 Redis 落库的周期

	alertAfterFailures = 2 // 任务连续失败这么多个周期后打告警日志

	// 公开排行榜物化（docs/基准测试与排行榜数据服务技术方案.md §3.3）：每 30 分钟重算
	// 今天和昨天；每天全量重建一次（回填上限受 usage_hourly 的保留期限制，同 rollupBackfillDays）。
	publicUsageInterval     = 30 * time.Minute
	publicUsageRebuildEvery = 24 * time.Hour
	publicUsageRebuildDays  = rollupBackfillDays

	// 外部数据采集：每分钟看一次哪些来源到期（来源自己的周期在 price_sources.schedule 里）。
	// 单轮任务超时给足 30 分钟：一个来源最多跑 datasync.DefaultRunTimeout，一轮可能跑好几个。
	dataSyncInterval     = time.Minute
	dataSyncTickTimeout  = 30 * time.Minute
	offerExpiryInterval  = 10 * time.Minute
	dataSourceRunsRetain = 90 * 24 * time.Hour
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
	jobs := &jobRunner{ctx: ctx, wg: &wg, logger: logger, pg: pg}
	jobs.run("reclaim_expired_reservations", reclaimInterval, func(ctx context.Context) (map[string]any, error) {
		return reclaimExpiredReservations(ctx, logger, walletSvc)
	})
	jobs.run("ensure_request_logs_partitions", partitionInterval, func(ctx context.Context) (map[string]any, error) {
		return ensurePartitions(ctx, logger, pg)
	})
	jobs.run("reconcile", reconcileInterval, func(ctx context.Context) (map[string]any, error) {
		return runReconcile(ctx, logger, reconciler)
	})
	jobs.run("risk_scan", riskScanInterval, func(ctx context.Context) (map[string]any, error) {
		return runRiskScan(ctx, logger, riskEngine)
	})
	backfillUsageRollup(ctx, logger, pg)
	jobs.run("usage_rollup", rollupInterval, func(ctx context.Context) (map[string]any, error) {
		return rollupUsage(ctx, pg)
	})
	jobs.run("public_usage_daily", publicUsageInterval, func(ctx context.Context) (map[string]any, error) {
		return refreshPublicUsage(ctx, pg)
	})
	jobs.run("public_usage_rebuild", publicUsageRebuildEvery, func(ctx context.Context) (map[string]any, error) {
		return rebuildPublicUsage(ctx, pg)
	})
	jobs.run("health_events", healthEventsInterval, func(ctx context.Context) (map[string]any, error) {
		return drainHealthEvents(ctx, pg, rdb)
	})
	scheduler := newDataSyncScheduler(pg, walletSvc, cfg.DataSync, logger)
	jobs.runWithTimeout("datasync_tick", dataSyncInterval, dataSyncTickTimeout, func(ctx context.Context) (map[string]any, error) {
		res, err := scheduler.Tick(ctx)
		return map[string]any{"ran": res.Ran, "failed": res.Failed, "skipped": res.Skipped}, err
	})
	offerStore := offers.NewStore(pg)
	jobs.run("upstream_offers_expire", offerExpiryInterval, func(ctx context.Context) (map[string]any, error) {
		n, err := offerStore.ExpireEnded(ctx)
		if err != nil {
			return nil, err
		}
		purged, err := datasync.PurgeRuns(ctx, pg, dataSourceRunsRetain)
		return map[string]any{"expired": n, "runs_purged": purged}, err
	})

	if days := retentionDays(logger); days > 0 {
		jobs.run("request_logs_retention", retentionInterval, func(ctx context.Context) (map[string]any, error) {
			return dropExpiredPartitions(ctx, logger, pg, days)
		})
	}

	<-ctx.Done()
	logger.Info("shutdown signal received, waiting for task loops to finish")
	wg.Wait()
	logger.Info("worker shut down cleanly")
	return nil
}

// jobRunner 启动周期任务，并把每次执行记进 job_runs（开始、结束、成败、摘要），
// 运维可以直接查"某个任务上次什么时候成功"。记录失败只打日志，不影响任务本身。
type jobRunner struct {
	ctx    context.Context
	wg     *sync.WaitGroup
	logger *slog.Logger
	pg     *pgxpool.Pool
}

// run 启动一个周期任务：启动时立即跑一次，此后每 interval 跑一次，直到 ctx 被取消。
// 单次任务执行的超时不超过 interval，避免某次任务卡住导致后续周期永远排不上。
// 连续 alertAfterFailures 个周期失败时打一条带 alert=true 的 ERROR 日志，供日志告警规则
// 匹配（例如公开排行榜物化任务停摆时，榜单会停在旧数据上而不会报错）。
func (j *jobRunner) run(name string, interval time.Duration, task func(context.Context) (map[string]any, error)) {
	j.runWithTimeout(name, interval, interval, task)
}

// runWithTimeout 同 run，但单次执行的超时可以比周期长（数据采集一轮可能跑多个来源）。
// 上一次还没跑完时 ticker 的触发会被丢弃，不会并发执行同一个任务。
func (j *jobRunner) runWithTimeout(name string, interval, timeout time.Duration, task func(context.Context) (map[string]any, error)) {
	j.wg.Add(1)
	go func() {
		defer j.wg.Done()
		consecutiveFailures := 0
		runOnce := func() {
			taskCtx, cancel := context.WithTimeout(j.ctx, timeout)
			defer cancel()
			start := time.Now()
			var runID int64
			if err := j.pg.QueryRow(j.ctx, `INSERT INTO job_runs (job, started_at, status) VALUES ($1, $2, 'running') RETURNING id`, name, start).Scan(&runID); err != nil {
				j.logger.Warn("record job start failed", "task", name, "error", err)
			}
			detail, err := task(taskCtx)
			status := "success"
			if err == nil {
				consecutiveFailures = 0
			} else {
				status = "failed"
				consecutiveFailures++
				j.logger.Error("task failed", "task", name, "error", err)
				if consecutiveFailures >= alertAfterFailures {
					j.logger.Error("task failing repeatedly", "task", name, "consecutive_failures", consecutiveFailures, "alert", true)
				}
				if detail == nil {
					detail = map[string]any{}
				}
				detail["error"] = err.Error()
			}
			if runID != 0 {
				if _, err := j.pg.Exec(context.WithoutCancel(j.ctx),
					`UPDATE job_runs SET finished_at = now(), status = $2, detail = $3 WHERE id = $1`, runID, status, detail); err != nil {
					j.logger.Warn("record job finish failed", "task", name, "error", err)
				}
			}
			j.logger.Debug("task finished", "task", name, "status", status, "duration", time.Since(start))
		}

		runOnce()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-j.ctx.Done():
				return
			case <-ticker.C:
				runOnce()
			}
		}
	}()
}

// newDataSyncScheduler 装配外部数据采集：价格域（pricesync，含优惠识别）、优惠文案抽取
// （offers）、评测榜单（benchsync）共用一个调度器。
//
// 环境变量：
//   - UFT_DATASYNC_ALLOW_PRIVATE=true：允许访问内网地址（经内网代理出网、或本地联调）；
//   - 优惠文案抽取用的 LLM 来自配置段 datasync.llm_*（与 cmd/admin 共用；环境变量
//     UFT_DATASYNC_LLM_BASE_URL / _MODEL，密钥 UFT_DATASYNC_LLM_API_KEY）；
//   - UFT_DATASYNC_AA_API_KEY 等：评测来源 config 里 auth_header_env 引用的密钥；
//   - UFT_WORKER_METRICS_ADDR：非空时在该地址暴露 /metrics（uft_datasync_* 指标）。
func newDataSyncScheduler(pg *pgxpool.Pool, walletSvc *wallet.Service, llmCfg config.DataSyncConfig, logger *slog.Logger) *datasync.Scheduler {
	// 采集只用到建基准 / 写榜单 / 发布成本价，不涉及上游密钥，所以不需要 secretbox 与 pepper。
	adminSvc := admin.New(pg, walletSvc, nil, nil)
	offerStore := offers.NewStore(pg)
	priceJob := &pricesync.Job{Engine: pricesync.NewEngine(pg, adminSvc), Offers: offerStore}
	registry := priceJob.Registry()
	llmClient, missing := llm.FromConfig(llmCfg)
	if llmClient == nil {
		logger.Info("datasync LLM not configured; offer_page sources will be skipped", "missing", missing)
	}
	registry["offer_page"] = offers.PageJob{Store: offerStore, LLM: offers.NewLLM(llmClient)}
	registry["tabular"] = &benchsync.Job{Pool: pg, Publisher: adminSvc}

	env := datasync.NewEnv(datasync.EnvOptions{AllowPrivateNetworks: os.Getenv("UFT_DATASYNC_ALLOW_PRIVATE") == "true"})
	reg := prometheus.NewRegistry()
	metrics := datasync.NewMetrics(reg)
	if addr := os.Getenv("UFT_WORKER_METRICS_ADDR"); addr != "" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
		srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		go func() {
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Error("worker metrics server failed", "error", err)
			}
		}()
		logger.Info("worker metrics listening", "addr", addr)
	}
	return datasync.NewScheduler(pg, registry, env, logger, metrics)
}

func reclaimExpiredReservations(ctx context.Context, logger *slog.Logger, walletSvc *wallet.Service) (map[string]any, error) {
	n, err := walletSvc.ReclaimExpired(ctx, reclaimLimit)
	if err != nil {
		return nil, fmt.Errorf("reclaim expired reservations: %w", err)
	}
	if n > 0 {
		logger.Info("reclaimed expired reservations", "count", n)
	}
	return map[string]any{"reclaimed": n}, nil
}

func ensurePartitions(ctx context.Context, logger *slog.Logger, pg *pgxpool.Pool) (map[string]any, error) {
	if err := reqlog.EnsureFuturePartitions(ctx, pg, partitionDaysAhead); err != nil {
		return nil, fmt.Errorf("ensure request_logs partitions: %w", err)
	}
	// 兜底分区里有数据说明分区维护没跟上（worker 停过、或写入了超出范围的时间），
	// 数据没丢但需要处理：下次补建对应日期的分区时会自动挪走。
	n, err := reqlog.DefaultPartitionRows(ctx, pg)
	if err != nil {
		return nil, fmt.Errorf("check request_logs default partition: %w", err)
	}
	if n > 0 {
		logger.Error("request_logs default partition is not empty", "rows", n)
	}
	return map[string]any{"days_ahead": partitionDaysAhead, "default_partition_rows": n}, nil
}

func rollupUsage(ctx context.Context, pg *pgxpool.Pool) (map[string]any, error) {
	now := time.Now()
	n, err := reqlog.RollupUsage(ctx, pg, now.Add(-rollupLookback), now)
	if err != nil {
		return nil, fmt.Errorf("usage rollup: %w", err)
	}
	return map[string]any{"rows": n}, nil
}

// refreshPublicUsage 重算今天与昨天（Asia/Shanghai）的公开榜单日表。
func refreshPublicUsage(ctx context.Context, pg *pgxpool.Pool) (map[string]any, error) {
	now := time.Now()
	n, err := rankings.RefreshDaily(ctx, pg, now.AddDate(0, 0, -1), now)
	if err != nil {
		return nil, fmt.Errorf("public usage daily: %w", err)
	}
	return map[string]any{"rows": n}, nil
}

// rebuildPublicUsage 按天重建最近 publicUsageRebuildDays 天的公开榜单日表：首次部署时
// 回填历史，之后每天一次把账户排除标记的变更、迟到的汇总修正反映到全部历史。
// 按天分批，每天一个短事务，不长时间占锁。
func rebuildPublicUsage(ctx context.Context, pg *pgxpool.Pool) (map[string]any, error) {
	end := time.Now()
	var total int64
	for day := end.AddDate(0, 0, -publicUsageRebuildDays); !day.After(end); day = day.AddDate(0, 0, 1) {
		if ctx.Err() != nil {
			return map[string]any{"rows": total}, ctx.Err()
		}
		n, err := rankings.RefreshDaily(ctx, pg, day, day)
		if err != nil {
			return map[string]any{"rows": total}, fmt.Errorf("public usage rebuild %s: %w", day.Format(time.DateOnly), err)
		}
		total += n
	}
	return map[string]any{"rows": total, "days": publicUsageRebuildDays + 1}, nil
}

// drainHealthEvents 把网关推进 Redis 的熔断/冷却事件落到 channel_health_events，
// 并清理 30 天前的事件与任务记录。
func drainHealthEvents(ctx context.Context, pg *pgxpool.Pool, rdb *redis.Client) (map[string]any, error) {
	stored, err := health.PersistEvents(ctx, pg, rdb)
	if err != nil {
		return nil, err
	}
	if _, err := pg.Exec(ctx, `DELETE FROM channel_health_events WHERE occurred_at < now() - interval '30 days'`); err != nil {
		return nil, fmt.Errorf("purge health events: %w", err)
	}
	if _, err := pg.Exec(ctx, `DELETE FROM job_runs WHERE started_at < now() - interval '30 days'`); err != nil {
		return nil, fmt.Errorf("purge job runs: %w", err)
	}
	return map[string]any{"stored": stored}, nil
}

// backfillUsageRollup 在 usage_hourly 为空时（首次部署到已有数据的库上）按天回填
// 最近 rollupBackfillDays 天，否则长时间窗统计在回填完成前会是空的。
func backfillUsageRollup(ctx context.Context, logger *slog.Logger, pg *pgxpool.Pool) {
	var empty bool
	if err := pg.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM usage_hourly)`).Scan(&empty); err != nil {
		logger.Error("check usage_hourly failed", "error", err)
		return
	}
	if !empty {
		return
	}
	end := time.Now()
	start := end.AddDate(0, 0, -rollupBackfillDays)
	logger.Info("backfilling usage_hourly", "from", start, "to", end)
	var total int64
	for day := start; day.Before(end); day = day.AddDate(0, 0, 1) {
		if ctx.Err() != nil {
			return
		}
		n, err := reqlog.RollupUsage(ctx, pg, day, day.AddDate(0, 0, 1))
		if err != nil {
			logger.Error("usage_hourly backfill failed", "day", day.Format(time.DateOnly), "error", err)
			return
		}
		total += n
	}
	logger.Info("usage_hourly backfill done", "rows", total)
}

// retentionDays 读 UFT_REQLOG_RETENTION_DAYS：request_logs 是计费相关明细，默认
// 不删除；设置了才启用删除过期分区（删除前应确认已归档或同步到分析库）。
func retentionDays(logger *slog.Logger) int {
	v := os.Getenv("UFT_REQLOG_RETENTION_DAYS")
	if v == "" {
		return 0
	}
	days, err := strconv.Atoi(v)
	if err != nil || days < 31 {
		logger.Error("ignoring UFT_REQLOG_RETENTION_DAYS: must be an integer >= 31", "value", v)
		return 0
	}
	logger.Info("request_logs retention enabled", "days", days)
	return days
}

func dropExpiredPartitions(ctx context.Context, logger *slog.Logger, pg *pgxpool.Pool, days int) (map[string]any, error) {
	dropped, err := reqlog.DropExpiredPartitions(ctx, pg, time.Duration(days)*24*time.Hour)
	if len(dropped) > 0 {
		logger.Info("dropped expired request_logs partitions", "partitions", dropped)
	}
	return map[string]any{"dropped": dropped}, err
}

func runReconcile(ctx context.Context, logger *slog.Logger, reconciler *reconcile.Reconciler) (map[string]any, error) {
	report, err := reconciler.Run(ctx, reconcileLookback, reconcileBuffer)
	if err != nil {
		return nil, fmt.Errorf("reconcile run: %w", err)
	}
	// 成本价与上游报告成本的偏差只告警，不影响 Clean（多供应商实施方案 §8）。
	for _, d := range report.UpstreamCostDrifts {
		logger.Warn("reconcile: channel cost price drifts from upstream-reported cost",
			"channel_id", d.ChannelID, "virtual_model", d.VirtualModel, "requests", d.Requests,
			"cost_cny", d.CostCNY, "upstream_cny", d.UpstreamCNY, "upstream_native", d.UpstreamNative, "currency", d.Currency,
			"ratio", d.Ratio())
	}
	if report.Clean() {
		return map[string]any{"clean": true, "upstream_cost_drifts": len(report.UpstreamCostDrifts)}, nil
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
	detail := map[string]any{"clean": false, "wallet_discrepancies": len(report.WalletDiscrepancies)}
	if report.LedgerVsLogs != nil && report.LedgerVsLogs.Diff() != 0 {
		logger.Error("reconcile: ledger vs request_logs mismatch",
			"window_start", report.LedgerVsLogs.WindowStart, "window_end", report.LedgerVsLogs.WindowEnd,
			"ledger_total", report.LedgerVsLogs.LedgerTotal, "request_logs_total", report.LedgerVsLogs.RequestLogsTotal,
			"diff", report.LedgerVsLogs.Diff())
		detail["ledger_vs_logs_diff"] = report.LedgerVsLogs.Diff()
	}
	return detail, nil
}

// runRiskScan 跑一轮 §7.12 风控规则引擎；命中的规则已经在 Scan 内部执行完
// 对应的 Action（比如降级 tier），这里只负责把结果记日志——真正的人工通知
// 渠道（邮件/Slack）没有接，见 internal/risk 包文档。
func runRiskScan(ctx context.Context, logger *slog.Logger, engine *risk.Engine) (map[string]any, error) {
	findings, err := engine.Scan(ctx, time.Now())
	if err != nil {
		return nil, fmt.Errorf("risk scan: %w", err)
	}
	for _, f := range findings {
		logger.Warn("risk: rule matched",
			"rule", f.Rule, "account_id", f.AccountID, "ip", f.IP, "detail", f.Detail,
			"action", f.Action, "applied", f.Applied)
	}
	return map[string]any{"findings": len(findings)}, nil
}
