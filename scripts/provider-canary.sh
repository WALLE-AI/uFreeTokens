#!/usr/bin/env bash
# 供应商每日巡检（多供应商接口统一技术实施方案 §9.4）：按 docs/provider-certifications
# 下每份认证报告记录的参数重测，回写报告并刷新能力矩阵。
#
# 用法：
#   ./scripts/provider-canary.sh                 只测低成本能力（chat、chat_stream、embedding）
#   ./scripts/provider-canary.sh full            全部 9 项（含图像 / 语音，会产生少量上游费用）
#
# Key 从环境变量读取（报告的 key_env，如 SILICONFLOW_API_KEY）；未设置的供应商跳过。
# 若存在 tests/gateway_py/.env 会先加载它。退出码 2 = 有能力由通过变为失败，可接告警。
#
# 定时运行示例（crontab，每天 09:17）：
#   17 9 * * * cd /path/to/uFreeTokens && ./scripts/provider-canary.sh >> tmp/canary.log 2>&1
set -euo pipefail

cd "$(dirname "$0")/.."

if [[ -f tests/gateway_py/.env ]]; then
  set -a
  # shellcheck disable=SC1091
  . tests/gateway_py/.env
  set +a
fi

only="chat,chat_stream,embedding"
if [[ "${1:-}" == "full" ]]; then
  only=""
fi

exec go run ./cmd/providercheck \
  -canary docs/provider-certifications \
  ${only:+-only "$only"} \
  -matrix-out docs/provider-capability-matrix.md
