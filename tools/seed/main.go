// Command seed 向本地开发数据库写入一个可用的测试账户 + API Key + 一个虚拟模型，
// 用于手工联调 /v1/* 接口。仅供本地开发使用，不用于生产。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/WALLE-AI/uFreeTokens/internal/auth"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "seed: fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	dsn := flag.String("dsn", "postgres://uft:uft@localhost:5432/uft?sslmode=disable", "postgres DSN")
	pepper := flag.String("pepper", "dev-pepper-change-me", "api key HMAC pepper，须与网关 UFT_KEY_PEPPER 一致")
	flag.Parse()

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()

	var accountID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO accounts (type, name, status, tier) VALUES ('personal', 'seed-dev-account', 'active', 'free') RETURNING id`,
	).Scan(&accountID); err != nil {
		return fmt.Errorf("insert account: %w", err)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO wallets (account_id, cash_balance, bonus_balance) VALUES ($1, 5000000, 0)`, accountID); err != nil {
		return fmt.Errorf("insert wallet: %w", err)
	}

	var userID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, status, email_verified) VALUES ('seed@example.com', 'x', 'active', true) RETURNING id`,
	).Scan(&userID); err != nil {
		return fmt.Errorf("insert user: %w", err)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO account_members (account_id, user_id, role) VALUES ($1, $2, 'owner')`, accountID, userID); err != nil {
		return fmt.Errorf("insert account_member: %w", err)
	}

	key, err := auth.GenerateAPIKey([]byte(*pepper))
	if err != nil {
		return fmt.Errorf("generate api key: %w", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO api_keys (account_id, created_by, name, display_prefix, key_hmac, status) VALUES ($1, $2, 'seed-key', $3, $4, 'active')`,
		accountID, userID, key.DisplayPrefix, key.HMAC,
	); err != nil {
		return fmt.Errorf("insert api_key: %w", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO virtual_models (name, family, type, context_window, max_output, capabilities, visible_tiers, status)
		 VALUES ('deepseek-v4-flash', 'deepseek', 'chat', 128000, 8192, '{stream,tools}', '{free,pro,enterprise}', 'active')
		 ON CONFLICT (name) DO NOTHING`,
	); err != nil {
		return fmt.Errorf("insert virtual_model: %w", err)
	}

	fmt.Println("seed: done")
	fmt.Println("  account_id:", accountID)
	fmt.Println("  user_id:   ", userID)
	fmt.Println("  api_key:   ", key.Raw)
	return nil
}
