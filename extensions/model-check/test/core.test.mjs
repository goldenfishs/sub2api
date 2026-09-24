import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync, readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import http from 'node:http';
import { publicAddress, resolveEndpoint, requestJSON, secretVault, normalizeBase } from '../src/security.mjs';
import { makePrompt, makeScheduledPrompt, validateOptions, TOPICS } from '../src/prompts.mjs';
import { sanitizeArtwork } from '../src/sanitize.mjs';
import { assess, renderArtwork } from '../src/render.mjs';
import { sampleHTML } from '../src/samples.mjs';
import { Store, publicRun } from '../src/store.mjs';

test('monitor deletion rolls back its records and encrypted key if the transaction fails', () => {
  const store = new Store(':memory:');
  try {
    const channel = store.saveChannel({ name: 'Atomic deletion', interval_minutes: 60 }, 1, 'fixture-encrypted-key');
    const run = store.createRun({ channel_id: channel.id, owner_id: 1 });
    store.updateRun(run.id, { status: 'normal', image: 'fixture-image' });
    store.setBaseline(channel.id, run.id);
    store.db.exec("CREATE TRIGGER prevent_channel_delete BEFORE DELETE ON channels BEGIN SELECT RAISE(ABORT, 'fixture_delete_failure'); END;");
    assert.throws(() => store.deleteChannel(channel.id), /fixture_delete_failure/);
    assert.equal(store.channel(channel.id).encrypted_key, 'fixture-encrypted-key');
    assert.equal(store.channel(channel.id).baseline_id, run.id);
    assert.equal(store.run(run.id).image, 'fixture-image');
  } finally { store.close(); }
});

test('external endpoints block private, mapped, reserved and mixed DNS answers', async () => {
  for (const ip of ['127.0.0.1', '10.2.3.4', '172.16.4.7', '192.168.1.1', '100.64.0.1', '169.254.169.254', '0.0.0.0', '198.18.1.2', '::1', '::ffff:127.0.0.1', 'fd00::1', 'fe80::1', '2001:db8::1']) assert.equal(publicAddress(ip), false, ip);
  assert.equal(publicAddress('8.8.8.8'), true);
  assert.equal(publicAddress('2606.128'), false);
  await assert.rejects(resolveEndpoint('http://example.com', 'responses'), /https_required/);
  await assert.rejects(resolveEndpoint('https://example.com:8080', 'responses'), /https_port_required/);
  await assert.rejects(resolveEndpoint('https://example.com', 'responses', '', async () => [{ address: '8.9.6.2', family: 4 }, { address: '127.0.0.1', family: 4 }]), /private_address/);
  const pinned = await resolveEndpoint('https://example.com/v1', 'responses', '', async () => [{ address: '8.9.6.2', family: 4 }]);
  assert.equal(pinned.address.address, '8.9.6.2');
  assert.equal(pinned.url.href, 'https://example.com/v1/responses');
  assert.equal(normalizeBase('https://example.com/v1/responses'), 'https://example.com/v1');
  await assert.rejects(resolveEndpoint('https://user:secret@example.com', 'responses'), /invalid_base_url/);
});

test('HTTP redirects are refused and error bodies containing a secret are never returned', async t => {
  let hits = 0;
  const server = http.createServer((_req, res) => { hits++; res.writeHead(302, { Location: '/private' }); res.end('fixture-secret'); });
  await new Promise(r => server.listen(0, '127.0.0.1', r));
  t.after(() => new Promise(r => server.close(r)));
  const base = `http://127.0.0.1:${server.address().port}/v1`;
  const endpoint = await resolveEndpoint(base, 'responses', base);
  await assert.rejects(requestJSON(endpoint, { key: 'fixture-secret', body: {} }), error => error.message === 'redirect_refused:302');
  assert.equal(hits, 1);
});

test('saved scheduler credentials are authenticated ciphertext; self-test metadata excludes secrets', () => {
  const directory = mkdtempSync(join(tmpdir(), 'model-check-vault-'));
  try {
    const vault = secretVault(directory); const token = 'only-a-test-key'; const encrypted = vault.encrypt(token);
    assert.ok(!encrypted.includes(token)); assert.equal(vault.decrypt(encrypted), token);
    assert.equal(secretVault(directory).decrypt(encrypted), token);
    const tampered = Buffer.from(encrypted, 'base64'); tampered[20] ^= 1;
    assert.throws(() => vault.decrypt(tampered.toString('base64')));
    assert.equal(readFileSync(join(directory, 'encryption.key')).length, 32);
    assert.equal('owner_id' in publicRun({ owner_id: 12, status: 'failed', key: token, base_url: 'https://private.example' }), false);
    assert.ok(!JSON.stringify(publicRun({ owner_id: 12, status: 'failed', key: token, base_url: 'https://private.example' })).includes(token));
  } finally { rmSync(directory, { recursive: true, force: true }); }
});

test('fixed prompts are reproducible, creative conditions are saved, and baseline drops require a match', () => {
  assert.equal(makePrompt('creative', 42).prompt_hash, makePrompt('creative', 42).prompt_hash);
  assert.notEqual(makePrompt('creative', 42).prompt_hash, makePrompt('creative', 43).prompt_hash);
  assert.equal(makePrompt('creative', 42).conditions.length, 8);
  const metrics = { svg_count: 1, visible_shapes: 20, contrast: 30, occupied_ratio: .4, motion_ratio: 0, overflow_mobile: false, overflow_desktop: false };
  const result = assess(metrics, { score: 100 }, true);
  assert.equal(result.verdict, 'review'); assert.equal(result.delta, -25);
  assert.ok(result.reasons.includes('baseline_drop'));
  assert.equal(assess(metrics, { score: 100 }, false).delta, null);
});

test('only two prompt modes are accepted; random schedules resample while pelican schedules stay fixed', () => {
  assert.deepEqual(TOPICS.map(topic => topic.id), ['pelican', 'creative']);
  for (const topic of ['garden', 'kinetic', 'custom']) {
    assert.throws(() => validateOptions({ model: 'fixture-model', topic }), /invalid_topic/);
    assert.throws(() => makePrompt(topic), /invalid_topic/);
  }
  const fixed = { topic: 'pelican', seed: 20260923 };
  assert.equal(makeScheduledPrompt(fixed, 42).prompt_hash, makeScheduledPrompt(fixed, 43).prompt_hash);
  const random = { topic: 'creative', seed: 20260923 };
  const first = makeScheduledPrompt(random, 42), second = makeScheduledPrompt(random, 43);
  assert.notEqual(first.prompt_hash, second.prompt_hash);
  assert.equal(first.seed, 42);
  assert.equal(makePrompt('creative', first.seed).prompt, first.prompt, 'saved conditions reproduce the submitted prompt');
  assert.equal(first.conditions.length, 8);
  for (const value of first.conditions) assert.ok(first.prompt.includes(value));
});

test('HTML preview removes scripts, embedded documents, resource links and unsafe animation targets', () => {
  const result = sanitizeArtwork(`<html><head><meta http-equiv="refresh" content="0;url=https://evil.test"><script>fetch('secret')</script></head><body onload="steal()"><iframe src="https://evil.test"></iframe><svg><a href="javascript:alert(1)"><circle r="9"/></a><animate attributeName="href" to="https://evil.test"/><use href="https://evil.test/a.svg"/></svg></body></html>`);
  assert.ok(!/<script|<iframe|onload=|javascript:|attributeName="href"|href="https:/i.test(result.html));
  assert.ok(result.html.includes("script-src 'none'"));
  assert.ok(result.html.includes("connect-src 'none'"));
  assert.equal(result.removed_active_content, true);
  assert.throws(() => sanitizeArtwork('a'.repeat(500_001)), /html_too_large/);
});

test('private record authorization, restart recovery and per-owner retention', () => {
  const store = new Store(':memory:');
  try {
    const run = store.createRun({ owner_id: 2, source: 'self', model: 'fixture-model' });
    assert.equal(store.canRead(run, null), false); assert.equal(store.canRead(run, { id: 3, role: 'user' }), false);
    assert.equal(store.canRead(run, { id: 2, role: 'user' }), true); assert.equal(store.canRead(run, { id: 1, role: 'admin' }), false);
    store.updateRun(run.id, { status: 'failed' });
    for (let i = 0; i < 35; i++) { const item = store.createRun({ owner_id: 3 }); store.updateRun(item.id, { status: 'failed' }); }
    store.prune(); assert.equal(store.listRuns({ owner: 3 }).length, 30); assert.equal(store.listRuns({ owner: 2 }).length, 1);
  } finally { store.close(); }
  const directory = mkdtempSync(join(tmpdir(), 'model-check-restart-'));
  try {
    const file = join(directory, 'test.sqlite'); const first = new Store(file); const pending = first.createRun({ owner_id: 2 }); first.close();
    const second = new Store(file); assert.equal(second.run(pending.id).error, 'service_restarted'); assert.equal(second.run(pending.id).status, 'failed'); second.close();
  } finally { rmSync(directory, { recursive: true, force: true }); }
});

test('real isolated renderer observes animated frames and flags a static placeholder', { timeout: 60_000 }, async () => {
  const animated = await renderArtwork(sampleHTML('pelican'));
  const staticFrame = await renderArtwork(sampleHTML('pelican', true));
  assert.ok(animated.image.length > 1000);
  assert.ok(animated.metrics.motion_ratio >= .001, JSON.stringify(animated.metrics));
  assert.ok(assess(staticFrame.metrics).reasons.includes('motion_not_observed'));
  assert.equal(assess(animated.metrics).verdict, 'normal');
});
