export type TierProbeStatus = 'supported' | 'unsupported' | 'unknown' | 'error' | 'expired' | 'unprobed'
export interface TierProbeResult {
  model: string
  tier: string
  status: TierProbeStatus
  observed_tier?: string
  checked_at: string
  expires_at: string
}
export function serviceTierBadgeState(extra: Record<string, unknown> | null | undefined, tier: string, now = Date.now()) {
  const results: TierProbeResult[] = Object.entries(extra || {}).flatMap(([key, value]) => {
    if (!key.startsWith('openai_tier_probe_') || !value || typeof value !== 'object') return []
    const r = value as TierProbeResult
    if (r.tier !== tier || typeof r.model !== 'string') return []
    const expiry = Date.parse(r.expires_at)
    const status: TierProbeStatus = !Number.isFinite(expiry) || expiry <= now ? 'expired'
      : ['supported', 'unsupported', 'unknown', 'error'].includes(r.status) ? r.status : 'unknown'
    return [{ ...r, status }]
  })
  // "supported" means at least one model is confirmed; the tooltip lists every
  // model separately and the scheduler always uses an exact model match.
  const status: TierProbeStatus = results.length === 0 ? 'unprobed'
    : results.some(r => r.status === 'supported') ? 'supported'
    : results.every(r => r.status === 'unsupported') ? 'unsupported'
    : results.some(r => r.status === 'unknown') ? 'unknown'
    : results.some(r => r.status === 'error') ? 'error' : 'expired'
  return { status, results }
}
