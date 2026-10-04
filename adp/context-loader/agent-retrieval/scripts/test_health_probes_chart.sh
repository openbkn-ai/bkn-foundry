#!/usr/bin/env bash
set -euo pipefail
chart_dir="$(cd "$(dirname "$0")/../helm/agent-retrieval" && pwd)"
probe_block() {
  awk -v name="$1" '$0 ~ "^          " name ":" {active=1; print; next} active && /^          [^ ]/ {exit} active {print}'
}
assert_contains() {
  if [[ "$1" != *"$2"* ]]; then
    echo "Missing expected probe field: $2" >&2
    exit 1
  fi
}
rendered="$(helm template probe-test "$chart_dir" --show-only templates/deployment.yaml)"
liveness="$(printf '%s\n' "$rendered" | probe_block livenessProbe)"
assert_contains "$liveness" "path: /health/alive"
assert_contains "$liveness" "port: public-port"
assert_contains "$liveness" "periodSeconds: 10"
assert_contains "$liveness" "timeoutSeconds: 5"
assert_contains "$liveness" "failureThreshold: 5"
readiness="$(printf '%s\n' "$rendered" | probe_block readinessProbe)"
assert_contains "$readiness" "path: /health/ready"
assert_contains "$readiness" "periodSeconds: 5"
custom="$(helm template probe-test "$chart_dir" --show-only templates/deployment.yaml \
  --set service.livenessProbe.periodSeconds=20 \
  --set service.livenessProbe.timeoutSeconds=3 \
  --set service.livenessProbe.failureThreshold=4)"
custom_probe="$(printf '%s\n' "$custom" | probe_block livenessProbe)"
assert_contains "$custom_probe" "periodSeconds: 20"
assert_contains "$custom_probe" "timeoutSeconds: 3"
assert_contains "$custom_probe" "failureThreshold: 4"
dev="$(helm template probe-test "$chart_dir" --show-only templates/deployment.yaml --set runMode=dev)"
if [[ "$dev" == *'livenessProbe:'* || "$dev" == *'readinessProbe:'* ]]; then
  echo 'Development mode must omit health probes' >&2
  exit 1
fi
echo 'health probe chart regression checks passed'
