#!/usr/bin/env bash
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

set -euo pipefail

readonly STATE_FORMAT="openbkn-kn-migration-workloads-v1"
readonly -a WORKLOADS=(
  ontology-query
  agent-operator-integration
  bkn-backend
  bkn-safe
)
readonly -a STOP_ORDER=(
  ontology-query
  agent-operator-integration
  bkn-backend
  bkn-safe
)
readonly -a START_ORDER=(
  bkn-safe
  bkn-backend
  ontology-query
  agent-operator-integration
)

usage() {
  cat <<EOF
Usage:
  $0 stop [options]
  $0 start [options]
  $0 status [--expected-context CONTEXT] [options]

Control the four application Deployments used by the OpenBKN 0.1.4 to 0.1.5
knowledge-network data migration. Database and other infrastructure workloads
are not changed.

Options:
  --namespace NAMESPACE       Kubernetes namespace (default: openbkn)
  --expected-context CONTEXT  Optional explicit context safety check
  --state-file PATH           Replica snapshot (default: /tmp/openbkn-kn-data-migration-workloads.tsv)
  --timeout-seconds SECONDS   Stop/start timeout per Deployment (default: 300)
  -h, --help                  Show this help
EOF
}

action=${1:-}
case "$action" in
  stop|start|status)
    shift
    ;;
  -h|--help)
    usage
    exit 0
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac

namespace=${OPENBKN_MIGRATION_NAMESPACE:-openbkn}
expected_context=
state_file=${OPENBKN_MIGRATION_STATE_FILE:-/tmp/openbkn-kn-data-migration-workloads.tsv}
timeout_seconds=${OPENBKN_MIGRATION_TIMEOUT_SECONDS:-300}

while (( $# > 0 )); do
  case "$1" in
    --namespace)
      (( $# >= 2 )) || { echo "--namespace requires a value" >&2; exit 2; }
      namespace=$2
      shift 2
      ;;
    --namespace=*)
      namespace=${1#*=}
      shift
      ;;
    --expected-context)
      (( $# >= 2 )) || { echo "--expected-context requires a value" >&2; exit 2; }
      expected_context=$2
      shift 2
      ;;
    --expected-context=*)
      expected_context=${1#*=}
      shift
      ;;
    --state-file)
      (( $# >= 2 )) || { echo "--state-file requires a value" >&2; exit 2; }
      state_file=$2
      shift 2
      ;;
    --state-file=*)
      state_file=${1#*=}
      shift
      ;;
    --timeout-seconds)
      (( $# >= 2 )) || { echo "--timeout-seconds requires a value" >&2; exit 2; }
      timeout_seconds=$2
      shift 2
      ;;
    --timeout-seconds=*)
      timeout_seconds=${1#*=}
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

dns_label_pattern='^[a-z0-9]([-a-z0-9]*[a-z0-9])?$'
[[ $namespace =~ $dns_label_pattern ]] || {
  echo "invalid Kubernetes namespace: $namespace" >&2
  exit 2
}
[[ $timeout_seconds =~ ^[1-9][0-9]*$ ]] || {
  echo "--timeout-seconds must be a positive integer" >&2
  exit 2
}
command -v kubectl >/dev/null || {
  echo "kubectl is required" >&2
  exit 2
}

current_context=$(kubectl config current-context 2>/dev/null) || {
  echo "cannot resolve the current kubectl context" >&2
  exit 2
}
if [[ -n $expected_context && $current_context != "$expected_context" ]]; then
  echo "kubectl context mismatch: expected $expected_context, current $current_context" >&2
  exit 2
fi
expected_context=${expected_context:-$current_context}

kubectl_ns() {
  kubectl --context "$expected_context" --namespace "$namespace" "$@"
}

require_workloads() {
  local workload missing=false
  for workload in "${WORKLOADS[@]}"; do
    if ! kubectl_ns get deployment "$workload" >/dev/null 2>&1; then
      echo "required Deployment is missing: $namespace/$workload" >&2
      missing=true
    fi
  done
  [[ $missing == false ]]
}

declare -A saved_replicas=()
saved_namespace=
saved_context=

read_state() {
  local kind first second
  local format=
  saved_replicas=()
  saved_namespace=
  saved_context=
  [[ -f $state_file ]] || {
    echo "replica state file does not exist: $state_file" >&2
    return 1
  }
  while IFS=$'\t' read -r kind first second; do
    case "$kind" in
      format) format=$first ;;
      namespace) saved_namespace=$first ;;
      context) saved_context=$first ;;
      deployment)
        [[ $second =~ ^[0-9]+$ ]] || {
          echo "invalid replica state for $first" >&2
          return 1
        }
        saved_replicas["$first"]=$second
        ;;
      "") ;;
      *)
        echo "invalid replica state record: $kind" >&2
        return 1
        ;;
    esac
  done < "$state_file"
  [[ $format == "$STATE_FORMAT" ]] || {
    echo "unrecognized replica state file: $state_file" >&2
    return 1
  }
  [[ $saved_namespace == "$namespace" ]] || {
    echo "replica state namespace mismatch: $saved_namespace" >&2
    return 1
  }
  [[ $saved_context == "$expected_context" ]] || {
    echo "replica state context mismatch: $saved_context" >&2
    return 1
  }
  local workload
  for workload in "${WORKLOADS[@]}"; do
    [[ -n ${saved_replicas[$workload]+present} ]] || {
      echo "replica state is missing Deployment: $workload" >&2
      return 1
    }
  done
}

capture_state() {
  local state_directory temporary_state workload replicas
  state_directory=$(dirname -- "$state_file")
  [[ -d $state_directory ]] || {
    echo "replica state directory does not exist: $state_directory" >&2
    return 1
  }
  temporary_state=$(mktemp "$state_file.tmp.XXXXXX")
  {
    printf 'format\t%s\n' "$STATE_FORMAT"
    printf 'namespace\t%s\n' "$namespace"
    printf 'context\t%s\n' "$expected_context"
    for workload in "${WORKLOADS[@]}"; do
      replicas=$(kubectl_ns get deployment "$workload" -o jsonpath='{.spec.replicas}')
      [[ $replicas =~ ^[0-9]+$ ]] || {
        echo "invalid replica count for $workload: $replicas" >&2
        rm -f -- "$temporary_state"
        return 1
      }
      printf 'deployment\t%s\t%s\n' "$workload" "$replicas"
    done
  } > "$temporary_state"
  mv -- "$temporary_state" "$state_file"
  read_state
}

wait_until_stopped() {
  local workload=$1
  local deadline status replicas ready available
  deadline=$(( $(date +%s) + timeout_seconds ))
  while true; do
    status=$(kubectl_ns get deployment "$workload" -o \
      jsonpath='{.status.replicas}{" "}{.status.readyReplicas}{" "}{.status.availableReplicas}')
    read -r replicas ready available <<< "$status"
    replicas=${replicas:-0}
    ready=${ready:-0}
    available=${available:-0}
    if [[ $replicas == 0 && $ready == 0 && $available == 0 ]]; then
      return 0
    fi
    if (( $(date +%s) >= deadline )); then
      echo "Deployment did not stop within ${timeout_seconds}s: $namespace/$workload" >&2
      return 1
    fi
    sleep 2
  done
}

stop_workloads() {
  require_workloads
  if [[ -f $state_file ]]; then
    read_state
    echo "Reusing replica snapshot: $state_file"
  else
    capture_state
    echo "Saved replica snapshot: $state_file"
  fi

  local workload
  for workload in "${STOP_ORDER[@]}"; do
    echo "Stopping Deployment $namespace/$workload"
    kubectl_ns scale "deployment/$workload" --replicas=0 >/dev/null
    wait_until_stopped "$workload"
  done
  echo "Migration application workloads are stopped."
}

start_workloads() {
  read_state
  require_workloads

  local workload replicas
  for workload in "${START_ORDER[@]}"; do
    replicas=${saved_replicas[$workload]}
    echo "Restoring Deployment $namespace/$workload to $replicas replicas"
    kubectl_ns scale "deployment/$workload" --replicas="$replicas" >/dev/null
    if (( replicas > 0 )); then
      kubectl_ns rollout status "deployment/$workload" \
        --timeout="${timeout_seconds}s"
    fi
  done
  rm -f -- "$state_file"
  echo "Migration application workloads are restored."
}

show_status() {
  local workload status
  printf 'context=%s namespace=%s\n' "$expected_context" "$namespace"
  for workload in "${WORKLOADS[@]}"; do
    if ! kubectl_ns get deployment "$workload" >/dev/null 2>&1; then
      printf '%s missing\n' "$workload"
      continue
    fi
    status=$(kubectl_ns get deployment "$workload" -o \
      jsonpath='{.spec.replicas}{" "}{.status.readyReplicas}{" "}{.status.availableReplicas}')
    printf '%s desired-ready-available=%s\n' "$workload" "$status"
  done
}

case "$action" in
  stop) stop_workloads ;;
  start) start_workloads ;;
  status) show_status ;;
esac
