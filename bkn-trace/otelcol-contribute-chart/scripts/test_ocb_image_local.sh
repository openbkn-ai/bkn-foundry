#!/usr/bin/env bash
# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
# Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

set -euo pipefail

# This is a local artifact smoke test. The release workflow performs the
# multi-architecture build; this script deliberately never pushes or deploys.
image="${1:-otelcol-openbkn:0.2.0-local}"
chart_dir="${2:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../charts/otelcol-contrib" && pwd)}"

command -v docker >/dev/null || {
  echo "docker is required for local OCB image verification" >&2
  exit 1
}
command -v helm >/dev/null || {
  echo "helm is required for local OCB chart validation" >&2
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

manifest="$(mktemp)"
config="$(mktemp)"
trap 'rm -f "${manifest}" "${config}"' EXIT

# Parse the real enabled chart with the locally built OCB binary. The values
# are non-secret validation fixtures; the chart still references Secret-backed
# env fields and no credential is written to the rendered ConfigMap.
helm template ocb-image "${chart_dir}" \
  --set traceAdmission.clientID=trace-gateway \
  --set traceAdmission.clientSecretSecret=trace-gateway-oauth \
  --set traceAdmission.currentKeyID=trace-policy-2026q3 \
  --set traceAdmission.currentPublicKeySecret=trace-policy-public \
  --set traceAdmission.workloadIdentity=spiffe://cluster-a/ns/openbkn/sa/otelcol \
  >"${manifest}"
awk '
  $0 == "  collector-config.yaml: |" { in_config=1; next }
  in_config && $0 == "---" { exit }
  in_config { sub(/^    /, ""); print }
' "${manifest}" >"${config}"
chmod a+r "${config}"

docker run --rm \
  -e TRACE_ADMISSION_POLICY_URL=http://agent-observability-internal:8081/policy \
  -e TRACE_ADMISSION_CONFIGURATION_URL=http://agent-observability:8080/configuration \
  -e TRACE_ADMISSION_HEARTBEAT_URL=http://agent-observability-internal:8081/heartbeat \
  -e TRACE_ADMISSION_ACK_URL_BASE=http://agent-observability-internal:8081/operations \
  -e TRACE_ADMISSION_TOKEN_URL=http://bkn-safe:4444/oauth2/token \
  -e TRACE_ADMISSION_CLIENT_ID=trace-gateway \
  -e TRACE_ADMISSION_CLIENT_SECRET=local-test-secret \
  -e TRACE_ADMISSION_AUDIENCE=cluster-a \
  -e TRACE_ADMISSION_CURRENT_KEY_ID=trace-policy-2026q3 \
  -e TRACE_ADMISSION_CURRENT_PUBLIC_KEY=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA= \
  -e TRACE_ADMISSION_WORKLOAD_IDENTITY=spiffe://cluster-a/ns/openbkn/sa/otelcol \
  -e TRACE_ADMISSION_POLL_INTERVAL=10s \
  -e TRACE_ADMISSION_HTTP_TIMEOUT=3s \
  -v "${config}:/tmp/collector-config.yaml:ro" \
  --entrypoint /otelcol-openbkn "${image}" validate --config=/tmp/collector-config.yaml

echo "OCB local image artifact verified: ${image}"
