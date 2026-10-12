#!/usr/bin/env bash
# Copyright openbkn.ai
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
# Real Safe Chart rendering, with no cluster reads or writes.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
foundry="${FOUNDRY_ROOT:-${root}/..}"
chart="${foundry}/bkn-safe/charts/bkn-safe"
source "${root}/scripts/lib/common.sh"
source "${root}/scripts/services/openbkn.sh"
case_dir="$(mktemp -d)"
trap 'rm -rf "${case_dir}"' EXIT
export KUBECONFIG="${case_dir}/nonexistent-kubeconfig"
CORE_NAMESPACE=audit-test
CONFIG_YAML_PATH="${case_dir}/config.yaml"
printf 'bknSafe:\n  initialPassword: initial-test-only\n' >"${CONFIG_YAML_PATH}"
CORE_SET_VALUES=()
INSTALLED='{}'
SAME_VERSION=true
config_yaml_dep_field() { case "$2" in mqHost) printf broker ;; mqPort) printf 9092 ;; esac; }
_bkn_safe_instance_id() { printf 001234; }
log_info() { :; }
log_warn() { :; }
should_skip_upgrade_same_chart_version() { [[ "${SAME_VERSION}" == true ]]; }
helm() {
    if [[ "${1:-}" == status ]]; then printf '{"info":{"status":"deployed"}}'; return 0; fi
    [[ "$*" == 'get values bkn-safe -n audit-test --all -o json' ]] || { echo "Unexpected Helm call: $*" >&2; return 1; }
    printf '%s' "${INSTALLED}"
}
# Render the actual arguments produced by both installer entry points.
_openbkn_resolve_release_version() { printf 0.1.0; }
_openbkn_resolve_chart_name() { printf bkn-safe; }
find_cached_chart_tgz_by_version() { printf '%s' "${chart}"; }
build_chart_ref() { printf '%s' "${chart}"; }
INSTALL_CALLS=0
_openbkn_helm_upgrade_release() {
    local release="$1" namespace="$2"
    shift 2
    local -a args=("$@")
    [[ "${release}" == bkn-safe && "${namespace}" == audit-test ]]
    INSTALL_CALLS=$((INSTALL_CALLS + 1))
    command helm install "${args[@]:2}" --dry-run=client --hide-secret -o json >"${case_dir}/render.json"
}
check_render() {
    python3 - "${case_dir}/render.json" "$@" <<'PY'
import json, re, sys
rendered = json.load(open(sys.argv[1]))
values = rendered['config']
audit = values['auditPublisher']
assert audit['enabled'] is True
assert isinstance(audit['brokers'], str)
assert audit['environment'] == sys.argv[2]
assert audit['brokers'] == sys.argv[3]
assert audit['credentialsSecretName'] == sys.argv[4]
assert values['config']['license']['instanceId'] == '001234'
manifest = rendered['manifest']
for name, value in [('ENVIRONMENT', sys.argv[2]), ('KAFKA_BROKERS', sys.argv[3])]:
    assert re.search(r'name: BKN_AUDIT_' + name + r'\s+value: "' + re.escape(value) + '"', manifest)
assert re.search(r'name: BKN_AUDIT_KAFKA_SASL_MECHANISM\s+value: PLAIN', manifest)
for name, key in [('USERNAME', 'username'), ('PASSWORD', 'password')]:
    assert re.search(r'name: BKN_AUDIT_KAFKA_' + name + r'\s+valueFrom:\s+secretKeyRef:\s+name: "' + re.escape(sys.argv[4]) + r'"\s+key: "' + key + '"', manifest)
assert 'initial-test-only' == values['config']['initialPassword']
PY
}
for mode in local repo; do
  for state in fresh missing disabled; do
    INSTALLED='{}'
    SAME_VERSION=true
    [[ "${state}" != fresh ]] || SAME_VERSION=false
    [[ "${state}" != disabled ]] || INSTALLED='{"auditPublisher":{"enabled":false}}'
    if [[ "${mode}" == local ]]; then
        _install_openbkn_release_local bkn-safe "${case_dir}" audit-test || exit 1
    else
        _install_openbkn_release_repo bkn-safe audit-test ignored 0.1.0 || exit 1
    fi
    check_render development broker:9092 bkn-trace-evidence-kafka
    INSTALLED="$(python3 -c 'import json,sys; print(json.dumps(json.load(open(sys.argv[1]))["config"]))' "${case_dir}/render.json")"
    SAME_VERSION=true
    _openbkn_should_skip_upgrade bkn-safe audit-test bkn-safe 0.1.0 || exit 1
    INSTALLED='{"auditPublisher":{"enabled":false}}'
    if _openbkn_should_skip_upgrade bkn-safe audit-test bkn-safe 0.1.0; then
        echo 'Disabled Safe publisher must reconcile at the same Chart version' >&2; exit 1
    fi
  done
done
[[ "${INSTALL_CALLS}" -eq 6 ]]
# Explicit config and CLI values still win, including comma-separated brokers.
cat >"${CONFIG_YAML_PATH}" <<'YAML'
bknSafe:
  initialPassword: initial-test-only
auditPublisher:
  environment: production
  brokers: "broker-a:9092,broker-b:9092"
  credentialsSecretName: custom-kafka
YAML
CORE_SET_VALUES=(auditPublisher.environment=test)
_install_openbkn_release_local bkn-safe "${case_dir}" audit-test || exit 1
check_render test 'broker-a:9092,broker-b:9092' custom-kafka
INSTALLED="$(python3 -c 'import json,sys; print(json.dumps(json.load(open(sys.argv[1]))["config"]))' "${case_dir}/render.json")"
_openbkn_should_skip_upgrade bkn-safe audit-test bkn-safe 0.1.0 || exit 1
# The shared AO broker array must not leak into Safe's string-valued env.
printf 'bknSafe:\n  initialPassword: initial-test-only\nauditPublisher:\n  brokers: [broker-a:9092]\n' >"${CONFIG_YAML_PATH}"
CORE_SET_VALUES=()
_install_openbkn_release_local bkn-safe "${case_dir}" audit-test || exit 1
check_render development broker:9092 bkn-trace-evidence-kafka
# Version changes still upgrade; failed reads preserve the existing skip policy.
SAME_VERSION=false
if _openbkn_should_skip_upgrade bkn-safe audit-test bkn-safe 0.1.0; then exit 1; fi
SAME_VERSION=true
helm() { return 1; }
_openbkn_should_skip_upgrade bkn-safe audit-test bkn-safe 0.1.0 || exit 1
helm() { printf '{broken-json'; }
_openbkn_should_skip_upgrade bkn-safe audit-test bkn-safe 0.1.0 || exit 1
printf 'Safe Audit publisher install/upgrade and Chart rendering: PASS\n'
