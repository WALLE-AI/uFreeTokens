#!/usr/bin/env bash
# 本地手工测试用的后端：mock 上游 + cmd/admin + cmd/gateway + cmd/worker，
# 连接独立的开发库 uft_dev（与自动化测试用的 uft 库分开，测试数据不会污染它）。
# 想连数据库和两个前端一起启动，用 ./scripts/dev-all.sh。
# 详细说明见 docs/本地联调与测试手册.md。
#
# 用法：
#   ./scripts/dev-stack.sh up       编译并启动全部后端服务（自动执行数据库迁移）
#   ./scripts/dev-stack.sh down     停止全部后端服务
#   ./scripts/dev-stack.sh status   查看各服务是否在运行
#   ./scripts/dev-stack.sh reset    清空开发库（删库重建 + 迁移），需先 down
#   ./scripts/dev-stack.sh seed     写入演示数据（管理员账号、mock 供应商与模型、测试用户），需先 up
#
# 前置条件：Postgres（tools/devdb，默认 55432 端口）与 Redis（6379，Windows 上用 Memurai）已在运行。
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
# shellcheck source=lib/dev-common.sh
source scripts/lib/dev-common.sh

GOOSE="go run github.com/pressly/goose/v3/cmd/goose@v3.28.0"
ADMIN_DSN="postgres://uft:uft@localhost:${UFT_DEV_PG_PORT}/postgres?sslmode=disable"

migrate() {
  go run ./tools/devdb/reset -ensure -dsn "$ADMIN_DSN" -db "$UFT_DEV_DB"
  $GOOSE -dir migrations postgres "$UFT_POSTGRES_DSN" up > "$RUNDIR/migrate.log" 2>&1 || { cat "$RUNDIR/migrate.log" >&2; exit 1; }
  echo "  数据库 ${UFT_DEV_DB} 已迁移到最新版本"
}

start_svc() { # name port cmd...
  local name=$1 port=$2
  shift 2
  if running_named "$name" "$port"; then
    echo "  $name: 已在运行，跳过"
    return
  fi
  start_bg "$name" "$@"
  echo "  $name: 已启动（日志 $RUNDIR/$name.log）"
}

cmd_up() {
  port_open "$UFT_DEV_PG_PORT" || { echo "Postgres(:${UFT_DEV_PG_PORT}) 没在运行：用 ./scripts/dev-all.sh start，或先执行 go run ./tools/devdb -port ${UFT_DEV_PG_PORT} -data ${PGDATA_DIR}" >&2; exit 1; }
  port_open 6379 || { echo "Redis(6379) 没在运行：先启动 Memurai / redis-server" >&2; exit 1; }
  migrate
  echo "编译后端..."
  go build -o "$RUNDIR/uft-mockupstream$EXE" ./tools/mockupstream
  go build -o "$RUNDIR/uft-admin$EXE" ./cmd/admin
  go build -o "$RUNDIR/uft-gateway$EXE" ./cmd/gateway
  go build -o "$RUNDIR/uft-worker$EXE" ./cmd/worker
  echo "启动后端（数据库 ${UFT_DEV_DB}）..."
  start_svc mockupstream 18099 "$RUNDIR/uft-mockupstream$EXE" -addr "$MOCK_ADDR"
  start_svc admin 8081 "$RUNDIR/uft-admin$EXE" -config "$UFT_CONFIG"
  start_svc gateway 8080 "$RUNDIR/uft-gateway$EXE" -config "$UFT_CONFIG"
  start_svc worker "" "$RUNDIR/uft-worker$EXE"
  wait_port 18099 mockupstream && wait_port 8081 admin && wait_port 8080 gateway
  echo "后端就绪：admin http://localhost:8081  gateway http://localhost:8080  mock 上游 http://${MOCK_ADDR}/v1"
}

cmd_down() {
  for s in worker gateway admin mockupstream; do kill_named "$s"; done
  # 兜底：之前用旧版脚本（二进制不叫 uft-*）启动的进程按端口结束
  for p in 8080 8081 18099; do port_open "$p" && kill_port "$p"; done
  echo "  后端已停止"
}

cmd_status() {
  for pair in mockupstream:18099 admin:8081 gateway:8080; do
    name=${pair%%:*} port=${pair##*:}
    if port_open "$port"; then echo "  $name :$port 运行中"; else echo "  $name :$port 未运行"; fi
  done
  if running_named worker; then echo "  worker 运行中"; else echo "  worker 未运行"; fi
}

cmd_reset() {
  if port_open 8081 || port_open 8080; then echo "请先执行 ./scripts/dev-stack.sh down" >&2; exit 1; fi
  echo "删除并重建数据库 ${UFT_DEV_DB} ..."
  go run ./tools/devdb/reset -dsn "$ADMIN_DSN" -db "$UFT_DEV_DB"
  $GOOSE -dir migrations postgres "$UFT_POSTGRES_DSN" up > "$RUNDIR/migrate.log" 2>&1 || { cat "$RUNDIR/migrate.log" >&2; exit 1; }
  echo "完成：${UFT_DEV_DB} 已清空并迁移到最新版本"
}

cmd_seed() {
  port_open 8081 && port_open 8080 || { echo "请先执行 ./scripts/dev-stack.sh up" >&2; exit 1; }
  go run ./tools/devseed -admin http://localhost:8081 -gateway http://localhost:8080 -token "$UFT_ADMIN_TOKEN" \
    -mock "http://${MOCK_ADDR}/v1" -dsn "$UFT_POSTGRES_DSN"
}

case "${1:-}" in
  up) cmd_up ;;
  down) cmd_down ;;
  status) cmd_status ;;
  reset) cmd_reset ;;
  seed) cmd_seed ;;
  *) sed -n '2,14p' "$0"; exit 1 ;;
esac
