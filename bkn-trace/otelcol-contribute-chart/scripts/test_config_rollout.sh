#!/usr/bin/env bash
# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
# Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

set -euo pipefail

runtime_image="${OTELCOL_RUNTIME_IMAGE:-swr.cn-east-3.myhuaweicloud.com/openbkn-ai/otel/opentelemetry-collector-contrib:0.148.0}"

chart_dir="${1:-charts/otelcol-contrib}"
admission_args=(
  --set traceAdmission.clientID=trace-gateway
  --set traceAdmission.clientSecretSecret=trace-gateway-oauth
  --set traceAdmission.currentKeyID=trace-policy-2026q3
  --set traceAdmission.currentPublicKeySecret=trace-policy-public
  --set traceAdmission.workloadIdentity=spiffe://cluster-a/ns/openbkn/sa/otelcol
)

checksum() {
  helm template otelcol-contrib "${chart_dir}" "${admission_args[@]}" "$@" |
    awk '/checksum\/collector-config:/ { print $2; exit }'
}

baseline="$(checksum)"
changed_exporter="$(checksum --set opensearchExporter.http.endpoint=http://example.invalid:9200)"
timestamp_pipeline_rendered="$(helm template otelcol-contrib "${chart_dir}" "${admission_args[@]}" --set opensearchExporter.pipeline=bkn-trace-span-timestamp-v1)"
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

if grep -Fq "pipeline: bkn-trace-span-timestamp-v1" <<<"${timestamp_pipeline_rendered}"; then
  echo "collector OpenSearch exporter must not render unsupported pipeline configuration" >&2
  exit 1
fi

rendered_config="$(mktemp)"
trap 'rm -f "${rendered_config}" "${rendered_config}.manifest"' EXIT
# The default chart is the governed OCB configuration. The release job builds
# that image separately; this regression job uses the upstream binary only for
# generic chart/config validation. Remove the custom processor from this one
# validation render while retaining the enabled configuration checks above;
# `test_ocb_image_local.sh` validates the real enabled config with the OCB image.
helm template otelcol-contrib "${chart_dir}" "${admission_args[@]}" \
  --set config.processors.traceadmission=null \
  --set-json 'config.service.pipelines.traces.processors=["memory_limiter","batch"]' \
  >"${rendered_config}.manifest"
awk '
  $0 == "  collector-config.yaml: |" { in_config=1; next }
  in_config && $0 == "---" { exit }
  in_config { sub(/^    /, ""); print }
' "${rendered_config}.manifest" >"${rendered_config}"
chmod a+r "${rendered_config}"
docker run --rm \
  -v "${rendered_config}:/tmp/collector-config.yaml:ro" \
  "${runtime_image}" validate --config=/tmp/collector-config.yaml

echo "collector config rollout checksum verified"
