// Command devseed 给本地开发库写入一套演示数据（scripts/dev-stack.sh seed 调用）。
//
// 除管理员账号外，所有数据都通过真实的管理接口 / 控制台接口写入（而不是直接写表），
// 所以流水、审计日志、价格版本、钱包余额都是一致的，和运营在后台手工操作的结果一样：
//
//   - 5 个管理员账号，每种角色一个（直接用 internal/adminauth 创建，没有对应的 HTTP 接口
//     可以在"还没有任何管理员"时调用）；
//   - 汇率 USD→CNY；供应商 mockai + 上游账号（指向 tools/mockupstream）+ 密钥；
//   - 通过批量导入接口上架 7 个 mock 模型（含用来测试异常的 mock-fail / mock-limit / mock-slow）；
//   - 1 个控制台用户（经 /console/register 注册），人工充值 ¥100、赠送 ¥10，并代开一把 API Key。
//
// 只应对刚 reset 过的空库运行；检测到已经 seed 过会直接退出。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/WALLE-AI/uFreeTokens/internal/adminauth"
)

const (
	adminPassword = "Uft-dev-2026!"
	userEmail     = "demo@uft.local"
	userPassword  = "Demo-user-2026"
)

var admins = []struct{ email, name, role string }{
	{"admin@uft.local", "超级管理员", "super_admin"},
	{"operator@uft.local", "运营小王", "operator"},
	{"pricing@uft.local", "定价小李", "pricing"},
	{"finance@uft.local", "财务小张", "finance"},
	{"support@uft.local", "客服小赵", "support"},
}

type client struct {
	base, token string
}

func (c client) call(method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, raw)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "devseed:", err)
		os.Exit(1)
	}
}

func main() {
	adminURL := flag.String("admin", "http://localhost:8081", "cmd/admin 地址")
	gatewayURL := flag.String("gateway", "http://localhost:8080", "cmd/gateway 地址")
	token := flag.String("token", "dev-admin-token-change-me", "应急令牌 UFT_ADMIN_TOKEN")
	mock := flag.String("mock", "http://127.0.0.1:18099/v1", "mock 上游地址")
	dsn := flag.String("dsn", "postgres://uft:uft@localhost:55432/uft_dev?sslmode=disable", "开发库 DSN（创建管理员账号用）")
	flag.Parse()
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, *dsn)
	must(err)
	defer pool.Close()
	var seeded bool
	must(pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM admin_users WHERE email = $1)`, admins[0].email).Scan(&seeded))
	if seeded {
		fmt.Println("devseed: 已经 seed 过（admin@uft.local 已存在）。要清空重来请执行 ./scripts/dev-all.sh reset")
		return
	}

	// 1) 管理员账号
	auth := adminauth.New(pool, adminauth.Config{})
	for _, a := range admins {
		_, err := auth.CreateAdmin(ctx, adminauth.CreateAdminInput{Email: a.email, Name: a.name, Password: adminPassword, Roles: []string{a.role}})
		must(err)
	}
	fmt.Println("✓ 管理员账号 ×5")

	ac := client{base: *adminURL, token: *token}

	// 2) 汇率与供应商
	must(ac.call("POST", "/fx-rates", map[string]any{"base": "USD", "quote": "CNY", "rate": "7.2", "source": "seed"}, nil))
	var provider, account struct {
		ID int64 `json:"id"`
	}
	must(ac.call("POST", "/providers", map[string]any{"code": "mockai", "name": "MockAI（本地模拟上游）", "protocol": "openai"}, &provider))
	must(ac.call("POST", "/provider-accounts", map[string]any{"provider_id": provider.ID, "name": "mock-account", "base_url": *mock, "cost_multiplier": "1"}, &account))
	must(ac.call("POST", fmt.Sprintf("/provider-accounts/%d/keys", account.ID), map[string]any{"secret": "sk-mock-dev-key", "weight": 100}, nil))
	fmt.Println("✓ 汇率 USD→CNY 7.2；供应商 mockai + 上游账号 + 密钥")

	// 3) 批量导入模型（服务端按 30% 加价计算售价）
	item := func(model, family, typ string, in, out string) map[string]any {
		return map[string]any{"upstream_model": model, "family": family, "type": typ, "context_window": 128000, "max_output": 8192,
			"capabilities": []string{"stream", "tools"}, "cost_input": in, "cost_output": out}
	}
	var imported struct {
		Items []struct {
			UpstreamModel string                    `json:"upstream_model"`
			OK            bool                      `json:"ok"`
			Errors        []string                  `json:"errors"`
			Error         *struct{ Message string } `json:"error"`
		} `json:"items"`
	}
	must(ac.call("POST", fmt.Sprintf("/provider-accounts/%d/import-models", account.ID), map[string]any{
		"dry_run": false, "currency": "USD", "markup_percent": "30",
		"items": []any{
			item("mock-chat", "mock", "chat", "0.5", "1.5"),
			item("mock-chat-pro", "mock", "chat", "2", "6"),
			item("mock-reasoner", "mock", "chat", "1", "4"),
			item("mock-embedding", "mock", "embedding", "0.1", "0.1"),
			item("mock-fail", "mock-test", "chat", "0.5", "1.5"),
			item("mock-limit", "mock-test", "chat", "0.5", "1.5"),
			item("mock-slow", "mock-test", "chat", "0.5", "1.5"),
		},
	}, &imported))
	for _, it := range imported.Items {
		if !it.OK {
			msg := strings.Join(it.Errors, "、")
			if it.Error != nil {
				msg = it.Error.Message
			}
			must(fmt.Errorf("导入 %s 失败：%s", it.UpstreamModel, msg))
		}
	}
	fmt.Printf("✓ 上架 %d 个模型\n", len(imported.Items))

	// 公开目录的展示信息（web 模型广场显示用）
	var vm struct {
		ID int64 `json:"id"`
	}
	if ac.call("GET", "/virtual-models/lookup?name=mock-chat", nil, &vm) == nil {
		_ = ac.call("PUT", fmt.Sprintf("/virtual-models/%d/metadata", vm.ID), map[string]any{
			"display_name": "Mock Chat", "description": "本地模拟模型，回显你的输入，用于联调计费与日志。",
			"provider_display": "MockAI", "tags": []string{"测试", "对话"}, "scores": map[string]any{},
		}, nil)
	}

	// 4) 控制台用户：注册 → 充值 → 赠送 → 代开 API Key
	gc := client{base: *gatewayURL}
	var reg struct {
		AccountID int64 `json:"account_id"`
	}
	must(gc.call("POST", "/console/register", map[string]any{"email": userEmail, "password": userPassword}, &reg))
	must(ac.call("POST", fmt.Sprintf("/accounts/%d/wallet/adjust", reg.AccountID), map[string]any{
		"amount_micro": 100_000_000, "ref_id": "SEED-TOPUP-001", "reason": "开发环境初始充值"}, nil))
	must(ac.call("POST", fmt.Sprintf("/accounts/%d/credit-grants", reg.AccountID), map[string]any{
		"source": "promotion", "amount_micro": 10_000_000, "ref_id": "SEED-GRANT-001", "reason": "开发环境赠送",
		"expires_at": time.Now().AddDate(0, 3, 0).UTC().Format(time.RFC3339)}, nil))
	var key struct {
		RawKey string `json:"raw_key"`
	}
	must(ac.call("POST", fmt.Sprintf("/accounts/%d/api-keys", reg.AccountID), map[string]any{"name": "seed-key"}, &key))
	fmt.Printf("✓ 控制台用户 %s（账户 #%d）：现金 ¥100、赠送 ¥10\n", userEmail, reg.AccountID)

	out := fmt.Sprintf(`管理员（运营后台 http://localhost:3001，密码均为 %s）：
  admin@uft.local     超级管理员
  operator@uft.local  运营
  pricing@uft.local   定价
  finance@uft.local   财务
  support@uft.local   客服（只读）
控制台用户（用户端 http://localhost:3000）：%s / %s（账户 #%d）
API Key（只显示这一次）：%s
`, adminPassword, userEmail, userPassword, reg.AccountID, key.RawKey)
	_ = os.WriteFile("tmp/dev-stack/seed-output.txt", []byte(out), 0o600)
	fmt.Println()
	fmt.Print(out)
	fmt.Println("（以上信息已保存到 tmp/dev-stack/seed-output.txt）")
}
