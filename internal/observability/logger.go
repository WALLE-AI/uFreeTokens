// Package observability 统一初始化日志、指标与（后续）链路追踪。
package observability

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/WALLE-AI/uFreeTokens/internal/config"
)

// contextKey 用于在 context 中传递 request-scoped logger。
type ctxKey struct{}

var loggerKey ctxKey

// NewLogger 按配置构造 slog.Logger。format=json 用于生产环境（结构化采集），
// format=console 在本地开发时输出更易读的文本。
func NewLogger(cfg config.LogConfig) *slog.Logger {
	level := parseLevel(cfg.Level)
	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if strings.EqualFold(cfg.Format, "console") {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(handler)
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// WithLogger 把 logger 绑定到 context，供中间件（如 access log、request id 注入）使用。
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, l)
}

// FromContext 取出 context 中的 logger；不存在时回退到 slog.Default()。
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}
