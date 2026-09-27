#!/usr/bin/env bash
# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
# Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

check_chart() {
  local chart="$1"
  shift
  local rendered
  rendered="$(helm template publisher-test "$repo_root/$chart" "$@")"
  for pair in \
    'BKN_TRACE_EVIDENCE_QUEUE_MAX_BYTES 67108864' \
    'BKN_TRACE_EVIDENCE_MAX_RECORD_BYTES 1048576'; do
    read -r name expected <<< "$pair"
    actual="$(printf '%s\n' "$rendered" | awk -v name="$name" '$0 ~ "name: " name {getline; gsub(/^[[:space:]]*value: |\"/, ""); print; exit}')"
    if [[ "$actual" != "$expected" ]]; then
      printf '%s: %s expected %s, got %s\n' "$chart" "$name" "$expected" "$actual" >&2
      exit 1
    fi
  done
}

check_chart adp/bkn/ontology-query/helm/ontology-query \
  --set bknTrace.evidencePublisher.enabled=true \
  --set bknTrace.evidencePublisher.brokers=kafka:9092 \
  --set bknTrace.evidencePublisher.usernameSecretName=kafka-auth \
  --set bknTrace.evidencePublisher.passwordSecretName=kafka-auth \
  --set bknTrace.evidencePublisher.traceAdmission.clientID=ontology-query \
  --set bknTrace.evidencePublisher.traceAdmission.clientSecretSecretName=oauth-client \
  --set bknTrace.evidencePublisher.traceAdmission.currentKeyID=test-key \
  --set bknTrace.evidencePublisher.traceAdmission.currentPublicKeySecretName=policy-key

check_chart adp/bkn/bkn-backend/helm/bkn-backend \
  --set bknTrace.evidencePublisher.enabled=true \
  --set bknTrace.evidencePublisher.brokers=kafka:9092 \
  --set bknTrace.evidencePublisher.usernameSecretName=kafka-auth \
  --set bknTrace.evidencePublisher.passwordSecretName=kafka-auth \
  --set bknTrace.evidencePublisher.producerId=bkn-backend \
  --set bknTrace.evidencePublisher.workloadIdentity=bkn-backend \
  --set bknTrace.evidencePublisher.producerStreamId=bkn-backend \
  --set bknTrace.evidencePublisher.traceAdmission.clientID=bkn-backend \
  --set bknTrace.evidencePublisher.traceAdmission.clientSecretSecretName=oauth-client \
  --set bknTrace.evidencePublisher.traceAdmission.currentKeyID=test-key \
  --set bknTrace.evidencePublisher.traceAdmission.currentPublicKeySecretName=policy-key

check_chart adp/context-loader/agent-retrieval/helm/agent-retrieval \
  --set observability.evidencePublisher.enabled=true \
  --set observability.evidencePublisher.brokers=kafka:9092 \
  --set observability.evidencePublisher.usernameSecretName=kafka-auth \
  --set observability.evidencePublisher.passwordSecretName=kafka-auth \
  --set observability.evidencePublisher.traceAdmission.clientID=agent-retrieval \
  --set observability.evidencePublisher.traceAdmission.clientSecretSecretName=oauth-client \
  --set observability.evidencePublisher.traceAdmission.currentKeyID=test-key \
  --set observability.evidencePublisher.traceAdmission.currentPublicKeySecretName=policy-key

check_chart infra/bkn-agent/charts \
  --set observability.evidencePublisher.enabled=true \
  --set observability.evidencePublisher.traceAdmission.clientID=bkn-agent \
  --set observability.evidencePublisher.traceAdmission.clientSecretSecretName=oauth-client \
  --set observability.evidencePublisher.traceAdmission.currentKeyID=test-key \
  --set observability.evidencePublisher.traceAdmission.currentPublicKeySecretName=policy-key

check_chart adp/execution-factory/operator-integration/helm/agent-operator-integration \
  --set observability.evidence.publisher.brokers=kafka:9092 \
  --set observability.evidence.publisher.credentials_secret_name=kafka-auth \
  --set observability.evidence.publisher.trace_admission.client_id=agent-operator-integration \
  --set observability.evidence.publisher.trace_admission.client_secret_name=oauth-client \
  --set observability.evidence.publisher.trace_admission.current_key_id=test-key \
  --set observability.evidence.publisher.trace_admission.current_public_key_secret_name=policy-key
