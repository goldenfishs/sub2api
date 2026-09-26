import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { mkdtempSync, rmSync, readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createService, generateRun } from '../src/server.mjs';
import { CheckError, secretVault } from '../src/security.mjs';
import { makePrompt } from '../src/prompts.mjs';

const artifact = { html: '<html><svg></svg></html>', image: '', thumbnail: '', metrics: { svg_count: 1, visible_shapes: 20, contrast: 35, occupied_ratio: .5, motion_ratio: .1, overflow_mobile: false, overflow_desktop: false } };
const platformKey = 'fixture-platform-key-do-not-persist';
async function harness(t, overrides = {}) {
  const directory = mkdtempSync(join(tmpdir(), 'model-check-api-'));
  let group = { id: 21, name: 'Codex Plus' };
  const backend = http.createServer((req, res) => {
    const id = req.headers.authorization === 'Bearer fixture-admin' ? 1 : req.headers.authorization === 'Bearer fixture-user' ? 2 : req.headers.authorization === 'Bearer fixture-other' ? 3 : 0;
    res.setHeader('Content-Type', 'application/json');
    if (!id) { res.writeHead(401); res.end('{}'); return; }
    if (req.url === '/api/v1/auth/me') { res.end(JSON.stringify({ code: 0, data: { id, role: id === 1 ? 'admin' : 'user' } })); return; }
    const key = { id: 13, user_id: id, key: platformKey, name: 'Fixture key', status: 'active', group_id: group?.id ?? null, group };
    res.end(JSON.stringify({ code: 0, data: req.url.startsWith('/api/v1/keys?') ? { items: [key] } : key }));
  });
  await new Promise(r => backend.listen(0, '127.0.0.1', r));
  const config = { dataDir: directory, backend: `http://127.0.0.1:${backend.address().port}`, siteApiBase: `http://127.0.0.1:${backend.address().port}/v1`, demo: false };
  const app = createService(config, { retryWait: async () => {}, render: async () => artifact, generate: async () => ({ html: '<html><svg></svg></html>', usage: { input_tokens: 2, output_tokens: 3 } }), ...overrides });
  await new Promise(r => app.server.listen(0, '127.0.0.1', r));
  t.after(async () => { await app.close(); await new Promise(r => backend.close(r)); rmSync(directory, { recursive: true, force: true }); });
  const base = `http://127.0.0.1:${app.server.address().port}/api/v1/model-check`;
  async function call(path, token, method = 'GET', body) {
    const res = await fetch(base + path, { method, headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer fixture-${token}` } : {}) }, ...(body ? { body: JSON.stringify(body) } : {}) });
    return { status: res.status, ...(await res.json()) };
  }
  async function finished(id) {
    const until = Date.now() + 5000;
    while (Date.now() < until) {
      const run = app.store.run(id);
      if (['normal', 'review', 'failed'].includes(run.status)) return run;
      await new Promise(r => setTimeout(r, 10));
    }
    throw new Error('Job did not finish');
  }
  return { app, call, finished, directory, setGroup: value => { group = value; } };
}
const options = { key_source: 'existing', key_id: 13, model: 'fixture-model', topic: 'pelican', protocol: 'responses', reasoning: 'default', max_tokens: 8000 };
const channelConfig = { ...options, name: 'Fixture monitor', interval_minutes: 5, enabled: false, public: false };

test('self-tests validate account ownership, remain private and never persist supplied keys', async t => {
  let passedKey = '';
  const { app, call, finished, directory } = await harness(t, { generate: async (_run, credential) => { passedKey = credential.key; return { html: '<html></html>', usage: null }; } });
  assert.equal((await call('/tests', null, 'POST', options)).status, 401);
  assert.equal((await call('/admin/channels', 'user')).status, 403);
  const keys = await call('/keys', 'user'); assert.equal(keys.status, 200); assert.ok(!JSON.stringify(keys).includes(platformKey));
  const response = await call('/tests', 'user', 'POST', options); assert.equal(response.status, 202);
  const run = await finished(response.data.id); assert.equal(run.status, 'normal'); assert.equal(passedKey, platformKey);
  assert.equal((await call('/runs/' + run.id, 'other')).status, 404);
  assert.equal((await call('/runs/' + run.id, null)).status, 401);
  assert.equal((await call('/runs/' + run.id, 'user')).status, 200);
  assert.equal((await call('/overview')).data.works.length, 0);
  assert.equal((await call('/mine', 'user')).data.length, 1);
  const dump = JSON.stringify(app.store.listRuns()); assert.ok(!dump.includes(platformKey));
  assert.ok(!readFileSync(join(directory, 'model-check.sqlite')).includes(platformKey));
});

test('admin schedules persist encrypted keys, public visibility is explicit, and baselines are validated', async t => {
  const { app, call, finished } = await harness(t);
  const saved = await call('/admin/channels', 'admin', 'POST', channelConfig);
  assert.equal(saved.status, 200); assert.equal(saved.data.has_key, true);
  assert.ok(!JSON.stringify(saved).includes(platformKey)); assert.ok(!JSON.stringify(app.store.channels()).includes(platformKey));
  const id = saved.data.id;
  const manual = await call(`/admin/channels/${id}/run`, 'admin', 'POST', {});
  const run = await finished(manual.data.id);
  assert.equal((await call('/overview')).data.works.length, 0);
  assert.equal((await call(`/admin/channels/${id}/baseline`, 'admin', 'POST', { run_id: run.id })).status, 200);
  await call(`/admin/channels/${id}`, 'admin', 'PUT', { ...channelConfig, public: true });
  assert.equal(app.store.channel(id).baseline_id, run.id, 'visibility edits retain matching baseline');
  const overview = await call('/overview'); assert.equal(overview.data.works.length, 1); assert.equal(overview.data.channels.length, 1);
  assert.ok(!JSON.stringify(overview).includes('base_url')); assert.ok(!JSON.stringify(overview).includes('owner_id'));
  assert.equal((await call('/runs/' + run.id)).status, 200);
  await call(`/admin/channels/${id}`, 'admin', 'PUT', { ...channelConfig, topic: 'creative' });
  assert.equal(app.store.channel(id).baseline_id, null);
  assert.equal((await call(`/admin/channels/${id}/baseline`, 'admin', 'POST', { run_id: run.id })).status, 400);
  assert.equal((await call('/runs/' + run.id, 'user')).status, 404);
});

test('self-tests and admin schedules enforce the two server-owned prompt modes', async t => {
  const submitted = [];
  const { app, call, finished } = await harness(t, { generate: async run => { submitted.push(run); return { html: '<html></html>', usage: null }; } });
  for (const topic of ['garden', 'kinetic', 'custom']) {
    assert.equal((await call('/tests', 'user', 'POST', { ...options, topic })).status, 400);
    assert.equal((await call('/admin/channels', 'admin', 'POST', { ...channelConfig, topic })).status, 400);
  }
  assert.equal(app.store.channels().length, 0);
  assert.equal(submitted.length, 0);
  for (const topic of ['pelican', 'creative']) {
    const result = await call('/tests', 'user', 'POST', { ...options, topic, prompt: 'replace the server prompt', conditions: ['arbitrary'], seed: 0 });
    assert.equal(result.status, 202);
    const run = await finished(result.data.id);
    assert.equal(run.status, 'normal');
    assert.equal(run.prompt, makePrompt(topic, run.seed).prompt);
    assert.ok(!run.prompt.includes('replace the server prompt'));
  }
  assert.equal(submitted.length, 2);
});

test('random scheduled works keep their sampled brief and appear publicly with the configured model and name', async t => {
  const { app, call, finished } = await harness(t);
  const saved = await call('/admin/channels', 'admin', 'POST', { ...channelConfig, name: 'Public random monitor', model: 'fixture-random-model', topic: 'creative', enabled: true, public: true });
  assert.equal(saved.status, 200);
  const channel = app.store.channel(saved.data.id);
  app.tick(channel.next_run + 1);
  const run = await finished(app.store.listRuns({ channel: channel.id })[0].id);
  assert.equal(run.source, 'scheduled');
  assert.notEqual(run.seed, channel.seed, 'random runs do not reuse the fixed monitor seed');
  assert.equal(run.prompt, makePrompt('creative', run.seed).prompt);
  assert.equal(run.conditions.length, 8);
  assert.equal((await call(`/admin/channels/${channel.id}/baseline`, 'admin', 'POST', { run_id: run.id })).status, 200);
  const publicWorks = (await call('/overview')).data.works;
  assert.equal(publicWorks.length, 1);
  assert.equal(publicWorks[0].label, 'Public random monitor');
  assert.equal(publicWorks[0].model, 'fixture-random-model');
  assert.ok(!JSON.stringify(publicWorks).includes(platformKey));
});

test('schedule ticks do not overlap jobs, and an upstream failure is not called poor visual performance', async t => {
  let release; let calls = 0;
  const gate = new Promise(resolve => { release = resolve; });
  const { app, call, finished } = await harness(t, { generate: async () => { calls++; await gate; throw new CheckError('upstream_limit:429', 502); } });
  const saved = await call('/admin/channels', 'admin', 'POST', { ...channelConfig, enabled: true });
  const channel = app.store.channel(saved.data.id);
  app.tick(channel.next_run + 1); app.tick(channel.next_run + 2);
  const run = app.store.listRuns({ channel: channel.id })[0];
  assert.equal(calls, 1); assert.equal((await call(`/admin/channels/${channel.id}/run`, 'admin', 'POST', {})).status, 409);
  release(); const result = await finished(run.id);
  assert.equal(result.status, 'failed'); assert.equal(result.assessment, undefined); assert.equal(result.error, 'upstream_limit:429');
  assert.equal(calls, 3, 'temporary rate limits receive at most two retries within the same job');
});

test('the actual adapter supports Responses and Chat Completions with no paid calls', async t => {
  const seen = [];
  const upstream = http.createServer(async (req, res) => {
    const chunks = []; for await (const chunk of req) chunks.push(chunk);
    seen.push({ path: req.url, body: JSON.parse(Buffer.concat(chunks)), authorization: req.headers.authorization });
    res.setHeader('Content-Type', 'application/json');
    const html = '<html><svg></svg></html>';
    res.end(JSON.stringify(req.url.endsWith('/responses') ? { output: [{ content: [{ type: 'output_text', text: html }] }], usage: { input_tokens: 5, output_tokens: 6 } } : { choices: [{ message: { content: html } }], usage: { prompt_tokens: 7, completion_tokens: 8 } }));
  });
  await new Promise(r => upstream.listen(0, '127.0.0.1', r));
  t.after(() => new Promise(r => upstream.close(r)));
  const base = `http://127.0.0.1:${upstream.address().port}/v1`;
  const run = { ...options, prompt: 'test scene' };
  const a = await generateRun(run, { key: 'fixture', base_url: base, trusted: true }, base);
  const b = await generateRun({ ...run, protocol: 'chat', reasoning: 'medium' }, { key: 'fixture', base_url: base, trusted: true }, base);
  assert.ok(a.html.includes('<svg>')); assert.equal(a.usage.output_tokens, 6); assert.equal(b.usage.output_tokens, 8);
  assert.equal(seen[0].body.stream, true); assert.equal(seen[0].body.store, false); assert.equal(seen[0].body.max_output_tokens, 8000);
  assert.equal(seen[1].body.reasoning_effort, 'medium'); assert.equal(seen[1].path, '/v1/chat/completions');
});

test('Responses JSON incomplete reasons distinguish token limits from content filtering', async t => {
  let reason;
  const upstream = http.createServer((_req, res) => {
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify({ status: 'incomplete', incomplete_details: reason !== undefined ? { reason } : undefined }));
  });
  await new Promise(resolve => upstream.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise(resolve => upstream.close(resolve)));
  const base = `http://127.0.0.1:${upstream.address().port}/v1`;
  for (const [incompleteReason, expectedError] of [['max_output_tokens', 'truncated_output'], [undefined, 'truncated_output'], ['content_filter', 'upstream_error'], ['other_reason', 'upstream_error'], ['', 'upstream_error'], [0, 'upstream_error'], [false, 'upstream_error']]) {
    reason = incompleteReason;
    await assert.rejects(generateRun({ ...options, prompt: 'fixture' }, { key: 'fixture', base_url: base, trusted: true }, base), error => error.code === expectedError);
  }
});

test('group names come from the owned key and remain attached to each historical work', async t => {
  const { app, call, finished, setGroup } = await harness(t);
  const forged = { group_id: 999, group_name: 'Forged group' };
  const self = await call('/tests', 'user', 'POST', { ...options, ...forged });
  const selfRun = await finished(self.data.id);
  assert.equal(selfRun.group_id, 21);
  assert.equal(selfRun.group_name, 'Codex Plus');
  const saved = await call('/admin/channels', 'admin', 'POST', { ...channelConfig, public: true, ...forged });
  assert.equal(saved.data.group_id, 21);
  const id = saved.data.id;
  const first = await call('/admin/channels/' + id + '/run', 'admin', 'POST', {});
  const old = await finished(first.data.id);
  assert.equal((await call('/admin/channels/' + id + '/baseline', 'admin', 'POST', { run_id: old.id })).status, 200);
  setGroup({ id: 22, name: 'Codex Ultra' });
  const updated = await call('/admin/channels/' + id, 'admin', 'PUT', { ...channelConfig, public: true });
  assert.equal(updated.data.group_name, 'Codex Ultra');
  assert.equal(app.store.channel(id).baseline_id, null, 'a different group invalidates the old baseline');
  assert.equal((await call('/admin/channels/' + id + '/baseline', 'admin', 'POST', { run_id: old.id })).status, 400);
  const second = await call('/admin/channels/' + id + '/run', 'admin', 'POST', {});
  const current = await finished(second.data.id);
  const works = (await call('/overview')).data.works;
  assert.equal(works.find(run => run.id === old.id).group_name, 'Codex Plus');
  assert.equal(works.find(run => run.id === current.id).group_name, 'Codex Ultra');
  assert.equal((await call('/runs/' + old.id)).data.group_name, 'Codex Plus');
  const currentStats = (await call('/overview')).data.channels.find(channel => channel.id === id).statistics;
  assert.equal(currentStats.total, 1, 'the new group does not inherit results from the previous group');
  assert.ok(!JSON.stringify(works).includes(platformKey));
  assert.ok(!works.some(run => run.id === selfRun.id));
});

test('each public monitor has its own statistics without private tests or demo results mixed in', async t => {
  const { app, call, finished } = await harness(t, { generate: async run => {
    if (run.model === 'fixture-fails') throw new CheckError('upstream_limit:429', 502);
    return { html: '<html></html>', usage: null };
  } });
  const good = await call('/admin/channels', 'admin', 'POST', { ...channelConfig, public: true });
  const bad = await call('/admin/channels', 'admin', 'POST', { ...channelConfig, model: 'fixture-fails', name: 'Failed monitor', public: true });
  const hidden = await call('/admin/channels', 'admin', 'POST', { ...channelConfig, name: 'Private fixture monitor' });
  for (const channel of [good, bad, hidden]) {
    const run = await call('/admin/channels/' + channel.data.id + '/run', 'admin', 'POST', {});
    await finished(run.data.id);
  }
  const self = await call('/tests', 'user', 'POST', options);
  await finished(self.data.id);
  const demo = app.store.saveChannel({ ...channelConfig, public: true, demo: true, model: 'Local example' }, 0, '');
  const demoRun = app.store.createRun({ channel_id: demo.id, source: 'demo', topic: 'pelican', model: 'Local example' });
  app.store.updateRun(demoRun.id, { status: 'normal', total_ms: 100 });
  const overview = (await call('/overview')).data;
  assert.equal(overview.channels.length, 3);
  const a = overview.channels.find(channel => channel.id === good.data.id).statistics;
  const b = overview.channels.find(channel => channel.id === bad.data.id).statistics;
  const sample = overview.channels.find(channel => channel.id === demo.id).statistics;
  assert.equal(a.total, 1);
  assert.equal(a.pass_rate, 1);
  assert.equal(b.total, 1);
  assert.equal(b.pass_rate, null);
  assert.equal(b.failed, 1);
  assert.equal(sample.total, 1);
  assert.ok(!JSON.stringify(overview).includes('Private fixture monitor'));
  assert.ok(!JSON.stringify(overview).includes(self.data.id));
});

test('an external monitor can add a display group without changing or exposing its stored key', async t => {
  const { app, call, finished, directory } = await harness(t);
  const config = { ...channelConfig, key_source: 'external', key_id: null, base_url: 'https://provider.example/v1', group_id: null, group_name: null, seed: 20260923 };
  const saved = app.store.saveChannel(config, 1, secretVault(directory).encrypt(platformKey));
  const updated = await call('/admin/channels/' + saved.id, 'admin', 'PUT', { ...config, key: '', group_name: 'External Pro pool', public: true });
  assert.equal(updated.status, 200);
  assert.equal(updated.data.group_name, 'External Pro pool');
  const submitted = await call('/admin/channels/' + saved.id + '/run', 'admin', 'POST', {});
  const run = await finished(submitted.data.id);
  assert.equal(run.group_id, null);
  assert.equal(run.group_name, 'External Pro pool');
  assert.equal(run.key_source, 'external');
  const overview = (await call('/overview')).data;
  assert.equal(overview.works[0].group_name, 'External Pro pool');
  assert.ok(!JSON.stringify(overview).includes(platformKey));
});
