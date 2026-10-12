#!/usr/bin/env bash
# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
# Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

set -euo pipefail
chart="$(cd "$(dirname "${BASH_SOURCE[0]}")/../charts/agent-observability" && pwd)"
rendered="$(helm template agent-observability "${chart}" --kube-version 1.23.0)"
if grep -Eq 'BKN_TRACE_CAPTURE_POLICY_(SIGNING|AUDIENCE)|capture-policy-signing' <<<"${rendered}"; then
  echo "unexpected capture policy signing configuration" >&2
  exit 1
fi
grep -Fq 'BKN_TRACE_CAPTURE_POLICY_SNAPSHOT_TTL' <<<"${rendered}"
echo "PASS: internal policy snapshots render without a signing Secret"
