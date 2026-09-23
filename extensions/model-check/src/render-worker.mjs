import { chromium } from 'playwright';
import sharp from 'sharp';
import { sanitizeArtwork } from './sanitize.mjs';

async function pixels(buffer) { return sharp(buffer).resize(160, 100, { fit: 'fill' }).removeAlpha().raw().toBuffer(); }

function pixelMetrics(frames) {
  const first = frames[0]; let sum = 0; let squares = 0; let moving = 0;
  const colors = new Map();
  for (let i = 0; i < first.length; i += 3) {
    const grey = (first[i] + first[i + 1] + first[i + 2]) / 3;
    sum += grey; squares += grey * grey;
    const quantized = `${first[i] >> 4},${first[i + 1] >> 4},${first[i + 2] >> 4}`;
    colors.set(quantized, (colors.get(quantized) || 0) + 1);
    if (frames.slice(1).some(f => Math.abs(first[i] - f[i]) + Math.abs(first[i + 1] - f[i + 1]) + Math.abs(first[i + 2] - f[i + 2]) > 36)) moving++;
  }
  const count = first.length / 3;
  return { contrast: Math.sqrt(Math.max(0, squares / count - (sum / count) ** 2)), occupied_ratio: 1 - Math.max(...colors.values()) / count, motion_ratio: moving / count };
}

process.on('message', async ({ html }) => {
  let browser;
  try {
    const safe = sanitizeArtwork(html);
    browser = await chromium.launch({ headless: true, args: ['--disable-background-networking', '--disable-extensions', '--no-proxy-server', '--disable-dev-shm-usage'] });
    const context = await browser.newContext({ viewport: { width: 960, height: 600 }, deviceScaleFactor: 1, javaScriptEnabled: false, offline: true, serviceWorkers: 'block', acceptDownloads: false });
    await context.route('**/*', route => route.abort());
    const page = await context.newPage();
    page.setDefaultTimeout(4000);
    await page.setContent(safe.html, { waitUntil: 'domcontentloaded', timeout: 4000 });
    const measure = () => {
      const shapes = [...document.querySelectorAll('svg path,svg circle,svg rect,svg ellipse,svg polygon,svg polyline,svg line,svg text')];
      return {
        svg_count: document.querySelectorAll('svg').length,
        visible_shapes: shapes.filter(el => { const r = el.getBoundingClientRect(); const s = getComputedStyle(el); return r.width * r.height > 4 && r.right > 0 && r.bottom > 0 && r.left < innerWidth && r.top < innerHeight && s.opacity !== '0' && s.visibility !== 'hidden' && s.display !== 'none'; }).length,
        overflow: document.documentElement.scrollWidth > innerWidth + 8 || document.documentElement.scrollHeight > innerHeight + 30,
      };
    };
    const desktop = await page.evaluate(measure);
    const frames = [];
    for (const delay of [180, 650, 730]) {
      await new Promise(resolve => setTimeout(resolve, delay));
      frames.push(await page.screenshot({ type: 'png', timeout: 4000 }));
    }
    await page.setViewportSize({ width: 390, height: 600 });
    const mobile = await page.evaluate(measure);
    const stats = pixelMetrics(await Promise.all(frames.map(pixels)));
    const image = await sharp(frames[0]).webp({ quality: 85 }).toBuffer();
    const thumbnail = await sharp(frames[0]).resize(480, 300).webp({ quality: 78 }).toBuffer();
    await browser.close(); browser = null;
    process.send({ result: { html: safe.html, image: image.toString('base64'), thumbnail: thumbnail.toString('base64'), metrics: { ...stats, svg_count: desktop.svg_count, visible_shapes: desktop.visible_shapes, overflow_desktop: desktop.overflow, overflow_mobile: mobile.overflow, removed_active_content: safe.removed_active_content, sample_times_ms: [180, 830, 1560], viewport: '960×600 / 390×600' } } });
  } catch (error) {
    if (browser) await browser.close().catch(() => {});
    const known = ['html_too_large', 'too_many_nodes', 'empty_html'];
    process.send({ error: known.includes(error.message) ? error.message : 'render_unavailable' });
  }
});
