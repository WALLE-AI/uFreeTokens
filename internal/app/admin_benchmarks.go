package app

import (
	"context"
	"net/http"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
)

// 基准测试与公开榜单的运营接口（docs/基准测试与排行榜数据服务技术方案.md §3.5、§8.2）：
// 基准定义的增改、run 录入/批量导入、发布与删除；应用榜的屏蔽/合并规则。所有写操作在同一
// 事务里写审计日志（audited）。

func (h *adminHandlers) listBenchmarks(w http.ResponseWriter, r *http.Request) {
	q := &queryParser{r: r}
	en := admin.EnumValues()
	in := admin.ListBenchmarksInput{Category: q.enum("category", en.BenchmarkCategories...), Status: q.enum("status", en.BenchmarkStatuses...)}
	if !q.ok(w) {
		return
	}
	list, err := h.svc.ListBenchmarks(r.Context(), in)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": list})
}

func (h *adminHandlers) createBenchmark(w http.ResponseWriter, r *http.Request) {
	var in admin.CreateBenchmarkInput
	if err := decodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	b, err := audited(h, r, func(ctx context.Context) (*admin.Benchmark, auditEntry, error) {
		b, err := h.svc.CreateBenchmark(ctx, in)
		if err != nil {
			return nil, auditEntry{}, err
		}
		return b, auditEntry{"benchmark.create", "benchmark", idStr(b.ID), nil, b}, nil
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	w.Header().Set("ETag", etag(b.Version))
	httpx.WriteJSON(w, http.StatusCreated, b)
}

func (h *adminHandlers) getBenchmark(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "benchmarkID", "benchmark")
	if !ok {
		return
	}
	d, err := h.svc.GetBenchmarkDetail(r.Context(), id)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	w.Header().Set("ETag", etag(d.Version))
	httpx.WriteJSON(w, http.StatusOK, d)
}

func (h *adminHandlers) updateBenchmark(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "benchmarkID", "benchmark")
	if !ok {
		return
	}
	patchHandler(h, w, r, id, "benchmark.update", "benchmark", "benchmarks", h.svc.UpdateBenchmark, h.svc.GetBenchmark)
}

func (h *adminHandlers) createBenchmarkRun(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "benchmarkID", "benchmark")
	if !ok {
		return
	}
	var in admin.CreateBenchmarkRunInput
	if err := decodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body: "+err.Error())
		return
	}
	run, err := audited(h, r, func(ctx context.Context) (*admin.BenchmarkRunDetail, auditEntry, error) {
		run, err := h.svc.CreateBenchmarkRun(ctx, id, in)
		if err != nil {
			return nil, auditEntry{}, err
		}
		// 审计只记 run 本身与结果条数，不把上千行结果塞进审计日志。
		after := map[string]any{"run": run.BenchmarkRun, "published": run.Published}
		return run, auditEntry{"benchmark_run.create", "benchmark_run", idStr(run.ID), nil, after}, nil
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, run)
}

func (h *adminHandlers) getBenchmarkRun(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "runID", "benchmark run")
	if !ok {
		return
	}
	run, err := h.svc.GetBenchmarkRun(r.Context(), id)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, run)
}

func (h *adminHandlers) publishBenchmarkRun(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "runID", "benchmark run")
	if !ok {
		return
	}
	run, err := audited(h, r, func(ctx context.Context) (*admin.BenchmarkRunDetail, auditEntry, error) {
		prev, err := h.svc.PublishBenchmarkRun(ctx, id)
		if err != nil {
			return nil, auditEntry{}, err
		}
		run, err := h.svc.GetBenchmarkRun(ctx, id)
		if err != nil {
			return nil, auditEntry{}, err
		}
		before := map[string]any{"previously_published_run": prev}
		return run, auditEntry{"benchmark_run.publish", "benchmark_run", idStr(id), before, run.BenchmarkRun}, nil
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, run)
}

func (h *adminHandlers) deleteBenchmarkRun(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "runID", "benchmark run")
	if !ok {
		return
	}
	_, err := audited(h, r, func(ctx context.Context) (struct{}, auditEntry, error) {
		run, err := h.svc.DeleteBenchmarkRun(ctx, id)
		if err != nil {
			return struct{}{}, auditEntry{}, err
		}
		return struct{}{}, auditEntry{"benchmark_run.delete", "benchmark_run", idStr(id), run, nil}, nil
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- 公开应用榜的屏蔽 / 合并规则（§8.2） ----------

// publicAppsResponse 是 GET /public-apps 的响应；MinDistinctAccounts 是网关公开榜单使用的
// 隐私阈值（独立账户数少于它的应用不会公开上榜），供后台页面提示。
type publicAppsResponse struct {
	Data                []admin.PublicAppCandidate `json:"data"`
	MinDistinctAccounts int                        `json:"min_distinct_accounts"`
}

func (h *adminHandlers) listPublicApps(w http.ResponseWriter, r *http.Request) {
	q := &queryParser{r: r}
	days := q.int("days")
	if !q.ok(w) {
		return
	}
	if days == 0 {
		days = 7
	}
	list, err := h.svc.ListPublicAppCandidates(r.Context(), days)
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, publicAppsResponse{Data: list, MinDistinctAccounts: h.publicMinAccounts})
}

func (h *adminHandlers) listPublicAppRules(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.ListPublicAppRules(r.Context())
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": list})
}

func (h *adminHandlers) createPublicAppRule(w http.ResponseWriter, r *http.Request) {
	var in admin.CreatePublicAppRuleInput
	if err := decodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	rule, err := audited(h, r, func(ctx context.Context) (*admin.PublicAppRule, auditEntry, error) {
		rule, err := h.svc.CreatePublicAppRule(ctx, in)
		if err != nil {
			return nil, auditEntry{}, err
		}
		return rule, auditEntry{"public_app_rule.create", "public_app_rule", idStr(rule.ID), nil, rule}, nil
	})
	if err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, rule)
}

func (h *adminHandlers) deletePublicAppRule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "ruleID", "public app rule")
	if !ok {
		return
	}
	if _, err := audited(h, r, func(ctx context.Context) (struct{}, auditEntry, error) {
		rule, err := h.svc.DeletePublicAppRule(ctx, id)
		if err != nil {
			return struct{}{}, auditEntry{}, err
		}
		return struct{}{}, auditEntry{"public_app_rule.delete", "public_app_rule", idStr(id), rule, nil}, nil
	}); err != nil {
		writeAdminError(w, r, h.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
