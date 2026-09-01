import { api } from './client'
import type { Tool, ToolTestRun, ToolVersion } from './types'

export async function listTools(): Promise<Tool[]> {
  const { data } = await api.get<Tool[]>('/tools')
  return data ?? []
}

export interface CreateToolRequest {
  name: string
  source_type: string
  description: string
  schema_json: string
  endpoint_config: string
  auth_config: string
  sensitive: boolean
}

export async function createTool(req: CreateToolRequest): Promise<Tool> {
  const { data } = await api.post<Tool>('/tools', req)
  return data
}

// 真正调用工具并记录试调结果，成功后才允许发布。
export async function testTool(toolId: string, input: unknown): Promise<ToolTestRun> {
  const { data } = await api.post<ToolTestRun>(`/tools/${toolId}/test`, { input })
  return data
}

export async function listToolVersions(toolId: string): Promise<ToolVersion[]> {
  const { data } = await api.get<ToolVersion[]>(`/tools/${toolId}/versions`)
  return data ?? []
}

export async function createToolVersion(
  toolId: string,
  body: Pick<ToolVersion, 'schema_json' | 'endpoint_config' | 'auth_config' | 'retry_policy'>,
): Promise<ToolVersion> {
  const { data } = await api.post<ToolVersion>(`/tools/${toolId}/versions`, body)
  return data
}

export async function publishToolVersion(toolId: string, versionId: string): Promise<void> {
  await api.post(`/tools/${toolId}/versions/${versionId}/publish`)
}
