import { api } from './client'

export interface ApprovalPresentation {
  title?: string
  operation_label?: string
  risk_label?: string
  field_labels?: Record<string, string>
  field_order?: string[]
  currency_fields?: Record<string, string>
}

export interface ApprovalView {
  id: string
  conversation_id: string
  tool_name: string
  arguments: string
  status: 'pending' | 'approved' | 'rejected' | 'completed'
  created_at?: string
  presentation?: ApprovalPresentation
}

export async function resolveApproval(id: string, decision: 'approve' | 'reject'): Promise<void> {
  await api.post(`/approvals/${encodeURIComponent(id)}/${decision}`)
}
