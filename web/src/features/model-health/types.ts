/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
export type ModelHealthStatus = 'healthy' | 'fluctuating' | 'unstable' | 'idle'

export type ModelHealthWindow = '12h' | '24h' | '7d' | '30d'

export type ModelHealthCatalogItem = {
  alias: string
}

export type ModelHealthPoolCatalogItem = {
  key: string
  alias: string
  group: string
}

export type ModelHealthCatalog = {
  groups: ModelHealthCatalogItem[]
  models: ModelHealthCatalogItem[]
  pools: ModelHealthPoolCatalogItem[]
}

export type ProbeMetrics = {
  status: ModelHealthStatus
  availability: number | null
  sample_count: number
  probe_run_count?: number
  probe_attempt_count?: number
  probe_success_count?: number
  latest_latency_ms: number | null
  last_checked_at?: string | null
}

export type ObservedMetrics = {
  status: ModelHealthStatus
  success_rate: number | null
  request_count: number
  avg_latency_ms: number | null
  avg_ttft_ms: number | null
  avg_tps: number | null
}

export type ModelHealthRow = {
  group: string
  model: string
  status: ModelHealthStatus
  signal_conflict: boolean
  probe: ProbeMetrics
  observed: ObservedMetrics
}

export type ModelHealthPoolRow = ModelHealthRow & {
  pool_key: string
  pool: string
}

export type ModelHealthSummary = {
  probe_availability: number | null
  probe_run_count?: number
  probe_attempt_count?: number
  probe_success_count?: number
  observed_success_rate: number | null
  observed_request_count: number
  healthy: number
  fluctuating: number
  unstable: number
  idle: number
}

export type ModelHealthOverview = {
  generated_at: string
  window: ModelHealthWindow
  bucket_seconds: number
  summary: ModelHealthSummary
  rows: ModelHealthRow[]
  pool_rows: ModelHealthPoolRow[]
}

export type ModelHealthPoint = {
  ts: number
  probe_availability: number | null
  probe_status: ModelHealthStatus
  probe_run_count?: number
  probe_success_count?: number
  probe_attempt_count?: number
  success_count?: number
  attempt_count?: number
  observed_success_rate: number | null
  observed_request_count: number
}

export type ModelHealthSeriesRow = {
  group: string
  model: string
  points: ModelHealthPoint[]
}

export type ModelHealthPoolSeriesRow = ModelHealthSeriesRow & {
  pool_key: string
  pool: string
}

export type ModelHealthSeries = {
  bucket_seconds: number
  rows: ModelHealthSeriesRow[]
  pool_rows: ModelHealthPoolSeriesRow[]
}

export type ModelHealthFilters = {
  window: ModelHealthWindow
  groups: string[]
  models: string[]
  pools: string[]
}

export type ModelHealthTargetMode = 'local' | 'upstream'

export type ModelHealthSamplingMode = 'fixed' | 'confirm_on_failure'

export type ModelHealthProtocol =
  | 'openai_chat'
  | 'openai_responses'
  | 'anthropic_messages'
  | 'gemini_generate_content'

export type ModelHealthTargetModel = {
  name: string
  public_alias: string
  required: boolean
}

export type ModelHealthTarget = {
  id: number
  name: string
  mode: ModelHealthTargetMode
  token_id: number | null
  protocol: ModelHealthProtocol
  endpoint_masked?: string
  endpoint?: string
  has_key: boolean
  credential_fingerprint?: string
  credentials_need_replacement?: boolean
  public_group_alias: string
  public_pool_alias: string
  publish_pool_detail: boolean
  observed_group?: string
  observed_groups?: string[]
  observed_channel_ids?: number[]
  enabled: boolean
  public: boolean
  interval_seconds: number
  timeout_seconds: number
  latency_slo_ms: number | null
  sampling_mode?: ModelHealthSamplingMode
  samples_per_run?: number
  minimum_successes?: number
  sample_spacing_seconds?: number
  last_checked_at?: string | null
  models: ModelHealthTargetModel[]
}

export type ModelHealthProbeAttemptResult = {
  attempt_index: number
  status: string
  latency_ms: number
  http_status: number
  error_class?: string
}

export type ModelHealthTargetValidationResult = {
  batch_status: ModelHealthStatus
  availability: number
  planned_attempts: number
  actual_attempts: number
  success_count: number
  attempts: ModelHealthProbeAttemptResult[]
}

export type ModelHealthAdminTokenOption = {
  id: number
  name: string
  status: number
  group: string
  remain_quota: number
  unlimited_quota: boolean
  expired_time: number
  model_limits_enabled: boolean
  model_limits: string[]
  available: boolean
  marked?: boolean
  reference_count?: number
}

export type ModelHealthAdminGroupOption = {
  name: string
  display_name: string
  models: string[]
}

export type ModelHealthAdminChannelOption = {
  id: number
  name: string
  status: number
  groups: string[]
  models: string[]
  available?: boolean
}

export type ModelHealthAdminOptions = {
  tokens: ModelHealthAdminTokenOption[]
  groups: ModelHealthAdminGroupOption[]
  models: string[]
  channels: ModelHealthAdminChannelOption[]
}

export type ModelHealthProbeToken = ModelHealthAdminTokenOption & {
  marked: boolean
  reference_count: number
}

export type ModelHealthTargetPayload = Omit<
  ModelHealthTarget,
  | 'id'
  | 'has_key'
  | 'credential_fingerprint'
  | 'endpoint_masked'
  | 'last_checked_at'
> & {
  id?: number
  api_key?: string
}

export type ApiResponse<T> = {
  success: boolean
  message?: string
  data: T
}
