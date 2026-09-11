#!/usr/bin/env bash
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

set -euo pipefail

script_directory=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
control_script="$script_directory/service_control.sh"
temporary_directory=$(mktemp -d)
fake_binary_directory="$temporary_directory/bin"
fake_cluster_directory="$temporary_directory/cluster"
calls_file="$temporary_directory/calls"

cleanup() {
  [[ -n $temporary_directory && $temporary_directory == /tmp/* ]] || return
  rm -rf -- "$temporary_directory"
}
trap cleanup EXIT

mkdir -p "$fake_binary_directory" "$fake_cluster_directory"
cat > "$fake_binary_directory/kubectl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

printf '%s\n' "$*" >> "$FAKE_KUBE_CALLS"
if [[ ${1:-} == config && ${2:-} == current-context ]]; then
  printf '%s' test-context
  exit 0
fi

if [[ ${1:-} == --context ]]; then
  shift 2
fi
if [[ ${1:-} == --namespace ]]; then
  shift 2
fi

command=${1:-}
case "$command" in
  get)
    resource=${2:-}
    name=${3:-}
    [[ $resource == deployment ]] || exit 2
    [[ ${FAKE_KUBE_MISSING:-} != "$name" ]] || exit 1
    replica_file="$FAKE_KUBE_CLUSTER/$name"
    [[ -f $replica_file ]] || exit 1
    replicas=$(<"$replica_file")
    output=
    while (( $# > 0 )); do
      if [[ $1 == -o ]]; then
        output=${2:-}
        break
      fi
      shift
    done
    if [[ -z $output ]]; then
      exit 0
    fi
    if [[ $output == *status.replicas* ]]; then
      printf '%s %s %s' "$replicas" "$replicas" "$replicas"
    elif [[ $output == *status.readyReplicas* ]]; then
      printf '%s %s %s' "$replicas" "$replicas" "$replicas"
    else
      printf '%s' "$replicas"
    fi
    ;;
  scale)
    target=${2:-}
    name=${target#deployment/}
    replicas=
    shift 2
    while (( $# > 0 )); do
      case "$1" in
        --replicas=*) replicas=${1#*=} ;;
      esac
      shift
    done
    [[ $replicas =~ ^[0-9]+$ ]] || exit 2
    printf '%s' "$replicas" > "$FAKE_KUBE_CLUSTER/$name"
    ;;
  rollout)
    target=${3:-}
    name=${target#deployment/}
    if [[ ${FAKE_KUBE_FAIL_ROLLOUT:-} == "$name" ]]; then
      exit 1
    fi
    ;;
  *)
    exit 2
    ;;
esac
EOF
chmod +x "$fake_binary_directory/kubectl"

export PATH="$fake_binary_directory:$PATH"
export FAKE_KUBE_CALLS="$calls_file"
export FAKE_KUBE_CLUSTER="$fake_cluster_directory"

reset_cluster() {
  : > "$calls_file"
  printf '1' > "$fake_cluster_directory/ontology-query"
  printf '1' > "$fake_cluster_directory/vega-backend"
  printf '1' > "$fake_cluster_directory/agent-operator-integration"
  printf '2' > "$fake_cluster_directory/bkn-backend"
  printf '1' > "$fake_cluster_directory/bkn-safe"
}

run_control() {
  "$control_script" "$@" --namespace openbkn
}

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

reset_cluster
state_file="$temporary_directory/success-state.tsv"
run_control stop --state-file "$state_file" >/dev/null
[[ -f $state_file ]] || fail "stop did not persist the replica snapshot"
run_control verify-stopped --state-file "$state_file" >/dev/null ||
  fail "verify-stopped rejected stopped workloads"
grep -q $'deployment\tbkn-backend\t2' "$state_file" ||
  fail "replica snapshot did not preserve bkn-backend=2"
stop_order=$(grep ' --replicas=0$' "$calls_file" |
  sed -E 's#.*deployment/([^ ]+) --replicas=0#\1#')
expected_stop_order=$'vega-backend\nontology-query\nagent-operator-integration\nbkn-backend\nbkn-safe'
[[ $stop_order == "$expected_stop_order" ]] ||
  fail "unexpected stop order: $stop_order"

: > "$calls_file"
run_control start --state-file "$state_file" >/dev/null
[[ ! -e $state_file ]] || fail "successful start did not remove the consumed state"
start_order=$(grep ' scale deployment/' "$calls_file" |
  sed -E 's#.*deployment/([^ ]+) --replicas=[0-9]+#\1#')
expected_start_order=$'bkn-safe\nbkn-backend\nvega-backend\nontology-query\nagent-operator-integration'
[[ $start_order == "$expected_start_order" ]] ||
  fail "unexpected start order: $start_order"
[[ $(<"$fake_cluster_directory/bkn-backend") == 2 ]] ||
  fail "start did not restore the original bkn-backend replicas"

reset_cluster
not_stopped_state_file="$temporary_directory/not-stopped-state.tsv"
run_control stop --state-file "$not_stopped_state_file" >/dev/null
printf '1' > "$fake_cluster_directory/vega-backend"
set +e
run_control verify-stopped --state-file "$not_stopped_state_file" >/dev/null 2>&1
not_stopped_status=$?
set -e
[[ $not_stopped_status -ne 0 ]] || fail "verify-stopped accepted a running workload"

reset_cluster
failed_state_file="$temporary_directory/failed-state.tsv"
run_control stop --state-file "$failed_state_file" >/dev/null
: > "$calls_file"
set +e
FAKE_KUBE_FAIL_ROLLOUT=bkn-backend run_control start --state-file "$failed_state_file" >/dev/null 2>&1
failed_status=$?
set -e
[[ $failed_status -ne 0 ]] || fail "start unexpectedly succeeded"
[[ -f $failed_state_file ]] || fail "failed start removed the recovery state"
if grep -q 'scale deployment/ontology-query --replicas=1' "$calls_file"; then
  fail "dependent workload started after bkn-backend rollout failed"
fi

reset_cluster
missing_state_file="$temporary_directory/missing-state.tsv"
set +e
FAKE_KUBE_MISSING=ontology-query run_control stop --state-file "$missing_state_file" >/dev/null 2>&1
missing_status=$?
set -e
[[ $missing_status -ne 0 ]] || fail "stop unexpectedly accepted a missing workload"
[[ ! -e $missing_state_file ]] || fail "preflight failure created a state file"
if grep -q ' scale deployment/' "$calls_file"; then
  fail "preflight failure changed a Deployment"
fi

reset_cluster
set +e
run_control stop --expected-context another-context \
  --state-file "$temporary_directory/context-state.tsv" >/dev/null 2>&1
context_status=$?
set -e
[[ $context_status -ne 0 ]] || fail "explicit context mismatch unexpectedly succeeded"
if grep -q ' scale deployment/' "$calls_file"; then
  fail "context mismatch changed a Deployment"
fi

echo "service_control tests passed"
