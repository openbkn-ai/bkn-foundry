#!/usr/bin/env bash
set -euo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
rendered="$(helm template agent-observability "$chart_dir" --namespace observability \
  --set core.capturePolicySigning.existingSecret=test-signing-key \
  --set bknSafe.baseURL=http://bkn-safe:3000)"
if ! printf '%s\n' "$rendered" | grep -B 4 'module: ontology-query' | grep -q 'kubernetes.io/metadata.name: "openbkn"'; then
  echo 'Ontology Query in openbkn must be allowed to reach private Trace Admission even when AO is in observability' >&2
  exit 1
fi
