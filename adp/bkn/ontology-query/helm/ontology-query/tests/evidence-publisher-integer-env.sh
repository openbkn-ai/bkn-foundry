#!/usr/bin/env bash
# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
# Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

set -euo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
rendered="$(helm template ontology-query "$chart_dir" \
  --set bknTrace.evidencePublisher.enabled=true \
  --set bknTrace.evidencePublisher.brokers=kafka:9092 \
  --set bknTrace.evidencePublisher.usernameSecretName=kafka-auth \
  --set bknTrace.evidencePublisher.passwordSecretName=kafka-auth \
  --set bknTrace.evidencePublisher.traceAdmission.clientID=ontology-query \
  --set bknTrace.evidencePublisher.traceAdmission.clientSecretSecretName=oauth-client \
  --set bknTrace.evidencePublisher.traceAdmission.currentKeyID=test-key \
  --set bknTrace.evidencePublisher.traceAdmission.currentPublicKeySecretName=policy-key)"

for pair in \
  'BKN_TRACE_EVIDENCE_QUEUE_MAX_BYTES 67108864' \
  'BKN_TRACE_EVIDENCE_MAX_RECORD_BYTES 1048576'; do
  read -r name expected <<< "$pair"
  actual="$(printf '%s\n' "$rendered" | awk -v name="$name" '$0 ~ "name: " name {getline; gsub(/^[[:space:]]*value: |\"/, ""); print; exit}')"
  if [[ "$actual" != "$expected" ]]; then
    printf '%s: expected %s, got %s\n' "$name" "$expected" "$actual" >&2
    exit 1
  fi
done
