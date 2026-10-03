package config

import (
	"os"
	"testing"
	"time"
)

func TestLoad_Defaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Gateway.Addr != ":8080" {
		t.Errorf("Gateway.Addr = %q, want :8080", cfg.Gateway.Addr)
	}
	if cfg.Gateway.ReadTimeout != 30*time.Second {
		t.Errorf("Gateway.ReadTimeout = %v, want 30s", cfg.Gateway.ReadTimeout)
	}
	if cfg.Billing.ReservationTTL != 30*time.Minute {
		t.Errorf("Billing.ReservationTTL = %v, want 30m", cfg.Billing.ReservationTTL)
	}
	if cfg.Postgres.MaxConns != 20 {
		t.Errorf("Postgres.MaxConns = %d, want 20", cfg.Postgres.MaxConns)
	}
}

func TestLoad_EnvOverride(t *testing.T) {
	t.Setenv("UFT_GATEWAY_ADDR", ":9999")
	t.Setenv("UFT_GATEWAY_READ_TIMEOUT", "45s")
	t.Setenv("UFT_POSTGRES_MAX_CONNS", "40")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Gateway.Addr != ":9999" {
		t.Errorf("Gateway.Addr = %q, want :9999", cfg.Gateway.Addr)
	}
	if cfg.Gateway.ReadTimeout != 45*time.Second {
		t.Errorf("Gateway.ReadTimeout = %v, want 45s", cfg.Gateway.ReadTimeout)
	}
	if cfg.Postgres.MaxConns != 40 {
		t.Errorf("Postgres.MaxConns = %d, want 40", cfg.Postgres.MaxConns)
	}
}

func TestLoad_FileOverride(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/gateway.yaml"
	yaml := "gateway:\n  addr: \":7070\"\nbilling:\n  reserve_output_cap: 4096\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Gateway.Addr != ":7070" {
		t.Errorf("Gateway.Addr = %q, want :7070", cfg.Gateway.Addr)
	}
	if cfg.Billing.ReserveOutputCap != 4096 {
		t.Errorf("Billing.ReserveOutputCap = %d, want 4096", cfg.Billing.ReserveOutputCap)
	}
	// 文件未覆盖的字段应保留默认值
	if cfg.Redis.Addr != "localhost:6379" {
		t.Errorf("Redis.Addr = %q, want localhost:6379 (default)", cfg.Redis.Addr)
	}
}

func TestLoad_Defaults_CORSOriginsEmpty(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Gateway.CORSOrigins != "" {
		t.Errorf("Gateway.CORSOrigins = %q, want empty (CORS disabled by default)", cfg.Gateway.CORSOrigins)
	}
}

func TestLoad_EnvOverride_CORSOrigins(t *testing.T) {
	t.Setenv("UFT_GATEWAY_CORS_ORIGINS", "https://app.example.com,https://admin.example.com")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := "https://app.example.com,https://admin.example.com"
	if cfg.Gateway.CORSOrigins != want {
		t.Errorf("Gateway.CORSOrigins = %q, want %q", cfg.Gateway.CORSOrigins, want)
	}
}

func TestLoad_EnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/gateway.yaml"
	if err := os.WriteFile(path, []byte("gateway:\n  addr: \":7070\"\n"), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	t.Setenv("UFT_GATEWAY_ADDR", ":6060")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Gateway.Addr != ":6060" {
		t.Errorf("Gateway.Addr = %q, want :6060 (env should win over file)", cfg.Gateway.Addr)
	}
}

// datasync.* 的环境变量名与早期直接读环境变量时一致（UFT_DATASYNC_LLM_BASE_URL 等），
// 已配置好的部署不需要改名。
func TestLoad_DataSyncLLM(t *testing.T) {
	t.Setenv("UFT_DATASYNC_LLM_BASE_URL", "http://127.0.0.1:8080/v1")
	t.Setenv("UFT_DATASYNC_LLM_MODEL", "deepseek/deepseek-chat")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := DataSyncConfig{LLMBaseURL: "http://127.0.0.1:8080/v1", LLMModel: "deepseek/deepseek-chat", LLMAPIKeyEnv: "UFT_DATASYNC_LLM_API_KEY"}
	if cfg.DataSync != want {
		t.Errorf("DataSync = %+v, want %+v", cfg.DataSync, want)
	}
}
