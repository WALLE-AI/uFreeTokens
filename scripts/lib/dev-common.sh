# shellcheck shell=bash
# dev-stack.sh / dev-all.sh 共用的环境变量与进程管理函数（本地开发专用）。
# 调用方需要先 cd 到仓库根目录再 source 本文件。

: "${UFT_DEV_PG_PORT:=55432}"
: "${UFT_DEV_DB:=uft_dev}"
export UFT_POSTGRES_DSN="postgres://uft:uft@localhost:${UFT_DEV_PG_PORT}/${UFT_DEV_DB}?sslmode=disable"
export UFT_KEK="${UFT_KEK:-8PTzdjl8kOMJalrTbGLXTlQ4RiX1Tsi6zqm2MTrq/+U=}"
export UFT_KEY_PEPPER="${UFT_KEY_PEPPER:-dev-pepper-change-me}"
export UFT_ADMIN_TOKEN="${UFT_ADMIN_TOKEN:-dev-admin-token-change-me}"
# 仅本地：允许 http 与内网上游（mock 上游跑在 127.0.0.1），生产环境不要开
export UFT_ADMIN_ALLOW_PRIVATE_UPSTREAM=true
# 优先用本地配置 config/gateway.yaml（不入库，放本机的 LLM 地址等），没有再用示例配置；
# 也可以自己先 export UFT_CONFIG 指定。
if [ -z "${UFT_CONFIG:-}" ]; then
  if [ -f config/gateway.yaml ]; then UFT_CONFIG=config/gateway.yaml; else UFT_CONFIG=config/gateway.example.yaml; fi
fi
export UFT_CONFIG

RUNDIR=tmp/dev-stack
PGDATA_DIR=tmp/testpg
MOCK_ADDR=127.0.0.1:18099
mkdir -p "$RUNDIR"

is_windows() { [[ "${OS:-}" == "Windows_NT" ]]; }
EXE=""
is_windows && EXE=".exe"

port_open() { (echo > "/dev/tcp/127.0.0.1/$1") 2>/dev/null; }

# wait_port <port> <name> [seconds]
wait_port() {
  local secs=${3:-30}
  for _ in $(seq 1 "$secs"); do port_open "$1" && return 0; sleep 1; done
  echo "  $2 没有在 ${secs} 秒内起来，看 $RUNDIR/$2.log" >&2
  return 1
}

# start_bg <name> <cmd...>：后台启动，输出写到 $RUNDIR/<name>.log，脚本退出后进程继续运行。
start_bg() {
  local name=$1
  shift
  nohup "$@" > "$RUNDIR/$name.log" 2>&1 &
  echo $! > "$RUNDIR/$name.pid"
  disown 2>/dev/null || true
}

# kill_port <port>：结束监听该端口的进程（含子进程）。Windows 上 bash 记录的 PID 与
# Windows PID 不是一回事，所以按端口查 Windows PID 再 taskkill /T。
kill_port() {
  local port=$1 pids
  if is_windows; then
    pids=$(netstat -ano | awk -v p=":$port" '$2 ~ p"$" && $4 == "LISTENING" {print $5}' | sort -u)
    for pid in $pids; do taskkill //PID "$pid" //T //F > /dev/null 2>&1 || true; done
  elif command -v lsof > /dev/null; then
    pids=$(lsof -ti "tcp:$port" -sTCP:LISTEN || true)
    [ -n "$pids" ] && kill $pids 2> /dev/null || true
  fi
}

# kill_named <name>：按进程名结束（二进制都以 uft- 前缀命名，避免误杀同名进程），
# 非 Windows 上按 pid 文件结束。
kill_named() {
  local name=$1
  if is_windows; then
    taskkill //IM "uft-$name.exe" //T //F > /dev/null 2>&1 || true
  elif [ -f "$RUNDIR/$name.pid" ]; then
    kill "$(cat "$RUNDIR/$name.pid")" 2> /dev/null || true
  fi
  rm -f "$RUNDIR/$name.pid"
}

running_named() { # name port
  if [ -n "${2:-}" ]; then port_open "$2" && return 0 || return 1; fi
  if is_windows; then
    tasklist //FI "IMAGENAME eq uft-$1.exe" 2> /dev/null | grep -qi "uft-$1.exe"
  else
    [ -f "$RUNDIR/$1.pid" ] && kill -0 "$(cat "$RUNDIR/$1.pid")" 2> /dev/null
  fi
}
