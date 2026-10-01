package admin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/store"
)

// 上游 base_url 的安全校验（防 SSRF / 上游密钥外带），见
// docs/运营后台接口与数据库设计问题分析及执行方案.md §2.1 S4。
//
// base_url 会被 cmd/gateway 用来转发真实流量，也会被 ListUpstreamModels 带着
// 解密后的上游密钥请求——如果允许指向任意主机，持有写权限的人就能把密钥发到
// 自己的服务器，或者让服务端去探测内网。因此：
//   - 只允许 https（开发环境可放开 http）；
//   - 不允许 userinfo（https://user:pass@host）；
//   - 供应商配置了 allowed_hosts 时，主机必须等于其中一项或是其子域名；
//   - 主机解析出的任一地址落在回环/私网/链路本地/CGNAT 等范围内即拒绝，并且
//     真正发请求时在拨号阶段再校验一次，防 DNS rebinding。

var (
	ErrUnsafeUpstreamURL = errors.New("admin: unsafe upstream url")
	ErrKEKNotConfigured  = errors.New("admin: server has no KEK configured")
	ErrVersionConflict   = errors.New("admin: version conflict")
)

// UpstreamURLPolicy 控制 base_url 校验的严格程度。零值即最严格的生产策略。
type UpstreamURLPolicy struct {
	AllowHTTP            bool // 允许 http://（仅开发环境）
	AllowPrivateNetworks bool // 允许回环/私网地址（仅开发与测试环境）
}

// PermissiveUpstreamURLPolicy 用于本地联调与测试（上游是 httptest/本机 mock）。
func PermissiveUpstreamURLPolicy() UpstreamURLPolicy {
	return UpstreamURLPolicy{AllowHTTP: true, AllowPrivateNetworks: true}
}

// SetUpstreamURLPolicy 替换 base_url 校验策略；默认是最严格的零值策略。
func (s *Service) SetUpstreamURLPolicy(p UpstreamURLPolicy) { s.urlPolicy = p }

func unsafeURL(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrUnsafeUpstreamURL, fmt.Sprintf(format, a...))
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

func isDisallowedAddr(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsInterfaceLocalMulticast() || a.IsMulticast() || a.IsUnspecified() || cgnat.Contains(a) ||
		(a.Is4() && a.As4()[0] == 0)
}

// hostAllowed 判断 host 是否等于 allowed 中的一项或是其子域名；allowed 为空视为全部允许。
func hostAllowed(host string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, a := range allowed {
		a = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(a), "."))
		if a != "" && (host == a || strings.HasSuffix(host, "."+a)) {
			return true
		}
	}
	return false
}

// validateUpstreamURL 校验 raw 是否可以作为 providerID 名下账号的 base_url，
// 返回规范化后的地址（去掉末尾的 '/'）。
func (s *Service) validateUpstreamURL(ctx context.Context, q store.Querier, providerID int64, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", unsafeURL("base_url must be an absolute URL")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !s.urlPolicy.AllowHTTP {
			return "", unsafeURL("base_url must use https")
		}
	default:
		return "", unsafeURL("base_url must use https")
	}
	if u.User != nil {
		return "", unsafeURL("base_url must not contain credentials")
	}
	if u.Fragment != "" || u.RawQuery != "" {
		return "", unsafeURL("base_url must not contain a query string or fragment")
	}
	var allowed []string
	if err := q.QueryRow(ctx, `SELECT allowed_hosts FROM providers WHERE id = $1`, providerID).Scan(&allowed); err != nil {
		if isNoRows(err) {
			return "", ErrProviderNotFound
		}
		return "", fmt.Errorf("admin: load provider allowed_hosts: %w", err)
	}
	host := u.Hostname()
	if !hostAllowed(host, allowed) {
		return "", unsafeURL("host %q is not in the provider's allowed_hosts %v", host, allowed)
	}
	if !s.urlPolicy.AllowPrivateNetworks {
		if err := checkHostResolvesPublic(ctx, host); err != nil {
			return "", err
		}
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func checkHostResolvesPublic(ctx context.Context, host string) error {
	if a, err := netip.ParseAddr(host); err == nil {
		if isDisallowedAddr(a) {
			return unsafeURL("host %s is a private or reserved address", host)
		}
		return nil
	}
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return unsafeURL("host %s is a private or reserved address", host)
	}
	lctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupNetIP(lctx, "ip", host)
	if err != nil || len(addrs) == 0 {
		return unsafeURL("host %s cannot be resolved", host)
	}
	for _, a := range addrs {
		if isDisallowedAddr(a) {
			return unsafeURL("host %s resolves to a private or reserved address (%s)", host, a)
		}
	}
	return nil
}

// upstreamHTTPClient 返回请求上游用的客户端：策略不允许私网时，在拨号阶段
// 对实际连接的地址再校验一次（DNS rebinding 下解析结果可能与校验时不同）。
func (s *Service) upstreamHTTPClient() *http.Client {
	if s.urlPolicy.AllowPrivateNetworks {
		return discoveryHTTPClient
	}
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			a, err := netip.ParseAddr(host)
			if err != nil || isDisallowedAddr(a) {
				return unsafeURL("refusing to connect to private or reserved address %s", host)
			}
			return nil
		},
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = dialer.DialContext
	transport.Proxy = nil
	return &http.Client{
		Timeout:   discoveryHTTPClient.Timeout,
		Transport: transport,
		// 重定向目标同样可能指向内网或别的主机，一律不跟随。
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// normalizeHosts 校验并规范化域名白名单：小写、去空格与末尾的点、去重；
// 只接受裸主机名（不含协议、端口、路径）。
func normalizeHosts(in []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, h := range in {
		h = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(h), "."))
		if h == "" {
			continue
		}
		if strings.ContainsAny(h, "/:@ ") || len(h) > 253 {
			return nil, invalid("allowed_hosts entries must be bare host names, got %q", h)
		}
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out, nil
}
