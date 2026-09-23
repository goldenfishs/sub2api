import { createHash } from 'node:crypto';
import { CheckError } from './security.mjs';
import { validateOptions } from './prompts.mjs';
import { publicRun } from './store.mjs';

export const API_PREFIX = '/api/v1/model-check';
export const isFinished = run => ['normal', 'review', 'failed'].includes(run?.status);
const hash = value => createHash('sha256').update(value).digest('hex');
const optionFields = ['model', 'topic', 'protocol', 'reasoning', 'max_tokens'];

export function prepareAPITest(input, channel, requestKey) {
  const allowed = ['channel_id', 'wait_seconds', ...optionFields];
  if (Object.keys(input).some(key => !allowed.includes(key))) throw new CheckError('invalid_request');
  const wait = input.wait_seconds ?? 0;
  if (!Number.isInteger(wait) || wait < 0 || wait > 120) throw new CheckError('invalid_wait_seconds');
  const overrides = Object.fromEntries(optionFields.filter(field => input[field] !== undefined).map(field => [field, input[field]]));
  const options = validateOptions({ ...channel, ...overrides });
  let request = null;
  if (requestKey !== undefined) {
    if (typeof requestKey !== 'string' || !/^[A-Za-z0-9._:-]{1,128}$/.test(requestKey)) throw new CheckError('invalid_idempotency_key');
    // The header itself and all credentials stay out of the idempotency table.
    request = { key: hash(requestKey), fingerprint: hash(JSON.stringify({
      channel_id: channel.id, ...options, seed: channel.seed, base_url: channel.base_url,
      key_source: channel.key_source, key_id: channel.key_id, group_id: channel.group_id,
      group_name: channel.group_name, credential_version: channel.encrypted_key,
    })) };
  }
  return { options, waitMs: wait * 1000, request };
}

export function apiRun(run, detail = true) {
  const resultURL = `${API_PREFIX}/admin/runs/${run.id}`;
  return { ...publicRun(run, detail), completed: isFinished(run), result_url: resultURL,
    image_url: run.image ? `${resultURL}/image` : null,
    ...(!isFinished(run) ? { poll_after_ms: 2000 } : {}) };
}

export function waitForResult(store, id, waitMs, response) {
  if (!waitMs || isFinished(store.run(id)) || response.destroyed) return Promise.resolve(store.run(id));
  return new Promise(resolve => {
    const finish = () => {
      clearInterval(interval); clearTimeout(timeout); response.off('close', finish);
      resolve(store.run(id));
    };
    const interval = setInterval(() => { if (isFinished(store.run(id))) finish(); }, 100);
    const timeout = setTimeout(finish, waitMs);
    response.once('close', finish);
  });
}

export function sendImage(response, run) {
  if (!isFinished(run)) throw new CheckError('result_not_ready', 409);
  if (!run.image) throw new CheckError('image_unavailable', 404);
  const bytes = Buffer.from(run.image, 'base64');
  response.writeHead(200, { 'Content-Type': 'image/webp', 'Content-Length': bytes.length,
    'Content-Disposition': `inline; filename="model-check-${run.id}.webp"`,
    'Cache-Control': 'private, no-store', 'X-Content-Type-Options': 'nosniff', 'Referrer-Policy': 'no-referrer' });
  response.end(bytes);
}
