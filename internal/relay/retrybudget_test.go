package relay

import (
	"sync"
	"testing"
)

func TestNewRetryBudget_InvalidParamsReturnNil(t *testing.T) {
	if b := NewRetryBudget(0, 0.2); b != nil {
		t.Error("maxTokens=0 should return nil (unlimited)")
	}
	if b := NewRetryBudget(10, 0); b != nil {
		t.Error("tokenRatio=0 should return nil (unlimited)")
	}
	if b := NewRetryBudget(-1, 0.2); b != nil {
		t.Error("negative maxTokens should return nil")
	}
}

func TestRetryBudget_NilIsAlwaysUnlimited(t *testing.T) {
	var b *RetryBudget
	b.RecordRequest() // must not panic
	for i := 0; i < 1000; i++ {
		if !b.TryRetry() {
			t.Fatal("nil budget must always allow retries")
		}
	}
}

func TestRetryBudget_StartsFullAllowsColdStartBurst(t *testing.T) {
	b := NewRetryBudget(3, 0.2)
	for i := 0; i < 3; i++ {
		if !b.TryRetry() {
			t.Fatalf("retry %d should be allowed (bucket starts full at maxTokens)", i+1)
		}
	}
	if b.TryRetry() {
		t.Fatal("4th retry should be denied, bucket should be empty now")
	}
}

func TestRetryBudget_RecordRequestReplenishesTokens(t *testing.T) {
	b := NewRetryBudget(1, 0.5)
	if !b.TryRetry() {
		t.Fatal("first retry should be allowed (starts full)")
	}
	if b.TryRetry() {
		t.Fatal("second retry should be denied, bucket is empty")
	}
	// 0.5 每个请求，两个正常请求攒够 1 个令牌。
	b.RecordRequest()
	if b.TryRetry() {
		t.Fatal("still should be denied after only 1 recorded request (0.5 tokens < 1)")
	}
	b.RecordRequest()
	if !b.TryRetry() {
		t.Fatal("should be allowed after 2 recorded requests (0.5*2 = 1 token)")
	}
}

func TestRetryBudget_RecordRequestCapsAtMaxTokens(t *testing.T) {
	b := NewRetryBudget(2, 1) // ratio=1 会让第一次 RecordRequest 就想冲到 3
	for i := 0; i < 100; i++ {
		b.RecordRequest()
	}
	got := 0
	for b.TryRetry() {
		got++
		if got > 2 {
			t.Fatal("tokens must be capped at maxTokens=2, got more than 2 allowed retries")
		}
	}
	if got != 2 {
		t.Errorf("allowed retries = %d, want exactly 2 (capped at maxTokens)", got)
	}
}

func TestRetryBudget_ConcurrentTryRetryNeverExceedsTokens(t *testing.T) {
	b := NewRetryBudget(50, 0.2)
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for i := 0; i < 500; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if b.TryRetry() {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != 50 {
		t.Errorf("allowed retries = %d, want exactly 50 (maxTokens, no over-spend under concurrency)", allowed)
	}
}
