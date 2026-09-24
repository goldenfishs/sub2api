import { lookup } from 'node:dns/promises';
import http from 'node:http';
import https from 'node:https';
import { createCipheriv, createDecipheriv, createHash, createHmac, randomBytes, timingSafeEqual } from 'node:crypto';
import { readFileSync, writeFileSync, mkdirSync, chmodSync } from 'node:fs';
import { join } from 'node:path';
import ipaddr from 'ipaddr.js';
import { createModelStream } from './stream.mjs';

export class CheckError extends Error {
  constructor(code, status = 400) { super(code); this.code = code; this.status = status; }
}

// This protocol matches backend/internal/pkg/modelcheckbridge. A signature
// carries network identity only; account authorization always happens in Go.
export const BRIDGE_HEADER = 'x-lumivia-model-check-bridge';

export function validBridgeSecret(secret) {
  return typeof secret === 'string' && /^[\x21-\x7e]{32,256}$/.test(secret);
}

export function loopbackAddress(address) {
  try { return ipaddr.process(address).range() === 'loopback'; } catch { return false; }
}

export function loopbackOrigin(input) {
  try {
    const url = new URL(input);
    return url.protocol === 'http:' && !url.username && !url.password && url.href === url.origin + '/' &&
      (!url.pathname || url.pathname === '/') && loopbackAddress(url.hostname.replace(/^\[|\]$/g, ''));
  } catch { return false; }
}

function bridgeHeader(headers, name) {
  const value = Object.entries(headers || {}).find(([key]) => key.toLowerCase() === name)?.[1];
  return typeof value === 'string' ? value : '';
}

function bridgeCredentialHash(headers) {
  const hasAdminKey = Object.keys(headers || {}).some(name => name.toLowerCase() === 'x-api-key');
  const material = `${bridgeHeader(headers, 'authorization')}\0${bridgeHeader(headers, 'x-api-key')}${hasAdminKey ? '\0present' : ''}`;
  return createHash('sha256').update(Buffer.from(material, 'latin1')).digest('hex');
}

export function signBridge(secret, req, identity, now = Date.now()) {
  if (!validBridgeSecret(secret) || !identity || !ipaddr.isValid(identity.ip) || Buffer.byteLength(identity.userAgent || '', 'latin1') > 512 || req.url.length > 4096) {
    throw new CheckError('invalid_bridge_context', 503);
  }
  const payload = Buffer.from(JSON.stringify({ v: 1, ts: Math.floor(now / 1000), ip: identity.ip,
    ua: Buffer.from(identity.userAgent || '', 'latin1').toString('base64url'), auth: bridgeCredentialHash(req.headers), method: req.method, path: req.url })).toString('base64url');
  return `${payload}.${createHmac('sha256', secret).update(payload).digest('base64url')}`;
}

export function verifyBridge(secret, req, now = Date.now()) {
  if (!validBridgeSecret(secret) || !loopbackAddress(req.socket?.remoteAddress)) return null;
  const token = bridgeHeader(req.headers, BRIDGE_HEADER);
  if (token.length > 8192 || !/^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]{43}$/.test(token)) return null;
  const [payload, signature] = token.split('.');
  const expected = createHmac('sha256', secret).update(payload).digest();
  const actual = Buffer.from(signature, 'base64url');
  if (actual.length !== expected.length || !timingSafeEqual(actual, expected)) return null;
  try {
    const data = JSON.parse(Buffer.from(payload, 'base64url').toString('utf8'));
    const seconds = Math.floor(now / 1000);
    const auth = bridgeCredentialHash(req.headers);
    if (data.v !== 1 || !Number.isSafeInteger(data.ts) || data.ts < seconds - 30 || data.ts > seconds + 5 ||
        typeof data.ip !== 'string' || !ipaddr.isValid(data.ip) || data.method !== req.method || data.path !== req.url ||
        typeof data.auth !== 'string' || data.auth.length !== auth.length || !timingSafeEqual(Buffer.from(data.auth), Buffer.from(auth)) ||
        typeof data.ua !== 'string' || !/^[A-Za-z0-9_-]*$/.test(data.ua)) return null;
    const ua = Buffer.from(data.ua, 'base64url');
    if (ua.length > 512 || !ua.equals(Buffer.from(bridgeHeader(req.headers, 'user-agent'), 'latin1'))) return null;
    return { ip: data.ip, userAgent: ua.toString('latin1') };
  } catch { return null; }
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

export async function requestJSON(endpoint, { method = 'POST', key = '', body, timeout = 600_000, headers = {}, streamProtocol = null } = {}) {
  const { url, address } = endpoint;
  const payload = body === undefined ? null : JSON.stringify(body);
  return new Promise((resolve, reject) => {
    let settled = false;
    const finish = (error, value) => { if (settled) return; settled = true; clearTimeout(timer); error ? reject(error) : resolve(value); };
    const req = (url.protocol === 'https:' ? https : http).request(url, {
      method,
      headers: { 'Content-Type': 'application/json', 'Accept': streamProtocol ? 'text/event-stream, application/json' : 'application/json', 'Accept-Encoding': 'identity', ...(key ? { Authorization: `Bearer ${key}` } : {}), ...headers },
      lookup: (_host, options, done) => options.all ? done(null, [address]) : done(null, address.address, address.family),
      agent: false,
    }, res => {
      if (res.statusCode < 200 || res.statusCode >= 300) {
        const code = res.statusCode === 401 || res.statusCode === 403 ? 'upstream_auth' : res.statusCode === 429 ? 'upstream_limit' : res.statusCode >= 300 && res.statusCode < 400 ? 'redirect_refused' : 'upstream_http';
        finish(new CheckError(`${code}:${res.statusCode}`, 502));
        res.destroy(); req.destroy();
        return;
      }
      const streaming = streamProtocol && String(res.headers['content-type']).split(';')[0].trim().toLowerCase() === 'text/event-stream';
      const parser = streaming ? createModelStream(streamProtocol) : null;
      const streamError = error => new CheckError(['upstream_error', 'truncated_output', 'upstream_disconnected', 'response_too_large'].includes(error.message) ? error.message : 'invalid_upstream_response', 502);
      let size = 0; const chunks = [];
      res.on('data', chunk => {
        if (settled) return;
        size += chunk.length;
        if (size > (streaming ? 16_000_000 : 2_000_000)) { finish(new CheckError('response_too_large', 502)); req.destroy(); return; }
        if (parser) {
          try {
            parser.write(chunk);
            if (parser.done) { finish(null, parser.end()); req.destroy(); }
          } catch (error) { finish(streamError(error)); req.destroy(); }
        } else chunks.push(chunk);
      });
      res.on('aborted', () => finish(new CheckError('upstream_disconnected', 502)));
      res.on('error', () => finish(new CheckError('upstream_disconnected', 502)));
      res.on('end', () => {
        if (settled) return;
        try { finish(null, parser ? parser.end() : JSON.parse(Buffer.concat(chunks).toString('utf8'))); }
        catch (error) { finish(parser ? streamError(error) : new CheckError('invalid_upstream_response', 502)); }
      });
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
