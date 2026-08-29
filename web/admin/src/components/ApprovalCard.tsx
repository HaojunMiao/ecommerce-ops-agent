import {
  CheckCircleFilled, ClockCircleOutlined, CloseCircleFilled, LoadingOutlined, SafetyCertificateFilled,
} from '@ant-design/icons'
import { Alert, Button, Card, Descriptions, Space, Tag, Typography } from 'antd'
import type { ApprovalView } from '@/api/approval'

interface Props {
  approval: ApprovalView
  loading?: boolean
  onDecision: (decision: 'approve' | 'reject') => void
}

export function ApprovalCard({ approval, loading, onDecision }: Props) {
  let args: Record<string, unknown> = {}
  try { args = JSON.parse(approval.arguments || '{}') as Record<string, unknown> } catch { args = { arguments: approval.arguments } }
  const presentation = approval.presentation ?? {}
  const keys = presentation.field_order?.filter((key) => key in args) ?? Object.keys(args)
  const status = approval.status
  const statusView = {
    pending: { label: '等待人工审批', color: 'orange', icon: <ClockCircleOutlined /> },
    approved: { label: '已批准，等待执行', color: 'blue', icon: <LoadingOutlined spin /> },
    completed: { label: '操作执行完成', color: 'green', icon: <CheckCircleFilled /> },
    rejected: { label: '审批已拒绝', color: 'red', icon: <CloseCircleFilled /> },
  }[status]

  return (
    <Card
      size="small"
      title={presentation.title || '敏感操作审批'}
      extra={<Tag color="red" icon={<SafetyCertificateFilled />}>{presentation.risk_label || '高风险'}</Tag>}
      style={{ borderRadius: 12, boxShadow: '0 8px 24px rgba(0, 0, 0, 0.06)' }}
    >
      <Space direction="vertical" size={14} style={{ width: '100%' }}>
        <Alert type={status === 'rejected' ? 'error' : status === 'completed' ? 'success' : 'warning'} showIcon icon={statusView.icon} message={statusView.label} />
        <Typography.Text type="secondary">{presentation.operation_label || approval.tool_name}</Typography.Text>
        <Descriptions size="small" column={1} bordered>
          {keys.map((key) => {
            const currency = presentation.currency_fields?.[key] ?? ''
            return <Descriptions.Item key={key} label={presentation.field_labels?.[key] || key}>{currency}{String(args[key] ?? '')}</Descriptions.Item>
          })}
        </Descriptions>
        {status === 'pending' && (
          <Space style={{ justifyContent: 'flex-end', width: '100%' }}>
            <Button danger loading={loading} onClick={() => onDecision('reject')}>拒绝</Button>
            <Button type="primary" loading={loading} onClick={() => onDecision('approve')}>批准并继续执行</Button>
          </Space>
        )}
      </Space>
    </Card>
  )
}
