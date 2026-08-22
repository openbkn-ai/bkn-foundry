#!/usr/bin/env bash
# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
# Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

set -euo pipefail

# Sends synthetic, non-sensitive OTLP logs and checks Collector counter deltas.
# Use RATE_PER_SECOND=100 DURATION_SECONDS=300 for the steady release gate, or
# RATE_PER_SECOND=300 DURATION_SECONDS=300 for the burst gate.

namespace="${NAMESPACE:-openbkn}"
service="${SERVICE:-otelcol-contrib}"
rate_per_second="${RATE_PER_SECOND:-1}"
duration_seconds="${DURATION_SECONDS:-5}"
otlp_endpoint="${OTLP_ENDPOINT:-}"
metrics_endpoint="${METRICS_ENDPOINT:-}"
port_forward_pid=""

cleanup() {
  if [[ -n "${port_forward_pid}" ]]; then
    kill "${port_forward_pid}" 2>/dev/null || true
  fi
}
trap cleanup EXIT

if [[ -z "${otlp_endpoint}" || -z "${metrics_endpoint}" ]]; then
  local_otlp_port="${LOCAL_OTLP_PORT:-14318}"
  local_metrics_port="${LOCAL_METRICS_PORT:-18888}"
  kubectl port-forward -n "${namespace}" "service/${service}" \
    "${local_otlp_port}:4318" "${local_metrics_port}:8888" >/tmp/openbkn-otelcol-capacity-probe.log 2>&1 &
  port_forward_pid=$!
  otlp_endpoint="http://127.0.0.1:${local_otlp_port}/v1/logs"
  metrics_endpoint="http://127.0.0.1:${local_metrics_port}/metrics"
  for _ in {1..30}; do
    if curl -fsS "${metrics_endpoint}" >/dev/null 2>&1; then
      break
    fi
    sleep 1
  done
fi

metric_sum() {
  local metric="$1"
  curl -fsS "${metrics_endpoint}" |
    awk -v metric="${metric}" '$1 ~ ("^" metric "{") {sum += $NF} END {print sum + 0}'
}

accepted_before="$(metric_sum otelcol_receiver_accepted_log_records_total)"
sent_before="$(metric_sum otelcol_exporter_sent_log_records_total)"
refused_before="$(metric_sum otelcol_receiver_refused_log_records_total)"
failed_before="$(metric_sum otelcol_exporter_send_failed_log_records_total)"

records_json=""
for ((record = 1; record <= rate_per_second; record++)); do
  [[ -n "${records_json}" ]] && records_json+=","
  records_json+="{\"timeUnixNano\":\"1786077600000000000\",\"severityText\":\"INFO\",\"body\":{\"stringValue\":\"collector capacity probe\"},\"attributes\":[{\"key\":\"event.name\",\"value\":{\"stringValue\":\"capacity.probe\"}}]}"
done
payload="{\"resourceLogs\":[{\"resource\":{\"attributes\":[{\"key\":\"service.name\",\"value\":{\"stringValue\":\"bkn-trace-capacity-probe\"}}]},\"scopeLogs\":[{\"scope\":{\"name\":\"bkn-trace.capacity\"},\"logRecords\":[${records_json}]}]}]}"
expected_records=$((rate_per_second * duration_seconds))

for ((second = 1; second <= duration_seconds; second++)); do
  curl -fsS -o /dev/null -X POST "${otlp_endpoint}" \
    -H 'Content-Type: application/json' --data "${payload}"
  sleep 1
done

for _ in {1..30}; do
  accepted_after="$(metric_sum otelcol_receiver_accepted_log_records_total)"
  sent_after="$(metric_sum otelcol_exporter_sent_log_records_total)"
  if ((accepted_after - accepted_before >= expected_records && sent_after - sent_before >= expected_records)); then
    break
  fi
  sleep 1
done

accepted_after="$(metric_sum otelcol_receiver_accepted_log_records_total)"
sent_after="$(metric_sum otelcol_exporter_sent_log_records_total)"
refused_after="$(metric_sum otelcol_receiver_refused_log_records_total)"
failed_after="$(metric_sum otelcol_exporter_send_failed_log_records_total)"

accepted_delta=$((accepted_after - accepted_before))
sent_delta=$((sent_after - sent_before))
refused_delta=$((refused_after - refused_before))
failed_delta=$((failed_after - failed_before))

printf 'expected=%d accepted=%d sent=%d refused=%d failed=%d\n' \
  "${expected_records}" "${accepted_delta}" "${sent_delta}" "${refused_delta}" "${failed_delta}"

if ((accepted_delta < expected_records || sent_delta < expected_records || refused_delta != 0 || failed_delta != 0)); then
  exit 1
fi
