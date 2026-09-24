import test from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { waitForResult } from '../src/integration.mjs';
import { createService } from '../src/server.mjs';
import { secretVault } from '../src/security.mjs';
import { validateOptions } from '../src/prompts.mjs';

test('waiting stops promptly when its record is deleted', async () => {
  let record = { status: 'rendering' };
  const response = new EventEmitter();
  const pending = waitForResult({ run: () => record }, 'fixture', 30_000, response);
  record = null;
  assert.equal(await pending, null);
  assert.equal(response.listenerCount('close'), 0);
});

test('a completed monitor deleted before the waiting API polls returns 410, not 500 or a timeout', async t => {
  const directory = mkdtempSync(join(tmpdir(), 'model-check-deleted-wait-'));
  const app = createService({ dataDir: directory, backend: 'http://127.0.0.1:8080', siteApiBase: 'http://127.0.0.1:8080/v1', demo: false }, {
    authenticate: async () => ({ id: 1, role: 'admin' }),
    generate: async () => {
      await new Promise(resolve => setTimeout(resolve, 20));
      return { html: '<svg></svg>', usage: null };
    },
    render: async () => ({ image: 'fixture', metrics: { svg_count: 1, visible_shapes: 5, contrast: 30, occupied_ratio: .5, motion_ratio: .1 } }),
  });
  await new Promise(resolve => app.server.listen(0, '127.0.0.1', resolve));
  t.after(async () => { await app.close(); rmSync(directory, { recursive: true, force: true }); });
  const channel = app.store.saveChannel({ ...validateOptions({ model: 'fixture' }), name: 'Fixture', interval_minutes: 60, enabled: false, public: false,
    seed: 1, key_source: 'existing', key_id: 1, base_url: 'http://127.0.0.1:8080/v1' }, 1, secretVault(directory).encrypt('fixture-model-key'));
  const updateRun = app.store.updateRun.bind(app.store);
  app.store.updateRun = (id, fields) => {
    const run = updateRun(id, fields);
    if (fields.status === 'normal') {
      // Reproduce deletion after job completion, before the 100 ms API poll.
      setImmediate(() => app.store.deleteChannel(channel.id));
    }
    return run;
  };
  const response = await fetch(`http://127.0.0.1:${app.server.address().port}/api/v1/model-check/admin/tests`, {
    method: 'POST', headers: { 'Content-Type': 'application/json', Authorization: 'Bearer fixture' },
    body: JSON.stringify({ channel_id: channel.id, wait_seconds: 30 }), signal: AbortSignal.timeout(2000),
  });
  assert.equal(response.status, 410);
  assert.equal((await response.json()).reason, 'result_expired');
  assert.equal(app.store.channel(channel.id), null);
});
