import { JSDOM } from 'jsdom';
import createDOMPurify from 'dompurify';

export const ARTWORK_CSP = "default-src 'none'; script-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src 'none'; connect-src 'none'; media-src 'none'; object-src 'none'; frame-src 'none'; base-uri 'none'; form-action 'none'";

export function sanitizeArtwork(source) {
  if (typeof source !== 'string' || !source.trim()) throw new Error('empty_html');
  if (Buffer.byteLength(source) > 500_000) throw new Error('html_too_large');
  const cleaned = source.trim().replace(/^```(?:html)?\s*/i, '').replace(/\s*```$/, '');
  const window = new JSDOM('').window;
  let outputWindow;
  try {
    const purify = createDOMPurify(window);
    const forbidden = ['script', 'iframe', 'object', 'embed', 'form', 'input', 'button', 'a', 'link', 'meta', 'base', 'canvas', 'img', 'image', 'video', 'audio', 'foreignObject', 'feImage', 'set'];
    let removed = /<(script|iframe|canvas|img|image|foreignObject|link)\b/i.test(cleaned);
    const fragment = purify.sanitize(cleaned, {
      WHOLE_DOCUMENT: true,
      FORBID_TAGS: forbidden,
      FORBID_ATTR: ['src', 'srcset', 'formaction', 'target', 'ping', 'autofocus'],
      ADD_TAGS: ['animate', 'animateTransform', 'animateMotion', 'mpath'],
      ADD_ATTR: ['attributeName', 'attributeType', 'begin', 'dur', 'repeatCount', 'repeatDur', 'values', 'keyTimes', 'keySplines', 'calcMode', 'from', 'to', 'by', 'additive', 'accumulate', 'path', 'rotate', 'type'],
      ALLOW_DATA_ATTR: false,
    });
    outputWindow = new JSDOM(fragment).window;
    const document = outputWindow.document;
    if (document.querySelectorAll('*').length > 5000) throw new Error('too_many_nodes');
    for (const el of document.querySelectorAll('*')) {
      for (const attr of [...el.attributes]) {
        if ((/href$/i.test(attr.name) && !/^#[A-Za-z0-9_-]+$/.test(attr.value)) || /^on/i.test(attr.name)) { el.removeAttribute(attr.name); removed = true; }
      }
      if (el.tagName.toLowerCase() === 'animate' && !/^(transform|x|y|cx|cy|r|rx|ry|opacity|fill|stroke|stroke-width|stroke-dashoffset|d|points)$/i.test(el.getAttribute('attributeName') || '')) { el.remove(); removed = true; }
    }
    const meta = document.createElement('meta'); meta.setAttribute('http-equiv', 'Content-Security-Policy'); meta.setAttribute('content', ARTWORK_CSP); document.head.prepend(meta);
    const charset = document.createElement('meta'); charset.setAttribute('charset', 'utf-8'); document.head.prepend(charset);
    const viewport = document.createElement('meta'); viewport.setAttribute('name', 'viewport'); viewport.setAttribute('content', 'width=device-width, initial-scale=1'); document.head.append(viewport);
    return { html: '<!doctype html>\n' + document.documentElement.outerHTML, removed_active_content: removed };
  } finally { outputWindow?.close(); window.close(); }
}
