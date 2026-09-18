#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CONFIG_SCRIPT="${ROOT_DIR}/deploy/scripts/services/config.sh"

if ! awk '
  $1 == "observability:" { in_observability = 1; next }
  in_observability && $1 == "traceEvidence:" { in_trace_evidence = 1; next }
  in_trace_evidence && $1 == "enabled:" && $2 == "false" { found = 1; exit }
  in_observability && /^[^[:space:]]/ { exit }
  END { exit(found ? 0 : 1) }
' "${CONFIG_SCRIPT}"; then
  echo "FAIL: config generator must emit observability.traceEvidence.enabled: false" >&2
  exit 1
fi

echo "trace_evidence_config_test: generated default is disabled"
