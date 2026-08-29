#!/usr/bin/env bash
# 一键启动平台、业务模拟器和 Langfuse；模型使用 .env 中的真实配置。
set -euo pipefail

local_no_proxy="localhost,127.0.0.1,::1"
export NO_PROXY="${NO_PROXY:+${NO_PROXY},}${local_no_proxy}"
export no_proxy="${no_proxy:+${no_proxy},}${local_no_proxy}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE=(docker compose)
if [[ -f "${REPO_ROOT}/.env" ]]; then
  COMPOSE+=(--env-file "${REPO_ROOT}/.env")
fi
COMPOSE+=(-p ecommerce-ops-agent -f "${REPO_ROOT}/deploy/docker-compose.yml" -f "${REPO_ROOT}/deploy/docker-compose.langfuse.yml")

"${REPO_ROOT}/scripts/langfuse-preflight.sh"

echo "==> 构建并启动环境（首次会拉取 Langfuse/ClickHouse 等镜像）"
# 同一 Compose 项目以前启用过的组件会成为孤儿容器；裁剪功能后应一并移除。
"${COMPOSE[@]}" up --build -d --remove-orphans

wait_http() {
  local name="$1"
  local url="$2"
  local attempts="${3:-120}"
  local i
  for ((i = 1; i <= attempts; i++)); do
    if curl -fsS --max-time 2 "${url}" >/dev/null 2>&1; then
      echo "✅ ${name} 已就绪：${url}"
      return 0
    fi
    if (( i % 10 == 0 )); then
      echo "    等待 ${name}（${i}/${attempts}）"
    fi
    sleep 2
  done
  echo "❌ ${name} 在限定时间内未就绪，最近日志：" >&2
  "${COMPOSE[@]}" logs --tail=80 >&2
  return 1
}

KBOT_URL="http://localhost:${KBOT_HTTP_PORT:-8080}"
LANGFUSE_URL="http://localhost:${LANGFUSE_PORT:-3000}"
CROSSBORDER_URL="http://localhost:${CROSSBORDER_PORT:-8091}"

wait_http "跨境电商业务模拟器" "${CROSSBORDER_URL}/healthz" 60
wait_http "Langfuse" "${LANGFUSE_URL}/api/public/health" 180
wait_http "Agent API" "${KBOT_URL}/readyz" 120

cat <<EOF

环境已启动：
  Admin Console: ${KBOT_URL}
  Langfuse:   ${LANGFUSE_URL}
  跨境电商:   ${CROSSBORDER_URL}
  平台账号:   admin@ecommerce-ops.local / admin12345
  Langfuse:   admin@ecommerce-ops.local / admin12345

下一步执行：make crossborder-install
EOF
