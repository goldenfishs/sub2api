interface UsageThroughput {
  output_tokens?: number | null
  duration_ms?: number | null
}

/** Average over the entire request, including the wait for the first token. */
export function formatUsageAverageTps(usage: UsageThroughput): string {
  const { output_tokens: output, duration_ms: duration } = usage
  if (output == null || duration == null || !Number.isFinite(output) || !Number.isFinite(duration)
    || output < 0 || duration <= 0) {
    return '-'
  }

  const tps = output / (duration / 1000)
  return Number.isFinite(tps) ? `${tps.toFixed(1)} tokens/s` : '-'
}
