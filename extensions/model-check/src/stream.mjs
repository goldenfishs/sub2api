import { StringDecoder } from 'node:string_decoder';
import { usageOf } from './usage.mjs';

// Keep only output text and usage, never arbitrary upstream error bodies.
// Errors are stable codes converted to CheckError at the transport boundary.
export function createModelStream(protocol) {
  const decoder = new StringDecoder('utf8');
  const parts = new Map();
  let buffer = '', data = [], event = '', textBytes = 0, eventBytes = 0;
  let chatText = '', finishReason = null, usage = null, result = null;
  const fail = (code, rawUsage = null) => {
    const error = new Error(code);
    if (code === 'truncated_output' && rawUsage) error.usage = usageOf({ usage: rawUsage });
    throw error;
  };
  const incomplete = response => {
    const reason = response?.incomplete_details?.reason;
    // Legacy compatible providers omit the reason. Explicit non-token reasons
    // must not trigger another billed generation with a higher output limit.
    fail(reason == null || reason === 'max_output_tokens' ? 'truncated_output' : 'upstream_error', response?.usage);
  };
  const text = value => {
    if (typeof value !== 'string') fail('invalid_upstream_response');
    textBytes += Buffer.byteLength(value);
    if (textBytes > 2_000_000) fail('response_too_large');
    return value;
  };
  const accumulated = () => [...parts.entries()]
    .sort(([a], [b]) => a[0] - b[0] || a[1] - b[1]).map(([, value]) => value).join('');
  function part(value, replace = false) {
    const index = value.output_index ?? 0, content = value.content_index ?? 0;
    if (!Number.isSafeInteger(index) || index < 0 || !Number.isSafeInteger(content) || content < 0) fail('invalid_upstream_response');
    const key = [...parts.keys()].find(k => k[0] === index && k[1] === content) || [index, content];
    const previous = parts.get(key) || '';
    if (replace) textBytes -= Buffer.byteLength(previous);
    parts.set(key, (replace ? '' : previous) + text(replace ? value.text : value.delta));
  }
  function dispatch() {
    const payload = data.join('\n'); const name = event;
    data = []; event = ''; eventBytes = 0;
    if (!payload || result) return;
    if (payload.trim() === '[DONE]') {
      if (protocol !== 'chat' || finishReason !== 'stop') fail('upstream_disconnected');
      result = { choices: [{ message: { content: chatText }, finish_reason: finishReason }], usage };
      return;
    }
    let value;
    try { value = JSON.parse(payload); } catch { fail('invalid_upstream_response'); }
    if (!value || typeof value !== 'object' || Array.isArray(value)) fail('invalid_upstream_response');
    const type = value.type || name;
    if (value.error || type === 'error' || ['response.failed', 'response.cancelled'].includes(type)) fail('upstream_error');
    if (type === 'response.incomplete') incomplete(value.response);
    if (protocol === 'responses') {
      if (type === 'response.output_text.delta') part(value);
      else if (type === 'response.output_text.done') part(value, true);
      else if (type === 'response.completed') {
        const response = value.response;
        if (!response || typeof response !== 'object') fail('invalid_upstream_response');
        if (response.status === 'incomplete') incomplete(response);
        if (response.error || (response.status && response.status !== 'completed')) fail('upstream_error');
        const output = response.output_text || response.output?.flatMap(item => item.content || [])
          .filter(item => item.type === 'output_text').map(item => item.text || '').join('') || accumulated();
        if (typeof output !== 'string') fail('invalid_upstream_response');
        if (Buffer.byteLength(output) > 2_000_000) fail('response_too_large');
        result = { output_text: output, status: 'completed', usage: response.usage ?? usage };
      }
    } else {
      if (value.usage) usage = value.usage;
      const choice = value.choices?.find(c => c.index === 0 || c.index === undefined);
      if (!choice) return; // Usage-only final chunks have an empty choices array.
      if (choice.delta?.content != null) chatText += text(choice.delta.content);
      if (choice.finish_reason != null) {
        finishReason = choice.finish_reason;
        if (finishReason === 'length') fail('truncated_output', usage);
        if (finishReason !== 'stop') fail('upstream_error');
      }
    }
  }
  function line(value) {
    if (!value) { dispatch(); return; }
    if (value.startsWith(':')) return;
    const colon = value.indexOf(':');
    const field = colon < 0 ? value : value.slice(0, colon);
    let content = colon < 0 ? '' : value.slice(colon + 1);
    if (content.startsWith(' ')) content = content.slice(1);
    if (field === 'data') {
      eventBytes += Buffer.byteLength(content) + 1;
      if (eventBytes > 2_000_000) fail('response_too_large');
      data.push(content);
    } else if (field === 'event') event = content;
  }
  function parse(final = false) {
    while (!result) {
      const match = /\r\n|\r|\n/.exec(buffer);
      if (!match || (!final && match[0] === '\r' && match.index === buffer.length - 1)) break;
      line(buffer.slice(0, match.index));
      buffer = buffer.slice(match.index + match[0].length);
    }
    if (Buffer.byteLength(buffer) > 2_000_000) fail('response_too_large');
  }
  return {
    get done() { return result !== null; },
    write(chunk) { buffer += decoder.write(chunk); parse(); },
    end() {
      if (!result) {
        buffer += decoder.end(); parse(true);
        if (buffer) { line(buffer); buffer = ''; }
        dispatch();
      }
      if (!result) fail('upstream_disconnected');
      return result;
    },
  };
}
