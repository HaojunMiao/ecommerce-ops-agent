import { Alert, Card, Table, Tag, Typography } from 'antd'
import { useQuery } from '@tanstack/react-query'
import { listModelConfigVersions, type ModelConfigVersion } from '@/api/model'
import { useAuthStore } from '@/store/authStore'

// API Key remains in the deployment environment; non-secret settings are immutable versions.
export function ModelsPage() {
  const workspaceId = useAuthStore((state) => state.workspaceId)
  const query = useQuery({
    queryKey: ['model-config-versions', workspaceId], queryFn: listModelConfigVersions, enabled: !!workspaceId,
  })
  return (
    <Card title="模型配置版本">
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
        message="API Key 由环境变量注入；地址、模型、超时和重试策略固化为不可变版本，Prompt/Agent 快照引用具体版本。"
      />
      <Table<ModelConfigVersion>
        rowKey="id"
        loading={query.isLoading}
        dataSource={query.data ?? []}
        pagination={false}
        columns={[
          { title: '配置版本', render: (_, row) => <Tag color="blue">v{row.version}</Tag> },
          { title: '版本 ID', dataIndex: 'id', render: (value: string) => <Typography.Text code>{value}</Typography.Text> },
          { title: '名称', dataIndex: 'name' },
          { title: '模型', dataIndex: 'model_name' },
          { title: 'Provider', dataIndex: 'provider_kind' },
          { title: '凭据引用', dataIndex: 'credential_ref' },
        ]}
      />
    </Card>
  )
}
