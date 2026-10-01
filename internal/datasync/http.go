package datasync

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"
)

// UserAgent 明确标识自己是采集机器人（技术方案 §1.1：不伪装成浏览器，对方看日志时知道是谁）。
const UserAgent = "uFreeTokensBot/1.0 (+https://ufreetokens.ai/bot; data-sync)"

// DefaultMaxBytes 是单个响应体的上限（LMArena 单个 parquet ~1MB，Epoch 的 zip ~3MB，models.dev ~5MB）。
const DefaultMaxBytes = 64 << 20

// Env 是 Job 执行时可用的环境：带每域限速、私网拦截、条件请求的 HTTP 客户端。
type Env struct {
	client      *http.Client
	minInterval time.Duration

	mu       sync.Mutex
	lastHit  map[string]time.Time
	hostLock map[string]*sync.Mutex
}

// EnvOptions 配置 Env。零值即生产默认：拒绝私网地址、同一主机两次请求至少间隔 2 秒。
type EnvOptions struct {
	// AllowPrivateNetworks 允许访问回环 / 内网地址（测试用 httptest.Server，或经内网代理出网）。
	AllowPrivateNetworks bool
	// MinHostInterval 是同一主机两次请求的最小间隔；0 = 2 秒，负数 = 不限速（测试用）。
	MinHostInterval time.Duration
	Timeout         time.Duration
}

func NewEnv(opts EnvOptions) *Env {
	interval := opts.MinHostInterval
	if interval == 0 {
		interval = 2 * time.Second
	}
	if interval < 0 {
		interval = 0
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	return &Env{
		client:      newHTTPClient(opts.AllowPrivateNetworks, timeout),
		minInterval: interval,
		lastHit:     map[string]time.Time{},
		hostLock:    map[string]*sync.Mutex{},
	}
}

var errUnsafeAddr = errors.New("datasync: refusing to connect to a private or reserved address")

func disallowedAddr(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsMulticast() || a.IsUnspecified() || netip.MustParsePrefix("100.64.0.0/10").Contains(a)
}

func newHTTPClient(allowPrivate bool, timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if !allowPrivate {
		// 来源 URL 由运营配置：在拨号阶段校验实际连接的地址，防 SSRF / DNS rebinding。
		dialer := &net.Dialer{
			Timeout: 15 * time.Second,
			Control: func(_, address string, _ syscall.RawConn) error {
				host, _, err := net.SplitHostPort(address)
				if err != nil {
					return err
				}
				a, err := netip.ParseAddr(host)
				if err != nil || disallowedAddr(a) {
					return fmt.Errorf("%w (%s)", errUnsafeAddr, host)
				}
				return nil
			},
		}
		transport.DialContext = dialer.DialContext
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		// 数据集托管（如 hf-mirror 的 resolve 链接）会 302 到 CDN，需要跟随；只允许 http(s)，最多 5 跳。
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("datasync: too many redirects")
			}
			if req.URL.Scheme != "https" && req.URL.Scheme != "http" {
				return fmt.Errorf("datasync: refusing redirect to %s", req.URL.Scheme)
			}
			return nil
		},
	}
}

// GetOptions 是一次 GET 的可选项。
type GetOptions struct {
	// ETag / LastModified 非空时发条件请求；对方返回 304 时 Response.NotModified=true。
	ETag         string
	LastModified string
	Headers      map[string]string
	MaxBytes     int64
}

// Response 是一次 GET 的结果。
type Response struct {
	Body         []byte
	NotModified  bool
	ETag         string
	LastModified string
	Hash         []byte // sha256(Body)
}

// Get 发一次 GET：限速、UA、条件请求、大小上限、非 200/304 视为错误。
func (e *Env) Get(ctx context.Context, rawURL string, opts GetOptions) (*Response, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("datasync: invalid url %q", rawURL)
	}
	if err := e.wait(ctx, strings.ToLower(u.Hostname())); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("datasync: build request: %w", err)
	}
	req.Header.Set("User-Agent", UserAgent)
	if opts.ETag != "" {
		req.Header.Set("If-None-Match", opts.ETag)
	}
	if opts.LastModified != "" {
		req.Header.Set("If-Modified-Since", opts.LastModified)
	}
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("datasync: GET %s: %w", u.Redacted(), err)
	}
	defer resp.Body.Close()
	out := &Response{ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified")}
	if resp.StatusCode == http.StatusNotModified {
		out.NotModified = true
		return out, nil
	}
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return nil, fmt.Errorf("datasync: GET %s: status %d: %s", u.Redacted(), resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	limit := opts.MaxBytes
	if limit <= 0 {
		limit = DefaultMaxBytes
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("datasync: read %s: %w", u.Redacted(), err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("datasync: response from %s exceeds %d bytes", u.Redacted(), limit)
	}
	sum := sha256.Sum256(body)
	out.Body, out.Hash = body, sum[:]
	return out, nil
}

// wait 保证同一主机两次请求至少间隔 minInterval（同一主机的请求串行化）。
func (e *Env) wait(ctx context.Context, host string) error {
	if e.minInterval <= 0 {
		return nil
	}
	e.mu.Lock()
	l, ok := e.hostLock[host]
	if !ok {
		l = &sync.Mutex{}
		e.hostLock[host] = l
	}
	e.mu.Unlock()

	l.Lock()
	defer l.Unlock()
	e.mu.Lock()
	last := e.lastHit[host]
	e.mu.Unlock()
	if d := time.Until(last.Add(e.minInterval)); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	e.mu.Lock()
	e.lastHit[host] = time.Now()
	e.mu.Unlock()
	return nil
}

// HashParts 把多个响应的 hash 合并成一个（多 URL 来源判断"整体内容没变"）。
func HashParts(parts ...[]byte) []byte {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}
