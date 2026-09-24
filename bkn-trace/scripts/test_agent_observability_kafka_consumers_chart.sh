#!/usr/bin/env bash
# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
# Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
chart="${root_dir}/agent-observability/charts/agent-observability"
sentinel="do-not-render-kafka-secret-value"

assert_contains() {
  local rendered="$1"
  local needle="$2"
  if ! grep -Fq -- "${needle}" <<<"${rendered}"; then
    echo "expected rendered chart to contain: ${needle}" >&2
    exit 1
  fi
}

assert_not_contains() {
  local rendered="$1"
  local needle="$2"
  if grep -Fq -- "${needle}" <<<"${rendered}"; then
    echo "rendered chart must not contain: ${needle}" >&2
    exit 1
  fi
}

default="$(helm template agent-observability "${chart}")"
assert_contains "${default}" 'name: BKN_TRACE_EVIDENCE_KAFKA_ENABLED'
assert_contains "${default}" 'name: BKN_TRACE_AUDIT_KAFKA_ENABLED'
assert_contains "${default}" 'value: "false"'
assert_not_contains "${default}" 'name: BKN_TRACE_EVIDENCE_KAFKA_PASSWORD'
assert_not_contains "${default}" 'name: BKN_TRACE_AUDIT_KAFKA_PASSWORD'

audit="$(helm template agent-observability "${chart}" \
  --set core.store=mariadb \
  --set core.autoMigrate=true \
  --set kafkaConsumers.audit.enabled=true \
  --set 'kafkaConsumers.audit.brokers[0]=broker.example.test:9092' \
  --set kafkaConsumers.audit.consumerGroup=c6-audit \
  --set kafkaConsumers.audit.saslMechanism=SCRAM-SHA-512 \
  --set kafkaConsumers.audit.existingSecret.name=existing-kafka \
  --set kafkaConsumers.audit.existingSecret.usernameKey=principal \
  --set kafkaConsumers.audit.existingSecret.passwordKey=credential \
  --set-string kafkaConsumers.audit.password="${sentinel}")"
assert_contains "${audit}" 'name: BKN_TRACE_AUDIT_KAFKA_BROKERS'
assert_contains "${audit}" 'name: "existing-kafka"'
assert_contains "${audit}" 'key: "principal"'
assert_contains "${audit}" 'key: "credential"'
assert_not_contains "${audit}" "${sentinel}"

evidence="$(helm template agent-observability "${chart}" \
  --set kafkaConsumers.evidence.enabled=true \
  --set 'kafkaConsumers.evidence.brokers[0]=broker.example.test:9092' \
  --set kafkaConsumers.evidence.saslMechanism=SCRAM-SHA-256 \
  --set kafkaConsumers.evidence.existingSecret.name=existing-evidence \
  --set kafkaConsumers.evidence.existingSecret.usernameKey=principal \
  --set kafkaConsumers.evidence.existingSecret.passwordKey=credential \
  --set-string kafkaConsumers.evidence.password="${sentinel}")"
assert_contains "${evidence}" 'value: "bkn-trace-evidence-ledger-v1"'
assert_contains "${evidence}" 'name: "existing-evidence"'
assert_contains "${evidence}" 'key: "principal"'
assert_contains "${evidence}" 'key: "credential"'
assert_not_contains "${evidence}" "${sentinel}"

if helm template agent-observability "${chart}" \
  --set core.store=mariadb --set core.autoMigrate=true \
  --set kafkaConsumers.audit.enabled=true \
  --set kafkaConsumers.audit.consumerGroup=c6-audit \
  --set kafkaConsumers.audit.saslMechanism=PLAIN >/dev/null 2>&1; then
  echo "Audit consumer render must fail when brokers and Secret reference are absent" >&2
  exit 1
fi

if helm template agent-observability "${chart}" \
  --set kafkaConsumers.audit.enabled=true \
  --set kafkaConsumers.audit.topic=wrong-topic >/dev/null 2>&1; then
  echo "Audit consumer render must reject a topic outside the frozen contract" >&2
  exit 1
fi

echo "Agent Observability Kafka consumer chart checks passed"
