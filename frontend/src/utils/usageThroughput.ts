interface UsageThroughput {
  output_tokens?: number | null
  duration_ms?: number | null
  first_token_ms?: number | null
}

/** Average output speed after the first token; missing timing is not estimated. */
export function formatUsageAverageTps(usage: UsageThroughput): string {
  const { output_tokens: output, duration_ms: duration, first_token_ms: firstToken } = usage
  if (output == null || duration == null || firstToken == null
    || !Number.isFinite(output) || !Number.isFinite(duration) || !Number.isFinite(firstToken)
    || output < 0 || firstToken < 0 || duration <= firstToken) {
    return '-'
  }

  const tps = output / ((duration - firstToken) / 1000)
  return Number.isFinite(tps) ? `${tps.toFixed(1)} tokens/s` : '-'
}
