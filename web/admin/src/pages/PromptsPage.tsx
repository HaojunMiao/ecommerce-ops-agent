import { Table, Button, Modal, Form, Input, Space, Typography, Tag, Alert, message } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useState } from 'react'
import { useLocation } from 'wouter'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { listPrompts, createPrompt, type CreatePromptRequest } from '@/api/prompt'
import { CodeEditor } from '@/components/CodeEditor'
import { fmtTime } from '@/lib/format'
import { useAuthStore } from '@/store/authStore'

export function PromptsPage() {
  const qc = useQueryClient()
  const [, navigate] = useLocation()
  const workspaceId = useAuthStore((s) => s.workspaceId)
  const [open, setOpen] = useState(false)
  const [template, setTemplate] = useState('你是一个有帮助的助手。\n\n用户问题：{{.question}}')
  const [schema, setSchema] = useState('{\n  "question": "string"\n}')
  const [form] = Form.useForm<{
    name: string
    category: string
  }>()

  const { data = [], isLoading } = useQuery({
    queryKey: ['prompts', workspaceId],
    queryFn: () => listPrompts(),
    enabled: !!workspaceId,
  })

  const create = useMutation({
    mutationFn: (req: CreatePromptRequest) => createPrompt(req),
    onSuccess: ({ prompt, version }) => {
      message.success(`已创建 Prompt v${version.version}`)
      setOpen(false)
      form.resetFields()
      qc.invalidateQueries({ queryKey: ['prompts'] })
      navigate(`/prompts/${prompt.id}`)
    },
    onError: (error: Error) => message.error(error.message),
  })

  if (!workspaceId) return <Alert type="info" showIcon message="请先在右上角选择一个工作空间" />

  return (
    <div>
      <Space style={{ marginBottom: 16, justifyContent: 'space-between', width: '100%' }}>
        <Typography.Title level={4} style={{ margin: 0 }}>
          Prompts
        </Typography.Title>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setOpen(true)}>
          新建 Prompt
        </Button>
      </Space>

      <Table
        rowKey="id"
        loading={isLoading}
        dataSource={data}
        pagination={{ pageSize: 10 }}
        columns={[
          {
            title: '名称',
            dataIndex: 'name',
            render: (v: string, row) => <a onClick={() => navigate(`/prompts/${row.id}`)}>{v}</a>,
          },
          { title: '分类', dataIndex: 'category', render: (v: string) => (v ? <Tag>{v}</Tag> : '-') },
          { title: '创建时间', dataIndex: 'created_at', render: fmtTime },
          { title: '操作', render: (_, row) => <a onClick={() => navigate(`/prompts/${row.id}`)}>详情 / 版本</a> },
        ]}
      />

      <Modal
        title="新建 Prompt"
        open={open}
        onCancel={() => setOpen(false)}
        onOk={() => form.submit()}
        confirmLoading={create.isPending}
        width={860}
        destroyOnClose
      >
        <Form
          form={form}
          layout="vertical"
          initialValues={{ category: 'general' }}
          onFinish={(values) => create.mutate({
            ...values,
            template,
            variables_schema: schema,
          })}
        >
          <Space style={{ width: '100%' }} size="large">
            <Form.Item name="name" label="名称" rules={[{ required: true, message: '请输入名称' }]} style={{ flex: 1, minWidth: 240 }}>
              <Input placeholder="如:客服回复模板" />
            </Form.Item>
            <Form.Item name="category" label="分类">
              <Input placeholder="general" />
            </Form.Item>
          </Space>
          <Typography.Text type="secondary">模板（Go template，变量写法 {'{{.变量名}}'}）</Typography.Text>
          <CodeEditor value={template} onChange={setTemplate} language="handlebars" height={200} />
          <Typography.Text type="secondary" style={{ display: 'block', marginTop: 12 }}>
            变量定义(JSON Schema)
          </Typography.Text>
          <CodeEditor value={schema} onChange={setSchema} language="json" height={140} />
          <Alert
            type="info"
            showIcon
            style={{ marginTop: 16 }}
            message="PromptVersion 只保存模板与变量契约"
            description="模型配置和生成参数在创建 AgentVersion 时选择并固化。"
          />
        </Form>
      </Modal>
    </div>
  )
}
