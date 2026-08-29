#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
apply_script="$script_dir/apply.sh"
rollback_script="$script_dir/rollback.sh"
verify_script="$script_dir/verify.sh"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_contains() {
  local file=$1 expected=$2
  grep -F -- "$expected" "$file" >/dev/null || fail "$file does not contain: $expected"
}

setup_case() {
  case_dir=$(mktemp -d)
  mkdir -p "$case_dir/bin"
  command_log="$case_dir/commands.log"
  : >"$command_log"

  cat >"$case_dir/bin/helm" <<'EOF'
#!/usr/bin/env bash
printf 'helm'
printf ' %q' "$@"
printf '\n'
EOF
  cat >"$case_dir/bin/kubectl" <<'EOF'
#!/usr/bin/env bash
printf 'kubectl'
printf ' %q' "$@"
printf '\n'
EOF
  chmod +x "$case_dir/bin/helm" "$case_dir/bin/kubectl"
}

teardown_case() {
  rm -rf "$case_dir"
}

run_script() {
  local script=$1
  shift
  set +e
  PATH="$case_dir/bin:$PATH" "$script" "$@" >"$case_dir/output.log" 2>"$case_dir/error.log"
  script_status=$?
  set -e
  return "$script_status"
}

case_dry_run_is_explicit_and_non_mutating() {
  setup_case
  if ! run_script "$apply_script" \
    --dry-run \
    --registry registry.example/openbkn-ai \
    --tag 0.1.4-supply-sample-p1; then
    cat "$case_dir/error.log" >&2
    fail "dry-run failed"
  fi
  assert_contains "$case_dir/output.log" "mode=dry-run"
  assert_contains "$case_dir/output.log" "agent-retrieval=registry.example/openbkn-ai/agent-retrieval:0.1.4-supply-sample-p1"
  assert_contains "$case_dir/output.log" "agent-operator-integration=registry.example/openbkn-ai/agent-operator-integration:0.1.4-supply-sample-p1"
  assert_contains "$case_dir/output.log" "sandbox-control-plane=registry.example/openbkn-ai/sandbox-control-plane:0.1.4-supply-sample-p1"
  [[ ! -s $case_dir/error.log ]] || fail "dry-run wrote stderr"
  teardown_case
}

case_apply_upgrades_only_the_three_patch_services() {
  setup_case
  if ! run_script "$apply_script" \
    --yes \
    --namespace demo \
    --registry registry.example/openbkn-ai \
    --tag 0.1.4-supply-sample-p1; then
    cat "$case_dir/error.log" >&2
    fail "apply failed"
  fi
  assert_contains "$case_dir/output.log" "patch apply complete"
  assert_contains "$case_dir/output.log" "helm upgrade --install agent-retrieval"
  assert_contains "$case_dir/output.log" "helm upgrade --install agent-operator-integration"
  assert_contains "$case_dir/output.log" "helm upgrade --install sandbox"
  assert_contains "$case_dir/output.log" "kubectl -n demo rollout status deployment/agent-retrieval"
  assert_contains "$case_dir/output.log" "kubectl -n demo rollout status deployment/agent-operator-integration"
  assert_contains "$case_dir/output.log" "kubectl -n demo rollout status deployment/sandbox-control-plane"
  teardown_case
}

case_rollback_requires_an_explicit_target_tag() {
  setup_case
  if run_script "$rollback_script" --yes --registry registry.example/openbkn-ai; then
    fail "rollback without --tag succeeded"
  fi
  assert_contains "$case_dir/error.log" "--tag is required"
  teardown_case
}

case_rollback_changes_only_the_three_patch_services() {
  setup_case
  if ! run_script "$rollback_script" \
    --yes \
    --namespace demo \
    --registry registry.example/openbkn-ai \
    --tag 0.1.4-release; then
    cat "$case_dir/error.log" >&2
    fail "rollback failed"
  fi
  assert_contains "$case_dir/output.log" "helm upgrade --install agent-retrieval"
  assert_contains "$case_dir/output.log" "helm upgrade --install agent-operator-integration"
  assert_contains "$case_dir/output.log" "helm upgrade --install sandbox"
  assert_contains "$case_dir/output.log" "patch rollback complete"
  teardown_case
}

case_verify_checks_all_patched_deployments() {
  setup_case
  if ! run_script "$verify_script" --namespace demo; then
    cat "$case_dir/error.log" >&2
    fail "verify failed"
  fi
  assert_contains "$case_dir/output.log" "kubectl -n demo rollout status deployment/agent-retrieval"
  assert_contains "$case_dir/output.log" "kubectl -n demo rollout status deployment/agent-operator-integration"
  assert_contains "$case_dir/output.log" "kubectl -n demo rollout status deployment/sandbox-control-plane"
  assert_contains "$case_dir/output.log" "MCP acceptance is required"
  teardown_case
}

case_dry_run_is_explicit_and_non_mutating
case_apply_upgrades_only_the_three_patch_services
case_rollback_requires_an_explicit_target_tag
case_rollback_changes_only_the_three_patch_services
case_verify_checks_all_patched_deployments
echo "test_patch_scripts: all checks passed"
