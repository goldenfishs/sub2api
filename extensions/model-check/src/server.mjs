import http from 'node:http';
import { mkdirSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { Store, publicRun, publicChannel } from './store.mjs';
import { CheckError, normalizeBase, resolveEndpoint, requestJSON, secretVault } from './security.mjs';
import { makePrompt, makeScheduledPrompt, validateOptions, TOPICS } from './prompts.mjs';
import { renderArtwork, assess } from './render.mjs';
import { sampleHTML } from './samples.mjs';
import { API_PREFIX, apiRun, prepareAPITest, waitForResult, sendImage, isFinished } from './integration.mjs';

const PREFIX = API_PREFIX;
const activeStatuses = ['queued', 'generating', 'rendering'];
const response = (res, status, data) => {
  res.writeHead(status, { 'Content-Type': 'application/json; charset=utf-8', 'Cache-Control': 'no-store', 'X-Content-Type-Options': 'nosniff', 'Referrer-Policy': 'no-referrer' });
  res.end(JSON.stringify(status < 400 ? { code: 0, data } : { code: status, message: data, reason: data }));
};

async function readBody(req) {
  if (!String(req.headers['content-type'] || '').startsWith('application/json')) throw new CheckError('json_required', 415);
  const chunks = []; let size = 0;
  for await (const chunk of req) {
    size += chunk.length; if (size > 16_384) throw new CheckError('request_too_large', 413); chunks.push(chunk);
  }
  let body;
  try { body = JSON.parse(Buffer.concat(chunks).toString('utf8') || '{}'); } catch { throw new CheckError('invalid_json'); }
  if (!body || typeof body !== 'object' || Array.isArray(body)) throw new CheckError('invalid_request');
  return body;
}

function usageOf(data) {
  const u = data.usage; if (!u) return null;
  const safe = value => Number.isFinite(value) && value >= 0 ? Math.floor(value) : null;
  return { input_tokens: safe(u.input_tokens ?? u.prompt_tokens), output_tokens: safe(u.output_tokens ?? u.completion_tokens), cached_tokens: safe(u.input_tokens_details?.cached_tokens ?? u.prompt_tokens_details?.cached_tokens) };
}

export async function generateRun(run, credential, trustedBase) {
  const route = run.protocol === 'responses' ? 'responses' : 'chat/completions';
  const endpoint = await resolveEndpoint(credential.base_url, route, credential.trusted ? trustedBase : '');
  let body;
  if (run.protocol === 'responses') {
    body = { model: run.model, input: [{ role: 'user', content: [{ type: 'input_text', text: run.prompt }] }], stream: false, store: false, max_output_tokens: run.max_tokens };
    if (run.reasoning !== 'default') body.reasoning = { effort: run.reasoning };
  } else {
    body = { model: run.model, messages: [{ role: 'user', content: run.prompt }], stream: false, max_tokens: run.max_tokens };
    if (run.reasoning !== 'default') body.reasoning_effort = run.reasoning;
  }
  const data = await requestJSON(endpoint, { key: credential.key, body });
  if (data.error) throw new CheckError('upstream_error', 502);
  if (data.status === 'incomplete' || data.choices?.[0]?.finish_reason === 'length') throw new CheckError('truncated_output', 502);
  let html = data.output_text || data.output?.flatMap(item => item.content || []).filter(c => c.type === 'output_text').map(c => c.text).join('');
  if (run.protocol === 'chat') {
    const content = data.choices?.[0]?.message?.content;
    html = typeof content === 'string' ? content : content?.map(item => item.text || '').join('');
  }
  if (!html || !/<(?:!doctype|html|svg|body)\b/i.test(html)) throw new CheckError('missing_html', 502);
  return { html, usage: usageOf(data) };
}

export function createService(config, dependencies = {}) {
  mkdirSync(config.dataDir, { recursive: true, mode: 0o700 });
  const store = dependencies.store || new Store(join(config.dataDir, 'model-check.sqlite'));
  const vault = secretVault(config.dataDir);
  const render = dependencies.render || renderArtwork;
  const generate = dependencies.generate || generateRun;
  const queue = []; const busyChannels = new Set(); const runningOwners = new Set();
  let running = 0; let closing = false; let lastDemo = 0;

  async function backend(req, route, adminKey = false) {
    const token = adminKey ? String(req.headers['x-api-key'] || '') : String(req.headers.authorization || '').match(/^Bearer ([^\s]{1,4096})$/)?.[1];
    if (!token || !/^[^\s]{1,4096}$/.test(token)) throw new CheckError(adminKey ? 'invalid_admin_key' : 'login_required', 401);
    const base = `${config.backend.replace(/\/$/, '')}/api/v1`;
    const endpoint = await resolveEndpoint(base, route, base);
    const headers = { 'User-Agent': String(req.headers['user-agent'] || '').slice(0, 500), ...(adminKey ? { 'x-api-key': token } : {}) };
    try { return await requestJSON(endpoint, { method: 'GET', key: adminKey ? '' : token, timeout: 6000, headers }); }
    catch (error) { if (error.code?.startsWith('upstream_auth')) throw newError401(adminKey); throw new CheckError('account_service_unavailable', 503); }
  }
  const authenticate = dependencies.authenticate || (async req => {
    const result = await backend(req, 'auth/me');
    const user = result.data;
    if (result.code !== 0 || !Number.isSafeInteger(user?.id) || user.id < 1 || !['admin', 'user'].includes(user.role)) throw newError401();
    return { id: user.id, role: user.role };
  });
  function newError401(adminKey = false) { return new CheckError(adminKey ? 'invalid_admin_key' : 'login_required', 401); }
  async function authenticateAdmin(req) {
    if (req.headers['x-api-key'] !== undefined) {
      // Verify with Sub2API, sending the credential only to the configured account service.
      // Never treat a key prefix as proof, cache the key, or forward it to a model provider.
      const verified = await backend(req, 'admin/settings/admin-api-key', true);
      if (verified.code !== 0 || verified.data?.exists !== true) throw newError401(true);
      // This global credential has no user ownership. It can inspect monitoring records,
      // but must not impersonate the owner of a private personal test.
      return { id: null, role: 'admin', auth_method: 'admin_api_key' };
    }
    const user = await authenticate(req);
    if (user.role !== 'admin') throw new CheckError('admin_required', 403);
    return user;
  }

  async function credentials(req, input, user, previous) {
    const externalGroup = input.group_name === undefined ? previous?.group_name || null : String(input.group_name || '').trim().slice(0, 120) || null;
    if (previous && !input.key && input.key_source === 'external' && previous.key_source === 'external' && normalizeBase(input.base_url) === previous.base_url) {
      return { key: vault.decrypt(previous.encrypted_key), base_url: previous.base_url, trusted: false, group_id: null, group_name: externalGroup };
    }
    if (input.key_source === 'existing') {
      if (!Number.isSafeInteger(Number(input.key_id)) || Number(input.key_id) < 1) throw new CheckError('key_required');
      const result = await backend(req, `keys/${Number(input.key_id)}`);
      if (result.code !== 0 || result.data?.user_id !== user.id || result.data?.status !== 'active' || !result.data?.key) throw new CheckError('key_unavailable');
      const groupId = result.data.group_id ?? result.data.group?.id;
      return { key: result.data.key, base_url: normalizeBase(config.siteApiBase), trusted: true,
        group_id: Number.isSafeInteger(groupId) && groupId > 0 ? groupId : null,
        group_name: typeof result.data.group?.name === 'string' ? result.data.group.name.trim().slice(0, 120) : null };
    }
    if (input.key_source !== 'external') throw new CheckError('invalid_key_source');
    const key = String(input.key || '').trim();
    if (!key || key.length > 4096 || /[\r\n]/.test(key)) throw new CheckError('key_required');
    const base_url = normalizeBase(input.base_url);
    await resolveEndpoint(base_url, 'responses'); // Validate on save as well as on every scheduled request.
    return { key, base_url, trusted: false, group_id: null, group_name: externalGroup };
  }

  function assertCapacity(userId, channelId) {
    if (closing || queue.length + running >= 8) throw new CheckError('queue_full', 429);
    if (channelId ? busyChannels.has(channelId) : runningOwners.has(userId)) throw new CheckError('already_running', 409);
  }
  function enqueue(run, credential, demoHtml) {
    if (run.channel_id) busyChannels.add(run.channel_id); else runningOwners.add(run.owner_id);
    queue.push({ run, credential, demoHtml });
    drain();
  }
  function drain() {
    while (!closing && running < 2 && queue.length) {
      const job = queue.shift(); running++;
      execute(job).finally(() => {
        job.credential = null;
        running--;
        if (job.run.channel_id) busyChannels.delete(job.run.channel_id); else runningOwners.delete(job.run.owner_id);
        drain();
      });
    }
  }
  async function execute(job) {
    const start = Date.now(); const { run } = job;
    try {
      store.updateRun(run.id, { status: 'generating' });
      const result = job.demoHtml ? { html: job.demoHtml, usage: null } : await generate(run, job.credential, config.siteApiBase);
      const generation_ms = job.demoHtml ? null : Date.now() - start;
      job.credential = null;
      store.updateRun(run.id, { status: 'rendering', generation_ms, usage: result.usage });
      const artifact = await render(result.html);
      const channel = run.channel_id ? store.channel(run.channel_id) : null;
      const baseline = channel?.baseline_id ? store.run(channel.baseline_id) : null;
      const compatible = baseline && baseline.prompt_hash === run.prompt_hash && baseline.model === run.model && baseline.reasoning === run.reasoning && baseline.max_tokens === run.max_tokens && baseline.protocol === run.protocol && baseline.group_id === run.group_id && baseline.key_source === run.key_source;
      const assessment = assess(artifact.metrics, baseline?.assessment, compatible);
      store.updateRun(run.id, { ...artifact, assessment, status: assessment.verdict, total_ms: Date.now() - start, finished_at: Date.now() });
    } catch (error) {
      // Only stable error codes are retained, never response bodies, credentials or stack traces.
      const code = error instanceof CheckError ? error.code : ['html_too_large', 'too_many_nodes', 'empty_html', 'render_timeout', 'render_unavailable'].includes(error.message) ? error.message : 'test_failed';
      store.updateRun(run.id, { status: 'failed', error: code, total_ms: Date.now() - start, finished_at: Date.now() });
    } finally { store.prune(); }
  }
  function channelRun(channel, source, options = validateOptions(channel), request = null) {
    assertCapacity(channel.owner_id, channel.id);
    const credential = { key: vault.decrypt(channel.encrypted_key), base_url: channel.base_url, trusted: channel.key_source === 'existing' };
    const run = store.createRun({ owner_id: channel.owner_id, channel_id: channel.id, source, ...options, ...makeScheduledPrompt({ ...channel, ...options }),
      key_source: channel.key_source, group_id: channel.group_id ?? null, group_name: channel.group_name || null, label: channel.name }, request);
    enqueue(run, credential);
    return run;
  }
  function tick(now = Date.now()) {
    if (closing) return;
    for (const channel of store.channels()) {
      if (channel.demo || !channel.enabled || channel.next_run > now || busyChannels.has(channel.id) || queue.length + running >= 8) continue;
      store.setNextRun(channel.id, now + channel.interval_minutes * 60_000);
      try { channelRun(channel, 'scheduled'); } catch { /* Key errors are handled in the job; a busy tick waits for the next interval. */ }
    }
  }
  const timer = setInterval(tick, 10_000); timer.unref();

  const server = http.createServer(async (req, res) => {
    try {
      const url = new URL(req.url, 'http://localhost');
      const path = url.pathname;
      if (!path.startsWith(PREFIX + '/')) throw new CheckError('not_found', 404);
      const route = path.slice(PREFIX.length);
      if (req.method === 'GET' && route === '/overview') {
        const supported = record => TOPICS.some(topic => topic.id === record.topic);
        const channels = store.channels().filter(c => c.public && supported(c));
        const visibleRuns = store.listRuns({ limit: 600 }).filter(r => supported(r) && store.canRead(r, null));
        const summaries = channels.map(c => publicChannel(c, visibleRuns.filter(r => c.demo ? r.source === 'demo' && r.topic === c.topic : r.channel_id === c.id)));
        const works = visibleRuns.filter(r => !activeStatuses.includes(r.status)).slice(0, 30);
        response(res, 200, { demo: config.demo, topics: TOPICS, channels: summaries, works: works.map(r => publicRun(r)), retention: 30, method: 'render-rules-v1', busy: running, updated_at: Date.now() }); return;
      }
      if (req.method === 'GET' && route === '/health') { response(res, 200, { status: 'ok', demo: config.demo, running, queued: queue.length }); return; }
      if (req.method === 'POST' && route === '/demo') {
        if (!config.demo) throw new CheckError('not_found', 404);
        if (Date.now() - lastDemo < 10_000) throw new CheckError('demo_cooldown', 429);
        assertCapacity(0, null);
        const input = await readBody(req); const prompt = makePrompt(input.topic || 'pelican');
        lastDemo = Date.now();
        const run = store.createRun({ source: 'demo', ...validateOptions({ model: 'Local example', topic: prompt.topic }), ...prompt, label: '本地示例' });
        enqueue(run, null, sampleHTML(prompt.topic));
        response(res, 202, publicRun(run)); return;
      }
      // Public records can be inspected without a session. Every private record needs current auth.
      const runMatch = route.match(/^\/runs\/([a-f0-9-]{36})$/);
      if (req.method === 'GET' && runMatch) {
        const run = store.run(runMatch[1]);
        let user = null;
        if (!store.canRead(run, null)) user = await authenticate(req);
        if (!store.canRead(run, user)) throw new CheckError('not_found', 404);
        response(res, 200, publicRun(run, true)); return;
      }
      // Machine-facing API: use the existing global Admin API Key (x-api-key),
      // or an administrator's current browser session. All reads recheck auth.
      const apiRunMatch = route.match(/^\/admin\/runs\/([a-f0-9-]{36}|latest)(\/image)?$/);
      if (route === '/admin/tests' || route === '/admin/runs' || apiRunMatch || (route === '/admin/channels' && req.method === 'GET')) {
        const admin = await authenticateAdmin(req);
        if (route === '/admin/channels' && req.method === 'GET') {
          response(res, 200, store.channels().filter(c => !c.demo).map(c => publicChannel(c, store.listRuns({ channel: c.id, limit: 60 }), true))); return;
        }
        if (route === '/admin/tests' && req.method === 'POST') {
          const input = await readBody(req);
          if (typeof input.channel_id !== 'string' || !/^[a-f0-9-]{36}$/.test(input.channel_id)) throw new CheckError('channel_required');
          const channel = store.channel(input.channel_id);
          if (!channel || channel.demo) throw new CheckError('not_found', 404);
          const prepared = prepareAPITest(input, channel, req.headers['idempotency-key']);
          const existing = prepared.request ? store.request(prepared.request.key) : null;
          if (existing && existing.fingerprint !== prepared.request.fingerprint) throw new CheckError('idempotency_conflict', 409);
          const accepted = existing ? store.run(existing.run_id) : channelRun(channel, 'manual', prepared.options, prepared.request);
          if (!accepted) throw new CheckError('result_expired', 410);
          const run = await waitForResult(store, accepted.id, prepared.waitMs, res);
          if (res.destroyed) return;
          const result = apiRun(run);
          res.setHeader('Location', result.result_url);
          if (!result.completed) res.setHeader('Retry-After', '2');
          response(res, result.completed ? 200 : 202, result); return;
        }
        if (req.method === 'GET' && (route === '/admin/runs' || apiRunMatch?.[1] === 'latest')) {
          const channelId = url.searchParams.get('channel_id');
          if (channelId !== null && (!/^[a-f0-9-]{36}$/.test(channelId) || !store.channel(channelId))) throw new CheckError('not_found', 404);
          const limit = Number(url.searchParams.get('limit') ?? 30);
          if (!Number.isInteger(limit) || limit < 1 || limit > 60) throw new CheckError('invalid_limit');
          const runs = store.listRuns({ ...(channelId ? { channel: channelId } : {}), limit: 600 }).filter(run => store.canRead(run, admin));
          if (route === '/admin/runs') { response(res, 200, runs.slice(0, limit).map(run => apiRun(run, false))); return; }
          const run = runs.find(isFinished);
          if (!run) throw new CheckError('not_found', 404);
          if (apiRunMatch[2]) { sendImage(res, run); return; }
          response(res, 200, apiRun(run)); return;
        }
        if (req.method === 'GET' && apiRunMatch) {
          const run = store.run(apiRunMatch[1]);
          if (!store.canRead(run, admin)) throw new CheckError('not_found', 404);
          if (apiRunMatch[2]) { sendImage(res, run); return; }
          response(res, 200, apiRun(run)); return;
        }
        throw new CheckError('not_found', 404);
      }
      const user = await authenticate(req);
      if (route === '/mine' && req.method === 'GET') { response(res, 200, store.listRuns({ owner: user.id, limit: 60 }).filter(r => !r.channel_id).map(r => publicRun(r))); return; }
      if (route === '/keys' && req.method === 'GET') {
        const result = await backend(req, 'keys?page=1&page_size=100&status=active');
        if (result.code !== 0) throw new CheckError('key_unavailable');
        response(res, 200, (result.data?.items || []).filter(k => k.user_id === user.id && k.status === 'active').map(k => ({ id: k.id, name: k.name, group_name: k.group?.name || '' }))); return;
      }
      if (route === '/tests' && req.method === 'POST') {
        assertCapacity(user.id, null);
        const input = await readBody(req); const options = validateOptions(input);
        const credential = await credentials(req, input, user);
        // Recheck after credential validation awaits, preventing concurrent submissions from racing.
        assertCapacity(user.id, null);
        const run = store.createRun({ owner_id: user.id, source: 'self', ...options, ...makePrompt(options.topic),
          key_source: input.key_source, group_id: credential.group_id, group_name: credential.group_name, label: '个人自测' });
        enqueue(run, credential); response(res, 202, publicRun(run)); return;
      }
      if (!route.startsWith('/admin/')) throw new CheckError('not_found', 404);
      if (user.role !== 'admin') throw new CheckError('admin_required', 403);
      const channelMatch = route.match(/^\/admin\/channels(?:\/([a-f0-9-]{36})(?:\/(run|baseline))?)?$/);
      if (!channelMatch) throw new CheckError('not_found', 404);
      const id = channelMatch[1]; const action = channelMatch[2]; const previous = id ? store.channel(id) : null;
      if (id && (!previous || previous.demo)) throw new CheckError('not_found', 404);
      if (action === 'run' && req.method === 'POST') { response(res, 202, publicRun(channelRun(previous, 'manual'))); return; }
      if (action === 'baseline' && req.method === 'POST') {
        const input = await readBody(req); const run = store.run(input.run_id);
        const expected = run && makePrompt(previous.topic, previous.topic === 'creative' ? run.seed : previous.seed);
        if (!run || run.channel_id !== id || run.status !== 'normal' || run.topic !== previous.topic || run.prompt_hash !== expected.prompt_hash || run.model !== previous.model || run.reasoning !== previous.reasoning || run.max_tokens !== previous.max_tokens || run.protocol !== previous.protocol || run.group_id !== previous.group_id || run.key_source !== previous.key_source) throw new CheckError('invalid_baseline');
        store.setBaseline(id, run.id); response(res, 200, { saved: true }); return;
      }
      if ((!id && req.method === 'POST') || (id && !action && req.method === 'PUT')) {
        const input = await readBody(req); const options = validateOptions(input);
        const name = String(input.name || '').trim();
        const interval = Number(input.interval_minutes);
        if (!name || name.length > 60) throw new CheckError('invalid_name');
        if (!Number.isInteger(interval) || interval < 5 || interval > 1440) throw new CheckError('invalid_interval');
        if (typeof input.enabled !== 'boolean' || typeof input.public !== 'boolean') throw new CheckError('invalid_flags');
        if (!id && store.channels().filter(c => !c.demo).length >= 20) throw new CheckError('channel_limit');
        if (id && busyChannels.has(id)) throw new CheckError('already_running', 409);
        const credential = await credentials(req, input, user, previous);
        const config = { ...options, name, interval_minutes: interval, enabled: input.enabled, public: input.public, base_url: credential.base_url, key_source: input.key_source, key_id: input.key_source === 'existing' ? Number(input.key_id) : null,
          group_id: credential.group_id, group_name: credential.group_name, seed: previous?.seed || 20260923 };
        const channel = store.saveChannel(config, user.id, vault.encrypt(credential.key), id);
        response(res, 200, publicChannel(channel, [], true)); return;
      }
      throw new CheckError('not_found', 404);
    } catch (error) {
      const validationCodes = ['invalid_model', 'invalid_topic', 'invalid_protocol', 'invalid_reasoning', 'invalid_token_limit'];
      response(res, error instanceof CheckError ? error.status : validationCodes.includes(error.message) ? 400 : 500, error instanceof CheckError ? error.code : validationCodes.includes(error.message) ? error.message : 'internal_error');
    }
  });
  server.requestTimeout = 15_000; server.headersTimeout = 10_000;

  async function seedDemo() {
    if (!config.demo) return;
    for (const [topic, name, weak] of [['pelican', '鹈鹕骑行 · 示例', false], ['creative', '随机创作 · 示例', false]]) {
      if (store.channels().some(c => c.demo && c.topic === topic)) continue;
      const channel = store.saveChannel({ ...validateOptions({ model: 'Local example', topic }), name, interval_minutes: 60, public: true, enabled: false, demo: true, seed: 20260923 }, 0, '');
      const run = store.createRun({ channel_id: channel.id, source: 'demo', ...validateOptions(channel), ...makePrompt(topic, channel.seed), label: name });
      enqueue(run, null, sampleHTML(topic, weak));
    }
  }
  return { server, store, tick, seedDemo, async close() {
    closing = true; clearInterval(timer); await new Promise(resolveClose => server.close(resolveClose));
    // Finish accepted calls before closing the SQLite file. A hard shutdown is recovered as failed.
    while (running) await new Promise(r => setTimeout(r, 50));
    for (const job of queue) { job.credential = null; store.updateRun(job.run.id, { status: 'failed', error: 'service_restarted', finished_at: Date.now() }); }
    store.close();
  } };
}

if (import.meta.url === pathToFileURL(process.argv[1] || '').href) {
  const config = { dataDir: resolve(process.env.MODEL_CHECK_DATA_DIR || '.data'), backend: process.env.SUB2API_BACKEND || 'http://127.0.0.1:8080', siteApiBase: process.env.MODEL_CHECK_SITE_API_BASE || 'http://127.0.0.1:8080/v1', demo: process.env.MODEL_CHECK_DEMO === '1' };
  const host = process.env.MODEL_CHECK_HOST || '127.0.0.1';
  if (config.demo && !['127.0.0.1', '::1', 'localhost'].includes(host)) throw new Error('Demo mode must bind to loopback.');
  const app = createService(config);
  app.server.listen(Number(process.env.MODEL_CHECK_PORT || 8096), host, async () => { console.log('Lumivia model check listening on loopback port ' + (process.env.MODEL_CHECK_PORT || 8096)); await app.seedDemo(); });
  for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => app.close().then(() => process.exit(0)));
}
