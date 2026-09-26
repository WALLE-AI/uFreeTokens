// Package admin 是控制面的业务逻辑层：账户、API Key、Provider/渠道/虚拟模型/
// 价格的管理。在这个包出现之前，搭起一套可用的配置（账户、Key、渠道、价格）
// 只能靠手写 SQL——所有测试 fixture 都是这么干的。这个包让这些操作变成
// 可以通过 cmd/admin 的 HTTP 接口完成的正常操作。
//
// 已知的范围限制（尚未实现，非遗漏）：
//   - 鉴权只到"共享密钥"这一级（internal/app.NewAdminRouter 用
//     httpx.RequireBearerToken 挡在所有业务路由前面），不是技术方案 §7.15
//     设计的独立域名 + 多用户登录 + RBAC。知道这一个密钥的人能做任何操作，
//     没有"谁在操作、这个人能不能做这个操作"的概念——真正的分权需要先把
//     users/account_members（角色 owner/admin/developer/billing/viewer）
//     那套用户体系接起来，目前完全没有 Go 代码用到这两张表。这不再是
//     "完全没有鉴权"，但仍然只应该部署在内网/加一层反向代理，不能假设
//     单一密钥泄露后还有第二道防线。
//   - 审计日志（audit.go 的 RecordAudit/ListAuditLogs）只接入了少数几个高价值
//     操作（钱包调账/赠款、上游 Key 添加、成本价/售价/汇率发布），不是每一个
//     写操作都会审计；ActorID 只能靠调用方在 X-Actor-ID 请求头里自觉声明，
//     因为还没有真正的管理员登录/鉴权，见上面第一条。
//   - 价格/渠道的更新都是"新增一条"，没有校验/审批流程；改错了只能再插一条
//     新版本覆盖，不能内联编辑历史版本（这是故意的——价格版本化本身要求历史
//     不可篡改，见技术方案 §6.4）。
package admin

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/WALLE-AI/uFreeTokens/internal/secretbox"
	"github.com/WALLE-AI/uFreeTokens/internal/wallet"
)

type Service struct {
	pool   *pgxpool.Pool
	wallet *wallet.Service
	box    *secretbox.Box
	pepper []byte
}

func New(pool *pgxpool.Pool, walletSvc *wallet.Service, box *secretbox.Box, pepper []byte) *Service {
	return &Service{pool: pool, wallet: walletSvc, box: box, pepper: pepper}
}

// Wallet 暴露内部持有的 wallet.Service，供余额调整这类"属于钱包、但由控制面
// 发起"的操作使用（比如手工充值）。真正执行写入的仍然是 wallet 包自己的方法，
// 这里只是把已经装配好的实例递出去，不是让 admin 包自己获得写钱包表的能力。
func (s *Service) Wallet() *wallet.Service { return s.wallet }
