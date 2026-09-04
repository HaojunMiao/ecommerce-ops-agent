#!/usr/bin/env bash
set -euo pipefail

# 本地平台调用不能经过终端中的 HTTP(S) 代理。
local_no_proxy="localhost,127.0.0.1,::1"
export NO_PROXY="${NO_PROXY:+${NO_PROXY},}${local_no_proxy}"
export no_proxy="${no_proxy:+${no_proxy},}${local_no_proxy}"

project_dir=$(cd "$(dirname "$0")/.." && pwd)
base_url=${KBOT_URL:-http://localhost:8080}
email=${KBOT_EMAIL:-admin@ecommerce-ops.local}
password=${KBOT_PASSWORD:-admin12345}
workspace_name=${CROSSBORDER_WORKSPACE:-跨境电商运营平台}
kb_name=${CROSSBORDER_KB_NAME:-跨境电商规则库}
agent_name=${CROSSBORDER_AGENT_NAME:-跨境电商运营与供应链协同 Agent}
prompt_name=${CROSSBORDER_PROMPT_NAME:-跨境电商运营系统提示词}
model_config_name=${CROSSBORDER_MODEL_CONFIG_NAME:-Doubao}
kb_root=${CROSSBORDER_KB_ROOT:-/scenarios/crossborder/knowledge}
tool_base_url=${CROSSBORDER_TOOL_BASE_URL:-http://crossborder-sim:8091}
model_config_bootstrap_hint=${MODEL_CONFIG_BOOTSTRAP_HINT:-make bootstrap-model-config}

for command_name in curl jq; do
  if ! command -v "$command_name" >/dev/null 2>&1; then
    echo "missing required command: $command_name" >&2
    exit 1
  fi
done

echo "==> 登录 Agent 平台"
token=$(curl -fsS -X POST "$base_url/api/v1/auth/login" -H 'Content-Type: application/json' \
  -d "$(jq -n --arg email "$email" --arg password "$password" '{email:$email,password:$password}')" | jq -r '.token')
base_auth=(-H "Authorization: Bearer $token" -H 'Content-Type: application/json')

echo "==> 获取或创建独立 Workspace"
workspaces=$(curl -fsS "$base_url/api/v1/workspaces" "${base_auth[@]}")
workspace_id=$(printf '%s' "$workspaces" | jq -r --arg name "$workspace_name" '.[] | select(.name==$name) | .id' | head -1)
if [ -z "$workspace_id" ]; then
  workspace_id=$(curl -fsS -X POST "$base_url/api/v1/workspaces" "${base_auth[@]}" \
    -d "$(jq -n --arg name "$workspace_name" '{name:$name,description:"跨境电商运营与供应链协同 Agent 项目"}')" | jq -r '.id')
fi
auth=("${base_auth[@]}" -H "X-Workspace-ID: $workspace_id")

echo "==> 解析不可变模型配置版本"
model_config_versions=$(curl -fsS "$base_url/api/v1/model-config-versions" "${auth[@]}")
model_config_version_id=$(printf '%s' "$model_config_versions" | jq -r --arg name "$model_config_name" \
  '[.[] | select(.name==$name)] | sort_by(.version) | last | .id // empty')
if [ -z "$model_config_version_id" ]; then
  echo "model config '$model_config_name' not found; initialize it before installing the scenario" >&2
  echo "example: $model_config_bootstrap_hint MODEL_CONFIG_WORKSPACE='$workspace_name'" >&2
  exit 1
fi

echo "==> 获取或创建 System PromptVersion"
system_prompt='你是跨境电商运营与供应链协同 Agent。所有订单、库存、物流与结算事实必须来自工具。敏感写操作必须等待人工审批。'
prompts=$(curl -fsS "$base_url/api/v1/prompts" "${auth[@]}")
prompt_id=$(printf '%s' "$prompts" | jq -r --arg name "$prompt_name" '.[] | select(.name==$name) | .id' | head -1)
if [ -z "$prompt_id" ]; then
  created_prompt=$(curl -fsS -X POST "$base_url/api/v1/prompts" "${auth[@]}" \
    -d "$(jq -n --arg name "$prompt_name" --arg template "$system_prompt" \
      '{name:$name,category:"crossborder-system",template:$template,variables_schema:"{}"}')")
  prompt_id=$(printf '%s' "$created_prompt" | jq -r '.prompt.id')
  prompt_version_id=$(printf '%s' "$created_prompt" | jq -r '.version.id')
else
  prompt_versions=$(curl -fsS "$base_url/api/v1/prompts/$prompt_id/versions" "${auth[@]}")
  prompt_version_id=$(printf '%s' "$prompt_versions" | jq -r --arg template "$system_prompt" \
    '[.[] | select(.template==$template)] | sort_by(.version) | last | .id // empty')
  if [ -z "$prompt_version_id" ]; then
    prompt_version_id=$(curl -fsS -X POST "$base_url/api/v1/prompts/$prompt_id/versions" "${auth[@]}" \
      -d "$(jq -n --arg template "$system_prompt" \
        '{template:$template,variables_schema:"{}"}')" | jq -r '.id')
  fi
fi

echo "==> 获取或创建知识库"
kbs=$(curl -fsS "$base_url/api/v1/kbs" "${auth[@]}")
kb_id=$(printf '%s' "$kbs" | jq -r --arg name "$kb_name" '.[] | select(.name==$name) | .id' | head -1)
if [ -z "$kb_id" ]; then
  kb_id=$(curl -fsS -X POST "$base_url/api/v1/kbs" "${auth[@]}" \
    -d "$(jq -n --arg name "$kb_name" '{name:$name}')" | jq -r '.id')
fi
# 同名知识库也要同步；服务端按内容哈希跳过未变化文档。
curl -fsS -X POST "$base_url/api/v1/kbs/$kb_id/connectors/markdown/sync" "${auth[@]}" \
  -d "$(jq -n --arg root "$kb_root" '{root_path:$root}')" >/dev/null

test_input() {
  case "$1" in
    search_knowledge_base) jq -nc --arg kb_id "$kb_id" '{kb_id:$kb_id,query:"库存调拨规则",top_k:3}' ;;
    get_order) jq -nc '{order_id:"TTS-20260801-1001"}' ;;
    get_inventory) jq -nc '{sku:"SKU-BLACK-M-01"}' ;;
    get_shipping_options) jq -nc '{order_id:"TTS-20260801-1003",warehouse_id:"WH-US-BOS"}' ;;
    get_statement) jq -nc '{statement_id:"STMT-2026-31"}' ;;
    create_inventory_transfer) jq -nc '{sku:"SKU-BLACK-M-01",from_warehouse:"WH-US-SFO",to_warehouse:"WH-US-LAX",quantity:1,idempotency_key:"tool-test-transfer",dry_run:true}' ;;
    approve_refund) jq -nc '{order_id:"TTS-20260801-1002",amount:59.90,reason:"buyer cancellation",idempotency_key:"tool-test-refund",dry_run:true}' ;;
    change_fulfillment_warehouse) jq -nc '{order_id:"TTS-20260801-1003",to_warehouse:"WH-US-BOS",reason:"transfer misses ship_by",idempotency_key:"tool-test-reroute",dry_run:true}' ;;
    create_reconciliation_case) jq -nc '{statement_id:"STMT-2026-31",reason:"tool test",idempotency_key:"tool-test-reconciliation",dry_run:true}' ;;
    *) jq -nc '{}' ;;
  esac
}

echo "==> 注册、试调并发布 Tool"
existing_tools=$(curl -fsS "$base_url/api/v1/tools" "${auth[@]}")
tool_version_ids=()
while IFS= read -r definition; do
  name=$(printf '%s' "$definition" | jq -r '.name')
  desired_schema=$(printf '%s' "$definition" | jq -c '.schema_json')
  desired_endpoint=$(printf '%s' "$definition" | jq -c --arg base "$tool_base_url" \
    'if .endpoint_config.url? then .endpoint_config.url |= sub("^http://crossborder-sim:8091";$base) else . end | .endpoint_config')
  tool_id=$(printf '%s' "$existing_tools" | jq -r --arg name "$name" '.[] | select(.name==$name) | .id' | head -1)
  if [ -z "$tool_id" ]; then
    payload=$(printf '%s' "$definition" | jq --arg base "$tool_base_url" '
      if .endpoint_config.url? then .endpoint_config.url |= sub("^http://crossborder-sim:8091";$base) else . end
      | {name,source_type,description,sensitive,schema_json:(.schema_json|tojson),endpoint_config:(.endpoint_config|tojson),auth_config:"{}"}')
    tool_id=$(curl -fsS -X POST "$base_url/api/v1/tools" "${auth[@]}" -H "Idempotency-Key: install-crossborder-$name" -d "$payload" | jq -r '.id')
  fi

  versions=$(curl -fsS "$base_url/api/v1/tools/$tool_id/versions" "${auth[@]}")
  tool_version_id=$(printf '%s' "$versions" | jq -r \
    --argjson schema "$desired_schema" --argjson endpoint "$desired_endpoint" '
      [.[] | select(.status=="published")
        | select((.schema_json|fromjson)==$schema)
        | select((.endpoint_config|fromjson)==$endpoint)
      ] | sort_by(.version) | last | .id // empty')

  if [ -z "$tool_version_id" ]; then
    current_draft_id=$(printf '%s' "$versions" | jq -r \
      --argjson schema "$desired_schema" --argjson endpoint "$desired_endpoint" '
        sort_by(.version) | last
        | select(.status=="draft")
        | select((.schema_json|fromjson)==$schema)
        | select((.endpoint_config|fromjson)==$endpoint)
        | .id // empty')
    if [ -n "$current_draft_id" ]; then
      tool_version_id=$current_draft_id
    else
      tool_version_id=$(curl -fsS -X POST "$base_url/api/v1/tools/$tool_id/versions" "${auth[@]}" \
        -d "$(jq -n --argjson schema "$desired_schema" --argjson endpoint "$desired_endpoint" \
          '{schema_json:($schema|tojson),endpoint_config:($endpoint|tojson)}')" | jq -r '.id')
    fi

    input=$(test_input "$name")
    test_result=$(curl -fsS -X POST "$base_url/api/v1/tools/$tool_id/test" "${auth[@]}" -d "$(jq -n --argjson input "$input" '{input:$input}')")
    if [ "$(printf '%s' "$test_result" | jq -r '.status')" != "success" ]; then
      printf 'tool %s test failed: %s\n' "$name" "$(printf '%s' "$test_result" | jq -r '.error')" >&2
      exit 1
    fi
    curl -fsS -X POST "$base_url/api/v1/tools/$tool_id/versions/$tool_version_id/publish" "${auth[@]}" -d '{}' >/dev/null
  fi
  tool_version_ids+=("$tool_version_id")
done < <(jq -c '.[]' "$project_dir/config/tools.json")
tool_version_ids_json=$(printf '%s\n' "${tool_version_ids[@]}" | jq -R . | jq -s .)

echo "==> 创建并发布 Skill"
existing_skills=$(curl -fsS "$base_url/api/v1/skills" "${auth[@]}")
skill_version_ids=()
while IFS= read -r skill_file; do
  skill_name=$(awk '/^name:/{print $2; exit}' "$skill_file")
  skill_md=$(sed "s/__KB_ID__/$kb_id/g" "$skill_file")
  # 与服务端 ParseSkill 一致：正文开头的空行不参与版本内容比较。
  skill_body=$(printf '%s\n' "$skill_md" | awk '
    BEGIN{separator=0; started=0}
    /^---$/{separator++; next}
    separator>=2 && !started && $0=="" {next}
    separator>=2 {started=1; print}
  ')
  skill_description=$(printf '%s\n' "$skill_md" | awk 'BEGIN{header=0} /^---$/{header++; next} header==1 && /^description:/{sub(/^description:[[:space:]]*/, ""); print; exit}')
  skill_requires_network=$(printf '%s\n' "$skill_md" | awk 'BEGIN{header=0} /^---$/{header++; next} header==1 && /^requires_network:/{sub(/^requires_network:[[:space:]]*/, ""); print; exit}')
  [ -n "$skill_requires_network" ] || skill_requires_network=false
  skill_allowed_tools=$(printf '%s\n' "$skill_md" | awk '
    /^allowed-tools:/ {inside=1; next}
    inside && /^  - / {sub(/^  - /, ""); print; next}
    inside {exit}
  ' | jq -R . | jq -s .)
  skill_allowed_kbs=$(printf '%s\n' "$skill_md" | awk '
    /^allowed-kbs:/ {inside=1; next}
    inside && /^  - / {sub(/^  - /, ""); print; next}
    inside {exit}
  ' | jq -R . | jq -s .)
  skill_id=$(printf '%s' "$existing_skills" | jq -r --arg name "$skill_name" '.[] | select(.name==$name) | .id' | head -1)
  if [ -z "$skill_id" ]; then
    created=$(curl -fsS -X POST "$base_url/api/v1/skills" "${auth[@]}" \
      -d "$(jq -n --arg category crossborder --arg skill_md "$skill_md" '{category:$category,skill_md:$skill_md}')")
    skill_id=$(printf '%s' "$created" | jq -r '.skill.id')
    version_id=$(printf '%s' "$created" | jq -r '.version.id')
    curl -fsS -X POST "$base_url/api/v1/skills/$skill_id/publish" "${auth[@]}" -d "$(jq -n --arg id "$version_id" '{version_id:$id}')" >/dev/null
  else
    versions=$(curl -fsS "$base_url/api/v1/skills/$skill_id/versions" "${auth[@]}")
    version_id=$(printf '%s' "$versions" | jq -r \
      --arg body "$skill_body" \
      --arg description "$skill_description" \
      --argjson allowed_tools "$skill_allowed_tools" \
      --argjson allowed_kbs "$skill_allowed_kbs" \
      --argjson requires_network "$skill_requires_network" '
        [.[] | select(.status=="published")
          | select(.body_md==$body)
          | select((.frontmatter_json|fromjson|.description)==$description)
          | select((.frontmatter_json|fromjson|.allowed_tools)==$allowed_tools)
          | select((.frontmatter_json|fromjson|.allowed_kbs)==$allowed_kbs)
          | select(((.frontmatter_json|fromjson|.requires_network)//false)==$requires_network)
        ][0].id // empty')
    if [ -z "$version_id" ]; then
      version_id=$(curl -fsS -X POST "$base_url/api/v1/skills/$skill_id/versions" "${auth[@]}" \
        -d "$(jq -n --arg skill_md "$skill_md" '{skill_md:$skill_md}')" | jq -r '.id')
      curl -fsS -X POST "$base_url/api/v1/skills/$skill_id/publish" "${auth[@]}" \
        -d "$(jq -n --arg id "$version_id" '{version_id:$id}')" >/dev/null
    fi
  fi
  skill_version_ids+=("$version_id")
done < <(find "$project_dir/skills" -name SKILL.md -type f | sort)
skill_ids_json=$(printf '%s\n' "${skill_version_ids[@]}" | jq -R . | jq -s .)

echo "==> 创建 Agent"
agents=$(curl -fsS "$base_url/api/v1/agents" "${auth[@]}")
agent_id=$(printf '%s' "$agents" | jq -r --arg name "$agent_name" '.[] | select(.name==$name) | .id' | head -1)
agent_config=$(jq -n \
  --arg system_prompt_version_id "$prompt_version_id" \
  --arg model_config_version_id "$model_config_version_id" \
  --arg kb "$kb_id" \
  --argjson tools "$tool_version_ids_json" \
  --argjson skills "$skill_ids_json" \
  '{system_prompt_version_id:$system_prompt_version_id,model_config_version_id:$model_config_version_id,generation_config:{temperature:0.2,max_output_tokens:2048},tool_version_ids:$tools,skill_version_ids:$skills,kb_ids:[$kb],allow_network:true,max_steps:16}')
if [ -z "$agent_id" ]; then
  agent_payload=$(printf '%s' "$agent_config" | jq --arg name "$agent_name" '. + {name:$name,template:"crossborder_commerce"}')
  agent_id=$(curl -fsS -X POST "$base_url/api/v1/agents" "${auth[@]}" -d "$agent_payload" | jq -r '.id')
else
  agent_versions=$(curl -fsS "$base_url/api/v1/agents/$agent_id/versions" "${auth[@]}")
  config_matches=$(printf '%s' "$agent_versions" | jq -r \
    --arg system_prompt_version_id "$prompt_version_id" \
    --arg model_config_version_id "$model_config_version_id" \
    --arg kb "$kb_id" \
    --argjson tools "$tool_version_ids_json" \
    --argjson skills "$skill_ids_json" '
      [.[] | select(.environments | index("dev"))][0].config as $config
      | ($config.system_prompt_version_id==$system_prompt_version_id
         and $config.model_config_version_id==$model_config_version_id
         and $config.generation_config.temperature==0.2
         and $config.generation_config.max_output_tokens==2048
         and $config.tool_version_ids==$tools
         and $config.skill_version_ids==$skills
         and $config.kb_ids==[$kb]
         and $config.allow_network==true
         and $config.max_steps==16)')
  if [ "$config_matches" != "true" ]; then
    curl -fsS -X POST "$base_url/api/v1/agents/$agent_id/versions" "${auth[@]}" \
      -d "$agent_config" >/dev/null
  fi
fi

jq -n --arg workspace_id "$workspace_id" --arg kb_id "$kb_id" --arg agent_id "$agent_id" '{workspace_id:$workspace_id,kb_id:$kb_id,agent_id:$agent_id}'
