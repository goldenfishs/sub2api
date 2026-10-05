import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { mkdtempSync, rmSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import sharp from 'sharp';
import { createService } from '../src/server.mjs';
import { publicRun } from '../src/store.mjs';
import { CheckError, secretVault } from '../src/security.mjs';
import { validateOptions } from '../src/prompts.mjs';
import { renderArtwork } from '../src/render.mjs';
import { sampleHTML } from '../src/samples.mjs';

const adminKey = 'admin-fixture-integration-only';
const modelKey = 'sk-fixture-model-only';
const imageBytes = await sharp({ create: { width: 8, height: 8, channels: 3, background: '#85baa1' } }).webp().toBuffer();
const artifact = { html: '<html><svg></svg></html>', image: imageBytes.toString('base64'), thumbnail: imageBytes.toString('base64'),
  metrics: { svg_count: 1, visible_shapes: 20, contrast: 35, occupied_ratio: .5, motion_ratio: .1, overflow_mobile: false, overflow_desktop: false } };

async function harness(t, overrides = {}) {
  const directory = mkdtempSync(join(tmpdir(), 'model-check-integration-'));
  const auth = { key: adminKey, available: true, exists: true, checked: 0, requests: [] };
  const backend = http.createServer((req, res) => {
    res.setHeader('Content-Type', 'application/json');
    auth.requests.push({ path: req.url, adminKey: req.headers['x-api-key'], bearer: req.headers.authorization });
    if (!auth.available) { res.writeHead(503); res.end('{}'); return; }
    if (req.url === '/api/v1/admin/settings/admin-api-key') {
      auth.checked++;
      if (req.headers['x-api-key'] !== auth.key) { res.writeHead(401); res.end(JSON.stringify({ message: 'DO_NOT_RELAY_AUTH_BODY' })); return; }
      res.end(JSON.stringify({ code: 0, data: { exists: auth.exists, masked_key: 'admin-***' } })); return;
    }
    if (req.url === '/api/v1/auth/me') {
      const role = req.headers.authorization === 'Bearer fixture-admin' ? 'admin' : req.headers.authorization === 'Bearer fixture-user' ? 'user' : null;
      if (role) { res.end(JSON.stringify({ code: 0, data: { id: role === 'admin' ? 1 : 2, role } })); return; }
    }
    res.writeHead(401); res.end('{}');
  });
  await new Promise(resolve => backend.listen(0, '127.0.0.1', resolve));
  const config = { dataDir: directory, backend: `http://127.0.0.1:${backend.address().port}`, siteApiBase: `http://127.0.0.1:${backend.address().port}/v1`, demo: false };
  const generated = [];
  const app = createService(config, { retryWait: overrides.retryWait || (async () => {}), retryNow: overrides.retryNow, render: overrides.render || (async () => artifact), generate: async (run, credential) => {
    generated.push({ run, credential });
    if (overrides.generate) return overrides.generate(run, credential);
    await new Promise(resolve => setTimeout(resolve, 20));
    return { html: sampleHTML(run.topic), usage: { input_tokens: 428, output_tokens: 5229 } };
  } });
  const channel = app.store.saveChannel({ ...validateOptions({ model: 'fixture-model', topic: 'pelican', reasoning: 'low' }),
    name: 'Fixture monitor', enabled: false, public: false, interval_minutes: 600, seed: 20260923,
    key_source: 'existing', key_id: 13, base_url: config.siteApiBase, group_id: 21, group_name: 'Fixture Pro' }, 1, secretVault(directory).encrypt(modelKey));
  await new Promise(resolve => app.server.listen(0, '127.0.0.1', resolve));
  const base = `http://127.0.0.1:${app.server.address().port}/api/v1/model-check`;
  t.after(async () => { overrides.beforeClose?.(); await app.close(); await new Promise(resolve => backend.close(resolve)); rmSync(directory, { recursive: true, force: true }); });
  async function call(path, { method = 'GET', body, key = adminKey, bearer, headers = {} } = {}) {
    const res = await fetch(base + path, { method, headers: { ...(key === null ? {} : { 'x-api-key': key }), ...(bearer ? { Authorization: `Bearer ${bearer}` } : {}),
      ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}), ...headers }, ...(body !== undefined ? { body: JSON.stringify(body) } : {}) });
    const bytes = Buffer.from(await res.arrayBuffer());
    return { status: res.status, headers: res.headers, bytes, ...(res.headers.get('content-type')?.startsWith('application/json') ? JSON.parse(bytes.toString()) : {}) };
  }
  return { app, auth, channel, call, directory, generated };
}

test('deleting a monitor requires an administrator session and removes only its records while keeping retry tombstones', async t => {
  const { call, app, channel, generated } = await harness(t);
  const path = '/admin/channels/' + channel.id;
  const request = { method: 'POST', headers: { 'Idempotency-Key': 'fixture-deleted-monitor' }, body: { channel_id: channel.id, wait_seconds: 2 } };
  const result = await call('/admin/tests', request);
  assert.equal(result.status, 200);
  app.store.setBaseline(channel.id, result.data.id);
  const personal = app.store.createRun({ owner_id: 1, source: 'self', model: 'private-personal-test' });
  app.store.updateRun(personal.id, { status: 'normal', ...artifact });
  const other = app.store.saveChannel({ ...channel, id: undefined, name: 'Other monitor' }, 1, channel.encrypted_key);
  const otherRun = app.store.createRun({ channel_id: other.id, owner_id: 1, source: 'manual', model: channel.model });
  app.store.updateRun(otherRun.id, { status: 'normal', ...artifact });
  assert.equal((await call(path, { method: 'DELETE', key: null })).status, 401);
  assert.equal((await call(path, { method: 'DELETE' })).status, 401, 'global API key cannot delete a monitor');
  assert.equal((await call(path, { method: 'DELETE', key: null, bearer: 'fixture-user' })).status, 403);
  const deleted = await call(path, { method: 'DELETE', key: null, bearer: 'fixture-admin' });
  assert.equal(deleted.status, 200);
  assert.deepEqual(deleted.data, { deleted: true, id: channel.id });
  assert.equal(app.store.channel(channel.id), null);
  assert.equal(app.store.run(result.data.id), null);
  assert.equal(app.store.db.prepare('SELECT run_id FROM api_requests WHERE run_id=?').get(result.data.id).run_id, result.data.id);
  assert.equal(app.store.listRuns({ channel: channel.id }).length, 0);
  assert.ok(app.store.run(personal.id));
  assert.ok(app.store.run(otherRun.id));
  assert.ok(app.store.channel(other.id));
  assert.equal((await call('/admin/tests', request)).status, 404);
  assert.equal(generated.length, 1, 'retrying a deleted monitor never creates another billed run');
  assert.equal((await call(path, { method: 'DELETE', key: null, bearer: 'fixture-admin' })).status, 404);
});

test('deleting running or queued monitors is rejected without changing their jobs', async t => {
  let release; const gate = new Promise(resolve => { release = resolve; });
  const { call, app, channel } = await harness(t, { beforeClose: release, generate: async () => { await gate; return { html: artifact.html, usage: null }; } });
  const monitors = [channel, ...Array.from({ length: 2 }, (_, index) => app.store.saveChannel({ ...channel, id: undefined, name: 'Busy monitor ' + index }, 1, channel.encrypted_key))];
  for (const monitor of monitors) {
    assert.equal((await call('/admin/tests', { method: 'POST', body: { channel_id: monitor.id } })).status, 202);
  }
  assert.equal(app.store.listRuns({ channel: monitors[2].id })[0].status, 'queued');
  for (const monitor of monitors) {
    const result = await call('/admin/channels/' + monitor.id, { method: 'DELETE', key: null, bearer: 'fixture-admin' });
    assert.equal(result.status, 409);
    assert.equal(result.reason, 'already_running');
    assert.ok(app.store.channel(monitor.id));
    assert.equal(app.store.listRuns({ channel: monitor.id }).length, 1);
  }
  release();
});

test('monitor previews retain the latest finished image during failures and new jobs without exposing full artifacts', async t => {
  const { call, app, channel } = await harness(t);
  const previous = app.store.createRun({ channel_id: channel.id, owner_id: 1, model: channel.model });
  app.store.updateRun(previous.id, { status: 'review', ...artifact, finished_at: Date.now() });
  const failed = app.store.createRun({ channel_id: channel.id, owner_id: 1, model: channel.model });
  app.store.updateRun(failed.id, { status: 'failed', error: 'upstream_timeout' });
  const current = app.store.createRun({ channel_id: channel.id, owner_id: 1, model: channel.model });
  const monitor = (await call('/admin/channels')).data.find(item => item.id === channel.id);
  assert.equal(monitor.latest.id, current.id);
  assert.equal(monitor.preview.id, previous.id);
  assert.equal(monitor.preview.status, 'review');
  assert.equal(monitor.preview.created_at, previous.created_at);
  assert.equal(monitor.preview.thumbnail, 'data:image/webp;base64,' + artifact.thumbnail);
  assert.equal(monitor.preview.image, undefined);
  assert.equal(monitor.preview.html, undefined);
  assert.equal((await call('/overview', { key: null })).data.channels.length, 0, 'private previews remain private');
  app.store.updateRun(current.id, { status: 'normal', ...artifact });
  assert.equal((await call('/admin/channels')).data[0].preview.id, current.id);
});

test('Admin API Keys use the existing verifier on every call and never fall back to another identity', async t => {
  const { call, auth } = await harness(t);
  assert.equal((await call('/admin/channels', { key: null })).status, 401);
  assert.equal((await call('/admin/channels', { key: null, bearer: 'fixture-user' })).status, 403);
  assert.equal((await call('/admin/channels', { key: null, bearer: 'fixture-admin' })).status, 200);
  const invalid = await call('/admin/channels', { key: 'admin-guessed', bearer: 'fixture-admin' });
  assert.equal(invalid.status, 401);
  assert.ok(!JSON.stringify(invalid).includes('DO_NOT_RELAY_AUTH_BODY'));
  assert.equal((await call('/admin/channels')).status, 200);
  assert.equal(auth.requests.filter(req => req.path.endsWith('admin-api-key')).every(req => !req.bearer), true);
  auth.key = 'admin-fixture-rotated';
  assert.equal((await call('/admin/channels')).status, 401, 'revoked keys are not cached');
  auth.key = adminKey; auth.exists = false;
  assert.equal((await call('/admin/channels')).status, 401, 'a success envelope alone does not prove a configured key');
  auth.available = false;
  assert.equal((await call('/admin/channels')).status, 503, 'account-service failure fails closed');
});

test('submit-and-wait returns the result, TPS and image using the saved model credential', async t => {
  const { call, channel, generated, directory, app } = await harness(t);
  const result = await call('/admin/tests', { method: 'POST', body: { channel_id: channel.id, wait_seconds: 2, topic: 'creative' } });
  assert.equal(result.status, 200);
  assert.equal(result.data.completed, true);
  assert.equal(result.data.status, 'normal');
  assert.equal(result.data.assessment.score, 100);
  assert.equal(result.data.topic, 'creative');
  assert.equal(result.data.conditions.length, 8);
  assert.equal(result.data.group_name, 'Fixture Pro');
  assert.equal(result.data.tps, 5229 / (result.data.generation_ms / 1000));
  assert.equal(result.data.image, 'data:image/webp;base64,' + imageBytes.toString('base64'));
  assert.equal(result.headers.get('location'), result.data.result_url);
  assert.equal(app.store.channel(channel.id).topic, 'pelican', 'one-off overrides do not edit the saved monitor');
  assert.equal(generated[0].credential.key, modelKey);
  assert.notEqual(generated[0].credential.key, adminKey);
  assert.ok(!JSON.stringify(result.data).includes(modelKey));
  assert.ok(!JSON.stringify(result.data).includes(adminKey));
  assert.ok(!readFileSync(join(directory, 'model-check.sqlite')).includes(adminKey));
  const fetched = await call('/admin/runs/' + result.data.id);
  assert.equal(fetched.data.id, result.data.id);
  const image = await call('/admin/runs/' + result.data.id + '/image');
  assert.equal(image.status, 200);
  assert.equal(image.headers.get('content-type'), 'image/webp');
  assert.equal(image.headers.get('cache-control'), 'private, no-store');
  assert.deepEqual(image.bytes, imageBytes);
  assert.equal((await call('/admin/runs/latest?channel_id=' + channel.id)).data.id, result.data.id);
  assert.deepEqual((await call('/admin/runs/latest/image?channel_id=' + channel.id)).bytes, imageBytes);
  const list = await call('/admin/runs?channel_id=' + channel.id + '&limit=1');
  assert.equal(list.data.length, 1);
  assert.equal(list.data[0].id, result.data.id);
  assert.equal(list.data[0].html, undefined, 'listing is lightweight');
});

test('async jobs and retries use one persisted run; mismatched retry parameters are rejected', async t => {
  let release; const gate = new Promise(resolve => { release = resolve; });
  const { call, app, channel, generated } = await harness(t, { beforeClose: release, generate: async () => { await gate; return { html: artifact.html, usage: null }; } });
  const request = { method: 'POST', headers: { 'Idempotency-Key': 'fixture-request-001' }, body: { channel_id: channel.id } };
  const first = await call('/admin/tests', request);
  assert.equal(first.status, 202);
  assert.equal(first.headers.get('retry-after'), '2');
  assert.equal(first.data.completed, false);
  assert.equal(first.data.image_url, null);
  const repeats = await Promise.all([call('/admin/tests', request), call('/admin/tests', request)]);
  assert.ok(repeats.every(result => result.data.id === first.data.id));
  assert.equal(generated.length, 1);
  assert.equal(app.store.listRuns().length, 1);
  assert.equal((await call('/admin/tests', { ...request, body: { channel_id: channel.id, topic: 'creative' } })).status, 409);
  assert.equal((await call('/admin/tests', { method: 'POST', body: { channel_id: channel.id } })).status, 409, 'another request cannot overlap the same monitor');
  assert.equal((await call('/admin/runs/' + first.data.id + '/image')).status, 409);
  assert.equal((await call('/admin/runs/' + first.data.id)).data.completed, false);
  release();
  const retried = await call('/admin/tests', { ...request, body: { channel_id: channel.id, wait_seconds: 2 } });
  assert.equal(retried.status, 200);
  assert.equal(retried.data.id, first.data.id);
  assert.equal(generated.length, 1, 'waiting or retrying never generates another billed request');
});

test('truncated retries preserve idempotency and report effective limits without changing requested settings', async t => {
  let release; const gate = new Promise(resolve => { release = resolve; });
  let count = 0;
  const { call, app, channel, generated } = await harness(t, {
    beforeClose: release, retryWait: async () => { await gate; },
    generate: async () => {
      if (++count === 1) {
        const error = new CheckError('truncated_output', 502);
        error.usage = { input_tokens: 25, output_tokens: 8000, private_message: 'must-not-persist' };
        throw error;
      }
      return { html: artifact.html, usage: { output_tokens: 20 } };
    },
  });
  const request = { method: 'POST', headers: { 'Idempotency-Key': 'token-retry-same-run' }, body: { channel_id: channel.id } };
  const submitted = await call('/admin/tests', request);
  const waiting = (await call('/admin/runs/' + submitted.data.id)).data;
  assert.equal(waiting.status, 'generating');
  assert.equal(waiting.max_tokens, 8000);
  assert.equal(waiting.effective_max_tokens, 8000);
  assert.equal(waiting.retry_max_tokens, 32768);
  assert.equal(waiting.last_attempt_error, 'truncated_output');
  assert.equal((await call('/admin/tests', request)).data.id, submitted.data.id);
  assert.equal(generated.length, 1);
  release();
  const done = (await call('/admin/tests', { ...request, body: { channel_id: channel.id, wait_seconds: 2 } })).data;
  assert.equal(done.id, submitted.data.id);
  assert.equal(done.status, 'normal');
  assert.equal(done.max_tokens, 8000);
  assert.equal(done.effective_max_tokens, 32768);
  assert.equal(done.retry_max_tokens, null);
  assert.deepEqual(done.attempts.map(attempt => attempt.max_tokens), [8000, 32768]);
  assert.deepEqual(done.attempts[0].usage, { input_tokens: 25, output_tokens: 8000, cached_tokens: null });
  assert.ok(!JSON.stringify(done).includes('must-not-persist'));
  assert.equal(app.store.channel(channel.id).max_tokens, 8000);
  assert.equal(app.store.listRuns().length, 1);
  assert.equal(generated[0].run.id, generated[1].run.id);
  assert.equal(generated[0].run.prompt, generated[1].run.prompt);
  assert.equal(generated[0].run.seed, generated[1].run.seed);
  assert.equal(generated[0].credential, generated[1].credential);
  const listed = (await call('/admin/runs?channel_id=' + channel.id)).data[0];
  assert.equal(listed.effective_max_tokens, 32768);
  assert.deepEqual(listed.attempts.map(attempt => attempt.max_tokens), [8000, 32768]);
  const baseline = await call(`/admin/channels/${channel.id}/baseline`, { method: 'POST', key: null, bearer: 'fixture-admin', body: { run_id: done.id } });
  assert.equal(baseline.status, 400, 'a 32768-token result is not a matching baseline for an 8000-token monitor');
  assert.equal(baseline.reason, 'invalid_baseline');
});

test('baseline comparison uses effective attempt limits, including legacy fallback', async t => {
  for (const baselineCap of [8000, 16000, 32768]) {
    await t.test('baseline ' + baselineCap, async sub => {
      let failNext = false;
      const { call, app, channel } = await harness(sub, { generate: async () => {
        if (failNext) { failNext = false; throw new CheckError('truncated_output', 502); }
        return { html: artifact.html, usage: null };
      } });
      const first = (await call('/admin/tests', { method: 'POST', body: { channel_id: channel.id, wait_seconds: 2 } })).data;
      app.store.updateRun(first.id, { effective_max_tokens: baselineCap === 8000 ? null : baselineCap });
      app.store.setBaseline(channel.id, first.id);
      failNext = true;
      const current = (await call('/admin/tests', { method: 'POST', body: { channel_id: channel.id, wait_seconds: 2 } })).data;
      assert.equal(current.effective_max_tokens, 32768);
      assert.equal(current.assessment.delta, baselineCap === 32768 ? 0 : null);
    });
  }
});

test('bounded waits leave the accepted job available for polling', async t => {
  let release; const gate = new Promise(resolve => { release = resolve; });
  const { call, channel } = await harness(t, { beforeClose: release, generate: async () => { await gate; return { html: artifact.html, usage: null }; } });
  const submitted = await call('/admin/tests', { method: 'POST', body: { channel_id: channel.id, wait_seconds: 1 } });
  assert.equal(submitted.status, 202);
  assert.equal(submitted.data.completed, false);
  assert.equal((await call('/admin/runs/' + submitted.data.id)).status, 200);
  release();
});

test('temporary upstream failures retry one idempotent run and keep successful-attempt timing separate', async t => {
  let release; const gate = new Promise(resolve => { release = resolve; });
  let clock = 1000; let count = 0;
  const waits = [];
  const { call, app, channel, generated } = await harness(t, {
    beforeClose: release, retryNow: () => clock,
    retryWait: async ms => { waits.push(ms); clock += ms; await gate; },
    generate: async () => {
      count++; clock += count === 1 ? 150 : 250;
      if (count === 1) throw new CheckError('upstream_http:502', 502);
      return { html: artifact.html, usage: { input_tokens: 10, output_tokens: 100 } };
    },
  });
  const request = { method: 'POST', headers: { 'Idempotency-Key': 'retry-same-run' }, body: { channel_id: channel.id } };
  const first = await call('/admin/tests', request);
  const waiting = (await call('/admin/runs/' + first.data.id)).data;
  assert.equal(waiting.status, 'generating');
  assert.equal(waiting.attempt_count, 1);
  assert.equal(waiting.max_attempts, 3);
  assert.equal(waiting.retry_at, 3150);
  assert.equal(waiting.last_attempt_error, 'upstream_http:502');
  assert.equal(waiting.attempts[0].duration_ms, 150);
  assert.equal((await call('/admin/tests', request)).data.id, first.data.id);
  assert.equal(generated.length, 1);
  assert.equal((await call('/admin/tests', { method: 'POST', body: { channel_id: channel.id } })).status, 409);
  release();
  const completed = await call('/admin/tests', { ...request, body: { channel_id: channel.id, wait_seconds: 2 } });
  assert.equal(completed.status, 200);
  assert.equal(completed.data.status, 'normal');
  assert.equal(completed.data.attempt_count, 2);
  assert.equal(completed.data.retry_at, null);
  assert.equal(completed.data.generation_ms, 250);
  assert.equal(completed.data.tps, 400);
  assert.equal(completed.data.usage_scope, 'successful_attempt');
  assert.equal(completed.data.attempts[0].usage, null);
  assert.equal(completed.data.attempts[1].usage.output_tokens, 100);
  assert.deepEqual(waits, [2000]);
  assert.equal(app.store.listRuns().length, 1);
  assert.equal(generated[0].run, generated[1].run, 'prompt and sampled seed stay on the same run object');
  assert.equal(generated[0].credential, generated[1].credential, 'model key remains in memory for this job');
  assert.ok(!JSON.stringify(app.store.listRuns()).includes(modelKey));
});

test('pruning an image preserves the idempotency tombstone and never silently recharges a retry', async t => {
  const { call, app, channel, generated } = await harness(t);
  const request = { method: 'POST', headers: { 'Idempotency-Key': 'fixture-pruned-retry' }, body: { channel_id: channel.id, wait_seconds: 2 } };
  const first = await call('/admin/tests', request);
  assert.equal(first.status, 200);
  for (let i = 0; i < 30; i++) {
    const extra = app.store.createRun({ channel_id: channel.id, source: 'manual', owner_id: 1, ...validateOptions(channel) });
    app.store.updateRun(extra.id, { status: 'normal' });
  }
  app.store.prune();
  assert.equal(app.store.run(first.data.id), null);
  const retry = await call('/admin/tests', request);
  assert.equal(retry.status, 410);
  assert.equal(retry.reason, 'result_expired');
  assert.equal(generated.length, 1);
  app.store.db.prepare('UPDATE api_requests SET created_at=?').run(Date.now() - 25 * 60 * 60 * 1000);
  const expired = await call('/admin/tests', request);
  assert.equal(expired.status, 200, 'idempotency keys may be reused after the documented 24-hour window');
  assert.notEqual(expired.data.id, first.data.id);
  assert.equal(generated.length, 2);
});

test('total duration includes retry waits while rendering failures and quality review never regenerate', async t => {
  await t.test('total duration includes the backoff', async sub => {
    let count = 0;
    const { call, channel } = await harness(sub, { retryWait: async () => { await new Promise(resolve => setTimeout(resolve, 30)); },
      generate: async () => { if (++count === 1) throw new CheckError('connection_failed', 502); return { html: artifact.html, usage: null }; } });
    const result = await call('/admin/tests', { method: 'POST', body: { channel_id: channel.id, wait_seconds: 2 } });
    assert.equal(result.data.attempt_count, 2);
    assert.ok(result.data.total_ms >= 25 + result.data.generation_ms);
  });
  await t.test('render failures', async sub => {
    const { call, channel, generated } = await harness(sub, { render: async () => { throw new CheckError('upstream_http:502', 502); } });
    const result = await call('/admin/tests', { method: 'POST', body: { channel_id: channel.id, wait_seconds: 2 } });
    assert.equal(result.data.status, 'failed');
    assert.equal(generated.length, 1);
  });
  await t.test('quality review', async sub => {
    const { call, channel, generated } = await harness(sub, { render: async () => ({ ...artifact, metrics: { ...artifact.metrics, motion_ratio: 0 } }) });
    const result = await call('/admin/tests', { method: 'POST', body: { channel_id: channel.id, wait_seconds: 2 } });
    assert.equal(result.data.status, 'review');
    assert.equal(generated.length, 1);
  });
});

test('private monitoring images require authentication and personal privacy is preserved', async t => {
  const { call, app, channel } = await harness(t);
  const result = await call('/admin/tests', { method: 'POST', body: { channel_id: channel.id, wait_seconds: 2 } });
  const id = result.data.id;
  assert.equal((await call('/admin/runs/' + id + '/image', { key: null })).status, 401);
  assert.equal((await call('/admin/runs/' + id + '/image', { key: null, bearer: 'fixture-user' })).status, 403);
  assert.equal((await call('/overview', { key: null })).data.works.length, 0);
  const personal = app.store.createRun({ owner_id: 2, source: 'self', ...validateOptions({ model: 'private-self-test' }) });
  app.store.updateRun(personal.id, { ...artifact, status: 'normal' });
  assert.equal((await call('/admin/runs/' + personal.id)).status, 404);
  assert.equal((await call('/admin/runs/' + personal.id + '/image')).status, 404);
  assert.ok(!(await call('/admin/runs')).data.some(run => run.id === personal.id));
});

test('invalid options do not create jobs; failed tests return explicit errors and no substitute image', async t => {
  const { call, channel, generated, app } = await harness(t, { generate: async () => { throw new CheckError('upstream_limit:429', 502); } });
  const payloads = [null, [], {}, { channel_id: channel.id, topic: 'custom' }, { channel_id: channel.id, prompt: 'override' },
    { channel_id: channel.id, wait_seconds: -1 }, { channel_id: channel.id, wait_seconds: 121 }, { channel_id: channel.id, max_tokens: 10 }];
  for (const body of payloads) assert.equal((await call('/admin/tests', { method: 'POST', body })).status, 400);
  assert.equal((await call('/admin/tests', { method: 'POST', body: { channel_id: channel.id }, headers: { 'Idempotency-Key': 'invalid key' } })).status, 400);
  assert.equal(app.store.listRuns().length, 0);
  assert.equal(generated.length, 0);
  const failure = await call('/admin/tests', { method: 'POST', body: { channel_id: channel.id, wait_seconds: 2 } });
  assert.equal(failure.status, 200);
  assert.equal(failure.data.status, 'failed');
  assert.equal(failure.data.completed, true);
  assert.equal(failure.data.error, 'upstream_limit:429');
  assert.equal(failure.data.image, null);
  assert.equal(failure.data.image_url, null);
  assert.equal(failure.data.assessment, undefined);
  assert.equal((await call('/admin/runs/' + failure.data.id + '/image')).status, 404);
  assert.equal((await call('/admin/runs/latest?channel_id=' + channel.id)).data.status, 'failed');
  assert.equal((await call('/admin/runs?limit=999')).status, 400);
});

test('TPS uses only valid generation duration and output usage, including historical records', () => {
  const run = { source: 'manual', generation_ms: 112200, total_ms: 115400, usage: { output_tokens: 5229 } };
  assert.equal(publicRun(run).tps, 5229 / 112.2);
  assert.equal(publicRun({ ...run, usage: { output_tokens: 0 } }).tps, 0);
  for (const changed of [{ source: 'demo' }, { generation_ms: null }, { generation_ms: 0 }, { generation_ms: -1 }, { generation_ms: Infinity },
    { usage: null }, { usage: { output_tokens: -1 } }, { usage: { output_tokens: '5229' } }, { usage: { output_tokens: NaN } }]) {
    assert.equal(publicRun({ ...run, ...changed }).tps, null);
  }
});

test('the API returns a real rendered WebP result without using a paid model', async t => {
  const { call, channel } = await harness(t, { render: renderArtwork });
  const result = await call('/admin/tests', { method: 'POST', body: { channel_id: channel.id, wait_seconds: 15 } });
  assert.equal(result.status, 200);
  assert.equal(result.data.status, 'normal');
  assert.ok(result.data.image.startsWith('data:image/webp;base64,'));
  const downloaded = await call('/admin/runs/' + result.data.id + '/image');
  const metadata = await sharp(downloaded.bytes).metadata();
  assert.equal(metadata.format, 'webp');
  assert.equal(metadata.width, 960);
  assert.equal(metadata.height, 600);
  assert.equal(result.data.assessment.method, 'render-rules-v1');
});
