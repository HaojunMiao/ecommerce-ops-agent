import { api } from './client'

export interface ModelConfigVersion {
  id: string
  workspace_id: string
  name: string
  version: number
  provider_kind: string
  base_url: string
  model_name: string
  credential_ref: string
  timeout_ms: number
  max_retries: number
  input_price_per_million: number
  output_price_per_million: number
  cached_input_price_per_million: number
  created_by: string
  created_at: string
}

export const listModelConfigVersions = async () =>
  (await api.get<ModelConfigVersion[]>('/model-config-versions')).data ?? []
