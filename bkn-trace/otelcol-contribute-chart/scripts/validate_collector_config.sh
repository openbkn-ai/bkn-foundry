#!/usr/bin/env bash
set -euo pipefail

chart_dir="${1:-charts/otelcol-contrib}"
image="${OTELCOL_IMAGE:-otel/opentelemetry-collector-contrib:0.148.0}"
config_file="$(mktemp -t openbkn-otelcol-config.XXXXXX.yaml)"
trap 'rm -f "${config_file}"' EXIT

helm template otelcol-contrib "${chart_dir}" --show-only templates/configmap.yaml |
  awk '/collector-config.yaml: \|/{found=1; next} found{sub(/^    /, ""); print}' >"${config_file}"

docker run --rm \
  -v "${config_file}:/conf/collector-config.yaml:ro" \
  "${image}" validate --config=/conf/collector-config.yaml
