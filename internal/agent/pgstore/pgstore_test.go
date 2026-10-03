package pgstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/WALLE-AI/uFreeTokens/internal/agent/kernel"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("UFT_TEST_PG_DSN")
	if dsn == "" {
		dsn = "postgres://uft:uft@localhost:5432/uft?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("skipping: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("skipping: postgres not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestStore_RunMutexMessagesAndDecisions(t *testing.T) {
	pool := testPool(t)
	s := New(pool)
	ctx := context.Background()
	sess, err := s.CreateSession(ctx, NewSession{AdminUserID: 0, Title: "t", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}

	// 并发占用运行位：只有一个成功。
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins, busy := 0, 0
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.ClaimRun(ctx, sess.ID, fmt.Sprintf("r%d", i), time.Now().Add(time.Minute))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, ErrBusy):
				busy++
			default:
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if wins != 1 || busy != 7 {
		t.Fatalf("wins=%d busy=%d, want 1/7", wins, busy)
	}

	// 并发追加消息：seq 连续且唯一。
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.AppendMessage(ctx, sess.ID, &kernel.Message{Role: "user", Content: "x"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	msgs, _ := s.Messages(ctx, sess.ID)
	for i, m := range msgs {
		if m.Seq != i+1 {
			t.Fatalf("seq gap: %+v", msgs)
		}
	}

	// 提案：同一目标去重；决定只生效一次。
	rec := func(id string) *kernel.ToolCallRecord {
		return &kernel.ToolCallRecord{ID: id, SessionID: sess.ID, Tool: "approve_price_change", Risk: kernel.RiskWrite,
			Args: []byte(`{}`), Status: kernel.CallPendingApproval, RequiredPerm: "price_change:approve"}
	}
	target := fmt.Sprint(time.Now().UnixNano())
	prop := func(id string) *kernel.ProposalRecord {
		return &kernel.ProposalRecord{ToolCallID: id, SessionID: sess.ID, Tool: "approve_price_change", TargetType: "price_change_request",
			TargetID: target, Summary: "s", RequiredPerm: "price_change:approve"}
	}
	id1, id2 := fmt.Sprintf("t%s_1", target), fmt.Sprintf("t%s_2", target)
	if err := s.SaveProposal(ctx, rec(id1), prop(id1)); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveProposal(ctx, rec(id2), prop(id2)); !errors.Is(err, kernel.ErrDuplicateProposal) {
		t.Fatalf("duplicate proposal err = %v", err)
	}
	if _, err := s.GetToolCall(ctx, id2); !errors.Is(err, ErrNotFound) {
		t.Errorf("duplicate proposal left a tool call behind: %v", err)
	}
	if n, _ := s.CountPending(ctx, []string{"price_change:approve"}); n < 1 {
		t.Errorf("CountPending = %d", n)
	}
	ok1, _ := s.ClaimDecision(ctx, id1, kernel.CallApproved, 0, "")
	ok2, _ := s.ClaimDecision(ctx, id1, kernel.CallRejected, 0, "")
	if !ok1 || ok2 {
		t.Errorf("ClaimDecision = %v %v, want true false", ok1, ok2)
	}
	list, err := s.ListProposals(ctx, ProposalFilter{TargetType: "price_change_request", TargetIDs: []string{target}})
	if err != nil || len(list) != 1 || list[0].Status != "approved" {
		t.Errorf("ListProposals = %+v %v", list, err)
	}
	if err := s.FinishRun(ctx, sess.ID, "nope", "completed", ""); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetSession(ctx, sess.ID)
	if got.Status != "running" {
		t.Errorf("FinishRun with a stale run id must not change status, got %s", got.Status)
	}
}
