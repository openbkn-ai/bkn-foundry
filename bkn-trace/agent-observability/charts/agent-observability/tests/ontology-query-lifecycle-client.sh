#!/usr/bin/env bash
# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
# Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

set -euo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
rendered="$(helm template agent-observability "$chart_dir" --namespace observability \
  --set core.capturePolicySigning.existingSecret=test-signing-key \
  --set bknSafe.baseURL=http://bkn-safe:3000)"
if ! printf '%s\n' "$rendered" | grep -B 4 'module: ontology-query' | grep -q 'kubernetes.io/metadata.name: "openbkn"'; then
  echo 'Ontology Query in openbkn must be allowed to reach private Trace Admission even when AO is in observability' >&2
  exit 1
fi

# An upgrade that retains an older explicit allowlist must not silently omit
# this 0.2 publisher from the private listener.
rendered="$(helm template agent-observability "$chart_dir" --namespace observability \
  --set core.capturePolicySigning.existingSecret=test-signing-key \
  --set bknSafe.baseURL=http://bkn-safe:3000 \
  --set networkPolicy.allowedClients[0].namespace=observability \
  --set networkPolicy.allowedClients[0].podLabels.app=agent-retrieval)"
if ! printf '%s\n' "$rendered" | grep -B 4 'module: ontology-query' | grep -q 'kubernetes.io/metadata.name: "openbkn"'; then
  echo 'An older retained allowlist must still admit Ontology Query in openbkn' >&2
  exit 1
fi
