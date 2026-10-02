import { describe, expect, it } from 'vitest'
import { formatUsageAverageTps } from '../usageThroughput'

describe('formatUsageAverageTps', () => {
  it('subtracts first-token latency and rounds to one decimal place', () => {
    expect(formatUsageAverageTps({ output_tokens: 230, duration_ms: 31270, first_token_ms: 17270 })).toBe('16.4 tokens/s')
    expect(formatUsageAverageTps({ output_tokens: 20, duration_ms: 500, first_token_ms: 0 })).toBe('40.0 tokens/s')
  })

  it('keeps a recorded zero output distinct from missing output', () => {
    expect(formatUsageAverageTps({ output_tokens: 0, duration_ms: 1000, first_token_ms: 100 })).toBe('0.0 tokens/s')
    expect(formatUsageAverageTps({ duration_ms: 1000, first_token_ms: 100 })).toBe('-')
  })

  it.each([0, -1, null, undefined, NaN, Infinity])('does not calculate with invalid duration %s', (duration) => {
    expect(formatUsageAverageTps({ output_tokens: 230, duration_ms: duration, first_token_ms: 100 })).toBe('-')
  })

  it.each([-1, null, undefined, NaN, Infinity])('does not calculate with invalid output %s', (output) => {
    expect(formatUsageAverageTps({ output_tokens: output, duration_ms: 1000, first_token_ms: 100 })).toBe('-')
  })

  it.each([-1, null, undefined, NaN, Infinity, 1000, 1100])('does not estimate missing or invalid generation time: %s', (firstToken) => {
    expect(formatUsageAverageTps({ output_tokens: 230, duration_ms: 1000, first_token_ms: firstToken })).toBe('-')
  })

  it('does not display numeric overflow', () => {
    expect(formatUsageAverageTps({ output_tokens: Number.MAX_VALUE, duration_ms: Number.MIN_VALUE, first_token_ms: 0 })).toBe('-')
  })
})
