import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { setImmediate as nextTurn } from 'node:timers/promises';
import { generateRun } from '../src/server.mjs';
import { CheckError, requestJSON } from '../src/security.mjs';
import { generateWithRetry } from '../src/retry.mjs';

const html = '<html><svg><text>鹈鹕骑行</text></svg></html>';
const secret = 'fixture-stream-key-must-not-leak';
const run = { model: 'fixture-stream-model', protocol: 'responses', reasoning: 'default', max_tokens: 8000, prompt: 'Local stream fixture' };
const event = data => `data: ${JSON.stringify(data)}\n\n`;
const delta = text => ({ type: 'response.output_text.delta', item_id: 'msg_1', output_index: 0, content_index: 0, delta: text });
const completed = (text = html) => ({ type: 'response.completed', response: { status: 'completed', output: [{ id: 'msg_1', type: 'message', content: [{ type: 'output_text', text }] }], usage: { input_tokens: 11, output_tokens: 17, input_tokens_details: { cached_tokens: 3 } } } });
const chat = (content, finish = null) => ({ choices: [{ index: 0, delta: { content }, finish_reason: finish }] });

async function upstream(t, handler) {
  const seen = [];
  const server = http.createServer(async (req, res) => {
    try {
      const chunks = [];
      for await (const chunk of req) chunks.push(chunk);
      seen.push({ path: req.url, headers: req.headers, body: chunks.length ? JSON.parse(Buffer.concat(chunks)) : null });
      await handler(req, res);
    } catch (error) { res.destroy(error); }
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise(resolve => { server.close(resolve); server.closeAllConnections(); }));
  const base = `http://127.0.0.1:${server.address().port}/v1`;
  return {
    seen,
    generate: (protocol = 'responses', options = {}) => generateRun({ ...run, ...options, protocol }, { key: secret, base_url: base, trusted: true }, base),
    request: options => requestJSON({ url: new URL(base + '/responses'), address: { address: '127.0.0.1', family: 4 } }, { body: {}, streamProtocol: 'responses', ...options }),
  };
}

function sse(res) { res.writeHead(200, { 'Content-Type': 'text/event-stream; charset=utf-8' }); }
function safeFailure(code) {
  return error => {
    assert.ok(error instanceof CheckError);
    assert.equal(error.code, code);
    assert.equal(error.message, code);
    assert.ok(!JSON.stringify(error).includes(secret));
    return true;
  };
}

test('Responses streams reassemble split UTF-8, CRLF and multiline data without duplicating final output', async t => {
  const source = ': heartbeat\r\n\r\nevent: response.created\r\ndata: {"type":"response.created",\r\ndata: "response":{"status":"in_progress"}}\r\n\r\n' +
    event(delta('<html><svg><text>鹈鹕')).replaceAll('\n', '\r\n') +
    event(delta('骑行</text></svg></html>')).replaceAll('\n', '\r\n') +
    event({ type: 'response.output_text.done', text: html, item_id: 'msg_1', output_index: 0, content_index: 0 }).replaceAll('\n', '\r\n') +
    event(completed()).replaceAll('\n', '\r\n');
  const fixture = await upstream(t, async (_req, res) => {
    sse(res);
    const bytes = Buffer.from(source);
    // A write on every byte exercises CR/LF, JSON and multibyte character boundaries.
    for (const byte of bytes) { res.write(Buffer.from([byte])); await nextTurn(); }
    res.end();
  });
  const result = await fixture.generate();
  assert.equal(result.html, html);
  assert.deepEqual(result.usage, { input_tokens: 11, output_tokens: 17, cached_tokens: 3 });
  assert.equal(fixture.seen[0].path, '/v1/responses');
  assert.equal(fixture.seen[0].body.stream, true);
  assert.equal(fixture.seen[0].body.store, false);
  assert.equal(fixture.seen[0].headers.authorization, `Bearer ${secret}`);
});

test('Responses can use accumulated deltas when the completed response omits its output', async t => {
  const fixture = await upstream(t, (_req, res) => { sse(res); res.end(event(delta(html)) + event({ type: 'response.completed', response: { status: 'completed', usage: { output_tokens: 7 } } })); });
  const result = await fixture.generate();
  assert.equal(result.html, html);
  assert.equal(result.usage.output_tokens, 7);
});

test('Chat streams collect content and trailing usage through finish and DONE', async t => {
  const fixture = await upstream(t, (_req, res) => {
    sse(res);
    res.end(': keepalive\n\n' + event({ choices: [{ index: 0, delta: { role: 'assistant' }, finish_reason: null }] }) +
      event(chat('<html><svg>')) + event(chat('<text>鹈鹕骑行</text></svg></html>')) +
      event(chat('', 'stop')) + event({ choices: [], usage: { prompt_tokens: 13, completion_tokens: 19, prompt_tokens_details: { cached_tokens: 4 } } }) + 'data: [DONE]\n\n');
  });
  const result = await fixture.generate('chat');
  assert.equal(result.html, html);
  assert.deepEqual(result.usage, { input_tokens: 13, output_tokens: 19, cached_tokens: 4 });
  assert.equal(fixture.seen[0].path, '/v1/chat/completions');
  assert.equal(fixture.seen[0].body.stream, true);
  assert.deepEqual(fixture.seen[0].body.stream_options, { include_usage: true });
});

for (const [type, expected] of [['response.incomplete', 'truncated_output'], ['response.failed', 'upstream_error'], ['error', 'upstream_error']]) {
  test(`Responses rejects ${type} without exposing upstream error details`, async t => {
    const fixture = await upstream(t, (_req, res) => {
      sse(res);
      const failure = type === 'response.incomplete'
        ? { type, response: { status: 'incomplete', incomplete_details: { reason: 'max_output_tokens' } } }
        : type === 'response.failed'
          ? { type, response: { status: 'failed', error: { message: secret, code: secret } } }
          : { type, message: secret, code: secret };
      res.end(event(delta(html)) + event(failure));
    });
    await assert.rejects(fixture.generate(), safeFailure(expected));
  });
}

for (const [reason, expected] of [['length', 'truncated_output'], ['content_filter', 'upstream_error']]) {
  test(`Chat rejects ${reason} despite receiving partial HTML`, async t => {
    const fixture = await upstream(t, (_req, res) => { sse(res); res.end(event(chat(html)) + event(chat('', reason)) + 'data: [DONE]\n\n'); });
    await assert.rejects(fixture.generate('chat'), safeFailure(expected));
  });
}

for (const type of ['response.incomplete', 'response.completed']) {
  for (const reason of ['max_output_tokens', undefined, 'content_filter', 'unexpected_non_token_reason']) {
    test(`${type} classifies ${reason ?? 'missing reason'} before deciding whether to raise the token limit`, async t => {
      const retryExpected = reason == null || reason === 'max_output_tokens';
      let calls = 0;
      const fixture = await upstream(t, (_req, res) => {
        sse(res);
        if (++calls > 1) { res.end(event(completed())); return; }
        const incomplete = { status: 'incomplete', ...(reason === undefined ? {} : { incomplete_details: { reason } }) };
        res.end(event(delta(html)) + event({ type, response: incomplete }));
      });
      let persisted = {};
      const pending = generateWithRetry({
        run, credential: {}, signal: new AbortController().signal,
        generate: options => fixture.generate('responses', options),
        update: fields => { persisted = { ...persisted, ...fields }; }, wait: async () => {},
      });
      if (retryExpected) {
        const result = await pending;
        assert.equal(result.html, html);
        assert.equal(result.effective_max_tokens, 16000);
        assert.deepEqual(fixture.seen.map(request => request.body.max_output_tokens), [8000, 16000]);
      } else {
        await assert.rejects(pending, safeFailure('upstream_error'));
        assert.equal(calls, 1, 'non-token incompletion must not cause another billed request');
        assert.equal(persisted.retry_max_tokens, null);
      }
      assert.ok(!JSON.stringify(persisted).includes(secret));
    });
  }
}

test('Chat stream errors never expose the upstream message or credential', async t => {
  const fixture = await upstream(t, (_req, res) => { sse(res); res.end(event({ error: { message: secret, code: secret } })); });
  await assert.rejects(fixture.generate('chat'), safeFailure('upstream_error'));
});

for (const protocol of ['responses', 'chat']) {
  test(`${protocol} rejects EOF without a success terminal even if HTML is present`, async t => {
    const fixture = await upstream(t, (_req, res) => { sse(res); res.end(event(protocol === 'responses' ? delta(html) : chat(html))); });
    await assert.rejects(fixture.generate(protocol), safeFailure('upstream_disconnected'));
  });
}

test('malformed SSE JSON is a sanitized upstream protocol error', async t => {
  const fixture = await upstream(t, (_req, res) => { sse(res); res.end(`data: {${secret}\n\n`); });
  await assert.rejects(fixture.generate(), safeFailure('invalid_upstream_response'));
});

test('SSE byte limits include comments and framing', async t => {
  const fixture = await upstream(t, (_req, res) => { sse(res); res.end((':' + 'x'.repeat(1024) + '\n\n').repeat(16_000) + event(completed())); });
  await assert.rejects(fixture.generate(), safeFailure('response_too_large'));
});

test('a single SSE data event cannot exceed two megabytes', async t => {
  const fixture = await upstream(t, (_req, res) => { sse(res); res.end(event(delta('x'.repeat(2_000_001)))); });
  await assert.rejects(fixture.generate(), safeFailure('response_too_large'));
});

for (const protocol of ['responses', 'chat']) {
  test(`${protocol} accumulated text cannot exceed two megabytes across smaller events`, async t => {
    const fixture = await upstream(t, (_req, res) => {
      sse(res);
      const content = 'x'.repeat(700_000);
      res.end(event(protocol === 'responses' ? delta(content) : chat(content)).repeat(3));
    });
    await assert.rejects(fixture.generate(protocol), safeFailure('response_too_large'));
  });
}

test('Responses output_text.done replaces its deltas instead of appending another copy', async t => {
  const fixture = await upstream(t, (_req, res) => {
    sse(res);
    res.end(event(delta(html)) + event({ type: 'response.output_text.done', item_id: 'msg_1', output_index: 0, content_index: 0, text: html }) +
      event({ type: 'response.completed', response: { status: 'completed' } }));
  });
  assert.equal((await fixture.generate()).html, html);
});

test('redirects are refused without issuing a second request or forwarding credentials', async t => {
  const fixture = await upstream(t, (_req, res) => { res.writeHead(307, { Location: '/must-not-follow' }); res.end(secret); });
  await assert.rejects(fixture.generate(), safeFailure('redirect_refused:307'));
  assert.equal(fixture.seen.length, 1);
});

test('a total deadline expires while heartbeat bytes continue to arrive', async t => {
  const fixture = await upstream(t, (_req, res) => {
    sse(res);
    res.write(': connected\n\n');
    const interval = setInterval(() => res.write(': heartbeat\n\n'), 10);
    res.on('close', () => clearInterval(interval));
  });
  const start = Date.now();
  await assert.rejects(fixture.request({ timeout: 120 }), safeFailure('upstream_timeout'));
  assert.ok(Date.now() - start < 1500, 'heartbeats must not reset the total request deadline');
});

test('a broken connection after streamed content is rejected instead of accepted as HTML', async t => {
  const fixture = await upstream(t, (_req, res) => {
    sse(res);
    res.write(event(delta(html)));
    const timer = setTimeout(() => res.destroy(), 20);
    res.on('close', () => clearTimeout(timer));
  });
  await assert.rejects(fixture.generate(), safeFailure('upstream_disconnected'));
});
