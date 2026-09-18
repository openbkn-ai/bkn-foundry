#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TEST_DIR="$(mktemp -d "${TMPDIR:-/tmp}/openbkn-trace-evidence-switch.XXXXXX")"
trap 'rm -rf -- "${TEST_DIR}"' EXIT

CONFIG_YAML_PATH="${TEST_DIR}/config.yaml"
cat >"${CONFIG_YAML_PATH}" <<'EOF'
observability:
  traceEvidence:
    enabled: true
EOF

# shellcheck source=../services/openbkn.sh
source "${ROOT_DIR}/deploy/scripts/services/openbkn.sh"

check_contains() {
  local label="$1"
  local needle="$2"
  shift 2
  local value
  for value in "$@"; do
    if [[ "${value}" == "${needle}" ]]; then
      return 0
    fi
  done
  echo "FAIL: ${label} missing ${needle}" >&2
  exit 1
}

if [[ "$(_openbkn_trace_evidence_enabled)" != "true" ]]; then
  echo "FAIL: trace evidence config should be enabled" >&2
  exit 1
fi

_openbkn_release_extra_sets bkn-backend openbkn
check_contains "bkn-backend" "config.otel.trace.enabled=true" "${CORE_RELEASE_EXTRA_SETS[@]}"
check_contains "bkn-backend" "bknTrace.producerOutbox.enabled=true" "${CORE_RELEASE_EXTRA_SETS[@]}"
check_contains "bkn-backend" "bknTrace.producerOutbox.workerEnabled=true" "${CORE_RELEASE_EXTRA_SETS[@]}"

_openbkn_release_extra_sets agent-retrieval openbkn
check_contains "agent-retrieval" "observability.trace.enabled=true" "${CORE_RELEASE_EXTRA_SETS[@]}"

cat >"${CONFIG_YAML_PATH}" <<'EOF'
observability:
  traceEvidence:
    enabled: false
EOF

_openbkn_release_extra_sets ontology-query openbkn
check_contains "ontology-query" "config.otel.trace.enabled=false" "${CORE_RELEASE_EXTRA_SETS[@]}"
check_contains "ontology-query" "bknTrace.producerOutbox.enabled=false" "${CORE_RELEASE_EXTRA_SETS[@]}"
check_contains "ontology-query" "bknTrace.producerOutbox.workerEnabled=false" "${CORE_RELEASE_EXTRA_SETS[@]}"

echo "openbkn_trace_evidence_switch_test: all checks passed"
