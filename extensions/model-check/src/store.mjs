import { DatabaseSync } from 'node:sqlite';
import { randomUUID } from 'node:crypto';
import { summarizeRuns } from './statistics.mjs';

const finished = ['normal', 'review', 'failed'];
const requestRetentionMs = 24 * 60 * 60 * 1000;
export class Store {
  constructor(path) {
    this.db = new DatabaseSync(path);
    this.db.exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=3000;
      CREATE TABLE IF NOT EXISTS channels (id TEXT PRIMARY KEY, owner_id INTEGER NOT NULL, config TEXT NOT NULL, encrypted_key TEXT NOT NULL, next_run INTEGER NOT NULL, baseline_id TEXT, created_at INTEGER NOT NULL);
      CREATE TABLE IF NOT EXISTS runs (id TEXT PRIMARY KEY, owner_id INTEGER NOT NULL, channel_id TEXT, source TEXT NOT NULL, status TEXT NOT NULL, created_at INTEGER NOT NULL, data TEXT NOT NULL, html TEXT, image TEXT, thumbnail TEXT);
      CREATE INDEX IF NOT EXISTS runs_channel_created ON runs(channel_id,created_at DESC);
      CREATE INDEX IF NOT EXISTS runs_owner_created ON runs(owner_id,created_at DESC);
      CREATE TABLE IF NOT EXISTS api_requests (request_key TEXT PRIMARY KEY, fingerprint TEXT NOT NULL, run_id TEXT NOT NULL, created_at INTEGER NOT NULL);
      CREATE INDEX IF NOT EXISTS api_requests_created ON api_requests(created_at);`);
    this.db.prepare('DELETE FROM api_requests WHERE created_at<?').run(Date.now() - requestRetentionMs);
    // In-flight secrets exist only in memory. Never replay a potentially billed call after restart.
    for (const row of this.db.prepare("SELECT id FROM runs WHERE status IN ('queued','generating','rendering')").all()) this.updateRun(row.id, { status: 'failed', error: 'service_restarted', finished_at: Date.now() });
  }
  close() { this.db.close(); }
  channels() { return this.db.prepare('SELECT * FROM channels ORDER BY created_at').all().map(row => this.channelRow(row)); }
  channelRow(row) { return row ? { ...row, ...JSON.parse(row.config), config: undefined } : null; }
  channel(id) { return this.channelRow(this.db.prepare('SELECT * FROM channels WHERE id=?').get(id)); }
  saveChannel(config, owner, encryptedKey, id = randomUUID()) {
    const previous = this.channel(id);
    const nextRun = Date.now() + config.interval_minutes * 60_000;
    const baseline = previous && ['model', 'topic', 'protocol', 'reasoning', 'max_tokens', 'seed', 'base_url', 'key_source', 'key_id', 'group_id'].every(field => previous[field] === config[field]) ? previous.baseline_id : null;
    this.db.prepare(`INSERT INTO channels(id,owner_id,config,encrypted_key,next_run,baseline_id,created_at) VALUES(?,?,?,?,?,?,?)
      ON CONFLICT(id) DO UPDATE SET config=excluded.config,encrypted_key=excluded.encrypted_key,next_run=excluded.next_run,baseline_id=excluded.baseline_id`).run(id, owner, JSON.stringify(config), encryptedKey || previous?.encrypted_key || '', nextRun, baseline, previous?.created_at || Date.now());
    return this.channel(id);
  }
  setBaseline(channel, run) { this.db.prepare('UPDATE channels SET baseline_id=? WHERE id=?').run(run, channel); }
  setNextRun(id, time) { this.db.prepare('UPDATE channels SET next_run=? WHERE id=?').run(time, id); }
  createRun({ owner_id = 0, channel_id = null, source = 'self', ...data }, request = null) {
    const id = randomUUID(); const created_at = Date.now();
    this.db.exec('BEGIN IMMEDIATE');
    try {
      this.db.prepare('INSERT INTO runs(id,owner_id,channel_id,source,status,created_at,data) VALUES(?,?,?,?,?,?,?)').run(id, owner_id, channel_id, source, 'queued', created_at, JSON.stringify(data));
      if (request) this.db.prepare('INSERT INTO api_requests(request_key,fingerprint,run_id,created_at) VALUES(?,?,?,?)').run(request.key, request.fingerprint, id, created_at);
      this.db.exec('COMMIT');
    } catch (error) { this.db.exec('ROLLBACK'); throw error; }
    return this.run(id);
  }
  request(key) {
    const request = this.db.prepare('SELECT fingerprint,run_id,created_at FROM api_requests WHERE request_key=?').get(key);
    if (request && request.created_at < Date.now() - requestRetentionMs) {
      this.db.prepare('DELETE FROM api_requests WHERE request_key=?').run(key);
      return undefined;
    }
    return request;
  }
  run(id) {
    const row = this.db.prepare('SELECT * FROM runs WHERE id=?').get(id);
    return row ? { ...JSON.parse(row.data), ...row, data: undefined } : null;
  }
  updateRun(id, fields) {
    const current = this.run(id); if (!current) return null;
    const { html, image, thumbnail, status, data, ...record } = { ...current, ...fields };
    this.db.prepare('UPDATE runs SET status=?,data=?,html=?,image=?,thumbnail=? WHERE id=?').run(status, JSON.stringify(record), html || null, image || null, thumbnail || null, id);
    return this.run(id);
  }
  listRuns({ owner, channel, limit = 60 } = {}) {
    const clauses = []; const params = [];
    if (owner !== undefined) { clauses.push('owner_id=?'); params.push(owner); }
    if (channel !== undefined) { clauses.push('channel_id=?'); params.push(channel); }
    const sql = `SELECT id FROM runs ${clauses.length ? 'WHERE ' + clauses.join(' AND ') : ''} ORDER BY created_at DESC,rowid DESC LIMIT ?`;
    return this.db.prepare(sql).all(...params, limit).map(row => this.run(row.id));
  }
  canRead(run, user) {
    if (!run) return false;
    if (user && run.owner_id === user.id) return true;
    if (user?.role === 'admin' && run.channel_id) return true;
    if (run.source === 'demo') return true;
    return run.channel_id ? !!this.channel(run.channel_id)?.public : false;
  }
  // Enforce separate channel/user quotas so a busy account cannot evict everybody's work.
  prune() {
    // Keep retry tombstones for 24 hours even when the associated image is pruned.
    this.db.prepare('DELETE FROM api_requests WHERE created_at<?').run(Date.now() - requestRetentionMs);
    const rows = this.db.prepare('SELECT id,owner_id,channel_id,source,status FROM runs ORDER BY created_at DESC,rowid DESC').all();
    const counts = new Map(); let global = 0;
    for (const row of rows) {
      if (!finished.includes(row.status)) continue;
      const bucket = row.channel_id ? `channel:${row.channel_id}` : `owner:${row.owner_id}:${row.source}`;
      const count = (counts.get(bucket) || 0) + 1; counts.set(bucket, count); global++;
      if (count > 30 || global > 600) {
        // A baseline is retained until it is replaced; at most one per channel.
        if (this.db.prepare('SELECT id FROM channels WHERE baseline_id=?').get(row.id)) continue;
        this.db.prepare('DELETE FROM runs WHERE id=?').run(row.id);
      }
    }
  }
}

export function publicRun(run, detail = false) {
  const tokens = run.usage?.output_tokens;
  const tps = run.source !== 'demo' && Number.isFinite(tokens) && tokens >= 0 && Number.isFinite(run.generation_ms) && run.generation_ms > 0
    ? tokens / (run.generation_ms / 1000) : null;
  const result = {
    id: run.id, channel_id: run.channel_id, source: run.source, status: run.status,
    model: run.model, topic: run.topic, reasoning: run.reasoning, protocol: run.protocol,
    key_source: run.key_source || null, group_id: run.group_id ?? null, group_name: run.group_name || null,
    created_at: run.created_at, finished_at: run.finished_at, generation_ms: run.generation_ms,
    total_ms: run.total_ms, usage: run.usage, tps: Number.isFinite(tps) ? tps : null, assessment: run.assessment, error: run.error,
    label: run.label, prompt_hash: run.prompt_hash, prompt_version: run.prompt_version,
    thumbnail: run.thumbnail ? `data:image/webp;base64,${run.thumbnail}` : null,
  };
  if (detail) Object.assign(result, { prompt: run.prompt, seed: run.seed, conditions: run.conditions, html: run.html, metrics: run.metrics, image: run.image ? `data:image/webp;base64,${run.image}` : null, max_tokens: run.max_tokens });
  return result;
}

export function publicChannel(channel, runs = [], admin = false) {
  const matching = runs.filter(run =>
    ['model', 'topic', 'reasoning', 'protocol', 'max_tokens'].every(field => run[field] === channel[field]) &&
    (run.group_id ?? null) === (channel.group_id ?? null) &&
    (channel.group_id != null || (run.group_name || null) === (channel.group_name || null)) &&
    (!run.key_source || run.key_source === channel.key_source));
  const result = {
    id: channel.id, name: channel.name, model: channel.model, topic: channel.topic, reasoning: channel.reasoning,
    protocol: channel.protocol, max_tokens: channel.max_tokens, enabled: channel.enabled, public: channel.public,
    key_source: channel.key_source || null, group_id: channel.group_id ?? null, group_name: channel.group_name || null,
    interval_minutes: channel.interval_minutes, next_run: channel.next_run, baseline_id: channel.baseline_id,
    demo: !!channel.demo, latest: runs.length ? publicRun(runs[0]) : null, statistics: summarizeRuns(channel.demo ? runs : matching),
    history: runs.slice(0, 24).reverse().map(run => ({ id: run.id, status: run.status, score: run.assessment?.score ?? null, created_at: run.created_at })),
  };
  if (admin) Object.assign(result, { base_url: channel.base_url, key_source: channel.key_source, key_id: channel.key_id, has_key: !!channel.encrypted_key, seed: channel.seed });
  return result;
}
