// Package admin 是控制面的业务逻辑层：账户、API Key、Provider/渠道/虚拟模型/
// 价格的管理。在这个包出现之前，搭起一套可用的配置（账户、Key、渠道、价格）
// 只能靠手写 SQL——所有测试 fixture 都是这么干的。这个包让这些操作变成
// 可以通过 cmd/admin 的 HTTP 接口完成的正常操作。
//
// 已知的范围限制（尚未实现，非遗漏）：
//   - 没有鉴权/权限控制（技术方案 §7.15 设计的是独立域名 + RBAC）。当前
//     cmd/admin 的接口对任何能连到它的人都完全开放，只应该部署在内网/加一层
//     反向代理鉴权，不能直接暴露到公网。这是当前最大的一个安全缺口，必须在
//     生产部署前解决。
//   - 没有审计日志（admin_audit_logs 表已经在迁移里，但这里还没有写它）。
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
