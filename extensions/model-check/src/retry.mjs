import { setTimeout as sleep } from 'node:timers/promises';
import { CheckError } from './security.mjs';

export const MAX_ATTEMPTS = 3;
const MAX_OUTPUT_TOKENS = 16000;
const delays = [2000, 5000];
const temporaryErrors = new Set([
  'upstream_timeout', 'upstream_disconnected', 'connection_failed', 'upstream_limit:429',
  ...[429, 502, 503, 504, 520, 521, 522, 523, 524].map(code => `upstream_http:${code}`),
]);
export const retryable = (error, maxTokens) => error instanceof CheckError && (
  temporaryErrors.has(error.code) || (error.code === 'truncated_output' && Number.isInteger(maxTokens) && maxTokens > 0 && maxTokens < MAX_OUTPUT_TOKENS)
);
export const retryWait = (ms, signal) => sleep(ms, undefined, { signal });

// One logical test owns its credential and capacity slot throughout these attempts.
// Only bounded, stable metadata reaches persistence; error bodies and keys never do.
export async function generateWithRetry({ run, credential, trustedBase, generate, update, signal, wait = retryWait, now = Date.now }) {
  const attempts = [];
  let lastError = null;
  let maxTokens = run.max_tokens;
  for (let attempt = 1; attempt <= MAX_ATTEMPTS; attempt++) {
    if (signal.aborted) throw new CheckError('service_restarted', 503);
    const started_at = now();
    update({ attempt_count: attempt, effective_max_tokens: maxTokens, retry_at: null, retry_max_tokens: null });
    try {
      // Never mutate requested parameters, the saved monitor or its idempotency fingerprint.
      const options = maxTokens === run.max_tokens ? run : { ...run, max_tokens: maxTokens };
      const result = await generate(options, credential, trustedBase);
      const finished_at = now();
      const duration_ms = Math.max(0, finished_at - started_at);
      attempts.push({ attempt, started_at, finished_at, duration_ms, max_tokens: maxTokens, error: null, usage: result.usage ?? null });
      update({ attempts: [...attempts], retry_at: null, retry_max_tokens: null });
      return { ...result, generation_ms: duration_ms, effective_max_tokens: maxTokens };
    } catch (error) {
      const finished_at = now();
      lastError = error instanceof CheckError ? error.code : 'test_failed';
      attempts.push({ attempt, started_at, finished_at, duration_ms: Math.max(0, finished_at - started_at), max_tokens: maxTokens, error: lastError, usage: null });
      const shouldRetry = attempt < MAX_ATTEMPTS && retryable(error, maxTokens) && !signal.aborted;
      const nextMaxTokens = lastError === 'truncated_output' ? MAX_OUTPUT_TOKENS : maxTokens;
      update({ attempts: [...attempts], last_attempt_error: lastError, retry_at: shouldRetry ? finished_at + delays[attempt - 1] : null, retry_max_tokens: shouldRetry ? nextMaxTokens : null });
      if (!shouldRetry) throw error;
      try { await wait(delays[attempt - 1], signal); }
      catch (waitError) {
        update({ retry_at: null, retry_max_tokens: null });
        if (signal.aborted) throw new CheckError('service_restarted', 503);
        throw waitError;
      }
      maxTokens = nextMaxTokens;
    }
  }
}
