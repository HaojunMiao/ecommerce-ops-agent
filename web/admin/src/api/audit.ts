import { api } from './client'
import type { AuditLog } from './types'

export interface AuditFilter {
  conversation_id?: string
  actor?: string
  limit?: number
}

export async function listAuditLogs(filter: AuditFilter): Promise<AuditLog[]> {
  const { data } = await api.get<AuditLog[]>('/audit/logs', { params: filter })
  return data ?? []
}
