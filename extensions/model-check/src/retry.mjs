import { setTimeout as sleep } from 'node:timers/promises';
import { CheckError } from './security.mjs';

export const MAX_ATTEMPTS = 3;
const delays = [2000, 5000];
const temporaryErrors = new Set([
  'upstream_timeout', 'upstream_disconnected', 'connection_failed', 'upstream_limit:429',
  ...[429, 502, 503, 504, 520, 521, 522, 523, 524].map(code => `upstream_http:${code}`),
]);
export const retryable = error => error instanceof CheckError && temporaryErrors.has(error.code);
export const retryWait = (ms, signal) => sleep(ms, undefined, { signal });

// One logical test owns its credential and capacity slot throughout these attempts.
// Only bounded, stable metadata reaches persistence; error bodies and keys never do.
export async function generateWithRetry({ run, credential, trustedBase, generate, update, signal, wait = retryWait, now = Date.now }) {
  const attempts = [];
  let lastError = null;
  for (let attempt = 1; attempt <= MAX_ATTEMPTS; attempt++) {
    if (signal.aborted) throw new CheckError('service_restarted', 503);
    const started_at = now();
    update({ attempt_count: attempt, retry_at: null });
    try {
      const result = await generate(run, credential, trustedBase);
      const finished_at = now();
      const duration_ms = Math.max(0, finished_at - started_at);
      attempts.push({ attempt, started_at, finished_at, duration_ms, error: null, usage: result.usage ?? null });
      update({ attempts: [...attempts], retry_at: null });
      return { ...result, generation_ms: duration_ms };
    } catch (error) {
      const finished_at = now();
      lastError = error instanceof CheckError ? error.code : 'test_failed';
      attempts.push({ attempt, started_at, finished_at, duration_ms: Math.max(0, finished_at - started_at), error: lastError, usage: null });
      const shouldRetry = attempt < MAX_ATTEMPTS && retryable(error) && !signal.aborted;
      update({ attempts: [...attempts], last_attempt_error: lastError, retry_at: shouldRetry ? finished_at + delays[attempt - 1] : null });
      if (!shouldRetry) throw error;
      try { await wait(delays[attempt - 1], signal); }
      catch (waitError) {
        update({ retry_at: null });
        if (signal.aborted) throw new CheckError('service_restarted', 503);
        throw waitError;
      }
    }
  }
}
