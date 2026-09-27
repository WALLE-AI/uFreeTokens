// Package config 负责加载进程配置：默认值 < YAML 文件 < 环境变量（UFT_ 前缀）。
// 业务配置（价格、渠道、促销……）不在这里，那些在 DB 中由 internal/catalog 管理。
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

type Config struct {
	Gateway  GatewayConfig  `koanf:"gateway"`
	Metrics  MetricsConfig  `koanf:"metrics"`
	Retry    RetryConfig    `koanf:"retry"`
	Billing  BillingConfig  `koanf:"billing"`
	Postgres PostgresConfig `koanf:"postgres"`
	Redis    RedisConfig    `koanf:"redis"`
	Secrets  SecretsConfig  `koanf:"secrets"`
	Log      LogConfig      `koanf:"log"`
	Console  ConsoleConfig  `koanf:"console"`
}

type GatewayConfig struct {
	Addr              string        `koanf:"addr"`
	MaxBodyBytes      int64         `koanf:"max_body_bytes"`
	ReadHeaderTimeout time.Duration `koanf:"read_header_timeout"`
	ReadTimeout       time.Duration `koanf:"read_timeout"`
	IdleTimeout       time.Duration `koanf:"idle_timeout"`
	StreamIdleTimeout time.Duration `koanf:"stream_idle_timeout"`
	ShutdownGrace     time.Duration `koanf:"shutdown_grace"`
	// CORSOrigins 是逗号分隔的允许跨域调用 /v1 的 Origin 白名单（环境变量
	// UFT_GATEWAY_CORS_ORIGINS）；留空（默认）不启用 CORS，同源反代场景不需要它
	// （dev 用 Vite proxy，prod 用 Nginx，见 frontend/web/README.md）。存成单个
	// 字符串而不是 []string：koanf 的环境变量 provider 不会把逗号分隔的字符串
	// 拆成 slice，拆分交给 app.NewGatewayRouter 做。
	CORSOrigins string `koanf:"cors_origins"`
}

type MetricsConfig struct {
	Addr string `koanf:"addr"`
}

type RetryConfig struct {
	MaxAttempts   int           `koanf:"max_attempts"`
	TotalDeadline time.Duration `koanf:"total_deadline"`
	BudgetRatio   float64       `koanf:"budget_ratio"`
}

type BillingConfig struct {
	ReserveOutputCap int           `koanf:"reserve_output_cap"`
	ReservationTTL   time.Duration `koanf:"reservation_ttl"`
	Rounding         string        `koanf:"rounding"`
}

type PostgresConfig struct {
	DSN      string `koanf:"dsn"`
	MaxConns int32  `koanf:"max_conns"`
}

type RedisConfig struct {
	Addr     string `koanf:"addr"`
	Password string `koanf:"password"`
	DB       int    `koanf:"db"`
}

type SecretsConfig struct {
	KEKSource       string `koanf:"kek_source"` // env / aliyun-kms / aws-kms
	KEKEnv          string `koanf:"kek_env"`
	APIKeyPepperEnv string `koanf:"api_key_pepper_env"`
	AdminTokenEnv   string `koanf:"admin_token_env"` // cmd/admin 的共享密钥鉴权（httpx.RequireBearerToken），见 internal/app.NewAdminRouter
}

type LogConfig struct {
	Level  string `koanf:"level"`
	Format string `koanf:"format"` // console / json
}

// ConsoleConfig 控制 internal/console（/console/* 自助控制台接口）的行为。
type ConsoleConfig struct {
	// CookieSecure 见 console.Config.CookieSecure 的注释：本地 http 开发环境
	// 必须留空/false，生产 HTTPS 环境应该设为 true。
	CookieSecure bool `koanf:"cookie_secure"`
}

func defaults() *koanf.Koanf {
	k := koanf.New(".")
	_ = k.Load(confmap.Provider(map[string]any{
		"gateway.addr":                ":8080",
		"gateway.max_body_bytes":      20 * 1024 * 1024,
		"gateway.read_header_timeout": "5s",
		"gateway.read_timeout":        "30s",
		"gateway.idle_timeout":        "120s",
		"gateway.stream_idle_timeout": "120s",
		"gateway.shutdown_grace":      "30s",
		"metrics.addr":                ":9090",
		"retry.max_attempts":          3,
		"retry.total_deadline":        "90s",
		"retry.budget_ratio":          0.2,
		"billing.reserve_output_cap":  8192,
		"billing.reservation_ttl":     "30m",
		"billing.rounding":            "ceil_micro",
		"postgres.dsn":                "postgres://uft:uft@localhost:5432/uft?sslmode=disable",
		"postgres.max_conns":          20,
		"redis.addr":                  "localhost:6379",
		"redis.db":                    0,
		"secrets.kek_source":          "env",
		"secrets.kek_env":             "UFT_KEK",
		"secrets.api_key_pepper_env":  "UFT_KEY_PEPPER",
		"secrets.admin_token_env":     "UFT_ADMIN_TOKEN",
		"log.level":                   "info",
		"log.format":                  "json",
		"console.cookie_secure":       false,
	}, "."), nil)
	return k
}

// Load 按 defaults < file < env 的顺序合并配置。path 为空时跳过文件层。
func Load(path string) (*Config, error) {
	k := defaults()

	if path != "" {
		if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
			return nil, fmt.Errorf("config: load file %s: %w", path, err)
		}
	}

	// 我们的配置结构只有两层：<section>.<field>，且 field 本身可能含下划线
	// （如 gateway.read_timeout、secrets.api_key_pepper_env）。所以只在第一个
	// 下划线处切分成 section/field，其余下划线原样保留，不能整串替换成点号，
	// 否则 UFT_GATEWAY_READ_TIMEOUT 会变成 gateway.read.timeout 而匹配不到
	// gateway.read_timeout，导致该项配置被静默忽略（这里曾经是个真实 bug，
	// 由 config_test.go 的 TestLoad_EnvOverride 捕获）。
	// 例：UFT_GATEWAY_READ_TIMEOUT -> gateway.read_timeout
	if err := k.Load(env.Provider("UFT_", ".", func(s string) string {
		s = strings.ToLower(strings.TrimPrefix(s, "UFT_"))
		section, field, found := strings.Cut(s, "_")
		if !found {
			return s
		}
		return section + "." + field
	}), nil); err != nil {
		return nil, fmt.Errorf("config: load env: %w", err)
	}

	var cfg Config
	uc := koanf.UnmarshalConf{
		Tag: "koanf",
		DecoderConfig: &mapstructure.DecoderConfig{
			Result:           &cfg,
			WeaklyTypedInput: true,
			DecodeHook: mapstructure.ComposeDecodeHookFunc(
				mapstructure.StringToTimeDurationHookFunc(),
			),
		},
	}
	if err := k.UnmarshalWithConf("", &cfg, uc); err != nil {
		return nil, fmt.Errorf("config: unmarshal: %w", err)
	}
	return &cfg, nil
}
