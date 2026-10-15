#!/usr/bin/env bash
set -euo pipefail

sha="${1:?commit SHA is required}"
[[ "$sha" =~ ^[0-9a-f]{40}$ ]] || { echo "Invalid commit SHA" >&2; exit 1; }
image="narra-api:$sha"
archive="images/incoming/$sha.tar.gz"

cd /opt/narra
test -f .env || { echo "Missing /opt/narra/.env" >&2; exit 1; }
test -f "web/incoming/$sha.tar.gz" || { echo "Missing frontend artifact" >&2; exit 1; }
test -f "$archive" || { echo "Missing backend image archive" >&2; exit 1; }

release="web/releases/$sha"
if [ ! -d "$release" ]; then
  staging="$(mktemp -d "web/releases/.$sha.XXXXXX")"
  tar -xzf "web/incoming/$sha.tar.gz" -C "$staging"
  test -f "$staging/index.html" || { echo "Frontend artifact has no index.html" >&2; exit 1; }
  chmod 755 "$staging"
  mv "$staging" "$release"
fi

gzip -dc "$archive" | docker load
docker image inspect "$image" >/dev/null
rm -f "$archive"

previous_image="$(docker inspect --format '{{.Config.Image}}' narra-api 2>/dev/null || true)"
export IMAGE="$image"
compose=(docker compose --env-file /opt/narra/.env -f deploy/compose.app.yml)
"${compose[@]}" up -d --pull never api

ready=0
for i in $(seq 1 40); do
  if curl -fsS http://127.0.0.1:8080/api/v1/health >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 3
done

if [ "$ready" -ne 1 ]; then
  "${compose[@]}" logs --tail=100 api >&2
  if [ -n "$previous_image" ]; then
    echo "API health check failed; restoring $previous_image" >&2
    IMAGE="$previous_image" "${compose[@]}" up -d --pull never --force-recreate api
  fi
  exit 1
fi

ln -sfn "releases/$sha" "web/.current-$sha"
mv -Tf "web/.current-$sha" web/current
rm -f "web/incoming/$sha.tar.gz"
echo "Narra deployed: $sha"
