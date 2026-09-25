#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG="${ROOT}/builder-config.yaml"
DOCKERFILE="${ROOT}/Dockerfile.openbkn"
VALUES="${ROOT}/charts/otelcol-contrib/values.yaml"
README="${ROOT}/README.md"

require_line() {
  local file="$1"
  local pattern="$2"
  if ! grep -Fq -- "${pattern}" "${file}"; then
    echo "missing '${pattern}' in ${file}" >&2
    exit 1
  fi
}

# The image workflow intentionally uses the repository root as its build
# context because the local processor imports the shared agent-observability
# module. The Dockerfile and builder config remain under the chart directory.
require_line "${DOCKERFILE}" 'RUN /go/bin/builder --config /src/bkn-trace/otelcol-contribute-chart/builder-config.yaml'
require_line "${DOCKERFILE}" 'COPY --from=builder /src/_build/otelcol-openbkn /otelcol-openbkn'

# The custom processor is compiled from the checked-out source. A remote
# module/version is retained only for OCB's registration and compatibility
# checks; no release workflow may fetch an unpublished HEAD or publish a
# temporary module as a side effect.
require_line "${CONFIG}" 'gomod: github.com/openbkn-ai/bkn-foundry/bkn-trace/otelcol-contribute-chart/processor/traceadmissionprocessor v0.148.0-openbkn.1'
require_line "${CONFIG}" 'path: ./bkn-trace/otelcol-contribute-chart/processor/traceadmissionprocessor'
require_line "${CONFIG}" 'gomod: github.com/open-telemetry/opentelemetry-collector-contrib/exporter/opensearchexporter v0.148.0'
require_line "${CONFIG}" 'gomod: github.com/open-telemetry/opentelemetry-collector-contrib/extension/healthcheckextension v0.148.0'
require_line "${CONFIG}" 'github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability => /src/bkn-trace/agent-observability'

if grep -Fq 'gomod: go.opentelemetry.io/collector-contrib/exporter/opensearchexporter' "${CONFIG}"; then
  echo "OpenSearch exporter uses the wrong Go module path" >&2
  exit 1
fi
if grep -Fq 'gomod: go.opentelemetry.io/collector/extension/healthcheckextension' "${CONFIG}"; then
  echo "healthcheck extension uses the wrong Go module path" >&2
  exit 1
fi

test -f "${ROOT}/processor/traceadmissionprocessor/go.mod"
test -f "${ROOT}/processor/traceadmissionprocessor/processor.go"
require_line "${VALUES}" 'tag: "__VERSION__"'
require_line "${README}" "--set 'image.tag=<built-version>'"

# A source checkout must be deployable only when the operator supplies a real
# built image tag; the release workflow is the only path that substitutes the
# __VERSION__ sentinel in a packaged chart.
rendered="$(helm template ocb-contract "${ROOT}/charts/otelcol-contrib" \
  --set 'image.tag=0.2.0-local' \
  --set traceAdmission.clientID=trace-gateway \
  --set traceAdmission.clientSecretSecret=trace-gateway-oauth \
  --set traceAdmission.currentKeyID=trace-policy-2026q3 \
  --set traceAdmission.currentPublicKeySecret=trace-policy-public \
  --set traceAdmission.workloadIdentity=spiffe://cluster-a/ns/openbkn/sa/otelcol)"
grep -Fq 'image: "ghcr.io/openbkn-ai/otelcol-openbkn:0.2.0-local"' <<<"${rendered}"

if grep -Fq '0.148.0-openbkn.1' "${VALUES}"; then
  echo "chart must not pin an unpublished OCB development image" >&2
  exit 1
fi

echo "OCB build contract verified"
