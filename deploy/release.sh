#!/usr/bin/env bash
set -euo pipefail

sha="${1:?commit SHA is required}"
image="${2:?image reference is required}"
registry_user="${3:?GitHub actor is required}"
[[ "$sha" =~ ^[0-9a-f]{40}$ ]] || { echo "Invalid commit SHA" >&2; exit 1; }
[[ "$image" == "ghcr.io/qianqiang-del/narra-api:$sha" ]] || { echo "Unexpected image reference" >&2; exit 1; }
[[ "$registry_user" =~ ^[A-Za-z0-9-]+$ ]] || { echo "Invalid registry username" >&2; exit 1; }

cd /opt/narra
test -f .env || { echo "Missing /opt/narra/.env" >&2; exit 1; }
test -f "web/incoming/$sha.tar.gz" || { echo "Missing frontend artifact" >&2; exit 1; }

release="web/releases/$sha"
if [ ! -d "$release" ]; then
  staging="$(mktemp -d "web/releases/.$sha.XXXXXX")"
  tar -xzf "web/incoming/$sha.tar.gz" -C "$staging"
  test -f "$staging/index.html" || { echo "Frontend artifact has no index.html" >&2; exit 1; }
  chmod 755 "$staging"
  mv "$staging" "$release"
fi

IFS= read -r registry_token
printf '%s' "$registry_token" | docker login ghcr.io -u "$registry_user" --password-stdin >/dev/null
unset registry_token
trap 'docker logout ghcr.io >/dev/null 2>&1 || true' EXIT

previous_image="$(docker inspect --format '{{.Config.Image}}' narra-api 2>/dev/null || true)"
export IMAGE="$image"
compose=(docker compose --env-file /opt/narra/.env -f deploy/compose.app.yml)
"${compose[@]}" pull api
"${compose[@]}" up -d api

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
    IMAGE="$previous_image" "${compose[@]}" up -d --force-recreate api
  fi
  exit 1
fi

ln -sfn "releases/$sha" "web/.current-$sha"
mv -Tf "web/.current-$sha" web/current
rm -f "web/incoming/$sha.tar.gz"
echo "Narra deployed: $sha"
