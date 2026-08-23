#!/usr/bin/env bash
# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
# Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

set -euo pipefail

chart_dir="${1:-charts/otelcol-contrib}"

checksum() {
  helm template otelcol-contrib "${chart_dir}" "$@" |
    awk '/checksum\/collector-config:/ { print $2; exit }'
}

baseline="$(checksum)"
changed_exporter="$(checksum --set opensearchExporter.http.endpoint=http://example.invalid:9200)"
timestamp_pipeline_rendered="$(helm template otelcol-contrib "${chart_dir}" --set opensearchExporter.pipeline=bkn-trace-span-timestamp-v1)"
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

if ! grep -Fq "pipeline: bkn-trace-span-timestamp-v1" <<<"${timestamp_pipeline_rendered}"; then
  echo "collector must pass the configured OpenSearch ingest pipeline to its exporter" >&2
  exit 1
fi

echo "collector config rollout checksum verified"
