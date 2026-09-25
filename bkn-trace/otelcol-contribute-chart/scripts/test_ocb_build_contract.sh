#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG="${ROOT}/builder-config.yaml"
DOCKERFILE="${ROOT}/Dockerfile.openbkn"
VALUES="${ROOT}/charts/otelcol-contrib/values.yaml"

require_line() {
  local file="$1"
  local pattern="$2"
  if ! grep -Fq -- "${pattern}" "${file}"; then
    echo "missing '${pattern}' in ${file}" >&2
    exit 1
  fi
}

# The build context used by reusable-container-image.yml is this service
# directory. These paths must therefore resolve from /src, not from the
# repository root.
require_line "${DOCKERFILE}" 'RUN /go/bin/builder --config /src/builder-config.yaml'
require_line "${DOCKERFILE}" 'COPY --from=builder /src/_build/otelcol-openbkn /otelcol-openbkn'

# The custom processor is compiled from the checked-out source. A remote
# module/version is retained only for OCB's registration and compatibility
# checks; no release workflow may fetch an unpublished HEAD or publish a
# temporary module as a side effect.
require_line "${CONFIG}" 'gomod: github.com/openbkn-ai/bkn-foundry/bkn-trace/otelcol-contribute-chart/processor/traceadmissionprocessor v0.148.0-openbkn.1'
require_line "${CONFIG}" 'path: ./processor/traceadmissionprocessor'

test -f "${ROOT}/processor/traceadmissionprocessor/go.mod"
test -f "${ROOT}/processor/traceadmissionprocessor/processor.go"
require_line "${VALUES}" 'tag: "__VERSION__"'

if grep -Fq '/src/bkn-trace/otelcol-contribute-chart/' "${DOCKERFILE}"; then
  echo "Dockerfile contains a repository-root path that is invalid for the service build context" >&2
  exit 1
fi
if grep -Fq '0.148.0-openbkn.1' "${VALUES}"; then
  echo "chart must not pin an unpublished OCB development image" >&2
  exit 1
fi

echo "OCB build contract verified"
