package admin

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

func TestIsDisallowedAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1": true, "10.1.2.3": true, "172.16.0.1": true, "192.168.1.1": true,
		"169.254.169.254": true, "100.64.0.1": true, "0.0.0.0": true, "::1": true, "fc00::1": true,
		"fe80::1": true, "::ffff:127.0.0.1": true,
		"8.8.8.8": false, "203.0.113.7": false, "2606:4700::1111": false,
	} {
		if got := isDisallowedAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("isDisallowedAddr(%s) = %v, want %v", addr, got, want)
		}
	}
}

func TestHostAllowed(t *testing.T) {
	allowed := []string{"api.openai.com", "siliconflow.cn"}
	for host, want := range map[string]bool{
		"api.openai.com": true, "API.OPENAI.COM.": true, "siliconflow.cn": true, "api.siliconflow.cn": true,
		"evil-siliconflow.cn": false, "openai.com": false, "api.openai.com.evil.org": false,
	} {
		if got := hostAllowed(host, allowed); got != want {
			t.Errorf("hostAllowed(%q) = %v, want %v", host, got, want)
		}
	}
	if !hostAllowed("anything.example", nil) {
		t.Error("empty allow-list must allow any host")
	}
}

func TestValidateUpstreamURL_StrictPolicy(t *testing.T) {
	pool := testPool(t)
	s := New(pool, wallet.New(pool), nil, []byte(testPepper)) // 默认即严格策略
	ctx := context.Background()
	p, err := s.CreateProvider(ctx, CreateProviderInput{Code: uniqueCode(t), Name: "x", Protocol: "openai", AllowedHosts: []string{"example.com"}})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	for _, raw := range []string{
		"http://example.com/v1",              // 非 https
		"https://user:pw@example.com/v1",     // 带凭据
		"https://example.org/v1",             // 不在白名单
		"https://example.com/v1?x=1",         // 带 query
		"https://127.0.0.1/v1",               // 不在白名单（且是回环地址）
		"ftp://example.com",                  // 非 http(s)
		"not a url",                          // 非绝对地址
		"https://localhost.example.com.evil", // 不在白名单
	} {
		if _, err := s.validateUpstreamURL(ctx, pool, p.ID, raw); !errors.Is(err, ErrUnsafeUpstreamURL) {
			t.Errorf("validateUpstreamURL(%q) err = %v, want ErrUnsafeUpstreamURL", raw, err)
		}
	}

	// 没有白名单的供应商：仍然拒绝内网字面量地址。
	open, err := s.CreateProvider(ctx, CreateProviderInput{Code: uniqueCode(t), Name: "y", Protocol: "openai"})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	for _, raw := range []string{"https://10.0.0.5/v1", "https://[::1]/v1", "https://169.254.169.254/latest", "https://localhost/v1"} {
		if _, err := s.validateUpstreamURL(ctx, pool, open.ID, raw); !errors.Is(err, ErrUnsafeUpstreamURL) {
			t.Errorf("validateUpstreamURL(%q) err = %v, want ErrUnsafeUpstreamURL", raw, err)
		}
	}
	if got, err := s.validateUpstreamURL(ctx, pool, open.ID, "https://203.0.113.7/v1/"); err != nil || got != "https://203.0.113.7/v1" {
		t.Errorf("public IP literal: got %q err %v, want normalized https://203.0.113.7/v1", got, err)
	}
}

func TestListUpstreamModels_NoKEK(t *testing.T) {
	pool := testPool(t)
	s := New(pool, wallet.New(pool), nil, []byte(testPepper))
	s.SetUpstreamURLPolicy(PermissiveUpstreamURLPolicy())
	ctx := context.Background()
	p, err := s.CreateProvider(ctx, CreateProviderInput{Code: uniqueCode(t), Name: "x", Protocol: "openai"})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	pa, err := s.CreateProviderAccount(ctx, CreateProviderAccountInput{ProviderID: p.ID, Name: "a", BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("CreateProviderAccount: %v", err)
	}
	if _, err := s.ListUpstreamModels(ctx, pa.ID); !errors.Is(err, ErrKEKNotConfigured) {
		t.Fatalf("err = %v, want ErrKEKNotConfigured (not a nil-pointer panic)", err)
	}
}

func TestHourBucketLabel(t *testing.T) {
	sh, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skipf("no tzdata: %v", err)
	}
	if got := hourBucketLabel("2026-10-01T08:00:00", sh); got != "2026-10-01T08:00:00+08:00" {
		t.Errorf("Asia/Shanghai label = %q", got)
	}
	if got := hourBucketLabel("2026-10-01T08:00:00", time.UTC); got != "2026-10-01T08:00:00Z" {
		t.Errorf("UTC label = %q, want the legacy Z format", got)
	}
}

func TestPriceSourceConfigGuards(t *testing.T) {
	for key, secret := range map[string]bool{
		"api_key": true, "X-Api-Key": true, "access_token": true, "password": true, "Authorization": true,
		"row_selector": false, "model_name_map": false, "columns": false, "min_models": false,
	} {
		got := findSecretLikeKey(map[string]any{key: "v"}) != ""
		if got != secret {
			t.Errorf("findSecretLikeKey(%q) = %v, want %v", key, got, secret)
		}
	}
	if findSecretLikeKey(map[string]any{"headers": map[string]any{"cookie": "x"}}) == "" {
		t.Error("nested credential keys must be found")
	}
	for s, ok := range map[string]bool{"0 */6 * * *": true, "@hourly": true, "@every 30m": true, "@every 10s": false, "every hour": false, "* * *": false} {
		if validSchedule(s) != ok {
			t.Errorf("validSchedule(%q) = %v, want %v", s, !ok, ok)
		}
	}
}
