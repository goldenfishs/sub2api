import { describe, expect, it } from 'vitest'
import { serviceTierBadgeState } from '../serviceTierCapability'
const now = Date.parse('2026-10-10T12:00:00Z')
const row = { model: 'gpt-6-astra', tier: 'priority', status: 'supported', checked_at: '2026-10-10T11:00:00Z', expires_at: '2026-10-10T17:00:00Z' }
describe('service tier account badge', () => {
  it('distinguishes no probe, unknown response, failure, and confirmed tier', () => {
    expect(serviceTierBadgeState({}, 'priority', now).status).toBe('unprobed')
    for (const status of ['supported', 'unknown', 'error', 'unsupported']) {
      expect(serviceTierBadgeState({ openai_tier_probe_a: { ...row, status } }, 'priority', now).status).toBe(status)
    }
  })
  it('never presents expired or wrong-tier results as confirmed', () => {
    expect(serviceTierBadgeState({ openai_tier_probe_a: row }, 'ultrafast', now).status).toBe('unprobed')
    expect(serviceTierBadgeState({ openai_tier_probe_a: row }, 'priority', now + 24 * 3600_000).status).toBe('expired')
  })
  it('retains per-model results for a mixed-capability account', () => {
    const result = serviceTierBadgeState({ openai_tier_probe_a: row, openai_tier_probe_b: { ...row, model: 'gpt-6.1-sol', status: 'unknown' } }, 'priority', now)
    expect(result.status).toBe('supported')
    expect(result.results).toHaveLength(2)
  })
})
