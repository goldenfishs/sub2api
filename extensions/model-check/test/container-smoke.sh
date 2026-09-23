#!/usr/bin/env bash
# Only creates and removes its own disposable container and volume. Set
# DOCKER_CONTEXT for a non-default daemon without changing global context.
set -euo pipefail

image=${1:?Usage: container-smoke.sh <model-check-runtime-image>}
name="lumivia-model-check-smoke-${GITHUB_RUN_ID:-local}-$$"
volume="${name}-data"
cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker volume rm "$volume" >/dev/null 2>&1 || true
}
trap cleanup EXIT
docker volume create "$volume" >/dev/null

start() {
  docker run --detach --name "$name" --init --network none \
    --health-interval 1s --health-start-period 0s \
    --read-only --cap-drop ALL --security-opt no-new-privileges:true \
    --cpus 2 --memory 1g --memory-swap 1g --pids-limit 256 --shm-size 256m \
    --tmpfs /tmp:rw,nosuid,nodev,noexec,size=268435456,mode=1777 \
    --volume "$volume:/app/data" \
    --env MODEL_CHECK_BRIDGE_SECRET=container-smoke-only-00000000000000000000000000000000 \
    "$image" >/dev/null
  for ((attempt = 0; attempt < 30; attempt++)); do
    if [[ $(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}missing{{end}}' "$name") == healthy ]]; then
      return
    fi
    sleep 1
  done
  docker logs "$name" >&2
  echo 'Model-check runtime did not become healthy.' >&2
  exit 1
}

start
docker exec -i "$name" node --input-type=module <<'NODE'
import assert from 'node:assert/strict';
import { readFileSync, statSync, writeFileSync } from 'node:fs';
import { DatabaseSync } from 'node:sqlite';
import { secretVault } from './src/security.mjs';

assert.equal(process.getuid(), 10001, 'runtime must run as the application user');
assert.equal(process.env.MODEL_CHECK_DEMO, '0');
assert.equal(statSync('/app/data').uid, 10001, 'a fresh volume must be writable without a root entrypoint');
assert.equal(statSync('/app/data/encryption.key').mode & 0o777, 0o600);
assert.throws(() => writeFileSync('/app/runtime-write-probe', 'x'), error => ['EACCES', 'EROFS'].includes(error.code));
const listeners = readFileSync('/proc/net/tcp', 'utf8').trim().split('\n').slice(1)
  .map(line => line.trim().split(/\s+/)).filter(fields => fields[3] === '0A');
assert.ok(listeners.some(fields => fields[1] === '0100007F:1FA0'), 'service must bind loopback:8096');
assert.ok(!listeners.some(fields => fields[1].endsWith(':1FA0') && fields[1] !== '0100007F:1FA0'));
const vault = secretVault('/app/data');
const db = new DatabaseSync('/app/data/model-check.sqlite');
db.exec('CREATE TABLE container_smoke (ciphertext TEXT NOT NULL)');
db.prepare('INSERT INTO container_smoke VALUES (?)').run(vault.encrypt('container-persistence-fixture'));
db.close();
// A health probe is the only unauthenticated entry in production bridge mode.
const direct = await fetch('http://127.0.0.1:8096/api/v1/model-check/overview');
assert.equal(direct.status, 403, 'direct access must require the main application bridge');
NODE

# Recreate the process and writable layer, preserving only the named data
# volume. Both the SQLite record and its original encryption key must survive.
docker stop --time 35 "$name" >/dev/null
docker rm "$name" >/dev/null
start
docker exec -i "$name" node --input-type=module <<'NODE'
import assert from 'node:assert/strict';
import { DatabaseSync } from 'node:sqlite';
import { secretVault } from './src/security.mjs';
const db = new DatabaseSync('/app/data/model-check.sqlite');
const row = db.prepare('SELECT ciphertext FROM container_smoke').get();
assert.equal(secretVault('/app/data').decrypt(row.ciphertext), 'container-persistence-fixture');
assert.equal(db.prepare('PRAGMA integrity_check').get().integrity_check, 'ok');
db.close();
NODE

echo 'Model-check runtime startup, isolation and persistent encrypted data passed.'
