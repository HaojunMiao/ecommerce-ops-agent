import { Alert, Form, InputNumber, Select, Space, Switch, Tag, Typography } from 'antd'
import { useQuery } from '@tanstack/react-query'
import { listPrompts } from '@/api/prompt'
import { listVersions as listPromptVersions } from '@/api/prompt'
import { listTools, listToolVersions } from '@/api/tool'
import { listKBs } from '@/api/kb'
import { listSkills, listSkillVersions } from '@/api/skill'
import type { Prompt, PromptVersion, Skill, SkillVersion, Tool, ToolVersion } from '@/api/types'
import { useAuthStore } from '@/store/authStore'
import { ModelConfigVersionSelect } from '@/components/ModelConfigVersionSelect'

interface VersionOption extends SkillVersion {
  skill: Skill
  requiresNetwork: boolean
  allowedTools: string[]
  allowedKBs: string[]
}

interface PromptVersionOption extends PromptVersion {
  prompt: Prompt
}

interface ToolVersionOption extends ToolVersion {
  tool: Tool
}

function metadataOf(version: SkillVersion) {
  try {
    const value = JSON.parse(version.frontmatter_json) as {
      allowed_tools?: string[]
      allowed_kbs?: string[]
      requires_network?: boolean
    }
    return {
      requiresNetwork: !!value.requires_network,
      allowedTools: value.allowed_tools ?? [],
      allowedKBs: value.allowed_kbs ?? [],
    }
  } catch {
    return { requiresNetwork: false, allowedTools: [], allowedKBs: [] }
  }
}

export function AgentConfigFields() {
  const workspaceId = useAuthStore((s) => s.workspaceId)
  const prompts = useQuery({ queryKey: ['prompts', workspaceId], queryFn: listPrompts, enabled: !!workspaceId })
  const promptVersions = useQuery({
    queryKey: ['agent-prompt-versions', workspaceId, (prompts.data ?? []).map((v) => v.id).join(',')],
    enabled: !!workspaceId && !!prompts.data,
    queryFn: async (): Promise<PromptVersionOption[]> => {
      const rows = await Promise.all(
        (prompts.data ?? []).map(async (prompt) =>
          (await listPromptVersions(prompt.id)).map((version) => ({ ...version, prompt })),
        ),
      )
      return rows.flat()
    },
  })
  const tools = useQuery({ queryKey: ['tools', workspaceId], queryFn: listTools, enabled: !!workspaceId })
  const toolVersions = useQuery({
    queryKey: ['published-tool-versions', workspaceId, (tools.data ?? []).map((v) => v.id).join(',')],
    enabled: !!workspaceId && !!tools.data,
    queryFn: async (): Promise<ToolVersionOption[]> => {
      const rows = await Promise.all(
        (tools.data ?? []).map(async (tool) =>
          (await listToolVersions(tool.id)).map((version) => ({ ...version, tool })),
        ),
      )
      return rows.flat().filter((version) => version.status === 'published')
    },
  })
  const kbs = useQuery({ queryKey: ['kbs', workspaceId], queryFn: listKBs, enabled: !!workspaceId })
  const skills = useQuery({ queryKey: ['skills', workspaceId], queryFn: listSkills, enabled: !!workspaceId })
  const versions = useQuery({
    queryKey: ['published-skill-versions', workspaceId, (skills.data ?? []).map((v) => v.id).join(',')],
    enabled: !!workspaceId && !!skills.data,
    queryFn: async (): Promise<VersionOption[]> => {
      const rows = await Promise.all(
        (skills.data ?? []).map(async (skill) =>
          (await listSkillVersions(skill.id)).map((version) => ({ ...version, skill, ...metadataOf(version) })),
        ),
      )
      return rows.flat().filter((version) => version.status === 'published')
    },
  })
  const selectedVersionIDs = Form.useWatch<string[]>('skill_version_ids') ?? []
  const selectedVersions = (versions.data ?? []).filter((version) => selectedVersionIDs.includes(version.id))
  const networkRequired = selectedVersions.some((version) => version.requiresNetwork)
  const systemPromptVersions = (promptVersions.data ?? []).filter((item) => !item.prompt.category.endsWith('-user-template'))
  const userPromptVersions = (promptVersions.data ?? []).filter((item) => item.prompt.category.endsWith('-user-template'))

  return (
    <>
      <Form.Item name="system_prompt_version_id" label="System Prompt Version" rules={[{ required: true, message: '请选择 System Prompt 的具体版本' }]}>
        <Select
          showSearch
          optionFilterProp="label"
          loading={promptVersions.isLoading}
          options={systemPromptVersions.map((version) => ({
            value: version.id,
            label: `${version.prompt.name} · v${version.version} · ${version.prompt.category}`,
          }))}
          placeholder="选择明确的不可变 PromptVersion"
        />
      </Form.Item>
      <Form.Item name="user_prompt_version_id" label="User Prompt Version（首轮任务，可选）">
        <Select
          allowClear
          showSearch
          optionFilterProp="label"
          loading={promptVersions.isLoading}
          options={userPromptVersions.map((version) => ({
            value: version.id,
            label: `${version.prompt.name} · v${version.version}`,
          }))}
          placeholder="会话 Playground 将根据变量 Schema 生成业务任务表单"
        />
      </Form.Item>
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
        message="AgentVersion 会直接固定两类 Prompt 的具体版本；User Prompt 只渲染首轮任务，后续追问使用普通消息。"
      />
      <Form.Item name="model_config_version_id" label="Model Config Version" rules={[{ required: true, message: '请选择模型配置版本' }]}>
        <ModelConfigVersionSelect style={{ width: '100%' }} />
      </Form.Item>
      <Space wrap align="start">
        <Form.Item name={['generation_config', 'temperature']} label="temperature">
          <InputNumber min={0} max={2} step={0.1} placeholder="Provider 默认值" />
        </Form.Item>
        <Form.Item name={['generation_config', 'top_p']} label="top_p">
          <InputNumber min={0} max={1} step={0.1} placeholder="Provider 默认值" />
        </Form.Item>
        <Form.Item name={['generation_config', 'max_output_tokens']} label="max tokens">
          <InputNumber min={1} placeholder="Provider 默认值" />
        </Form.Item>
      </Space>
      <Form.Item name="tool_version_ids" label="已发布 Tool Versions">
        <Select
          mode="multiple"
          showSearch
          optionFilterProp="label"
          loading={toolVersions.isLoading}
          options={(toolVersions.data ?? []).map((version) => ({
            value: version.id,
            label: `${version.tool.name} · v${version.version} · ${version.tool.source_type}${version.tool.sensitive ? ' · 需审批' : ''}`,
          }))}
          placeholder="选择明确的已发布工具版本"
        />
      </Form.Item>
      <Form.Item name="kb_ids" label="知识库">
        <Select
          mode="multiple"
          showSearch
          optionFilterProp="label"
          loading={kbs.isLoading}
          options={(kbs.data ?? []).map((kb) => ({ value: kb.id, label: `${kb.name} · ${kb.status}` }))}
          placeholder="选择 Agent 可检索的知识库"
        />
      </Form.Item>
      <Form.Item name="skill_version_ids" label="已发布 Skills">
        <Select
          mode="multiple"
          showSearch
          optionFilterProp="label"
          loading={versions.isLoading}
          options={(versions.data ?? []).map((version) => ({
            value: version.id,
            label: `${version.skill.name} · v${version.version}${version.requiresNetwork ? ' · 需要网络' : ''}`,
          }))}
          placeholder="Skill 引用的 Tool 和 KB 也需要挂载到 Agent"
        />
      </Form.Item>
      {selectedVersions.length > 0 && (
        <Alert
          type={networkRequired ? 'warning' : 'info'}
          showIcon
          style={{ marginBottom: 16 }}
          message={
            <Space wrap>
              <Typography.Text>Skill 依赖</Typography.Text>
              {[...new Set(selectedVersions.flatMap((v) => v.allowedTools))].map((name) => <Tag key={name}>Tool: {name}</Tag>)}
              {[...new Set(selectedVersions.flatMap((v) => v.allowedKBs))].map((id) => <Tag key={id}>KB: {id.slice(0, 8)}</Tag>)}
              {networkRequired && <Tag color="orange">需要开启网络</Tag>}
            </Space>
          }
        />
      )}
      <Form.Item name="allow_network" label="允许网络工具" valuePropName="checked">
        <Switch checkedChildren="已授权" unCheckedChildren="已关闭" />
      </Form.Item>
      <Typography.Paragraph type="secondary">
        关闭后，REST 工具会在审批及执行前被运行时拒绝。
      </Typography.Paragraph>
      <Form.Item name="max_steps" label="最大步数">
        <InputNumber min={1} max={50} style={{ width: '100%' }} />
      </Form.Item>
    </>
  )
}
