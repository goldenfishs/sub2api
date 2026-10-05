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
  assert.equal(retryable(new CheckError('truncated_output'), 8000), true);
  assert.equal(retryable(new CheckError('truncated_output'), 16000), true);
  assert.equal(retryable(new CheckError('truncated_output'), 32768), false);
});

test('truncation raises only attempt options to 32768 and mixed failures share the three-attempt budget', async () => {
  for (const [errors, expectedCaps] of [
    [['truncated_output'], [8000, 32768]],
    [['upstream_http:502', 'truncated_output'], [8000, 8000, 32768]],
    [['truncated_output', 'upstream_http:502'], [8000, 32768, 32768]],
  ]) {
    const run = { id: 'fixed-run', prompt: 'fixed-prompt', seed: 42, max_tokens: 8000 };
    const credential = { key: 'fixture-key' }; const caps = []; const waits = []; let persisted = {};
    const result = await generateWithRetry({ run, credential, signal: new AbortController().signal,
      update: fields => { persisted = { ...persisted, ...fields }; },
      wait: async ms => { waits.push(ms); },
      generate: async (options, key) => {
        caps.push(options.max_tokens);
        assert.equal(options.id, run.id); assert.equal(options.prompt, run.prompt); assert.equal(options.seed, run.seed);
        assert.equal(key, credential);
        const code = errors[caps.length - 1]; if (code) throw new CheckError(code, 502);
        return { html: '<html></html>', usage: { output_tokens: 10 } };
      },
    });
    assert.deepEqual(caps, expectedCaps);
    assert.deepEqual(waits, [2000, 5000].slice(0, expectedCaps.length - 1));
    assert.deepEqual(persisted.attempts.map(attempt => attempt.max_tokens), expectedCaps);
    assert.equal(result.effective_max_tokens, 32768);
    assert.equal(persisted.effective_max_tokens, 32768);
    assert.equal(persisted.retry_max_tokens, null);
    assert.equal(run.max_tokens, 8000);
  }
});

test('truncation at the ceiling and exhausted mixed failures never add another attempt', async () => {
  for (const [initialCap, errors, expectedCaps] of [
    [32768, ['truncated_output'], [32768]],
    [8000, ['truncated_output', 'truncated_output'], [8000, 32768]],
    [8000, ['upstream_http:502', 'upstream_http:502', 'truncated_output'], [8000, 8000, 8000]],
  ]) {
    const caps = []; let persisted = {};
    await assert.rejects(generateWithRetry({ run: { max_tokens: initialCap }, credential: {}, signal: new AbortController().signal,
      update: fields => { persisted = { ...persisted, ...fields }; }, wait: async () => {},
      generate: async run => { caps.push(run.max_tokens); throw new CheckError(errors[caps.length - 1]); },
    }), /truncated_output/);
    assert.deepEqual(caps, expectedCaps);
    assert.equal(persisted.effective_max_tokens, expectedCaps.at(-1));
    assert.equal(persisted.retry_max_tokens, null);
    assert.equal(persisted.retry_at, null);
  }
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
    run: { max_tokens: 8000 }, credential: {}, signal: controller.signal,
    update: fields => { persisted = { ...persisted, ...fields }; if (fields.retry_at) entered(); },
    generate: async () => { calls++; throw new CheckError('truncated_output', 502); },
  });
  await waiting; controller.abort();
  await assert.rejects(pending, /service_restarted/);
  assert.equal(calls, 1);
  assert.equal(persisted.retry_at, null);
  assert.equal(persisted.retry_max_tokens, null);
  assert.equal(persisted.effective_max_tokens, 8000);
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
    store.updateRun(run.id, { status: 'generating', attempt_count: 1, effective_max_tokens: 8000, retry_max_tokens: 32768, retry_at: Date.now() + 5000, last_attempt_error: 'truncated_output' });
    store.close(); store = new Store(path);
    const recovered = store.run(run.id);
    assert.equal(recovered.status, 'failed');
    assert.equal(recovered.error, 'service_restarted');
    assert.equal(recovered.attempt_count, 1);
    assert.equal(recovered.retry_at, null);
    assert.equal(recovered.retry_max_tokens, null);
    assert.equal(recovered.effective_max_tokens, 8000);
  } finally { store?.close(); rmSync(directory, { recursive: true, force: true }); }
});
