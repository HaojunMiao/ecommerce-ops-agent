import {
  Alert, Tabs, Card, Button, Space, Typography, Select, Table, Tag, Input, Result, Spin, message,
} from 'antd'
import { ArrowLeftOutlined, SaveOutlined } from '@ant-design/icons'
import { Component, useMemo, useState, type ComponentType, type ErrorInfo, type ReactNode } from 'react'
import { useLocation, useRoute } from 'wouter'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import ReactDiffViewerImport, { type ReactDiffViewerProps } from 'react-diff-viewer-continued'
import {
  listPrompts, listVersions, createVersion, render as renderPrompt,
} from '@/api/prompt'
import type { PromptVersion } from '@/api/types'
import { CodeEditor } from '@/components/CodeEditor'
import { fmtTime } from '@/lib/format'

// react-diff-viewer-continued 3.x publishes a double-wrapped CommonJS default
// export. Vite 8 keeps that wrapper in production builds, so a direct default
// import becomes a module object and React rejects it as an element type.
const ReactDiffViewer = (
  (ReactDiffViewerImport as unknown as { default?: ComponentType<ReactDiffViewerProps> }).default
  ?? ReactDiffViewerImport
) as ComponentType<ReactDiffViewerProps>

class DiffErrorBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false }

  static getDerivedStateFromError() {
    return { failed: true }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('Prompt diff render failed', error, info)
  }

  render() {
    if (this.state.failed) {
      return <Alert type="error" showIcon message="版本对比渲染失败，请刷新页面后重试" />
    }
    return this.props.children
  }
}

export function PromptDetailPage() {
  const [, params] = useRoute('/prompts/:id')
  const id = params?.id ?? ''
  const [, navigate] = useLocation()
  const qc = useQueryClient()

  const promptsQ = useQuery({ queryKey: ['prompts'], queryFn: () => listPrompts() })
  const versionsQ = useQuery({ queryKey: ['prompt-versions', id], queryFn: () => listVersions(id) })
  const prompt = promptsQ.data?.find((p) => p.id === id)
  const versions = useMemo(
    () => [...(versionsQ.data ?? [])].sort((a, b) => b.version - a.version),
    [versionsQ.data],
  )
  const latest = versions[0]

  if (versionsQ.isLoading) return <Spin />
  if (versionsQ.isError) return <Result status="404" title="Prompt 不存在或无版本" />

  return (
    <div>
      <Space style={{ marginBottom: 16 }}>
        <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/prompts')}>
          返回
        </Button>
        <Typography.Title level={4} style={{ margin: 0 }}>
          {prompt?.name ?? 'Prompt'}
        </Typography.Title>
        {prompt?.category && <Tag>{prompt.category}</Tag>}
      </Space>

      <Tabs
        defaultActiveKey="editor"
        items={[
          { key: 'editor', label: 'Editor', children: <EditorTab promptId={id} latest={latest} onSaved={() => qc.invalidateQueries({ queryKey: ['prompt-versions', id] })} /> },
          { key: 'versions', label: `Versions (${versions.length})`, children: <VersionsTab promptId={id} versions={versions} /> },
          { key: 'diff', label: 'Diff', children: <DiffTab versions={versions} /> },
          { key: 'render', label: 'Render', children: <RenderTab promptId={id} /> },
        ]}
      />
    </div>
  )
}

// --- Editor:看最新版本(只读),或新建一版 ---
function EditorTab({ promptId, latest, onSaved }: { promptId: string; latest?: PromptVersion; onSaved: () => void }) {
  const [template, setTemplate] = useState(latest?.template ?? '')
  const [schema, setSchema] = useState(latest?.variables_schema ?? '{}')
  const save = useMutation({
    mutationFn: () => createVersion(promptId, template, schema),
    onSuccess: (v) => {
      message.success(`已保存为 v${v.version}(token≈${v.token_estimate})`)
      onSaved()
    },
    onError: (error: Error) => message.error(error.message),
  })

  return (
    <Card>
      <Space style={{ marginBottom: 8, justifyContent: 'space-between', width: '100%' }}>
        <Typography.Text type="secondary">
          当前最新:{latest ? `v${latest.version} · token≈${latest.token_estimate} · ${latest.hash.slice(0, 8)}` : '无版本'}
        </Typography.Text>
        <Button type="primary" icon={<SaveOutlined />} loading={save.isPending} onClick={() => save.mutate()}>
          保存为新版本
        </Button>
      </Space>
      <Typography.Text type="secondary">模板</Typography.Text>
      <CodeEditor value={template} onChange={setTemplate} language="handlebars" height={260} />
      <Typography.Text type="secondary" style={{ display: 'block', marginTop: 12 }}>
        变量定义(JSON Schema)
      </Typography.Text>
      <CodeEditor value={schema} onChange={setSchema} language="json" height={140} />
      <Alert type="info" showIcon style={{ marginTop: 16 }} message="模型配置与生成参数由 AgentVersion 管理" />
    </Card>
  )
}

// --- Versions:不可变模板版本列表 ---
function VersionsTab({ versions }: { promptId: string; versions: PromptVersion[] }) {
  return (
      <Table
        rowKey="id"
        dataSource={versions}
        pagination={false}
        columns={[
          { title: '版本', dataIndex: 'version', render: (v: number) => <Tag color="blue">v{v}</Tag> },
          { title: 'Hash', dataIndex: 'hash', render: (v: string) => <code>{v?.slice(0, 12)}</code> },
          { title: 'Token 估算', dataIndex: 'token_estimate' },
          { title: '创建时间', dataIndex: 'created_at', render: fmtTime },
        ]}
      />
  )
}

// --- Diff:选两版,用 react-diff-viewer 可视化对比模板 ---
function DiffTab({ versions }: { versions: PromptVersion[] }) {
  const [fromId, setFromId] = useState(versions[1]?.id ?? versions[0]?.id)
  const [toId, setToId] = useState(versions[0]?.id)
  const from = versions.find((v) => v.id === fromId)
  const to = versions.find((v) => v.id === toId)
  const opts = versions.map((v) => ({ value: v.id, label: `v${v.version}` }))

  return (
    <Card>
      <Space style={{ marginBottom: 12 }}>
        <span>从</span>
        <Select style={{ width: 120 }} value={fromId} onChange={setFromId} options={opts} />
        <span>到</span>
        <Select style={{ width: 120 }} value={toId} onChange={setToId} options={opts} />
      </Space>
      {from && to ? (
        <DiffErrorBoundary key={`${from.id}-${to.id}`}>
          <ReactDiffViewer oldValue={from.template} newValue={to.template} splitView leftTitle={`v${from.version}`} rightTitle={`v${to.version}`} />
        </DiffErrorBoundary>
      ) : (
        <Typography.Text type="secondary">至少需要两个版本才能对比</Typography.Text>
      )}
    </Card>
  )
}

// --- Render:指定 PromptVersion + vars(JSON)→ 渲染结果 ---
function RenderTab({ promptId }: { promptId: string }) {
  const versionsQ = useQuery({ queryKey: ['prompt-versions', promptId], queryFn: () => listVersions(promptId) })
  const [versionId, setVersionId] = useState<string>()
  const selectedVersionId = versionId ?? versionsQ.data?.at(-1)?.id
  const [vars, setVars] = useState('{\n  "question": "怎么退款?"\n}')
  const [result, setResult] = useState('')

  const run = useMutation({
    mutationFn: async () => {
      let parsed: Record<string, unknown> = {}
      try {
        parsed = JSON.parse(vars || '{}')
      } catch {
        throw new Error('变量 JSON 无效')
      }
      if (!selectedVersionId) throw new Error('请选择 Prompt 版本')
      return renderPrompt(promptId, selectedVersionId, parsed)
    },
    onSuccess: setResult,
    onError: (e: Error) => message.error(e.message),
  })

  return (
    <Card>
      <Space style={{ marginBottom: 12 }}>
        <span>版本</span>
        <Select
          style={{ width: 140 }}
          value={selectedVersionId}
          onChange={setVersionId}
          options={(versionsQ.data ?? []).map((v) => ({ value: v.id, label: `v${v.version}` }))}
        />
        <Button type="primary" loading={run.isPending} onClick={() => run.mutate()}>
          渲染
        </Button>
      </Space>
      <Typography.Text type="secondary">变量(JSON)</Typography.Text>
      <CodeEditor value={vars} onChange={setVars} language="json" height={140} />
      <Typography.Text type="secondary" style={{ display: 'block', marginTop: 12 }}>
        渲染结果
      </Typography.Text>
      <Input.TextArea value={result} readOnly rows={8} placeholder="点「渲染」查看该不可变版本的最终文本" />
    </Card>
  )
}
