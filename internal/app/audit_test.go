// 端到端集成测试：审计日志（技术方案 Phase 4 审计导出）通过真实的 admin HTTP
// 接口写入并能查回来——验证的是 internal/app 的 handler 层真的在调用
// admin.Service.RecordAudit，而不是只在 internal/admin 包内部单测过。
package app_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/admin"
	"github.com/WALLE-AI/uFreeTokens/internal/app"
	"github.com/WALLE-AI/uFreeTokens/internal/config"
	"github.com/WALLE-AI/uFreeTokens/internal/observability"
	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

func TestAuditLog_RecordedOnWalletAdjustAndQueryableViaHTTP(t *testing.T) {
	pool, box := testPool(t), testBox(t)
	logger := observability.NewLogger(config.LogConfig{Level: "error", Format: "console"})
	adminSvc := admin.New(pool, wallet.New(pool), box, []byte(testPepper))
	adminSrv := httptest.NewServer(app.NewAdminRouter(app.AdminDeps{Logger: logger, Admin: adminSvc, AdminToken: testAdminToken}))
	defer adminSrv.Close()
	ac := &adminClient{t: t, baseURL: adminSrv.URL}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var account struct{ ID int64 }
	ac.post("/accounts", map[string]any{"Type": "personal", "Name": "audit-" + suffix, "Tier": "free"}, &account)

	raw, _ := json.Marshal(map[string]any{"amount": 5000, "ref_id": "audit-test-" + suffix})
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/accounts/%d/wallet/adjust", adminSrv.URL, account.ID), bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testAdminToken)
	req.Header.Set("X-Actor-ID", "77")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("wallet adjust request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("wallet adjust status = %d", resp.StatusCode)
	}

	var list struct {
		AuditLogs []admin.AuditLogEntry `json:"audit_logs"`
	}
	ac.get(fmt.Sprintf("/audit-logs?target_type=account&target_id=%d", account.ID), &list)
	if len(list.AuditLogs) != 1 {
		t.Fatalf("audit_logs = %+v, want exactly 1", list.AuditLogs)
	}
	entry := list.AuditLogs[0]
	if entry.Action != "wallet.adjust" {
		t.Errorf("Action = %q, want wallet.adjust", entry.Action)
	}
	if entry.ActorID != 77 {
		t.Errorf("ActorID = %d, want 77 (from X-Actor-ID header)", entry.ActorID)
	}
	if len(entry.After) == 0 {
		t.Error("expected non-empty After JSON")
	}
}
