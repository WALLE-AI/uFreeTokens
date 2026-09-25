package app

import (
	"context"
	"net/http"
	"time"
)

// timeoutCtx 派生一个带超时的 context，用于健康检查等不应阻塞太久的探针请求。
func timeoutCtx(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}
