#!/usr/bin/env bash
# 本地手工联调用：编译并起 cmd/admin + cmd/gateway，挂载 test_web/ 页面
# （见 test_web/README.md）。依赖 Postgres/Redis 已经在跑（tools/devdb +
# Memurai，或者你自己的实例）。
#
# 用法：
#   ./scripts/dev-web-up.sh      起
#   ./scripts/dev-web-down.sh    停
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

: "${UFT_KEK:=8PTzdjl8kOMJalrTbGLXTlQ4RiX1Tsi6zqm2MTrq/+U=}"
: "${UFT_KEY_PEPPER:=dev-pepper-change-me}"
: "${UFT_ADMIN_TOKEN:=dev-admin-token-change-me}"
: "${UFT_TEST_WEB_DIR:=frontend/test_web}"
export UFT_KEK UFT_KEY_PEPPER UFT_ADMIN_TOKEN UFT_TEST_WEB_DIR

# 优先用本地配置 config/gateway.yaml（不入库），没有再用示例配置；UFT_CONFIG 可覆盖。
CONFIG=${UFT_CONFIG:-config/gateway.example.yaml}
[ -z "${UFT_CONFIG:-}" ] && [ -f config/gateway.yaml ] && CONFIG=config/gateway.yaml
RUNDIR=tmp/dev-web
mkdir -p "$RUNDIR"

check_port() { (echo > "/dev/tcp/127.0.0.1/$1") 2>/dev/null; }

if ! check_port 5432; then
  echo "dev-web-up: Postgres(5432) 没在跑，先起 tools/devdb 或你自己的 Postgres" >&2
  exit 1
fi
if ! check_port 6379; then
  echo "dev-web-up: Redis(6379) 没在跑，先起 Memurai/redis-server" >&2
  exit 1
fi
if check_port 8081; then
  echo "dev-web-up: 8081 已经被占用了（cmd/admin 是不是已经在跑？），先跑 scripts/dev-web-down.sh 或手动确认" >&2
  exit 1
fi
if check_port 8080; then
  echo "dev-web-up: 8080 已经被占用了（cmd/gateway 是不是已经在跑？），先跑 scripts/dev-web-down.sh 或手动确认" >&2
  exit 1
fi

echo "dev-web-up: 编译 cmd/admin、cmd/gateway ..."
go build -o "$RUNDIR/admin.exe" ./cmd/admin
go build -o "$RUNDIR/gateway.exe" ./cmd/gateway

"$RUNDIR/admin.exe" -config "$CONFIG" > "$RUNDIR/admin.log" 2>&1 &
echo $! > "$RUNDIR/admin.pid"
"$RUNDIR/gateway.exe" -config "$CONFIG" > "$RUNDIR/gateway.log" 2>&1 &
echo $! > "$RUNDIR/gateway.pid"

for i in 1 2 3 4 5; do
  sleep 1
  check_port 8081 && check_port 8080 && break
done

if check_port 8081; then
  echo "admin:   http://localhost:8081/   (pid $(cat "$RUNDIR/admin.pid"), log $RUNDIR/admin.log)"
else
  echo "dev-web-up: cmd/admin 没起来，看 $RUNDIR/admin.log" >&2
fi
if check_port 8080; then
  echo "gateway: http://localhost:8080/   (pid $(cat "$RUNDIR/gateway.pid"), log $RUNDIR/gateway.log)"
else
  echo "dev-web-up: cmd/gateway 没起来，看 $RUNDIR/gateway.log" >&2
fi
