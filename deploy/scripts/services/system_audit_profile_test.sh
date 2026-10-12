#!/usr/bin/env bash
# Copyright openbkn.ai
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
chart="${root}/bkn-trace/agent-observability/charts/agent-observability"
profile="${root}/deploy/conf/profiles/system-audit.yaml"
test_dir="$(mktemp -d)"
trap 'rm -rf "${test_dir}"' EXIT
helm template audit "${chart}" >"${test_dir}/default.yaml"
! grep -q 'BKN_TRACE_AUDIT_KAFKA_USERNAME' "${test_dir}/default.yaml"
# An explicit profile cannot silently enable with guessed infrastructure.
if helm template audit "${chart}" -f "${profile}" >"${test_dir}/incomplete.yaml" 2>"${test_dir}/error"; then
  echo 'Incomplete Audit profile must fail' >&2; exit 1
fi
helm template audit "${chart}" -f "${profile}" \
  --set kafkaConsumers.audit.brokers[0]=broker.example.invalid:9092 \
  --set kafkaConsumers.audit.existingSecret.name=audit-consumer \
  --set core.mariadb.existingSecret=core-store >"${test_dir}/enabled.yaml"
grep -A1 'name: BKN_TRACE_AUDIT_KAFKA_ENABLED' "${test_dir}/enabled.yaml" | grep -q 'value: "true"'
grep -A1 'name: BKN_TRACE_AUDIT_KAFKA_GROUP' "${test_dir}/enabled.yaml" | grep -q 'value: "bkn-trace-audit-ledger-v1"'
grep -A4 'name: BKN_TRACE_AUDIT_KAFKA_PASSWORD' "${test_dir}/enabled.yaml" | grep -q 'name: "audit-consumer"'
for override in 'kafkaConsumers.audit.consumerGroup=' 'kafkaConsumers.audit.saslMechanism=INVALID' 'core.autoMigrate=false'; do
  if helm template audit "${chart}" -f "${profile}" \
    --set kafkaConsumers.audit.brokers[0]=broker.example.invalid:9092 \
    --set kafkaConsumers.audit.existingSecret.name=audit-consumer \
    --set core.mariadb.existingSecret=core-store --set "${override}" >/dev/null 2>&1; then
    echo "Invalid Audit profile accepted: ${override}" >&2; exit 1
  fi
done
echo 'System Audit profile checks passed'
