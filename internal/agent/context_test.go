package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/WALLE-AI/uFreeTokens/internal/adminauth"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/kernel"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/playbooks"
)

func TestRepairHistory_FillsMissingAndDropsOrphans(t *testing.T) {
	msgs := []kernel.Message{
		{Role: "system"},
		{Role: "tool", ToolCallID: "orphan"},
		{Role: "user", Content: "q"},
		{Role: "assistant", ToolCalls: []kernel.ToolCall{{ID: "a"}, {ID: "b"}}},
		{Role: "tool", ToolCallID: "a"},
		{Role: "user", Content: "q2"},
	}
	out := repairHistory(msgs)
	var roles []string
	for _, m := range out {
		roles = append(roles, m.Role+":"+m.ToolCallID)
	}
	if got := strings.Join(roles, ","); got != "system:,user:,assistant:,tool:a,tool:b,user:" {
		t.Errorf("repaired = %s", got)
	}
}

func TestCompact_CutsAtUserBoundary(t *testing.T) {
	big := strings.Repeat("x", 3000)
	msgs := []kernel.Message{{Role: "system", Content: "sys"}}
	for i := 0; i < 10; i++ {
		msgs = append(msgs,
			kernel.Message{Role: "user", Content: "请求" + big},
			kernel.Message{Role: "assistant", ToolCalls: []kernel.ToolCall{{ID: "c", Name: "list"}}},
			kernel.Message{Role: "tool", ToolCallID: "c", Content: big},
			kernel.Message{Role: "assistant", Content: "结论"},
		)
	}
	out := compact(msgs, 5000)
	if len(out) >= len(msgs) || out[1].Role != kernel.RoleSummary || out[2].Role != kernel.RoleUser {
		t.Fatalf("compacted roles: %v %v %v (len %d)", out[0].Role, out[1].Role, out[2].Role, len(out))
	}
	if !strings.Contains(out[1].Content, "list×") {
		t.Errorf("summary = %s", out[1].Content)
	}
	// 未超阈值时原样返回
	if got := compact(msgs[:5], 1<<20); len(got) != 5 {
		t.Errorf("small history changed")
	}
}

func TestCheckEvidence(t *testing.T) {
	env := &kernel.Env{Pages: kernel.NewPageCache()}
	env.Pages.Put("https://x.com/a", "限时免费：2026 年 10 月  31 日前所有用户免费使用")
	spec := kernel.ToolSpec{Name: "set_offer_status"}
	confirmed := json.RawMessage(`{"status":"confirmed"}`)
	if err := CheckEvidence(context.Background(), env, spec, kernel.ProposalMeta{}, confirmed); err == nil {
		t.Error("confirmed without evidence should fail")
	}
	ok := kernel.ProposalMeta{Evidence: []kernel.Evidence{{URL: "https://x.com/a", Quote: "10 月 31 日前所有用户免费使用"}}}
	if err := CheckEvidence(context.Background(), env, spec, ok, confirmed); err != nil {
		t.Errorf("valid evidence rejected: %v", err)
	}
	forged := kernel.ProposalMeta{Evidence: []kernel.Evidence{{URL: "https://x.com/a", Quote: "永久免费"}}}
	if err := CheckEvidence(context.Background(), env, spec, forged, confirmed); err == nil {
		t.Error("forged quote accepted")
	}
	unfetched := kernel.ProposalMeta{Evidence: []kernel.Evidence{{URL: "https://y.com", Quote: "所有用户免费使用"}}}
	if err := CheckEvidence(context.Background(), env, spec, unfetched, confirmed); err == nil {
		t.Error("unfetched url accepted")
	}
}

func TestSystemPrompt_IncludesBoundariesPlaybookAndContext(t *testing.T) {
	pb, _ := playbooks.Get("price_triage")
	s := buildSystemPrompt(promptInput{
		Principal: &adminauth.Principal{AdminID: 3, Name: "alice", Roles: []string{"pricing"}, Permissions: []adminauth.Permission{"pricing:read"}},
		Playbook:  pb, Context: []ContextRef{{Type: "price_change_request", ID: "88", Label: "调价 #88"}},
	})
	for _, want := range []string{"trusted=\"false\"", "alice", "pricing:read", "调价审批预审", "调价 #88"} {
		if !strings.Contains(s, want) {
			t.Errorf("system prompt missing %q", want)
		}
	}
}

func TestParseContextRefs(t *testing.T) {
	if refs, err := parseContextRefs(json.RawMessage(`{"type":"listing","id":"1"}`)); err != nil || len(refs) != 1 {
		t.Errorf("single object: %v %v", refs, err)
	}
	if _, err := parseContextRefs(json.RawMessage(`[{"type":""}]`)); err == nil {
		t.Error("empty type accepted")
	}
}
