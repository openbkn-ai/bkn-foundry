#!/usr/bin/env bash
set -euo pipefail

chart_dir="${1:-charts}"
rendered="$(helm template bkn-agent "${chart_dir}")"

contains() {
  local needle="$1"
  if ! grep -Fq -- "${needle}" <<<"${rendered}"; then
    echo "missing rendered chart content: ${needle}" >&2
    exit 1
  fi
}

not_contains() {
  local needle="$1"
  if grep -Fq -- "${needle}" <<<"${rendered}"; then
    echo "unexpected rendered chart content: ${needle}" >&2
    exit 1
  fi
}

contains 'name: BKN_TRACE_EVIDENCE_PUBLISHER_ENABLED'
contains 'value: "false"'
not_contains 'name: BKN_TRACE_KAFKA_BROKERS'
not_contains 'name: TRACE_ADMISSION_POLICY_URL'
contains 'name: BKN_TRACE_ARTIFACT_INGEST_URL'
contains 'name: BKN_TRACE_ARTIFACT_INGEST_TOKEN'
contains 'name: OTEL_LOGS_ENABLED'
contains 'kind: ServiceAccount'
contains 'serviceAccountName: bkn-agent'
contains 'automountServiceAccountToken: false'

independent_logs="$(helm template bkn-agent "${chart_dir}" --set observability.otelEnabled=false --set observability.logsEnabled=true)"
if ! grep -A1 'name: OTEL_LOGS_ENABLED' <<<"${independent_logs}" | grep -Fq 'value: "true"'; then
  echo 'OTLP logs must remain enabled when Trace is disabled' >&2
  exit 1
fi

rendered="$(helm template bkn-agent "${chart_dir}" \
  --set observability.evidencePublisher.enabled=true \
  --set observability.evidencePublisher.traceAdmission.clientID=bkn-agent \
  --set observability.evidencePublisher.traceAdmission.clientSecretSecretName=bkn-agent-trace-admission-oauth \
  --set observability.evidencePublisher.traceAdmission.currentKeyID=test-key \
  --set observability.evidencePublisher.traceAdmission.currentPublicKeySecretName=trace-admission-verifier)"

contains 'name: BKN_TRACE_EVIDENCE_PUBLISHER_ENABLED'
contains 'value: "true"'
contains 'name: BKN_TRACE_KAFKA_BROKERS'
contains 'name: BKN_TRACE_KAFKA_USERNAME'
contains 'name: BKN_TRACE_KAFKA_PASSWORD'
contains 'name: TRACE_ADMISSION_POLICY_URL'
contains 'name: TRACE_ADMISSION_CONFIGURATION_URL'
contains 'name: TRACE_ADMISSION_HEARTBEAT_URL'
contains 'name: TRACE_ADMISSION_ACK_URL_BASE'
contains 'name: TRACE_ADMISSION_CLIENT_ID'
contains 'name: TRACE_ADMISSION_CLIENT_SECRET'
contains 'name: TRACE_ADMISSION_CURRENT_PUBLIC_KEY'
contains 'name: BKN_TRACE_EVIDENCE_QUEUE_MAX_RECORDS'
contains 'name: BKN_TRACE_EVIDENCE_QUEUE_MAX_BYTES'
contains 'name: BKN_TRACE_EVIDENCE_MAX_ATTEMPTS'
not_contains 'name: BKN_TRACE_CAPTURE_POLICY_REVISION'
not_contains 'name: BKN_TRACE_EVIDENCE_INGEST_URL'
not_contains 'name: BKN_TRACE_EVIDENCE_INGEST_TOKEN'
not_contains 'name: BKN_TRACE_EVIDENCE_TIMEOUT_S'

echo 'bkn-agent Kafka Evidence chart checks passed'
