#!/usr/bin/env bash
set -euo pipefail

# This is a local artifact smoke test. The release workflow performs the
# multi-architecture build; this script deliberately never pushes or deploys.
image="${1:-otelcol-openbkn:0.2.0-local}"

command -v docker >/dev/null || {
  echo "docker is required for local OCB image verification" >&2
  exit 1
}

docker image inspect "${image}" >/dev/null || {
  echo "local image not found: ${image}" >&2
  exit 1
}

components="$(docker run --rm --entrypoint /otelcol-openbkn "${image}" components)"
grep -Eq '^    - name: traceadmission$' <<<"${components}"
grep -Eq '^    - name: opensearch$' <<<"${components}"
grep -Eq '^    - name: otlp$' <<<"${components}"

echo "OCB local image artifact verified: ${image}"
