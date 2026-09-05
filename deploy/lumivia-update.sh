#!/usr/bin/env bash
# Run from the existing production Compose directory as a Docker-capable user.
set -euo pipefail
umask 077

image=${1:?Usage: sudo bash lumivia-update.sh ghcr.io/goldenfishs/sub2api:sha-<40-character-commit>}
if [[ ! "$image" =~ ^ghcr\.io/goldenfishs/sub2api(:sha-[0-9a-f]{40}|@sha256:[0-9a-f]{64})$ ]]; then
  echo 'Only a pinned Lumivia fork image is accepted.' >&2
  exit 1
fi
[[ -f docker-compose.yml && -f .env ]] || { echo 'Run in the existing production Compose directory.' >&2; exit 1; }
exec 9>.lumivia-update.lock
flock -n 9 || { echo 'Another update is already running.' >&2; exit 1; }
override=docker-compose.override.yml
if [[ -f "$override" ]] && ! grep -qx '# Managed by lumivia-update.sh' "$override"; then
  echo 'Existing Compose override is not managed by this script; review it before updating.' >&2
  exit 1
fi
docker compose config --quiet
app=$(docker compose ps -q sub2api)
db=$(docker compose ps -q postgres)
[[ -n "$app" && -n "$db" ]] || { echo 'Application and database must be running.' >&2; exit 1; }
docker pull "$image"
docker run --rm --entrypoint /app/sub2api "$image" -version

stamp=$(date -u +%Y%m%dT%H%M%SZ)
backup="$PWD/backups/lumivia-$stamp"
mkdir -p "$backup"
chmod 700 "$PWD/backups" "$backup"
cp -p docker-compose.yml .env "$backup/"
if [[ -f "$override" ]]; then cp -p "$override" "$backup/"; fi
rollback="sub2api-lumivia-rollback:$stamp"
# Captures prior in-container binary updates; the original image may be older.
docker commit "$app" "$rollback" > "$backup/rollback-image-id"
printf '%s\n' "$rollback" > "$backup/rollback-image"
printf '%s\n' "$image" > "$backup/new-image"

write_override() {
  printf '# Managed by lumivia-update.sh\nservices:\n  sub2api:\n    image: %s\n' "$1" > "$override.tmp"
  mv "$override.tmp" "$override"
}
recover() {
  status=$?
  if [[ "$status" == 0 ]]; then status=1; fi
  trap - ERR INT TERM
  echo "Update failed. Restoring the previous application snapshot; backups: $backup" >&2
  write_override "$rollback"
  docker compose up -d --no-deps --pull never --force-recreate sub2api || true
  echo 'Database migrations are not automatically reversed. Inspect application health before retrying.' >&2
  exit "${status:-1}"
}
trap recover ERR INT TERM
docker compose stop sub2api
docker exec "$db" sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' > "$backup/database.dump"
docker exec -i "$db" pg_restore --list < "$backup/database.dump" > "$backup/database.contents"
tar -czf "$backup/data.tar.gz" data
write_override "$image"
docker compose config --quiet
docker compose up -d --no-deps --pull never --force-recreate sub2api
for attempt in {1..60}; do
  app=$(docker compose ps -q sub2api)
  health=$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$app")
  if [[ "$health" == healthy ]]; then
    trap - ERR INT TERM
    docker exec "$app" /app/sub2api -version
    printf 'Updated successfully. Backup and rollback snapshot: %s\n' "$backup"
    exit 0
  fi
  sleep 3
done
echo 'New application did not become healthy.' >&2
false
