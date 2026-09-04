#!/usr/bin/env bash
set -euo pipefail

local_no_proxy="localhost,127.0.0.1,::1"
export NO_PROXY="${NO_PROXY:+${NO_PROXY},}${local_no_proxy}"
export no_proxy="${no_proxy:+${no_proxy},}${local_no_proxy}"

base_url=${KBOT_URL:-http://localhost:8080}
email=${KBOT_EMAIL:-admin@ecommerce-ops.local}
password=${KBOT_PASSWORD:-admin12345}
workspace_name=${CROSSBORDER_WORKSPACE:-跨境电商运营平台}
agent_name=${CROSSBORDER_AGENT_NAME:-跨境电商运营与供应链协同 Agent}

token=$(curl -fsS -X POST "$base_url/api/v1/auth/login" -H 'Content-Type: application/json' \
  -d "$(jq -n --arg email "$email" --arg password "$password" '{email:$email,password:$password}')" | jq -r '.token')
base_auth=(-H "Authorization: Bearer $token" -H 'Content-Type: application/json')
workspace_id=$(curl -fsS "$base_url/api/v1/workspaces" "${base_auth[@]}" | jq -r --arg name "$workspace_name" '.[] | select(.name==$name) | .id' | head -1)
if [ -z "$workspace_id" ]; then
  echo "workspace not found; run the matching crossborder install target first" >&2
  exit 1
fi
auth=("${base_auth[@]}" -H "X-Workspace-ID: $workspace_id")
agent_id=$(curl -fsS "$base_url/api/v1/agents" "${auth[@]}" | jq -r --arg name "$agent_name" '.[] | select(.name==$name) | .id' | head -1)
if [ -z "$agent_id" ]; then
  echo "agent not found; run the matching crossborder install target first" >&2
  exit 1
fi

stream_file=$(mktemp)
trap 'rm -f "$stream_file"' EXIT
prompt='/order_exception_triage 仅进行只读分析，禁止调用任何写操作工具。请查询订单 TTS-20260801-1001、SKU SKU-BLACK-M-01 的实时订单、库存、可用调拨线路与发货物流，判断能否在 ship_by 前恢复履约并给出有证据的建议；不要创建调拨、退款或申诉。'
curl -fsS -N --max-time 180 -X POST "$base_url/stream/agents/$agent_id/chat" "${auth[@]}" \
  -d "$(jq -n --arg message "$prompt" '{message:$message}')" >"$stream_file"

events=$(sed -n 's/^data: //p' "$stream_file")
if printf '%s\n' "$events" | jq -e 'select(.type=="error")' >/dev/null; then
  echo "model stream returned an error" >&2
  printf '%s\n' "$events" | jq -c 'select(.type=="error")' >&2
  exit 1
fi
if printf '%s\n' "$events" | jq -e 'select(.type=="approval_required")' >/dev/null; then
  echo "read-only smoke test unexpectedly requested a sensitive write operation; nothing was approved or executed" >&2
  exit 1
fi
if printf '%s\n' "$events" | jq -e 'select(.type=="tool_call" and (.text=="create_inventory_transfer" or .text=="change_fulfillment_warehouse" or .text=="approve_refund" or .text=="create_reconciliation_case"))' >/dev/null; then
  echo "read-only smoke test attempted a forbidden write tool; nothing was approved or executed" >&2
  exit 1
fi
if ! printf '%s\n' "$events" | jq -e 'select(.type=="answer_done")' >/dev/null; then
  echo "model stream did not complete an answer" >&2
  cat "$stream_file" >&2
  exit 1
fi

delta_count=$(printf '%s\n' "$events" | jq -s '[.[] | select(.type=="answer_delta" and ((.text // "") | length > 0))] | length')
if [ "$delta_count" -le 1 ]; then
  echo "model answer was not streamed in multiple chunks (answer_delta_count=$delta_count)" >&2
  cat "$stream_file" >&2
  exit 1
fi

conversation_id=$(printf '%s\n' "$events" | jq -r 'select(.type=="started") | .data.conversation_id' | head -1)
tools=$(printf '%s\n' "$events" | jq -r 'select(.type=="tool_call") | .text' | jq -Rsc 'split("\n") | map(select(length>0))')
jq -n --arg status passed --arg conversation_id "$conversation_id" --argjson tools "$tools" --argjson delta_count "$delta_count" \
	'{status:$status,mode:"real_model_read_only",conversation_id:$conversation_id,answer_delta_count:$delta_count,tools:$tools}'
