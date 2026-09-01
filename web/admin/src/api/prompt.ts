import { api } from './client'
import type { Prompt, PromptVersion } from './types'

export async function listPrompts(): Promise<Prompt[]> {
  const { data } = await api.get<Prompt[]>('/prompts')
  return data ?? []
}

export interface CreatePromptRequest {
  name: string
  category: string
  template: string
  variables_schema: string
}

export async function createPrompt(req: CreatePromptRequest): Promise<{ prompt: Prompt; version: PromptVersion }> {
  const { data } = await api.post('/prompts', req)
  return data
}

export async function listVersions(promptId: string): Promise<PromptVersion[]> {
  const { data } = await api.get<PromptVersion[]>(`/prompts/${promptId}/versions`)
  return data ?? []
}

export async function createVersion(
  promptId: string,
  template: string,
  variablesSchema: string,
): Promise<PromptVersion> {
  const { data } = await api.post<PromptVersion>(`/prompts/${promptId}/versions`, {
    template,
    variables_schema: variablesSchema,
  })
  return data
}

export async function render(promptId: string, versionId: string, vars: Record<string, unknown>): Promise<string> {
  const { data } = await api.post<{ rendered: string }>(`/prompts/${promptId}/render`, { version_id: versionId, vars })
  return data.rendered
}
