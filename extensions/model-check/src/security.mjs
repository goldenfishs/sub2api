import { lookup } from 'node:dns/promises';
import http from 'node:http';
import https from 'node:https';
import { createCipheriv, createDecipheriv, randomBytes } from 'node:crypto';
import { readFileSync, writeFileSync, mkdirSync, chmodSync } from 'node:fs';
import { join } from 'node:path';
import ipaddr from 'ipaddr.js';

export class CheckError extends Error {
  constructor(code, status = 400) { super(code); this.code = code; this.status = status; }
}

export function publicAddress(address) {
  try {
    const parsed = ipaddr.process(address);
    return parsed.range() === 'unicast';
  } catch { return false; }
}

export function normalizeBase(input) {
  let url;
  try { url = new URL(input); } catch { throw new CheckError('invalid_base_url'); }
  if (url.username || url.password || url.search || url.hash || !['http:', 'https:'].includes(url.protocol)) throw new CheckError('invalid_base_url');
  let path = url.pathname.replace(/\/+$/, '').replace(/\/(responses|chat\/completions)$/, '');
  if (!path) path = '/v1';
  url.pathname = path;
  return url.toString().replace(/\/$/, '');
}

// Resolve once and pin the connection to a validated address. Redirects are never followed.
// Only the operator-configured local Sub2API origin can bypass the public-address check.
export async function resolveEndpoint(base, route, trustedBase = '', resolver = lookup) {
  const normalized = normalizeBase(base);
  const trusted = trustedBase && normalized === normalizeBase(trustedBase);
  const url = new URL(`${normalized}/${route}`);
  if (!trusted && url.protocol !== 'https:') throw new CheckError('https_required');
  if (!trusted && url.port && url.port !== '443') throw new CheckError('https_port_required');
  const host = url.hostname.replace(/^\[|\]$/g, '');
  if (!trusted && (host.endsWith('.localhost') || host.endsWith('.local') || host === 'localhost')) throw new CheckError('private_address');
  const addresses = await resolver(host, { all: true, verbatim: true }).catch(() => { throw new CheckError('dns_failed', 502); });
  if (!addresses.length || (!trusted && addresses.some(a => !publicAddress(a.address)))) throw new CheckError('private_address');
  return { url, address: addresses[0] };
}

export async function requestJSON(endpoint, { method = 'POST', key = '', body, timeout = 180_000, headers = {} } = {}) {
  const { url, address } = endpoint;
  const payload = body === undefined ? null : JSON.stringify(body);
  return new Promise((resolve, reject) => {
    let settled = false;
    const finish = (error, value) => { if (settled) return; settled = true; clearTimeout(timer); error ? reject(error) : resolve(value); };
    const req = (url.protocol === 'https:' ? https : http).request(url, {
      method,
      headers: { 'Content-Type': 'application/json', 'Accept': 'application/json', 'Accept-Encoding': 'identity', ...(key ? { Authorization: `Bearer ${key}` } : {}), ...headers },
      lookup: (_host, options, done) => options.all ? done(null, [address]) : done(null, address.address, address.family),
      agent: false,
    }, res => {
      if (res.statusCode < 200 || res.statusCode >= 300) {
        res.resume();
        const code = res.statusCode === 401 || res.statusCode === 403 ? 'upstream_auth' : res.statusCode === 429 ? 'upstream_limit' : res.statusCode >= 300 && res.statusCode < 400 ? 'redirect_refused' : 'upstream_http';
        finish(new CheckError(`${code}:${res.statusCode}`, 502));
        return;
      }
      let size = 0; const chunks = [];
      res.on('data', chunk => {
        size += chunk.length;
        if (size > 2_000_000) { finish(new CheckError('response_too_large', 502)); req.destroy(); return; }
        chunks.push(chunk);
      });
      res.on('aborted', () => finish(new CheckError('upstream_disconnected', 502)));
      res.on('error', () => finish(new CheckError('upstream_disconnected', 502)));
      res.on('end', () => { try { finish(null, JSON.parse(Buffer.concat(chunks).toString('utf8'))); } catch { finish(new CheckError('invalid_upstream_response', 502)); } });
    });
    const timer = setTimeout(() => { finish(new CheckError('upstream_timeout', 504)); req.destroy(); }, timeout);
    req.on('error', () => finish(new CheckError('connection_failed', 502)));
    req.end(payload);
  });
}

export function secretVault(directory) {
  mkdirSync(directory, { recursive: true, mode: 0o700 });
  const path = join(directory, 'encryption.key');
  let key;
  try { key = readFileSync(path); } catch (e) {
    if (e.code !== 'ENOENT') throw e;
    key = randomBytes(32); writeFileSync(path, key, { mode: 0o600, flag: 'wx' });
  }
  if (key.length !== 32) throw new Error('Invalid encryption key');
  chmodSync(path, 0o600);
  return {
    encrypt(value) {
      const iv = randomBytes(12); const cipher = createCipheriv('aes-256-gcm', key, iv);
      const encrypted = Buffer.concat([cipher.update(value, 'utf8'), cipher.final()]);
      return Buffer.concat([iv, cipher.getAuthTag(), encrypted]).toString('base64');
    },
    decrypt(value) {
      const data = Buffer.from(value, 'base64'); const cipher = createDecipheriv('aes-256-gcm', key, data.subarray(0, 12));
      cipher.setAuthTag(data.subarray(12, 28));
      return Buffer.concat([cipher.update(data.subarray(28)), cipher.final()]).toString('utf8');
    },
  };
}
