// Command reset 维护本地开发数据库（scripts/dev-stack.sh / dev-all.sh 使用）：
//
//	默认          删除并重建数据库（清空全部数据）
//	-ensure       数据库不存在时才创建，已存在则什么也不做（首次启动用）
//
// 只允许操作本机地址，防止误删共享环境的库。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
)

func main() {
	dsn := flag.String("dsn", "postgres://uft:uft@localhost:55432/postgres?sslmode=disable", "连接到 postgres 维护库的 DSN")
	db := flag.String("db", "uft_dev", "数据库名")
	ensure := flag.Bool("ensure", false, "只在数据库不存在时创建，不删除已有数据")
	flag.Parse()
	if !strings.Contains(*dsn, "@localhost:") && !strings.Contains(*dsn, "@127.0.0.1:") {
		fail("只允许操作本机数据库")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, *dsn)
	if err != nil {
		fail("connect: " + err.Error())
	}
	defer conn.Close(ctx)
	name := pgx.Identifier{*db}.Sanitize()

	if *ensure {
		var exists bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, *db).Scan(&exists); err != nil {
			fail(err.Error())
		}
		if exists {
			fmt.Printf("reset: 数据库 %s 已存在\n", *db)
			return
		}
		exec(ctx, conn, "CREATE DATABASE "+name+" OWNER uft")
		return
	}
	exec(ctx, conn, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	exec(ctx, conn, "CREATE DATABASE "+name+" OWNER uft")
}

func exec(ctx context.Context, conn *pgx.Conn, stmt string) {
	if _, err := conn.Exec(ctx, stmt); err != nil {
		fail(stmt + ": " + err.Error())
	}
	fmt.Println("reset:", stmt)
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "reset:", msg)
	os.Exit(1)
}
