interface UsageThroughput {
  output_tokens?: number | null
  duration_ms?: number | null
  first_token_ms?: number | null
}

/** Average output speed after the first token; missing timing is not estimated. */
function usageAverageTps(usage: UsageThroughput): number | null {
  const { output_tokens: output, duration_ms: duration, first_token_ms: firstToken } = usage
  if (output == null || duration == null || firstToken == null
    || !Number.isFinite(output) || !Number.isFinite(duration) || !Number.isFinite(firstToken)
    || output < 0 || firstToken < 0 || duration <= firstToken) {
    return null
  }

  const tps = output / ((duration - firstToken) / 1000)
  return Number.isFinite(tps) ? tps : null
}

export function formatUsageAverageTps(usage: UsageThroughput): string {
  const tps = usageAverageTps(usage)
  return tps == null ? '-' : `${tps.toFixed(1)} tokens/s`
}

export function usageAverageTpsClass(usage: UsageThroughput): string {
  const tps = usageAverageTps(usage)
  if (tps == null) return 'text-gray-400 dark:text-gray-500'
  if (tps < 10) return 'text-red-600 dark:text-red-400'
  if (tps <= 20) return 'text-amber-600 dark:text-amber-400'
  return 'text-emerald-600 dark:text-emerald-400'
}
