// Package adminauth 是运营后台（cmd/admin）的管理员身份、会话与权限（RBAC）。
//
// 在它之前，cmd/admin 只有一个共享令牌，审计日志里的操作人来自客户端自填的
// X-Actor-* 请求头。现在：
//   - 管理员用邮箱 + 密码（argon2id，复用 internal/console 的实现）登录，拿到一个
//     不透明会话令牌，库里只存 SHA-256；
//   - 会话滑动续期（默认 12 小时无操作过期），另有绝对过期（默认 7 天）；
//   - 权限点是 "资源:动作" 字符串，挂在角色上，角色挂在管理员上；
//   - 共享令牌降级为应急通道（break-glass），身份固定为 system(id=0)、拥有全部
//     权限，可以通过配置关闭。
//
// 见 docs/运营后台接口与数据库设计问题分析及执行方案.md §3 B5。
package adminauth

import (
	"context"
	"slices"
)

// Permission 是 "资源:动作" 形式的权限点。
type Permission string

const (
	PermAll Permission = "*"

	PermAccountRead        Permission = "account:read"
	PermAccountWrite       Permission = "account:write"
	PermWalletAdjust       Permission = "wallet:adjust"
	PermCatalogRead        Permission = "catalog:read"
	PermCatalogWrite       Permission = "catalog:write"
	PermProviderKeyWrite   Permission = "provider_key:write"
	PermPricingRead        Permission = "pricing:read"
	PermPricingWrite       Permission = "pricing:write"
	PermPriceChangeApprove Permission = "price_change:approve"
	PermObserveRead        Permission = "observe:read"
	PermAuditRead          Permission = "audit:read"
	PermAdminUserManage    Permission = "admin_user:manage"
	// 运营智能体（Harness）：agent:use 能发起对话与处理提案（写操作仍按各自路由的权限校验），
	// agent:admin 能管理后台智能作业。
	PermAgentUse   Permission = "agent:use"
	PermAgentAdmin Permission = "agent:admin"
)

// AllPermissions 列出全部权限点，供 /meta、前端展示和测试使用。
var AllPermissions = []Permission{
	PermAccountRead, PermAccountWrite, PermWalletAdjust,
	PermCatalogRead, PermCatalogWrite, PermProviderKeyWrite,
	PermPricingRead, PermPricingWrite, PermPriceChangeApprove,
	PermObserveRead, PermAuditRead, PermAdminUserManage,
	PermAgentUse, PermAgentAdmin,
}

// SystemAdminID 是 admin_users 里保留的 system 身份（迁移 00016 插入）：历史
// 审计记录、应急令牌、系统自动操作都记在它名下。
const SystemAdminID int64 = 0

// Principal 是一次请求的已认证管理员身份，由鉴权中间件放进 context。
type Principal struct {
	AdminID     int64
	Name        string
	Email       string
	Roles       []string
	Permissions []Permission
	SessionID   int64 // 应急令牌登录时为 0
	BreakGlass  bool  // 通过共享应急令牌认证
}

// Can 判断是否拥有某个权限点；拥有 "*" 视为拥有全部。
func (p *Principal) Can(perm Permission) bool {
	if p == nil {
		return false
	}
	return slices.Contains(p.Permissions, PermAll) || slices.Contains(p.Permissions, perm)
}

// SystemPrincipal 是应急令牌对应的身份。
func SystemPrincipal() *Principal {
	return &Principal{AdminID: SystemAdminID, Name: "system", Roles: []string{"super_admin"}, Permissions: []Permission{PermAll}, BreakGlass: true}
}

type principalCtxKey struct{}

func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalCtxKey{}, p)
}

// FromContext 取出当前请求的管理员身份；未认证时返回 nil。
func FromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalCtxKey{}).(*Principal)
	return p
}
