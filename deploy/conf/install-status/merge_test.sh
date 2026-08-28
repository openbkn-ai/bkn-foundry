#!/usr/bin/env bash
# Focused regression coverage for the in-cluster install-status live merge.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
merge_filter="${script_dir}/merge.jq"

snapshot='{
  "releases": [],
  "serviceHealth": [
    {"name":"agent-observability-internal","source":"pod","ready":"1/2","state":"degraded"},
    {"name":"worker-svc","source":"pod","ready":"0/1","state":"down"},
    {"name":"third-party-internal","source":"pod","ready":"0/1","state":"down"}
  ]
}'

workloads='{
  "items": [
    {
      "metadata": {"annotations": {"meta.helm.sh/release-name": "agent-observability"}},
      "spec": {"replicas": 1, "template": {"spec": {"containers": []}}},
      "status": {"readyReplicas": 1}
    },
    {
      "metadata": {"annotations": {"meta.helm.sh/release-name": "worker"}},
      "spec": {"replicas": 2, "template": {"spec": {"containers": []}}},
      "status": {"readyReplicas": 2}
    }
  ]
}'

result="$(jq -n --arg now '2026-08-29T00:00:00Z' \
  --slurpfile snap <(printf '%s\n' "${snapshot}") \
  --slurpfile work <(printf '%s\n' "${workloads}") \
  -f "${merge_filter}")"

assert_entry() {
  local name="$1"
  local ready="$2"
  local state="$3"

  jq -e --arg name "${name}" --arg ready "${ready}" --arg state "${state}" \
    'any(.serviceHealth[]; .name == $name and .ready == $ready and .state == $state)' \
    <<<"${result}" >/dev/null
}

assert_entry 'agent-observability-internal' '1/1' 'up'
assert_entry 'worker-svc' '2/2' 'up'
assert_entry 'third-party-internal' '0/1' 'down'

echo 'install-status live merge regression tests passed'
