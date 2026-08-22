#!/usr/bin/env bash
# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
# Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

set -euo pipefail

chart_dir="${1:-charts/otelcol-contrib}"
rendered="$(helm template otelcol-contrib "${chart_dir}")"

assert_contains() {
  local needle="$1"
  if ! grep -Fq -- "${needle}" <<<"${rendered}"; then
    echo "expected rendered chart to contain: ${needle}" >&2
    exit 1
  fi
}

assert_contains "memory_limiter:"
assert_contains "limit_mib: 512"
assert_contains "spike_limit_mib: 128"
assert_contains "processors:"
assert_contains "- memory_limiter"
assert_contains "send_batch_max_size: 1024"
assert_contains "timeout: 1s"
assert_contains "sending_queue:"
assert_contains "num_consumers: 4"
assert_contains "queue_size: 512"
assert_contains "retry_on_failure:"
assert_contains "max_interval: 10s"
assert_contains "max_elapsed_time: 5m"
assert_contains "health_check:"
assert_contains "endpoint: 0.0.0.0:13133"
assert_contains "path: /"
assert_contains "port: health"
assert_contains "name: metrics"
assert_contains "containerPort: 8888"
assert_contains "host: 0.0.0.0"
assert_contains "port: 8888"

if ! grep -Fq "memory: 768Mi" <<<"${rendered}"; then
  echo "collector memory limit must leave headroom above the 512MiB limiter" >&2
  exit 1
fi

readme="$(<README.md)"
for documented_value in "500m CPU / 768MiB" "512MiB" "128MiB" "1 秒刷新" "512 批、4 个消费者" "10 秒"; do
  if ! grep -Fq -- "${documented_value}" <<<"${readme}"; then
    echo "4C8G README baseline is missing: ${documented_value}" >&2
    exit 1
  fi
done

if grep -Fq "kind: PrometheusRule" <<<"${rendered}" || grep -Fq "kind: NetworkPolicy" <<<"${rendered}"; then
  echo "cluster-specific monitoring and network policy resources must be opt in" >&2
  exit 1
fi

governed_rendered="$(helm template otelcol-contrib "${chart_dir}" \
  --set monitoring.prometheusRule.enabled=true \
  --set networkPolicy.enabled=true)"
if ! grep -Fq "kind: PrometheusRule" <<<"${governed_rendered}"; then
  echo "enabled collector alerts were not rendered" >&2
  exit 1
fi
if ! grep -Fq "otelcol_exporter_queue_size" <<<"${governed_rendered}" ||
   ! grep -Fq "otelcol_exporter_send_failed_log_records_total" <<<"${governed_rendered}"; then
  echo "collector queue and export-failure alerts are incomplete" >&2
  exit 1
fi
if ! grep -Fq "kind: NetworkPolicy" <<<"${governed_rendered}" ||
   ! grep -Fq "openbkn.ai/otel-producer: \"true\"" <<<"${governed_rendered}"; then
  echo "collector trusted-ingress policy was not rendered" >&2
  exit 1
fi
