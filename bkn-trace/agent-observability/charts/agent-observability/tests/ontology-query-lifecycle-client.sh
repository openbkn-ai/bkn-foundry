#!/usr/bin/env bash
# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
# Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

set -euo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
rendered="$(helm template agent-observability "$chart_dir" --namespace observability \
  --set bknSafe.baseURL=http://bkn-safe:3000)"
if ! printf '%s\n' "$rendered" | grep -B 4 'module: ontology-query' | grep -q 'kubernetes.io/metadata.name: "openbkn"'; then
  echo 'Ontology Query in openbkn must be allowed to reach private Trace Admission even when AO is in observability' >&2
  exit 1
fi

# Custom allowlists are authoritative, with no hidden workload bypass.
rendered="$(helm template agent-observability "$chart_dir" --namespace observability \
  --set networkPolicy.allowedClients[0].namespace=observability \
  --set networkPolicy.allowedClients[0].podLabels.app=agent-retrieval)"
if printf '%s\n' "$rendered" | grep -q 'module: ontology-query'; then
  echo 'Custom private client allowlists must not implicitly admit Ontology Query' >&2
  exit 1
fi
