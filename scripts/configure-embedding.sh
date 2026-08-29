#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
env_file=${KBOT_ENV_FILE:-"$repo_root/.env"}

if [ ! -f "$env_file" ]; then
  cp "$repo_root/.env.example" "$env_file"
fi

read -r -s -p "请输入新生成的硅基流动 API Key: " api_key
printf '\n'
if [ -z "$api_key" ]; then
  echo "API Key 不能为空" >&2
  exit 1
fi

tmp_file=$(mktemp "${env_file}.embedding.XXXXXX")
cleanup() {
  rm -f "$tmp_file"
}
trap cleanup EXIT

seen_kind=false
seen_base=false
seen_key=false
seen_dim=false
seen_model=false
while IFS= read -r line || [ -n "$line" ]; do
  case "$line" in
    KBOT_EMBEDDER=*) printf '%s\n' 'KBOT_EMBEDDER=openai'; seen_kind=true ;;
    KBOT_EMBEDDER_BASE_URL=*) printf '%s\n' 'KBOT_EMBEDDER_BASE_URL=https://api.siliconflow.cn/v1'; seen_base=true ;;
    KBOT_EMBEDDER_API_KEY=*) printf 'KBOT_EMBEDDER_API_KEY=%s\n' "$api_key"; seen_key=true ;;
    KBOT_EMBEDDER_DIM=*) printf '%s\n' 'KBOT_EMBEDDER_DIM=2048'; seen_dim=true ;;
    KBOT_EMBEDDER_MODEL=*) printf '%s\n' 'KBOT_EMBEDDER_MODEL=Qwen/Qwen3-Embedding-4B'; seen_model=true ;;
    *) printf '%s\n' "$line" ;;
  esac
done < "$env_file" > "$tmp_file"

$seen_kind || printf '%s\n' 'KBOT_EMBEDDER=openai' >> "$tmp_file"
$seen_base || printf '%s\n' 'KBOT_EMBEDDER_BASE_URL=https://api.siliconflow.cn/v1' >> "$tmp_file"
$seen_key || printf 'KBOT_EMBEDDER_API_KEY=%s\n' "$api_key" >> "$tmp_file"
$seen_dim || printf '%s\n' 'KBOT_EMBEDDER_DIM=2048' >> "$tmp_file"
$seen_model || printf '%s\n' 'KBOT_EMBEDDER_MODEL=Qwen/Qwen3-Embedding-4B' >> "$tmp_file"

chmod 600 "$tmp_file"
mv "$tmp_file" "$env_file"
trap - EXIT
unset api_key
echo "✅ Qwen Embedding 已写入本地 .env（权限 600，文件受 .gitignore 保护）"
