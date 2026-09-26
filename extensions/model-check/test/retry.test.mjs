import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { CheckError } from '../src/security.mjs';
import { generateWithRetry, retryable } from '../src/retry.mjs';
import { Store } from '../src/store.mjs';

test('only explicit temporary transport errors permit retries', () => {
  for (const code of ['upstream_timeout', 'upstream_disconnected', 'connection_failed', 'upstream_limit:429', ...[429, 502, 503, 504, 520, 521, 522, 523, 524].map(n => `upstream_http:${n}`)]) {
    assert.equal(retryable(new CheckError(code)), true, code);
  }
  for (const code of ['upstream_auth:401', 'upstream_auth:403', 'upstream_http:400', 'upstream_http:404', 'upstream_http:500', 'invalid_upstream_response', 'truncated_output', 'missing_html', 'upstream_error', 'render_timeout', 'review']) {
    assert.equal(retryable(new CheckError(code)), false, code);
  }
  assert.equal(retryable(Object.assign(new Error('secret response body'), { code: 'upstream_http:502' })), false);
});

test('attempt limit is three with two bounded delays and stable metadata', async () => {
  let calls = 0; let clock = 1000; const waits = []; let persisted = {};
  const controller = new AbortController();
  await assert.rejects(generateWithRetry({
    run: { id: 'same', seed: 42 }, credential: { key: 'fixture-secret' }, signal: controller.signal,
    now: () => clock, update: fields => { persisted = { ...persisted, ...fields }; },
    wait: async ms => { waits.push(ms); clock += ms; },
    generate: async () => { calls++; clock += 600000; throw new CheckError('upstream_timeout', 504); },
  }), /upstream_timeout/);
  assert.equal(calls, 3);
  assert.deepEqual(waits, [2000, 5000]);
  assert.equal(clock - 1000, 1807000);
  assert.equal(persisted.attempt_count, 3);
  assert.equal(persisted.retry_at, null);
  assert.equal(persisted.attempts.length, 3);
  assert.ok(persisted.attempts.every(attempt => attempt.duration_ms === 600000 && attempt.usage === null));
  assert.ok(!JSON.stringify(persisted).includes('fixture-secret'));
});

test('shutdown aborts retry waits promptly and never starts another model call', async () => {
  const controller = new AbortController(); let calls = 0; let persisted = {};
  let entered; const waiting = new Promise(resolve => { entered = resolve; });
  const pending = generateWithRetry({
    run: {}, credential: {}, signal: controller.signal,
    update: fields => { persisted = { ...persisted, ...fields }; if (fields.retry_at) entered(); },
    generate: async () => { calls++; throw new CheckError('upstream_http:502', 502); },
  });
  await waiting; controller.abort();
  await assert.rejects(pending, /service_restarted/);
  assert.equal(calls, 1);
  assert.equal(persisted.retry_at, null);
});

test('shutdown during an attempt prevents retries and permanent errors are single-attempt', async () => {
  for (const code of ['upstream_http:502', 'upstream_auth:403', 'truncated_output', 'missing_html']) {
    const controller = new AbortController(); let calls = 0;
    await assert.rejects(generateWithRetry({ run: {}, credential: {}, signal: controller.signal, update: () => {},
      wait: async () => { assert.fail('must not wait'); },
      generate: async () => { calls++; if (code === 'upstream_http:502') controller.abort(); throw new CheckError(code); },
    }), error => error.code === code);
    assert.equal(calls, 1);
  }
});

test('restart fails interrupted retry jobs without replay and clears their countdown', () => {
  const directory = mkdtempSync(join(tmpdir(), 'model-check-retry-'));
  let store;
  try {
    const path = join(directory, 'state.sqlite'); store = new Store(path);
    const run = store.createRun({ model: 'fixture-model', prompt: 'fixed', seed: 42 });
    store.updateRun(run.id, { status: 'generating', attempt_count: 1, retry_at: Date.now() + 5000, last_attempt_error: 'upstream_http:502' });
    store.close(); store = new Store(path);
    const recovered = store.run(run.id);
    assert.equal(recovered.status, 'failed');
    assert.equal(recovered.error, 'service_restarted');
    assert.equal(recovered.attempt_count, 1);
    assert.equal(recovered.retry_at, null);
  } finally { store?.close(); rmSync(directory, { recursive: true, force: true }); }
});
