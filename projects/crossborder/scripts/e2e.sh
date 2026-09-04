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
  echo "workspace not found; run install.sh first" >&2
  exit 1
fi
auth=("${base_auth[@]}" -H "X-Workspace-ID: $workspace_id")
agent_id=$(curl -fsS "$base_url/api/v1/agents" "${auth[@]}" | jq -r --arg name "$agent_name" '.[] | select(.name==$name) | .id' | head -1)
if [ -z "$agent_id" ]; then
  echo "agent not found; run install.sh first" >&2
  exit 1
fi

stream_file=$(mktemp)
trap 'rm -f "$stream_file"' EXIT
curl -fsS -N -X POST "$base_url/stream/agents/$agent_id/chat" "${auth[@]}" \
  -d '{"message":"/order_exception_triage 处理订单 TTS-20260801-1003：核实订单、SKU-BLUE-S-03 库存、调拨线路和候选仓发货渠道；若调拨无法在 ship_by 前到达，请将履约仓从 WH-US-LAX 切换到 WH-US-BOS 并等待人工审批。"}' >"$stream_file"

conversation_id=$(sed -n 's/^data: //p' "$stream_file" | jq -r 'select(.type=="started") | .data.conversation_id' | head -1)
approval_id=$(sed -n 's/^data: //p' "$stream_file" | jq -r 'select(.type=="approval_required") | .data.approval_id' | head -1)
if [ -z "$conversation_id" ] || [ -z "$approval_id" ]; then
  echo "stream did not produce approval checkpoint" >&2
  cat "$stream_file" >&2
  exit 1
fi
if ! sed -n 's/^data: //p' "$stream_file" | jq -e 'select(.type=="approval_required") | select(.data.tool_name=="change_fulfillment_warehouse") | select(.data.presentation.title=="履约仓变更审批")' >/dev/null; then
  echo "approval stream did not include approval metadata" >&2
  exit 1
fi

curl -fsS -X POST "$base_url/api/v1/approvals/$approval_id/approve" "${auth[@]}" -d '{}' >/dev/null

completed=false
for _ in $(seq 1 60); do
  conversation=$(curl -fsS "$base_url/api/v1/conversations/$conversation_id" "${auth[@]}")
  if printf '%s' "$conversation" | jq -e --arg approval_id "$approval_id" '
    (.conversation.status=="active")
    and any(.approvals[]?; .id==$approval_id and .status=="completed")
    and any(.messages[]?; .role=="assistant" and ((.content // "") | length >= 100))' >/dev/null; then
    completed=true
    break
  fi
  sleep 1
done
if [ "$completed" != "true" ]; then
  echo "approval resume did not reach completed/active state with a final answer" >&2
  printf '%s\n' "$conversation" | jq '{conversation:.conversation,approvals:.approvals,messages:.messages}' >&2
  exit 1
fi
final_answer=$(printf '%s' "$conversation" | jq -r '[.messages[] | select(.role=="assistant")][-1].content')
if ! printf '%s' "$final_answer" | grep -Eq 'FW-[0-9]+'; then
  echo "final answer does not report the fulfillment warehouse change ID" >&2
  printf '%s\n' "$final_answer" >&2
  exit 1
fi
if printf '%s' "$final_answer" | grep -Eq '等待.{0,6}审批|待人工审批'; then
  echo "final answer incorrectly says that the completed approval is still pending" >&2
  printf '%s\n' "$final_answer" >&2
  exit 1
fi

audit=$(curl -fsS "$base_url/api/v1/audit/logs?conversation_id=$conversation_id&limit=100" "${auth[@]}")
for action in approval_required resumed; do
  if ! printf '%s' "$audit" | jq -e --arg action "$action" '.[] | select(.action==$action)' >/dev/null; then
    echo "audit action missing: $action" >&2
    exit 1
  fi
done

jq -n --arg workspace_id "$workspace_id" --arg agent_id "$agent_id" --arg conversation_id "$conversation_id" --arg approval_id "$approval_id" '{status:"passed",workspace_id:$workspace_id,agent_id:$agent_id,conversation_id:$conversation_id,approval_id:$approval_id}'
