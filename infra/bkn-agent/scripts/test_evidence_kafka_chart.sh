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

contains 'name: BKN_TRACE_KAFKA_BROKERS'
contains 'name: BKN_TRACE_KAFKA_USERNAME'
contains 'name: BKN_TRACE_KAFKA_PASSWORD'
contains 'name: BKN_TRACE_CAPTURE_POLICY_REVISION'
contains 'name: BKN_TRACE_EVIDENCE_QUEUE_MAX_RECORDS'
contains 'name: BKN_TRACE_EVIDENCE_QUEUE_MAX_BYTES'
contains 'name: BKN_TRACE_EVIDENCE_MAX_ATTEMPTS'
contains 'name: BKN_TRACE_ARTIFACT_INGEST_URL'
contains 'name: BKN_TRACE_ARTIFACT_INGEST_TOKEN'
not_contains 'name: BKN_TRACE_EVIDENCE_INGEST_URL'
not_contains 'name: BKN_TRACE_EVIDENCE_INGEST_TOKEN'
not_contains 'name: BKN_TRACE_EVIDENCE_TIMEOUT_S'

echo 'bkn-agent Kafka Evidence chart checks passed'
