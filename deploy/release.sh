#!/usr/bin/env bash
set -euo pipefail

sha="${1:?commit SHA is required}"
[[ "$sha" =~ ^[0-9a-f]{40}$ ]] || { echo "Invalid commit SHA" >&2; exit 1; }
backend="${2:?backend flag is required}"
frontend="${3:?frontend flag is required}"
runtime="${4:?runtime flag is required}"
[[ "$backend" == true || "$backend" == false ]] || { echo "Invalid backend flag" >&2; exit 1; }
[[ "$frontend" == true || "$frontend" == false ]] || { echo "Invalid frontend flag" >&2; exit 1; }
[[ "$runtime" == true || "$runtime" == false ]] || { echo "Invalid runtime flag" >&2; exit 1; }
[[ "$runtime" == false || "$backend" == true ]] || { echo "Runtime update requires backend" >&2; exit 1; }
image="narra-api:$sha"
archive="images/incoming/$sha.tar.gz"
binary_archive="backend/incoming/$sha.tar.gz"

cd /opt/narra
test -f .env || { echo "Missing /opt/narra/.env" >&2; exit 1; }
if [[ "$frontend" == true ]]; then
  test -f "web/incoming/$sha.tar.gz" || { echo "Missing frontend artifact" >&2; exit 1; }
fi
if [[ "$backend" == true ]]; then
  test -f "$binary_archive" || { echo "Missing backend binary archive" >&2; exit 1; }
fi
if [[ "$runtime" == true ]]; then
  test -f "$archive" || { echo "Missing backend image archive" >&2; exit 1; }
fi

if [[ "$frontend" == true ]]; then
  release="web/releases/$sha"
  if [ ! -d "$release" ]; then
    staging="$(mktemp -d "web/releases/.$sha.XXXXXX")"
    tar -xzf "web/incoming/$sha.tar.gz" -C "$staging"
    test -f "$staging/index.html" || { echo "Frontend artifact has no index.html" >&2; exit 1; }
    chmod 755 "$staging"
    mv "$staging" "$release"
  fi
fi

if [[ "$backend" == true ]]; then
  binary_release="backend/releases/$sha"
  if [ ! -d "$binary_release" ]; then
    staging="$(mktemp -d "backend/releases/.$sha.XXXXXX")"
    tar -xzf "$binary_archive" -C "$staging"
    test -f "$staging/narra-api" && test -f "$staging/config.production.yaml" || { echo "Incomplete backend artifact" >&2; exit 1; }
    chmod 755 "$staging/narra-api"
    mv "$staging" "$binary_release"
  fi

  previous_image="$(docker inspect --format '{{.Config.Image}}' narra-api 2>/dev/null || true)"
  previous_binary="$(docker inspect --format '{{range .Mounts}}{{if eq .Destination "/app/narra-api"}}{{.Source}}{{end}}{{end}}' narra-api 2>/dev/null || true)"
  previous_release=""
  if [ -n "$previous_binary" ]; then
    previous_release="$(basename "$(dirname "$previous_binary")")"
  elif [ -n "$previous_image" ]; then
    previous_release="legacy-$sha"
    mkdir -p "backend/releases/$previous_release"
    docker cp narra-api:/app/narra-api "backend/releases/$previous_release/narra-api"
    docker cp narra-api:/app/configs/config.production.yaml "backend/releases/$previous_release/config.production.yaml"
  fi

  if [[ "$runtime" == true ]]; then
    # A 2G host cannot reliably load the runtime image alongside OCR's Python process.
    runtime_stopped=false
    restart_previous_runtime() {
      if [[ "$runtime_stopped" == true ]]; then
        echo "Restarting previous API container after runtime load failure" >&2
        docker start narra-api >&2 || true
      fi
    }
    trap restart_previous_runtime EXIT
    if [ -n "$previous_image" ]; then
      docker stop --time 90 narra-api
      runtime_stopped=true
    fi
    gzip -dc "$archive" | docker load
    rm -f "$archive"
  else
    image="$previous_image"
    test -n "$image" || { echo "No installed runtime image; a full runtime release is required" >&2; exit 1; }
  fi
  docker image inspect "$image" >/dev/null

  export IMAGE="$image"
  export APP_RELEASE_SHA="$sha"
  compose=(docker compose --env-file /opt/narra/.env -f deploy/compose.app.yml)
  restore_previous() {
    if [ -n "$previous_image" ] && [ -n "$previous_release" ]; then
      echo "Restoring $previous_image and $previous_release" >&2
      IMAGE="$previous_image" APP_RELEASE_SHA="$previous_release" "${compose[@]}" up -d --pull never --force-recreate api
    fi
  }
  if ! "${compose[@]}" up -d --pull never --force-recreate api; then
    echo "API container update failed" >&2
    restore_previous
    exit 1
  fi
  runtime_stopped=false
  trap - EXIT

  ready=0
  for i in $(seq 1 40); do
    if curl -fsS http://127.0.0.1:8080/api/v1/health >/dev/null 2>&1; then
      ready=1
      break
    fi
    sleep 3
  done

  if [ "$ready" -ne 1 ]; then
    "${compose[@]}" logs --tail=100 api >&2 || true
    echo "API health check failed" >&2
    restore_previous
    exit 1
  fi
  rm -f "$binary_archive"
fi

if [[ "$frontend" == true ]]; then
  ln -sfn "releases/$sha" "web/.current-$sha"
  mv -Tf "web/.current-$sha" web/current
  rm -f "web/incoming/$sha.tar.gz"
fi
echo "Narra deployed: $sha (backend=$backend, frontend=$frontend)"
