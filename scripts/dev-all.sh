#!/usr/bin/env bash
# 一键启动 / 停止本地整套环境：数据库 + 后端 + 两个前端。
#
#   数据库   Postgres（tools/devdb，:55432，数据目录 tmp/testpg）
#   后端     mock 上游(:18099) + cmd/admin(:8081) + cmd/gateway(:8080) + cmd/worker
#   前端     frontend/web(:3000) + frontend/admin(:3001)
#
# 用法：
#   ./scripts/dev-all.sh start     启动全部（首次会自动建库、迁移、写入演示数据、安装前端依赖）
#   ./scripts/dev-all.sh stop      停止全部（包括数据库）
#   ./scripts/dev-all.sh restart   重启全部（重新编译后端）
#   ./scripts/dev-all.sh status    查看各组件状态
#   ./scripts/dev-all.sh reset     清空开发库并重新写入演示数据（需要环境已启动）
#
# 需要 Redis 在 6379 运行：Windows 上用 Memurai 服务（本脚本不会启动它）；
# 其他系统上如果装了 redis-server 且 6379 没在监听，会自动拉起一个。
# 账号与测试步骤见 docs/本地联调与测试手册.md；演示账号也保存在 tmp/dev-stack/seed-output.txt。
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
# shellcheck source=lib/dev-common.sh
source scripts/lib/dev-common.sh

pg_ctl_bin() {
  local p="$HOME/.embedded-postgres-go/extracted/bin/pg_ctl$EXE"
  [ -x "$p" ] && echo "$p"
}

db_start() {
  if port_open "$UFT_DEV_PG_PORT"; then
    echo "  postgres :${UFT_DEV_PG_PORT} 已在运行"
    return
  fi
  go build -o "$RUNDIR/uft-devdb$EXE" ./tools/devdb
  start_bg devdb "$RUNDIR/uft-devdb$EXE" -port "$UFT_DEV_PG_PORT" -data "$PGDATA_DIR"
  echo "  postgres: 启动中（首次运行要下载 PostgreSQL，可能需要一两分钟）..."
  wait_port "$UFT_DEV_PG_PORT" devdb 240
  echo "  postgres :${UFT_DEV_PG_PORT} 已就绪"
}

db_stop() {
  local pgctl
  pgctl=$(pg_ctl_bin || true)
  if port_open "$UFT_DEV_PG_PORT" && [ -n "$pgctl" ] && [ -d "$PGDATA_DIR" ]; then
    "$pgctl" stop -D "$PGDATA_DIR" -m fast > /dev/null 2>&1 || true
  fi
  kill_named devdb
  port_open "$UFT_DEV_PG_PORT" && kill_port "$UFT_DEV_PG_PORT"
  echo "  postgres 已停止"
}

fe_start() { # dir port name
  local dir=$1 port=$2 name=$3
  if port_open "$port"; then
    echo "  $name :$port 已在运行"
    return
  fi
  if [ ! -d "$dir/node_modules" ]; then
    echo "  $name: 安装依赖（npm install）..."
    (cd "$dir" && npm install --no-fund --no-audit > "../../$RUNDIR/$name-install.log" 2>&1)
  fi
  (cd "$dir" && start_bg_in "$name" npm run dev)
  wait_port "$port" "$name" 60
  echo "  $name: http://localhost:$port"
}

# start_bg_in：在子目录里后台启动（日志路径相对仓库根目录）
start_bg_in() {
  local name=$1
  shift
  nohup "$@" > "../../$RUNDIR/$name.log" 2>&1 &
  disown 2>/dev/null || true
}

fe_stop() { # port name
  if port_open "$1"; then kill_port "$1"; fi
  echo "  $2 已停止"
}

check_redis() {
  port_open 6379 && return 0
  if command -v redis-server > /dev/null; then
    start_bg redis redis-server --port 6379
    wait_port 6379 redis 10 && return 0
  fi
  echo "Redis(6379) 没在运行：Windows 上请启动 Memurai 服务（services.msc 或管理员终端执行 net start Memurai），其他系统安装并启动 redis-server" >&2
  exit 1
}

cmd_start() {
  echo "[1/4] 数据库"
  db_start
  check_redis
  echo "[2/4] 后端"
  ./scripts/dev-stack.sh up
  echo "[3/4] 演示数据"
  ./scripts/dev-stack.sh seed | sed 's/^/  /'
  echo "[4/4] 前端"
  fe_start frontend/web 3000 web
  fe_start frontend/admin 3001 admin-ui
  echo
  echo "全部就绪："
  echo "  用户端    http://localhost:3000   demo@uft.local / Demo-user-2026"
  echo "  运营后台  http://localhost:3001   admin@uft.local / Uft-dev-2026!（其他角色见 tmp/dev-stack/seed-output.txt）"
  echo "  停止      ./scripts/dev-all.sh stop"
}

cmd_stop() {
  fe_stop 3001 admin-ui
  fe_stop 3000 web
  ./scripts/dev-stack.sh down
  db_stop
}

cmd_status() {
  if port_open "$UFT_DEV_PG_PORT"; then echo "  postgres :${UFT_DEV_PG_PORT} 运行中"; else echo "  postgres :${UFT_DEV_PG_PORT} 未运行"; fi
  if port_open 6379; then echo "  redis :6379 运行中"; else echo "  redis :6379 未运行"; fi
  ./scripts/dev-stack.sh status
  for pair in web:3000 admin-ui:3001; do
    if port_open "${pair##*:}"; then echo "  ${pair%%:*} :${pair##*:} 运行中"; else echo "  ${pair%%:*} :${pair##*:} 未运行"; fi
  done
}

cmd_reset() {
  port_open "$UFT_DEV_PG_PORT" || { echo "数据库没在运行，先执行 ./scripts/dev-all.sh start" >&2; exit 1; }
  ./scripts/dev-stack.sh down
  ./scripts/dev-stack.sh reset
  ./scripts/dev-stack.sh up
  ./scripts/dev-stack.sh seed
}

case "${1:-}" in
  start) cmd_start ;;
  stop) cmd_stop ;;
  restart) cmd_stop; cmd_start ;;
  status) cmd_status ;;
  reset) cmd_reset ;;
  *) sed -n '2,17p' "$0"; exit 1 ;;
esac
