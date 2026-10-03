package routes

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/WALLE-AI/uFreeTokens/internal/adminauth"
	"github.com/WALLE-AI/uFreeTokens/internal/agent/kernel"
)

// Dispatcher 以给定管理员身份在进程内调用管理路由（设计 §3.3）：Handler 是由路由表构造、
// 保留权限校验与幂等中间件、但不含令牌鉴权的内部子路由；响应写到内存，不经过网络。
type Dispatcher struct {
	Handler http.Handler
	// MaxResponseBytes 是响应体上限，超过的部分丢弃（工具结果交给模型前还会再裁剪）；0 = 2 MiB。
	MaxResponseBytes int
}

// Response 是一次进程内调用的结果。
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// ErrBreakGlass：应急令牌身份（system，拥有全部权限）不能驱动智能体。
var ErrBreakGlass = errors.New("agent: the break-glass identity cannot use the agent")

// Do 以 p 的身份执行一次请求。ref 写入 ctx 供审计关联；hdr 可附加 Idempotency-Key / If-Match。
func (d *Dispatcher) Do(ctx context.Context, p *adminauth.Principal, ref kernel.CallRef, method, path string, query url.Values, body []byte, hdr http.Header) (*Response, error) {
	if p == nil {
		return nil, errors.New("agent: no principal")
	}
	if p.BreakGlass {
		return nil, ErrBreakGlass
	}
	u := path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader = http.NoBody
	if body != nil {
		rd = bytes.NewReader(body)
	}
	// 清掉外层路由的 chi RouteContext：调用方的 ctx 往往来自 /agent/* 的 HTTP 请求，chi 发现 ctx 里已有
	// RouteContext 时会把内部路由当成挂载的子路由、沿用外层的方法与路径来匹配（GET 工具会被当成 POST）。
	ctx = context.WithValue(ctx, chi.RouteCtxKey, nil)
	ctx = adminauth.WithPrincipal(ctx, p)
	if ref.ToolCallID != "" {
		ctx = kernel.WithCall(ctx, ref)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "uft-agent")
	req.RemoteAddr = "127.0.0.1:0"
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	max := d.MaxResponseBytes
	if max <= 0 {
		max = 2 << 20
	}
	w := &memWriter{header: http.Header{}, max: max}
	d.Handler.ServeHTTP(w, req)
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return &Response{Status: w.status, Header: w.header, Body: w.buf.Bytes()}, nil
}

// memWriter 是内存中的 http.ResponseWriter。
type memWriter struct {
	header http.Header
	status int
	buf    bytes.Buffer
	max    int
}

func (w *memWriter) Header() http.Header { return w.header }

func (w *memWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}

func (w *memWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if room := w.max - w.buf.Len(); room > 0 {
		if len(b) > room {
			w.buf.Write(b[:room])
		} else {
			w.buf.Write(b)
		}
	}
	return len(b), nil
}
