import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { mkdtempSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { createService } from '../src/server.mjs';
import { publicRun, Store } from '../src/store.mjs';

test('quality reviews require admin authority, preserve machine results and retain a private audit trail', async t => {
  const directory = mkdtempSync(join(tmpdir(), 'model-check-quality-'));
  const backend = http.createServer((req, res) => {
    res.setHeader('Content-Type', 'application/json');
    if (req.url === '/api/v1/admin/settings/admin-api-key' && req.headers['x-api-key'] === 'fixture-admin-key') {
      res.end(JSON.stringify({ code: 0, data: { exists: true } })); return;
    }
    if (req.url === '/api/v1/auth/me' && ['Bearer admin', 'Bearer user'].includes(req.headers.authorization)) {
      const admin = req.headers.authorization === 'Bearer admin';
      res.end(JSON.stringify({ code: 0, data: { id: admin ? 1 : 2, role: admin ? 'admin' : 'user' } })); return;
    }
    res.writeHead(401); res.end('{}');
  });
  await new Promise(resolve => backend.listen(0, '127.0.0.1', resolve));
  const app = createService({ dataDir: directory, backend: `http://127.0.0.1:${backend.address().port}`, siteApiBase: 'https://example.com/v1', demo: false }, {
    generate: async () => { throw new Error('Reviewing must never generate or bill'); },
  });
  await new Promise(resolve => app.server.listen(0, '127.0.0.1', resolve));
  t.after(async () => { await app.close(); await new Promise(resolve => backend.close(resolve)); rmSync(directory, { recursive: true, force: true }); });
  const channel = app.store.saveChannel({ name: 'Quality fixture', model: 'fixture-model', interval_minutes: 60, enabled: false, public: true }, 1, 'fixture-encrypted');
  const run = app.store.createRun({ channel_id: channel.id, owner_id: 1, model: 'fixture-model' });
  const assessment = { score: 100, verdict: 'normal', reasons: [] };
  app.store.updateRun(run.id, { status: 'normal', image: 'fixture-image', thumbnail: 'fixture-thumbnail', assessment });
  app.store.setBaseline(channel.id, run.id);
  async function call(id, headers, body) {
    const res = await fetch(`http://127.0.0.1:${app.server.address().port}/api/v1/model-check/admin/runs/${id}/review`, {
      method: 'POST', headers: { 'Content-Type': 'application/json', ...headers }, body: JSON.stringify(body),
    });
    return { status: res.status, ...(await res.json()) };
  }
  const admin = { Authorization: 'Bearer admin' };
  assert.equal((await call(run.id, {}, { verdict: 'degraded' })).status, 401);
  assert.equal((await call(run.id, { Authorization: 'Bearer user' }, { verdict: 'degraded' })).status, 403);
  assert.equal((await call(run.id, admin, { verdict: 'not-a-verdict' })).status, 400);
  assert.equal((await call(run.id, admin, { verdict: 'normal', score: 100 })).status, 400);
  const degraded = await call(run.id, admin, { verdict: 'degraded' });
  assert.equal(degraded.status, 200);
  assert.equal(degraded.data.quality_review.verdict, 'degraded');
  assert.equal(degraded.data.status, 'normal', 'machine status is retained separately');
  assert.deepEqual(degraded.data.assessment, assessment);
  assert.equal(app.store.channel(channel.id).baseline_id, null, 'a rejected work cannot remain a baseline');
  assert.equal((await call(run.id, { 'x-api-key': 'fixture-admin-key' }, { verdict: 'normal' })).status, 200);
  assert.equal((await call(run.id, admin, { verdict: 'clear' })).data.quality_review, null);
  const audit = app.store.db.prepare('SELECT verdict,reviewer_id,auth_method FROM quality_reviews ORDER BY id').all();
  assert.deepEqual(audit.map(row => row.verdict), ['degraded', 'normal', 'clear']);
  assert.deepEqual(audit.map(row => row.reviewer_id), [1, null, 1]);
  assert.equal(audit[1].auth_method, 'admin_api_key');
  assert.equal(publicRun(app.store.run(run.id), true).reviewer_id, undefined);
  const privateRun = app.store.createRun({ owner_id: 2, source: 'self' });
  app.store.updateRun(privateRun.id, { status: 'normal', image: 'private' });
  assert.equal((await call(privateRun.id, { 'x-api-key': 'fixture-admin-key' }, { verdict: 'degraded' })).status, 404);
  for (const status of ['queued', 'generating', 'rendering', 'failed']) {
    const other = app.store.createRun({ channel_id: channel.id, owner_id: 1 });
    app.store.updateRun(other.id, { status });
    assert.equal((await call(other.id, admin, { verdict: 'degraded' })).reason, 'result_not_reviewable');
  }
  app.store.deleteChannel(channel.id);
  assert.equal(app.store.db.prepare('SELECT count(*) AS n FROM quality_reviews').get().n, 0);
  assert.ok(app.store.run(privateRun.id));
});

test('quality review and its audit survive restart and roll back together', () => {
  const directory = mkdtempSync(join(tmpdir(), 'quality-persistence-'));
  const path = join(directory, 'check.sqlite');
  let store = new Store(path);
  try {
    const run = store.createRun({ owner_id: 1 });
    store.updateRun(run.id, { status: 'normal' });
    store.reviewRun(run.id, 'normal', { id: 1 });
    store.close(); store = new Store(path);
    assert.equal(store.run(run.id).quality_review.verdict, 'normal');
    store.db.exec("CREATE TRIGGER reject_review BEFORE UPDATE ON runs BEGIN SELECT RAISE(ABORT,'fixture'); END;");
    assert.throws(() => store.reviewRun(run.id, 'degraded', { id: 1 }));
    assert.equal(store.run(run.id).quality_review.verdict, 'normal');
    assert.equal(store.db.prepare('SELECT count(*) AS n FROM quality_reviews').get().n, 1);
  } finally { store.close(); rmSync(directory, { recursive: true, force: true }); }
});
