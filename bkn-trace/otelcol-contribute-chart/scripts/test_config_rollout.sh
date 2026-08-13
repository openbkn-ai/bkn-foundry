#!/usr/bin/env bash
set -euo pipefail

chart_dir="${1:-charts/otelcol-contrib}"

checksum() {
  helm template otelcol-contrib "${chart_dir}" "$@" |
    awk '/checksum\/collector-config:/ { print $2; exit }'
}

baseline="$(checksum)"
changed_exporter="$(checksum --set opensearchExporter.http.endpoint=http://example.invalid:9200)"
unchanged="$(checksum)"

if [[ -z "${baseline}" ]]; then
  echo "collector config checksum was not rendered" >&2
  exit 1
fi

if [[ "${baseline}" == "${changed_exporter}" ]]; then
  echo "collector config checksum did not change after exporter configuration changed" >&2
  exit 1
fi

if [[ "${baseline}" != "${unchanged}" ]]; then
  echo "collector config checksum changed without a configuration change" >&2
  exit 1
fi

echo "collector config rollout checksum verified"
