import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { existsSync } from 'node:fs';
import { resolve } from 'node:path';

const directory = fileURLToPath(new URL('.', import.meta.url));
const frontend = resolve(directory, '../../frontend');
if (!existsSync(resolve(directory, 'node_modules')) || !existsSync(resolve(frontend, 'node_modules'))) {
  console.error('Install frontend dependencies and run npm ci in extensions/model-check first.'); process.exit(1);
}
const children = [
  spawn(process.execPath, ['src/server.mjs'], { cwd: directory, stdio: 'inherit', env: { ...process.env, MODEL_CHECK_DEMO: process.env.MODEL_CHECK_DEMO ?? '1' } }),
  spawn(process.execPath, ['node_modules/vite/bin/vite.js', '--config', 'vite.config.ts', '--host', '127.0.0.1', '--port', process.env.VITE_DEV_PORT || '3000', '--strictPort'], { cwd: frontend, stdio: 'inherit' }),
];
let stopping = false;
function stop() { if (stopping) return; stopping = true; for (const child of children) child.kill('SIGTERM'); }
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, stop);
for (const child of children) child.on('exit', code => { if (!stopping && code) { process.exitCode = code; stop(); } });
console.log('Local preview: http://localhost:3000/model-check');
