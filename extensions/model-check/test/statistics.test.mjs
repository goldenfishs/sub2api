import test from 'node:test';
import assert from 'node:assert/strict';
import { summarizeRuns } from '../src/statistics.mjs';
import { publicChannel } from '../src/store.mjs';

test('empty and all-failed histories have no visual pass rate', () => {
  const empty = summarizeRuns([]);
  assert.equal(empty.pass_rate, null);
  assert.equal(empty.success_rate, null);
  assert.equal(empty.latest_duration_ms, null);
  const failed = summarizeRuns([
    { id: 'old', status: 'failed', created_at: 1, total_ms: 118 },
    { id: 'new', status: 'failed', created_at: 2, total_ms: 280 },
    { id: 'active', status: 'generating', created_at: 3 },
  ]);
  assert.equal(failed.total, 2);
  assert.equal(failed.judged, 0);
  assert.equal(failed.pass_rate, null);
  assert.equal(failed.success_rate, 0);
  assert.equal(failed.latest_duration_ms, 280);
});

test('visual pass rate excludes failed requests and active jobs; history runs oldest to newest', () => {
  const summary = summarizeRuns([
    { id: 'a', status: 'normal', created_at: 1, total_ms: 1400, key: 'private-fixture' },
    { id: 'pending', status: 'rendering', created_at: 6 },
    { id: 'd', status: 'failed', created_at: 4, total_ms: 800 },
    { id: 'b', status: 'review', created_at: 2, total_ms: 2400 },
    { id: 'c', status: 'normal', created_at: 3, total_ms: 3600 },
  ]);
  assert.equal(summary.total, 4);
  assert.equal(summary.pass_rate, 2 / 3);
  assert.equal(summary.success_rate, 3 / 4);
  assert.deepEqual(summary.history.map(run => run.id), ['a', 'b', 'c', 'd']);
  assert.equal(summary.latest_duration_ms, 800);
  assert.ok(!JSON.stringify(summary).includes('private-fixture'));
});

test('statistics use the most recent 30 completed records and do not substitute a previous duration', () => {
  const rows = Array.from({ length: 35 }, (_, i) => ({ id: String(i), created_at: i, status: i < 5 ? 'review' : 'normal', total_ms: i === 34 ? null : 1000 }));
  const summary = summarizeRuns(rows);
  assert.equal(summary.total, 30);
  assert.equal(summary.pass_rate, 1);
  assert.equal(summary.review, 0);
  assert.equal(summary.history[0].id, '5');
  assert.equal(summary.history[29].id, '34');
  assert.equal(summary.latest_duration_ms, null);
});

test('a monitor summary does not combine results from a previous model or prompt mode', () => {
  const options = { model: 'fixture-model', topic: 'creative', reasoning: 'default', protocol: 'responses', max_tokens: 8000 };
  const channel = { ...options, id: 'monitor', name: 'Fixture monitor' };
  const results = [
    { ...options, id: 'current', status: 'failed', created_at: 3 },
    { ...options, id: 'old-topic', topic: 'pelican', status: 'normal', created_at: 2 },
    { ...options, id: 'old-model', model: 'old-model', status: 'normal', created_at: 1 },
  ];
  const summary = publicChannel(channel, results).statistics;
  assert.equal(summary.total, 1);
  assert.equal(summary.pass_rate, null);
  assert.equal(summary.failed, 1);
  assert.equal(publicChannel(channel, results).history.length, 3, 'old results remain available separately');
});
