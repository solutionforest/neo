#!/usr/bin/env bash
# Mirror the release CI (.github/workflows/release.yml) locally so build/test
# failures are caught BEFORE pushing a version tag. Requires Docker.
#
# Usage:
#   scripts/release-local.sh [VERSION]      # default: 0.0.0-local
#   make release-local VERSION=0.20.0
set -euo pipefail

VERSION="${1:-0.0.0-local}"
if [[ -z "${VERSION}" || "${VERSION}" == "dev" ]]; then
  VERSION="0.0.0-local"
fi
VERSION="${VERSION#v}"

IMAGE="neo-builder:local"
NAME="neo-builder-local"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "${ROOT}"

cleanup() { docker rm --force "${NAME}" >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "==> [1/5] Build neo-builder image (docker build .)"
docker build --tag "${IMAGE}" .

echo "==> [2/5] Run tests in the builder image (go test ./...)"
docker run --rm --entrypoint go "${IMAGE}" test ./...

echo "==> [3/5] Start neo-builder service"
cleanup
mkdir -p dist
docker run --detach --rm \
  --name "${NAME}" \
  --publish 127.0.0.1:9100:9100 \
  --volume "${ROOT}/dist:/output" \
  "${IMAGE}"

ready=false
for _ in $(seq 1 30); do
  if curl --fail --silent http://127.0.0.1:9100/health >/dev/null; then
    ready=true
    break
  fi
  sleep 1
done
if [[ "${ready}" != "true" ]]; then
  docker logs "${NAME}" || true
  echo "neo-builder did not become healthy" >&2
  exit 1
fi

echo "==> [4/5] Build release binaries for ${VERSION}"
http_code="$(curl --silent --show-error \
  --output dist/build-response.json \
  --write-out '%{http_code}' \
  --header 'Content-Type: application/json' \
  --data "{\"version\":\"${VERSION}\"}" \
  http://127.0.0.1:9100/build)"

if command -v jq >/dev/null 2>&1; then
  jq -r '.log // empty' dist/build-response.json || true
  status="$(jq -r '.status' dist/build-response.json)"
else
  cat dist/build-response.json
  status="unknown"
fi

if [[ "${http_code}" != "200" || ( "${status}" != "completed" && "${status}" != "unknown" ) ]]; then
  cat dist/build-response.json
  echo "neo-builder failed (HTTP ${http_code}, status ${status})" >&2
  exit 1
fi

echo "==> [5/5] Verify artifacts in dist/${VERSION}/"
ls -la "dist/${VERSION}/"
echo
echo "OK — local build mirrors the release CI. Safe to tag & push."
