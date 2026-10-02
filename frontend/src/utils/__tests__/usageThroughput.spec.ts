import { describe, expect, it } from 'vitest'
import { formatUsageAverageTps } from '../usageThroughput'

describe('formatUsageAverageTps', () => {
  it('uses total request duration and rounds to one decimal place', () => {
    expect(formatUsageAverageTps({ output_tokens: 230, duration_ms: 31270 })).toBe('7.4 tokens/s')
    expect(formatUsageAverageTps({ output_tokens: 20, duration_ms: 500 })).toBe('40.0 tokens/s')
  })

  it('keeps a recorded zero output distinct from missing output', () => {
    expect(formatUsageAverageTps({ output_tokens: 0, duration_ms: 1000 })).toBe('0.0 tokens/s')
    expect(formatUsageAverageTps({ duration_ms: 1000 })).toBe('-')
  })

  it.each([0, -1, null, undefined, NaN, Infinity])('does not calculate with invalid duration %s', (duration) => {
    expect(formatUsageAverageTps({ output_tokens: 230, duration_ms: duration })).toBe('-')
  })

  it.each([-1, null, undefined, NaN, Infinity])('does not calculate with invalid output %s', (output) => {
    expect(formatUsageAverageTps({ output_tokens: output, duration_ms: 1000 })).toBe('-')
  })

  it('does not display numeric overflow', () => {
    expect(formatUsageAverageTps({ output_tokens: Number.MAX_VALUE, duration_ms: Number.MIN_VALUE })).toBe('-')
  })
})
