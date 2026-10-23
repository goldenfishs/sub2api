import { apiClient } from '../client'

export interface RiskSessionConfig {
  enabled: boolean
  snapshot_retention_days: number
}

export interface RiskSessionRecord {
  id: number
  created_at: string
  user_id: number | null
  user_email: string
  api_key_id: number | null
  api_key_name: string
  source: 'local_audit' | 'upstream_cyber'
  reason: string
  model: string
  endpoint: string
  blocked: boolean
  released_at?: string | null
  released_by_user_id?: number | null
  release_reason?: string
  snapshot_status: 'stored' | 'expired' | 'unavailable_encryption' | 'unavailable_oversize' | 'unavailable_empty' | 'unavailable_storage' | 'unreadable'
  request_bytes: number
  response_bytes: number
  snapshot_scope: 'received_request_only' | 'websocket_turn_only' | 'audit_payload_only'
  response_scope?: 'local_rejection_summary' | 'upstream_error_body_bounded' | 'upstream_error_event' | 'reconstructed_upstream_error_event' | 'upstream_error_summary' | 'upstream_error_summary_oversize'
  snapshot_expires_at?: string | null
}

export interface RiskSessionDetail extends RiskSessionRecord {
  request_body: string
  response_body: string
  request_sha256?: string
  response_sha256?: string
  snapshot_error?: string
}

export interface RiskSessionList {
  items: RiskSessionRecord[]
  total: number
  page: number
  page_size: number
}

const basePath = '/admin/risk-control/sessions'

export const riskSessionsAPI = {
  async getConfig(): Promise<RiskSessionConfig> {
    const { data } = await apiClient.get<RiskSessionConfig>(`${basePath}/config`)
    return data
  },
  async updateConfig(config: RiskSessionConfig): Promise<RiskSessionConfig> {
    const { data } = await apiClient.put<RiskSessionConfig>(`${basePath}/config`, config)
    return data
  },
  async list(params: { page: number; page_size: number }): Promise<RiskSessionList> {
    const { data } = await apiClient.get<RiskSessionList>(basePath, { params })
    return data
  },
  async get(id: number): Promise<RiskSessionDetail> {
    const { data } = await apiClient.get<RiskSessionDetail>(`${basePath}/${id}`)
    return data
  },
  async release(id: number, reason: string): Promise<RiskSessionRecord> {
    const { data } = await apiClient.post<RiskSessionRecord>(`${basePath}/${id}/release`, { reason })
    return data
  },
}
