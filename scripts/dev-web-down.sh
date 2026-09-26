#!/usr/bin/env bash
# 停掉 scripts/dev-web-up.sh 起的 cmd/admin + cmd/gateway。
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

RUNDIR=tmp/dev-web
for name in admin gateway; do
  pidfile="$RUNDIR/$name.pid"
  if [[ -f "$pidfile" ]]; then
    pid=$(cat "$pidfile")
    if kill "$pid" 2>/dev/null; then
      echo "stopped $name (pid $pid)"
    else
      echo "$name (pid $pid) 已经不在了"
    fi
    rm -f "$pidfile"
  else
    echo "没找到 $name 的 pid 文件，跳过"
  fi
done
