import { apiClient } from './client'

export type CheckStatus = 'queued' | 'generating' | 'rendering' | 'normal' | 'review' | 'failed'
export type CheckTopic = 'pelican' | 'creative'
export interface CheckOptions {
  model: string
  topic: CheckTopic
  protocol: 'responses' | 'chat'
  reasoning: 'default' | 'low' | 'medium' | 'high'
  max_tokens: number
}
export interface CheckRun extends CheckOptions {
  id: string
  channel_id: string | null
  source: 'self' | 'manual' | 'scheduled' | 'demo'
  status: CheckStatus
  key_source?: 'existing' | 'external' | null
  group_id?: number | null
  group_name?: string | null
  created_at: number
  finished_at?: number
  attempt_count?: number | null
  max_attempts?: number | null
  retry_at?: number | null
  last_attempt_error?: string | null
  usage_scope?: 'successful_attempt' | null
  attempts?: { attempt: number; started_at: number; finished_at: number; duration_ms: number; error?: string | null; usage?: CheckRun['usage'] }[]
  generation_ms?: number | null
  tps?: number | null
  total_ms?: number
  usage?: { input_tokens: number | null; output_tokens: number | null; cached_tokens: number | null } | null
  quality_review?: { verdict: 'normal' | 'degraded'; reviewed_at: number } | null
  assessment?: { score: number; verdict: string; delta: number | null; reasons: string[]; dimensions: { id: string; score: number; max: number }[]; semantic_review: string; method: string }
  label?: string
  thumbnail?: string | null
  image?: string | null
  html?: string
  error?: string
  prompt?: string
  prompt_hash: string
  prompt_version: string
  seed?: number
  conditions?: string[]
  metrics?: Record<string, number | boolean | number[] | string>
}
export interface CheckStatistics {
  limit: number
  total: number
  judged: number
  normal: number
  review: number
  failed: number
  pass_rate: number | null
  success_rate: number | null
  latest_duration_ms: number | null
  history: { id: string; status: 'normal' | 'review' | 'failed'; created_at: number; duration_ms: number | null }[]
}
export interface CheckChannel extends CheckOptions {
  id: string
  name: string
  enabled: boolean
  public: boolean
  interval_minutes: number
  next_run: number
  baseline_id: string | null
  demo: boolean
  latest: CheckRun | null
  preview: CheckRun | null
  statistics?: CheckStatistics
  group_id?: number | null
  group_name?: string | null
  history: { id: string; status: CheckStatus; score: number | null; created_at: number }[]
  base_url?: string
  key_source?: 'existing' | 'external' | null
  key_id?: number | null
  has_key?: boolean
}
export interface CheckOverview {
  demo: boolean
  topics: { id: CheckTopic; name: string; name_en: string; description: string; description_en: string; color: string }[]
  channels: CheckChannel[]
  works: CheckRun[]
  retention: number
  updated_at: number
}
export interface CheckForm extends CheckOptions {
  key_source: 'existing' | 'external'
  group_name?: string | null
  key_id: number | null
  base_url: string
  key: string
}
export interface CheckChannelForm extends CheckForm {
  name: string
  interval_minutes: number
  enabled: boolean
  public: boolean
}
export interface CheckKey { id: number; name: string; group_name: string }

const base = '/model-check'
export const modelCheckAPI = {
  overview: async () => (await apiClient.get<CheckOverview>(`${base}/overview`)).data,
  mine: async () => (await apiClient.get<CheckRun[]>(`${base}/mine`)).data,
  keys: async () => (await apiClient.get<CheckKey[]>(`${base}/keys`)).data,
  run: async (id: string) => (await apiClient.get<CheckRun>(`${base}/runs/${id}`)).data,
  test: async (form: CheckForm) => (await apiClient.post<CheckRun>(`${base}/tests`, form)).data,
  demo: async (topic: CheckTopic) => (await apiClient.post<CheckRun>(`${base}/demo`, { topic })).data,
  channels: async () => (await apiClient.get<CheckChannel[]>(`${base}/admin/channels`)).data,
  save: async (form: CheckChannelForm, id?: string) => (id
    ? await apiClient.put<CheckChannel>(`${base}/admin/channels/${id}`, form)
    : await apiClient.post<CheckChannel>(`${base}/admin/channels`, form)).data,
  deleteChannel: async (id: string) => (await apiClient.delete(`${base}/admin/channels/${id}`)).data,
  runChannel: async (id: string) => (await apiClient.post<CheckRun>(`${base}/admin/channels/${id}/run`, {})).data,
  review: async (id: string, verdict: 'normal' | 'degraded' | 'clear') => (await apiClient.post<CheckRun>(`${base}/admin/runs/${id}/review`, { verdict })).data,
  baseline: async (id: string, run_id: string) => (await apiClient.post(`${base}/admin/channels/${id}/baseline`, { run_id })).data,
}

export function checkErrorCode(error: unknown): string {
  const e = error as { reason?: string; message?: string; response?: { data?: { reason?: string; message?: string } } }
  return String(e.response?.data?.reason || e.response?.data?.message || e.reason || e.message || 'connection_failed').split(':')[0]
}
export const isCheckRunning = (status: CheckStatus) => ['queued', 'generating', 'rendering'].includes(status)
