#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
cleanup_script="$script_dir/cleanup_legacy_bkn_trace_data.sh"
requested_case=${1:-all}
tests_run=0

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_contains() {
  local file=$1 expected=$2
  grep -F -- "$expected" "$file" >/dev/null || fail "$file does not contain: $expected"
}

assert_not_contains() {
  local file=$1 unexpected=$2
  if grep -F -- "$unexpected" "$file" >/dev/null; then
    fail "$file unexpectedly contains: $unexpected"
  fi
}

setup_case() {
  case_dir=$(mktemp -d)
  mkdir -p "$case_dir/bin" "$case_dir/state"
  command_log="$case_dir/commands.log"
  output_file="$case_dir/output.log"
  : >"$command_log"

  cat >"$case_dir/config.yaml" <<'YAML'
namespace: openbkn
depServices:
  rds:
    host: mariadb.resource.svc.cluster.local
    port: 3306
    root_password: root-secret
  opensearch:
    host: opensearch.resource.svc.cluster.local
    port: 9200
    protocol: https
    user: admin
    password: search-secret
YAML

  cat >"$case_dir/bin/kubectl" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
printf '%q ' "$@" >>"$MOCK_COMMAND_LOG"
printf '\n' >>"$MOCK_COMMAND_LOG"
joined=" $* "

if [[ $joined == *" config current-context "* ]]; then
  printf '%s\n' "${MOCK_CONTEXT:-production-cluster}"
  exit 0
fi

if [[ $joined == *" get deployment agent-observability -o json "* ]]; then
  if [[ -f $MOCK_STATE_DIR/scaled ]]; then
    status='"replicas":0,"readyReplicas":0,"availableReplicas":0,"updatedReplicas":0'
  else
    status='"replicas":1,"readyReplicas":1,"availableReplicas":1,"updatedReplicas":1'
  fi
  printf '%s\n' "{\"spec\":{\"replicas\":1,\"selector\":{\"matchLabels\":{\"app\":\"agent-observability\"}},\"template\":{\"spec\":{\"containers\":[{\"name\":\"agent-observability\",\"env\":[{\"name\":\"OPENSEARCH_TRACE_INDEX\",\"value\":\"trace-v013\"},{\"name\":\"OPENSEARCH_EVIDENCE_INDEX\",\"value\":\"evidence-v013\"},{\"name\":\"BKN_TRACE_PROJECTION_INDEX\",\"value\":\"bkn-trace-core\"}] }]}}},\"status\":{$status}}"
  exit 0
fi

if [[ $joined == *" get endpointslice "* ]]; then
  if [[ $joined == *"service-name=mariadb"* ]]; then pod=mariadb-0; else pod=opensearch-0; fi
  printf '%s\n' "{\"items\":[{\"endpoints\":[{\"conditions\":{\"ready\":true},\"targetRef\":{\"kind\":\"Pod\",\"name\":\"$pod\"}}]}]}"
  exit 0
fi

if [[ $joined == *" scale deployment agent-observability --replicas=0 "* ]]; then
  : >"$MOCK_STATE_DIR/scaled"
  exit 0
fi

if [[ $joined == *" wait --for=delete pod "* ]]; then exit 0; fi

if [[ $joined == *" exec "* && $joined != *" exec -i "* && $joined == *" command -v "* ]]; then exit 0; fi

if [[ $joined == *" exec "* && $joined == *" SELECT table_name FROM information_schema.tables "* ]]; then
  if [[ ! -f $MOCK_STATE_DIR/dropped ]]; then
    printf '%s\n' bkn_trace_conversations
    [[ ${MOCK_UNEXPECTED_TABLE:-0} == 1 ]] && printf '%s\n' unrelated_application_table
  fi
  exit 0
fi

if [[ $joined == *" exec "* && $joined == *" table_name = 'bkn_trace_conversations' "* ]]; then
  if [[ -f $MOCK_STATE_DIR/dropped ]]; then printf '0\n'; else printf '1\n'; fi
  exit 0
fi

if [[ $joined == *" exec "* && $joined == *" SELECT COUNT(*) FROM bkn_trace_conversations "* ]]; then
  printf '7\n'
  exit 0
fi

if [[ $joined == *" exec "* && $joined == *" SET FOREIGN_KEY_CHECKS=0; SET FOREIGN_KEY_CHECKS=1; "* ]]; then
  exit 0
fi

if [[ $joined == *" exec "* && $joined == *" DROP TABLE IF EXISTS "* ]]; then
  : >"$MOCK_STATE_DIR/dropped"
  printf '%s\n' destructive:mysql-drop >>"$MOCK_COMMAND_LOG"
  exit 0
fi

if [[ $joined == *" exec "* && $joined == *" SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() "* ]]; then
  if [[ -f $MOCK_STATE_DIR/dropped ]]; then printf '0\n'; else printf '1\n'; fi
  exit 0
fi

if [[ $joined == *" exec "* && $joined == *" /_alias/bkn-trace-core "* ]]; then
  if [[ ${MOCK_PROJECTION_ABSENT:-0} == 1 || -f $MOCK_STATE_DIR/projection_deleted ]]; then
    status=${MOCK_VERIFY_STATUS:-404}
    if [[ $status == transport ]]; then exit 7; fi
    printf '{"error":"mock"}\n%s\n' "$status"
  else
    printf '{"projection-v013":{"aliases":{"bkn-trace-core":{}}}}\n200\n'
  fi
  exit 0
fi

if [[ $joined == *" exec "* && $joined == *" /projection-v013/_count "* ]]; then
  printf '{"count":3}\n200\n'
  exit 0
fi

if [[ $joined == *" exec "* && $joined == *" DELETE "* && $joined == *" /projection-v013 "* ]]; then
  : >"$MOCK_STATE_DIR/projection_deleted"
  printf '%s\n' destructive:projection-delete >>"$MOCK_COMMAND_LOG"
  printf '{"acknowledged":true}\n200\n'
  exit 0
fi

if [[ $joined == *" exec "* && $joined == *" /projection-v013 "* ]]; then
  if [[ -f $MOCK_STATE_DIR/projection_deleted ]]; then
    printf '{"error":"not found"}\n404\n'
  else
    printf '{"projection-v013":{}}\n200\n'
  fi
  exit 0
fi

if [[ $joined == *" exec "* && ( $joined == *" /trace-v013/_count "* || $joined == *" /evidence-v013/_count "* ) ]]; then
  if [[ ${MOCK_INDEX_COUNT_STATUS:-} != "" ]]; then
    printf '{"error":"mock"}\n%s\n' "$MOCK_INDEX_COUNT_STATUS"
    exit 0
  fi
  if [[ ${MOCK_TRACE_INDEX_ABSENT:-0} == 1 && $joined == *" /trace-v013/_count "* ]]; then
    printf '{"error":"not found"}\n404\n'
    exit 0
  fi
  if [[ ${MOCK_EVIDENCE_INDEX_ABSENT:-0} == 1 && $joined == *" /evidence-v013/_count "* ]]; then
    printf '{"error":"not found"}\n404\n'
    exit 0
  fi
  if [[ ${MOCK_INDEX_MISSING_AFTER_SCALE:-0} == 1 && -f $MOCK_STATE_DIR/scaled ]]; then
    printf '{"error":"not found"}\n404\n'
    exit 0
  fi
  count=5
  if [[ -f $MOCK_STATE_DIR/deleted ]]; then count=0; fi
  if [[ ${MOCK_DRIFT:-0} == 1 && -f $MOCK_STATE_DIR/scaled && ! -f $MOCK_STATE_DIR/deleted ]]; then
    calls_file="$MOCK_STATE_DIR/count_calls"
    calls=0
    [[ -f $calls_file ]] && calls=$(<"$calls_file")
    calls=$((calls + 1)); printf '%s' "$calls" >"$calls_file"
    if (( calls > 2 )); then count=6; fi
  fi
  printf '{"count":%s}\n200\n' "$count"
  exit 0
fi

if [[ $joined == *" exec "* && $joined == *"_delete_by_query"* ]]; then
  : >"$MOCK_STATE_DIR/deleted"
  if [[ $joined == *"/evidence-v013/_delete_by_query"* ]]; then
    printf '%s\n' destructive:delete-by-query:evidence-v013 >>"$MOCK_COMMAND_LOG"
  else
    printf '%s\n' destructive:delete-by-query:trace-v013 >>"$MOCK_COMMAND_LOG"
  fi
  printf '{"failures":[]}\n200\n'
  exit 0
fi

echo "unhandled kubectl invocation: $*" >&2
exit 90
MOCK
  chmod +x "$case_dir/bin/kubectl"
}

teardown_case() {
  rm -rf "$case_dir"
}

run_cleanup() {
  local status
  set +e
  PATH="$case_dir/bin:$PATH" \
    MOCK_COMMAND_LOG="$command_log" \
    MOCK_STATE_DIR="$case_dir/state" \
    BKN_TRACE_CLEANUP_CONFIG="$case_dir/config.yaml" \
    BKN_TRACE_CLEANUP_QUIESCE_SECONDS=0 \
    BKN_TRACE_CLEANUP_OPENSEARCH_INSECURE=true \
    bash "$cleanup_script" "$@" >"$output_file" 2>&1
  status=$?
  set -e
  return "$status"
}

case_confirmation() {
  setup_case
  if run_cleanup --confirm --expected-context production-cluster --backup-confirmed; then
    fail "confirm without --writes-quiesced succeeded"
  fi
  assert_contains "$output_file" "--writes-quiesced is required with --confirm"
  assert_not_contains "$command_log" " scale "
  teardown_case
}

case_preview() {
  setup_case
  run_cleanup || { sed -n '1,240p' "$output_file" >&2; fail "preview failed"; }
  assert_contains "$output_file" "mode=preview"
  assert_contains "$output_file" "kubectl_context=production-cluster"
  assert_not_contains "$command_log" " scale "
  assert_not_contains "$command_log" "destructive:"
  teardown_case
}

case_context() {
  setup_case
  if run_cleanup --confirm --expected-context wrong-cluster --backup-confirmed --writes-quiesced; then
    fail "mismatched context succeeded"
  fi
  assert_contains "$output_file" "kubectl context mismatch"
  assert_not_contains "$command_log" " scale "
  teardown_case
}

case_quiescence() {
  setup_case
  export MOCK_DRIFT=1
  if run_cleanup --confirm --expected-context production-cluster --backup-confirmed --writes-quiesced; then
    fail "changing storage counts passed quiescence admission"
  fi
  unset MOCK_DRIFT
  assert_contains "$output_file" "Trace storage changed during the quiescence observation window"
  assert_not_contains "$command_log" "destructive:"
  [[ -f $case_dir/state/scaled ]] || fail "deployment was not left scaled down"
  teardown_case
}

case_opensearch_status() {
  setup_case
  export MOCK_VERIFY_STATUS=500
  if run_cleanup --confirm --expected-context production-cluster --backup-confirmed --writes-quiesced; then
    fail "Projection verification accepted HTTP 500"
  fi
  unset MOCK_VERIFY_STATUS
  assert_contains "$output_file" "http_status=500"
  [[ -f $case_dir/state/scaled ]] || fail "deployment was not left scaled down"
  teardown_case
}

case_missing_index() {
  setup_case
  export MOCK_INDEX_MISSING_AFTER_SCALE=1
  if run_cleanup --confirm --expected-context production-cluster --backup-confirmed --writes-quiesced; then
    fail "missing OpenSearch index count passed cleanup admission"
  fi
  unset MOCK_INDEX_MISSING_AFTER_SCALE
  assert_contains "$output_file" "OpenSearch index disappeared during cleanup admission: trace-v013"
  assert_not_contains "$command_log" "destructive:"
  [[ -f $case_dir/state/scaled ]] || fail "deployment was not left scaled down"
  teardown_case
}

case_inventory() {
  setup_case
  export MOCK_UNEXPECTED_TABLE=1
  if run_cleanup; then
    fail "unexpected database table passed cleanup preview"
  fi
  unset MOCK_UNEXPECTED_TABLE
  assert_contains "$output_file" "outside the allowlist"
  assert_contains "$output_file" "unrelated_application_table"
  assert_not_contains "$command_log" " scale "
  assert_not_contains "$command_log" "destructive:"
  teardown_case
}

case_projection_absent() {
  setup_case
  export MOCK_PROJECTION_ABSENT=1
  run_cleanup --confirm --expected-context production-cluster --backup-confirmed --writes-quiesced || {
    sed -n '1,260p' "$output_file" >&2
    fail "absent projection alias failed cleanup"
  }
  unset MOCK_PROJECTION_ABSENT
  assert_contains "$output_file" "projection_alias=bkn-trace-core status=absent action=already_clean"
  assert_not_contains "$command_log" "destructive:projection-delete"
  teardown_case
}

case_evidence_absent() {
  setup_case
  export MOCK_EVIDENCE_INDEX_ABSENT=1
  run_cleanup --confirm --expected-context production-cluster --backup-confirmed --writes-quiesced || {
    sed -n '1,260p' "$output_file" >&2
    fail "absent Evidence index failed cleanup"
  }
  unset MOCK_EVIDENCE_INDEX_ABSENT
  assert_contains "$output_file" "opensearch evidence-v013 status=absent action=already_clean"
  assert_contains "$command_log" "destructive:delete-by-query:trace-v013"
  assert_not_contains "$command_log" "destructive:delete-by-query:evidence-v013"
  teardown_case
}

case_trace_absent() {
  setup_case
  export MOCK_TRACE_INDEX_ABSENT=1
  run_cleanup --confirm --expected-context production-cluster --backup-confirmed --writes-quiesced || {
    sed -n '1,260p' "$output_file" >&2
    fail "absent Trace index failed cleanup"
  }
  unset MOCK_TRACE_INDEX_ABSENT
  assert_contains "$output_file" "opensearch trace-v013 status=absent action=already_clean"
  assert_contains "$command_log" "destructive:mysql-drop"
  assert_contains "$command_log" "destructive:delete-by-query:evidence-v013"
  assert_not_contains "$command_log" "destructive:delete-by-query:trace-v013"
  teardown_case
}

case_all_indexes_absent() {
  setup_case
  export MOCK_TRACE_INDEX_ABSENT=1
  export MOCK_EVIDENCE_INDEX_ABSENT=1
  export MOCK_PROJECTION_ABSENT=1
  run_cleanup --confirm --expected-context production-cluster --backup-confirmed --writes-quiesced || {
    sed -n '1,260p' "$output_file" >&2
    fail "all absent targets failed first cleanup"
  }
  destructive_before=$(grep -c '^destructive:' "$command_log" || true)
  run_cleanup --confirm --expected-context production-cluster --backup-confirmed --writes-quiesced || {
    sed -n '1,260p' "$output_file" >&2
    fail "all absent targets failed second cleanup"
  }
  destructive_after=$(grep -c '^destructive:' "$command_log" || true)
  unset MOCK_TRACE_INDEX_ABSENT MOCK_EVIDENCE_INDEX_ABSENT MOCK_PROJECTION_ABSENT
  [[ $destructive_after == "$destructive_before" ]] || fail "second cleanup performed a destructive operation"
  assert_contains "$output_file" "opensearch trace-v013 status=absent action=already_clean"
  assert_contains "$output_file" "opensearch evidence-v013 status=absent action=already_clean"
  teardown_case
}

case_opensearch_count_error() {
  setup_case
  export MOCK_INDEX_COUNT_STATUS=500
  if run_cleanup --confirm --expected-context production-cluster --backup-confirmed --writes-quiesced; then
    fail "OpenSearch HTTP 500 passed cleanup admission"
  fi
  unset MOCK_INDEX_COUNT_STATUS
  assert_contains "$output_file" "path=/trace-v013/_count http_status=500"
  assert_not_contains "$command_log" " scale "
  assert_not_contains "$command_log" "destructive:"
  teardown_case
}

case_credentials() {
  assert_not_contains "$cleanup_script" '--user "$username:$password"'
}

case_success() {
  setup_case
  run_cleanup --confirm --expected-context production-cluster --backup-confirmed --writes-quiesced || {
    sed -n '1,260p' "$output_file" >&2
    fail "confirmed cleanup failed"
  }
  assert_contains "$output_file" "cleanup complete"
  assert_contains "$output_file" "remains at 0 replicas"
  assert_contains "$command_log" "destructive:mysql-drop"
  assert_contains "$command_log" "destructive:delete-by-query"
  assert_contains "$command_log" "destructive:projection-delete"
  [[ -f $case_dir/state/scaled ]] || fail "deployment was not left scaled down"
  teardown_case
}

case_tls() {
  setup_case
  unset BKN_TRACE_CLEANUP_OPENSEARCH_INSECURE || true
  set +e
  PATH="$case_dir/bin:$PATH" MOCK_COMMAND_LOG="$command_log" MOCK_STATE_DIR="$case_dir/state" \
    BKN_TRACE_CLEANUP_CONFIG="$case_dir/config.yaml" \
    BKN_TRACE_CLEANUP_OPENSEARCH_CA_FILE=/usr/share/opensearch/config/root-ca.pem \
    BKN_TRACE_CLEANUP_OPENSEARCH_INSECURE=true \
    bash "$cleanup_script" >"$output_file" 2>&1
  status=$?
  set -e
  [[ $status -ne 0 ]] || fail "conflicting CA and insecure modes succeeded"
  assert_contains "$output_file" "cannot be used together"
  teardown_case
}

run_case() {
  local name=$1
  if [[ $requested_case != all && $requested_case != "$name" ]]; then return; fi
  "case_$name"
  tests_run=$((tests_run + 1))
  echo "PASS: $name"
}

run_case confirmation
run_case preview
run_case context
run_case quiescence
run_case opensearch_status
run_case missing_index
run_case inventory
run_case projection_absent
run_case evidence_absent
run_case trace_absent
run_case all_indexes_absent
run_case opensearch_count_error
run_case credentials
run_case success
run_case tls

(( tests_run > 0 )) || fail "unknown test group: $requested_case"
echo "PASS: $tests_run test group(s)"
