import { api } from './client'
import type { AuditLog, Approval } from './types'

export interface AuditFilter {
  conversation_id?: string
  actor?: string
  limit?: number
}

export async function listAuditLogs(filter: AuditFilter): Promise<AuditLog[]> {
  const { data } = await api.get<AuditLog[]>('/audit/logs', { params: filter })
  return data ?? []
}

// ---- 审批队列 ----
export async function listApprovals(): Promise<Approval[]> {
  const { data } = await api.get<Approval[]>('/approvals')
  return data ?? []
}

export async function approveApproval(id: string): Promise<void> {
  await api.post(`/approvals/${id}/approve`)
}

export async function rejectApproval(id: string): Promise<void> {
  await api.post(`/approvals/${id}/reject`)
}
