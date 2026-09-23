import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createService } from '../src/server.mjs';
import { BRIDGE_HEADER, signBridge, verifyBridge, validBridgeSecret, loopbackOrigin } from '../src/security.mjs';

const secret = '0123456789abcdef0123456789abcdef';
// Identical to the Go package's protocol vector.
const fixtureToken = 'eyJ2IjoxLCJ0cyI6MTgwMDAwMDAwMCwiaXAiOiIyMDMuMC4xMTMuOCIsInVhIjoiWm1sNGRIVnlaUzFoWjJWdWRBIiwiYXV0aCI6ImZlYzQxMTYzZmY5Y2IxYTNkNTU0NjdkYzQ3NDEyOGQxY2VmYmU3MTUwZmJiYjk2OWI2ZmY2MTViM2M4YjU0ZmUiLCJtZXRob2QiOiJHRVQiLCJwYXRoIjoiL2FwaS92MS9rZXlzP3N0YXR1cz1hY3RpdmUifQ.QGSEPphj_WUd58TcIjNDYEoHzQoYC6NJ5v93XeQMG2s';
const now = 1800000000000;
const fixtureIdentity = { ip: '203.0.113.8', userAgent: 'fixture-agent' };
const fixtureRequest = () => ({ method: 'GET', url: '/api/v1/keys?status=active', socket: { remoteAddress: '127.0.0.1' },
  headers: { authorization: 'Bearer fixture-user', 'user-agent': fixtureIdentity.userAgent, [BRIDGE_HEADER]: fixtureToken } });

test('bridge wire format agrees with Go and rejects tampering, replay and remote peers', () => {
  assert.equal(signBridge(secret, fixtureRequest(), fixtureIdentity, now), fixtureToken);
  assert.deepEqual(verifyBridge(secret, fixtureRequest(), now), fixtureIdentity);
  const adminRequest = { method: 'GET', url: '/api/v1/admin/settings/admin-api-key', headers: { 'x-api-key': 'fixture-admin-key', 'user-agent': 'fixture-agent' } };
  assert.ok(signBridge(secret, adminRequest, fixtureIdentity, now).endsWith('.8U55OvCq-XaPT9TM2nwvUFGA2NPwT-gKCSgcily8fBQ'));
  const mutations = [
    req => { req.socket.remoteAddress = '203.0.113.10'; },
    req => { req.headers[BRIDGE_HEADER] = fixtureToken.slice(0, -8) + 'AAAAAAAA'; },
    req => { req.headers[BRIDGE_HEADER] = [fixtureToken, fixtureToken]; },
    req => { req.headers.authorization = 'Bearer another-token'; },
    req => { req.headers['x-api-key'] = 'admin-key'; },
    req => { req.headers['x-api-key'] = ''; },
    req => { req.headers['user-agent'] = 'changed'; },
    req => { req.method = 'POST'; },
    req => { req.url = '/api/v1/auth/me'; },
    req => { req.url += '&extra=1'; },
  ];
  for (const mutate of mutations) { const req = fixtureRequest(); mutate(req); assert.equal(verifyBridge(secret, req, now), null); }
  assert.equal(verifyBridge(secret, fixtureRequest(), now + 31_000), null);
  assert.equal(verifyBridge(secret, fixtureRequest(), now - 6_000), null);
  const longUA = Buffer.from('A'.repeat(509) + '中').toString('latin1');
  const req = fixtureRequest(); req.headers['user-agent'] = longUA;
  req.headers[BRIDGE_HEADER] = signBridge(secret, req, { ip: '2001:db8::8', userAgent: longUA }, now);
  assert.equal(verifyBridge(secret, req, now).userAgent, longUA, 'all 512 original header bytes survive');
});

test('production bridge configuration is opt-in and rejects unsafe origins and secrets', () => {
  assert.equal(validBridgeSecret(secret), true);
  for (const value of ['', 'short', 'x'.repeat(31) + ' ', 'x'.repeat(257)]) assert.equal(validBridgeSecret(value), false);
  for (const value of ['https://127.0.0.1', 'http://localhost:8080', 'http://example.com', 'http://10.0.0.1', 'http://127.0.0.1/path', 'http://user:pass@127.0.0.1', 'http://127.0.0.1?x=1', 'http://127.0.0.1?', 'http://127.0.0.1#']) assert.equal(loopbackOrigin(value), false, value);
  assert.equal(loopbackOrigin('http://127.0.0.1:8080'), true);
  assert.equal(loopbackOrigin('http://[::1]:8080'), true);
  // Validation runs before touching a data directory.
  assert.throws(() => createService({ bridgeSecret: 'short' }), /MODEL_CHECK_BRIDGE_SECRET/);
  assert.throws(() => createService({ bridgeSecret: secret, backend: 'http://example.com' }), /loopback/);
  assert.throws(() => createService({ bridgeSecret: secret, backend: 'http://127.0.0.1', demo: true }), /Demo mode/);
});

const prefix = '/api/v1/model-check';
const adminKey = 'admin-fixture-global-credential';
const modelKey = 'sk-fixture-user-model-credential';
const agent = 'A'.repeat(512);
const originalIdentity = { ip: '203.0.113.8', userAgent: agent };
const options = { key_source: 'existing', key_id: 13, model: 'fixture-model', topic: 'pelican', protocol: 'responses', reasoning: 'default', max_tokens: 8000 };

async function harness(t) {
  const directory = mkdtempSync(join(tmpdir(), 'model-check-bridge-'));
  const callbacks = []; const modelRequests = []; let available = true;
  const backend = http.createServer(async (req, res) => {
    const reply = (status, data) => { res.writeHead(status, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(data)); };
    if (req.url === '/v1/responses') {
      const chunks = []; for await (const chunk of req) chunks.push(chunk);
      modelRequests.push({ headers: req.headers, body: JSON.parse(Buffer.concat(chunks)) });
      if (req.headers.authorization !== `Bearer ${modelKey}`) { reply(401, {}); return; }
      reply(200, { output_text: '<html><body><svg></svg></body></html>', usage: { input_tokens: 5, output_tokens: 10 } }); return;
    }
    const identity = verifyBridge(secret, req);
    callbacks.push({ path: req.url, identity, headers: req.headers });
    if (!available) { reply(503, { detail: 'private account-service diagnostic' }); return; }
    if (!identity || identity.ip !== originalIdentity.ip || identity.userAgent !== agent) { reply(401, {}); return; }
    if (req.url === '/api/v1/admin/settings/admin-api-key') {
      if (req.headers['x-api-key'] !== adminKey || req.headers.authorization) { reply(401, {}); return; }
      reply(200, { code: 0, data: { exists: true } }); return;
    }
    const id = req.headers.authorization === 'Bearer fixture-admin' ? 1 : req.headers.authorization === 'Bearer fixture-user' ? 2 : 0;
    if (!id || req.headers['x-api-key']) { reply(401, {}); return; }
    if (req.url === '/api/v1/auth/me') { reply(200, { code: 0, data: { id, role: id === 1 ? 'admin' : 'user' } }); return; }
    const key = { id: 13, user_id: req.url.endsWith('/14') ? 3 : id, key: modelKey, name: 'Fixture', status: 'active', group_id: 21, group: { id: 21, name: 'Fixture group' } };
    reply(200, { code: 0, data: req.url.startsWith('/api/v1/keys?') ? { items: [key] } : key });
  });
  await new Promise(resolve => backend.listen(0, '127.0.0.1', resolve));
  const base = `http://127.0.0.1:${backend.address().port}`;
  const app = createService({ dataDir: directory, backend: base, siteApiBase: base + '/v1', bridgeSecret: secret, demo: false }, {
    render: async html => ({ html, image: '', thumbnail: '', metrics: { svg_count: 1, visible_shapes: 20, contrast: 35, occupied_ratio: .5, motion_ratio: .1, overflow_mobile: false, overflow_desktop: false } }),
  });
  await new Promise(resolve => app.server.listen(0, '127.0.0.1', resolve));
  t.after(async () => { await app.close(); await new Promise(resolve => backend.close(resolve)); rmSync(directory, { recursive: true, force: true }); });
  async function call(path, { method = 'GET', bearer, key, body, signed = true, forged = false, identity = originalIdentity } = {}) {
    const headers = { 'user-agent': agent, 'content-type': 'application/json', ...(bearer ? { authorization: `Bearer ${bearer}` } : {}), ...(key === undefined ? {} : { 'x-api-key': key }) };
    if (signed) headers[BRIDGE_HEADER] = signBridge(secret, { method, url: prefix + path, headers }, identity);
    if (forged) headers[BRIDGE_HEADER] = 'forged-public-bridge';
    const res = await fetch(`http://127.0.0.1:${app.server.address().port}${prefix}${path}`, { method, headers, ...(body === undefined ? {} : { body: JSON.stringify(body) }) });
    return { status: res.status, ...(await res.json()) };
  }
  async function finished(id) {
    for (let i = 0; i < 500; i++) {
      const run = app.store.run(id);
      if (['normal', 'review', 'failed'].includes(run.status)) return run;
      await new Promise(resolve => setTimeout(resolve, 10));
    }
    throw new Error('fixture run did not finish');
  }
  return { app, call, callbacks, modelRequests, finished, failBackend: () => { available = false; } };
}

test('production requires the bridge, preserves bound user identity and keeps private tests owned', async t => {
  const { call, callbacks, modelRequests, finished } = await harness(t);
  assert.equal((await call('/health', { signed: false })).status, 200);
  assert.equal((await call('/overview', { signed: false })).status, 403);
  assert.equal((await call('/overview', { forged: true })).status, 403);
  assert.equal((await call('/overview')).status, 200);
  assert.equal((await call('/mine')).status, 401, 'bridge does not supply a user identity');
  assert.equal((await call('/mine', { bearer: 'invalid' })).status, 401);
  assert.equal((await call('/mine', { bearer: 'fixture-user', identity: { ...originalIdentity, ip: '203.0.113.9' } })).status, 401);
  const keys = await call('/keys', { bearer: 'fixture-user' });
  assert.equal(keys.status, 200); assert.equal(keys.data[0].id, 13); assert.ok(!JSON.stringify(keys).includes(modelKey));
  assert.equal((await call('/tests', { method: 'POST', bearer: 'fixture-user', body: { ...options, key_id: 14 } })).status, 400, 'foreign keys remain unavailable');
  const accepted = await call('/tests', { method: 'POST', bearer: 'fixture-user', body: options });
  assert.equal(accepted.status, 202);
  assert.equal((await finished(accepted.data.id)).status, 'normal');
  assert.equal((await call('/runs/' + accepted.data.id, { key: adminKey })).status, 401, 'global keys cannot become personal owners');
  assert.equal((await call('/runs/' + accepted.data.id, { bearer: 'fixture-user' })).status, 200);
  const keyCallbacks = callbacks.filter(c => c.path.startsWith('/api/v1/keys'));
  assert.ok(keyCallbacks.length >= 3);
  assert.ok(keyCallbacks.every(c => c.identity.ip === originalIdentity.ip && c.identity.userAgent === agent));
  assert.equal(modelRequests.length, 1);
  assert.equal(modelRequests[0].headers.authorization, `Bearer ${modelKey}`);
  assert.equal(modelRequests[0].headers[BRIDGE_HEADER], undefined);
  assert.equal(modelRequests[0].headers['x-api-key'], undefined);
  assert.ok(!JSON.stringify(modelRequests).includes(adminKey));
});

test('production retains admin JWT settings and global integration credentials without forwarding them to models', async t => {
  const { call, callbacks, modelRequests, failBackend } = await harness(t);
  const channel = { ...options, name: 'Fixture monitor', interval_minutes: 5, enabled: false, public: false };
  assert.equal((await call('/admin/channels', { method: 'POST', bearer: 'fixture-user', body: channel })).status, 403);
  assert.equal((await call('/admin/channels', { method: 'POST', key: adminKey, body: channel })).status, 401, 'global integration credential cannot edit JWT-owned settings');
  const saved = await call('/admin/channels', { method: 'POST', bearer: 'fixture-admin', body: channel });
  assert.equal(saved.status, 200);
  assert.equal((await call('/admin/channels', { key: 'wrong', bearer: 'fixture-admin' })).status, 401, 'invalid global key must not fall back to a valid JWT');
  assert.equal((await call('/admin/channels', { key: '', bearer: 'fixture-admin' })).status, 401, 'an empty global key still must not fall back to JWT');
  assert.equal((await call('/admin/channels', { key: adminKey })).status, 200);
  const tested = await call('/admin/tests', { method: 'POST', key: adminKey, body: { channel_id: saved.data.id, wait_seconds: 2 } });
  assert.equal(tested.status, 200); assert.equal(tested.data.completed, true);
  assert.ok(callbacks.filter(c => c.path.endsWith('/admin-api-key')).every(c => !c.headers.authorization && c.identity.ip === originalIdentity.ip));
  assert.equal(modelRequests.length, 1);
  assert.equal(modelRequests[0].headers.authorization, `Bearer ${modelKey}`);
  assert.equal(modelRequests[0].headers[BRIDGE_HEADER], undefined);
  assert.equal(modelRequests[0].headers['x-api-key'], undefined);
  failBackend();
  const failed = await call('/admin/channels', { key: adminKey });
  assert.equal(failed.status, 503);
  assert.ok(!JSON.stringify(failed).includes('private account-service diagnostic'));
});
