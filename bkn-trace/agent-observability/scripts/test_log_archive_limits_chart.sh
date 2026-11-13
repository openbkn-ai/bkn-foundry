#!/usr/bin/env bash
# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
# Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

set -euo pipefail
chart="${1:-charts/agent-observability}"
fixture="$(mktemp)"
trap 'rm -f "$fixture"' EXIT

check_limits() {
  local expected_count="$1" expected_bytes="$2" rendered actual name expected
  shift 2
  rendered="$(helm template archive-limits-test "$chart" "$@")"
  for name in BKN_OBSERVABILITY_LOG_ARCHIVE_MAX_CANDIDATES BKN_OBSERVABILITY_LOG_ARCHIVE_MAX_PAYLOAD_BYTES; do
    expected="$expected_count"
    if [[ "$name" == BKN_OBSERVABILITY_LOG_ARCHIVE_MAX_PAYLOAD_BYTES ]]; then
      expected="$expected_bytes"
    fi
    actual="$(printf '%s\n' "$rendered" | awk -v name="$name" '$0 ~ "name: " name {getline; gsub(/^[[:space:]]*value: |\"/, ""); print; exit}')"
    if [[ "$actual" != "$expected" ]]; then
      printf '%s expected integer %s, got %s\n' "$name" "$expected" "$actual" >&2
      exit 1
    fi
  done
}

# YAML numeric values are decoded as floats by Helm. The environment must still
# contain decimal integers accepted by strconv.Atoi, including large budgets.
check_limits 10000 10485760
cat > "$fixture" <<'YAML'
observability:
  archive:
    logLimits:
      maxCandidates: 250
      maxPayloadBytes: 20971520
YAML
check_limits 250 20971520 -f "$fixture"
check_limits 10000 67108864 --set observability.archive.logLimits.maxPayloadBytes=67108864
check_limits 100 1048576 --set-string observability.archive.logLimits.maxCandidates=100 --set-string observability.archive.logLimits.maxPayloadBytes=1048576
# Preserve invalid zero for application validation; never replace it by default.
check_limits 0 0 --set observability.archive.logLimits.maxCandidates=0 --set observability.archive.logLimits.maxPayloadBytes=0
check_limits 10000 10485760 --set observability.archive.logLimits=null
printf 'PASS: log archive limits render as integer environment values\n'
