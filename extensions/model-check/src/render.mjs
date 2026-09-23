import { fork } from 'node:child_process';
import { fileURLToPath } from 'node:url';

// One short-lived browser per job: no shared cookies, page context or generated scripts.
export function renderArtwork(html) {
  return new Promise((resolve, reject) => {
    const child = fork(fileURLToPath(new URL('./render-worker.mjs', import.meta.url)), [], { stdio: ['ignore', 'ignore', 'ignore', 'ipc'], detached: process.platform !== 'win32' });
    let complete = false;
    const stop = () => { try { process.kill(process.platform === 'win32' ? child.pid : -child.pid, 'SIGKILL'); } catch { child.kill('SIGKILL'); } };
    const timer = setTimeout(() => { if (complete) return; complete = true; stop(); reject(new Error('render_timeout')); }, 25_000);
    child.on('message', message => {
      if (complete) return; complete = true; clearTimeout(timer); stop();
      message.error ? reject(new Error(message.error)) : resolve(message.result);
    });
    child.on('exit', () => { if (!complete) { complete = true; clearTimeout(timer); reject(new Error('render_unavailable')); } });
    child.send({ html });
  });
}

export function assess(metrics, baseline = null, compatible = false) {
  const dimensions = [
    { id: 'drawing', score: metrics.svg_count > 0 && metrics.visible_shapes >= 4 ? 25 : metrics.svg_count > 0 ? 8 : 0, max: 25 },
    { id: 'visibility', score: metrics.contrast >= 12 && metrics.occupied_ratio >= 0.025 ? 25 : metrics.contrast >= 5 ? 10 : 0, max: 25 },
    { id: 'motion', score: metrics.motion_ratio >= 0.001 ? 25 : 0, max: 25 },
    { id: 'layout', score: metrics.overflow_desktop || metrics.overflow_mobile ? 8 : 25, max: 25 },
  ];
  const score = dimensions.reduce((sum, dimension) => sum + dimension.score, 0);
  const reasons = [];
  if (!metrics.svg_count) reasons.push('no_svg');
  if (metrics.visible_shapes < 4) reasons.push('few_shapes');
  if (metrics.contrast < 5 || metrics.occupied_ratio < 0.025) reasons.push('blank_frame');
  if (metrics.motion_ratio < 0.001) reasons.push('motion_not_observed');
  if (metrics.overflow_desktop || metrics.overflow_mobile) reasons.push('layout_overflow');
  if (metrics.removed_active_content) reasons.push('unsupported_content');
  const delta = compatible && baseline ? score - baseline.score : null;
  if (delta !== null && delta <= -20) reasons.push('baseline_drop');
  return { score, dimensions, reasons, delta, verdict: reasons.length ? 'review' : 'normal', method: 'render-rules-v1', semantic_review: 'human_required' };
}
