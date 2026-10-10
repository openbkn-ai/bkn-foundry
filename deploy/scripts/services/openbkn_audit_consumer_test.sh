#!/usr/bin/env bash
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${SCRIPT_DIR}/scripts/services/openbkn.sh"
test_dir="$(mktemp -d)"
trap 'rm -rf "${test_dir}"' EXIT
export PATH="${test_dir}:${PATH}"
export AUDIT_TEST_SECRET_STATE=ready
export AUDIT_TEST_RENDER="${test_dir}/render.json"
cat >"${test_dir}/kubectl" <<'SH'
#!/usr/bin/env bash
case "${AUDIT_TEST_SECRET_STATE}" in
    missing) exit 1 ;;
    missing-key) printf '{"data":{"username":"dGVzdA==","dsn":"dGVzdA=="}}' ;;
    missing-dsn) printf '{"data":{"username":"dGVzdA==","password":"dGVzdA=="}}' ;;
    *) printf '{"data":{"username":"dGVzdA==","password":"dGVzdA==","dsn":"dGVzdA=="}}' ;;
esac
SH
chmod +x "${test_dir}/kubectl"
log_info() { :; }
log_error() { :; }
_openbkn_drop_literal_env_now_from_secret() { :; }
_openbkn_adopt_unowned_resources() { return 1; }
mq_test_host=kafka
config_yaml_dep_field() {
    case "$2" in mqHost) printf '%s' "${mq_test_host}" ;; mqPort) printf '9092' ;; mechanism) printf 'PLAIN' ;; esac
}
_openbkn_prepare_audit_topic() { return 0; }
should_skip_upgrade_same_chart_version() { return 1; }
touch "${test_dir}/upgrades"
assert_upgrades() { [[ "$(wc -l <"${test_dir}/upgrades")" -eq "$1" ]]; }
test_installed=true
old_values='{"kafkaConsumers":{"audit":{"enabled":true,"brokers":["kafka:9092"],"consumerGroup":"audit-ledger-v1","topic":"openbkn.audit.v1","saslMechanism":"SCRAM-SHA-256","existingSecret":{"name":"audit-kafka","usernameKey":"username","passwordKey":"password"},"password":"must-not-be-preserved"}},"auditPublisher":{"enabled":true,"environment":"test","brokers":["publisher:9092"],"saslMechanism":"SCRAM-SHA-256","existingSecret":{"name":"approved-publisher","usernameKey":"username","passwordKey":"password"}},"unrelated":"must-not-be-preserved"}'
helm() {
    case "$1" in
        list) [[ "${test_installed}" != true ]] || printf 'agent-observability\n' ;;
        get) printf '%s' "${old_values}" ;;
        install) command helm "$@" | tee "${AUDIT_TEST_RENDER}" ;;
        upgrade) printf 'upgrade\n' >>"${test_dir}/upgrades" ;;
        *) return 1 ;;
    esac
}
chart="${SCRIPT_DIR}/../bkn-trace/agent-observability/charts/agent-observability"
args=(upgrade --install agent-observability "${chart}" --namespace openbkn
    --set core.store=mariadb --set core.mariadb.existingSecret=core-db --set auditPublisher.environment=test)

# An enabled consumer survives upgrades, without carrying unrelated settings.
_openbkn_helm_upgrade_release agent-observability openbkn "${args[@]}"
assert_upgrades 1
python3 - "${AUDIT_TEST_RENDER}" <<'PY'
import json, sys
config = json.load(open(sys.argv[1]))["config"]
audit = config["kafkaConsumers"]["audit"]
assert audit["enabled"] and audit["brokers"] == ["kafka:9092"]
assert audit["consumerGroup"] == "audit-ledger-v1"
assert audit["saslMechanism"] == "PLAIN"
assert "password" not in audit and "unrelated" not in config
assert config["auditPublisher"]["brokers"] == ["kafka:9092"]
assert config["auditPublisher"]["saslMechanism"] == "PLAIN"
assert config["auditPublisher"]["existingSecret"]["name"] == "approved-publisher"
PY

# Complete installation does not expose an Audit disable option.
printf '{"kafkaConsumers":{"audit":{"enabled":false}}}' >"${test_dir}/disabled.json"
export AUDIT_TEST_SECRET_STATE=missing
for override in kafkaConsumers.audit.enabled=false auditPublisher.enabled=false; do
    if _openbkn_helm_upgrade_release agent-observability openbkn "${args[@]}" --set "${override}" 2>/dev/null; then
        echo 'Complete platform installation cannot disable Audit' >&2; exit 1
    fi
done
assert_upgrades 1

for state in missing missing-key missing-dsn; do
    export AUDIT_TEST_SECRET_STATE="${state}"
    if _openbkn_helm_upgrade_release agent-observability openbkn "${args[@]}" 2>/dev/null; then
        echo "enabled Audit consumer must reject ${state} Secret" >&2
        exit 1
    fi
done
assert_upgrades 1

# Helm treats a non-empty string as enabled too; it must not bypass preflight.
export AUDIT_TEST_SECRET_STATE=missing
if _openbkn_helm_upgrade_release agent-observability openbkn "${args[@]}" --set-string kafkaConsumers.audit.enabled=true 2>/dev/null; then
    echo "string-valued Audit enablement must still validate Secret references" >&2
    exit 1
fi
assert_upgrades 1

python3 - "${SCRIPT_DIR}/scripts/lib/audit_consumer.py" <<'PY'
import contextlib, io, json, runpy, subprocess, sys
from unittest.mock import patch
module = runpy.run_path(sys.argv[1])
payload = {"config": {"kafkaConsumers": {"audit": {"enabled": True, "existingSecret": {"name": "audit-kafka"}}}}}
stderr = io.StringIO()
with patch.object(sys, "argv", ["audit_consumer.py", "validate", "openbkn"]), \
     patch.object(sys, "stdin", io.StringIO(json.dumps(payload))), \
     patch.object(subprocess, "run", side_effect=subprocess.TimeoutExpired("kubectl", 15)), \
     contextlib.redirect_stderr(stderr):
    assert module["main"]() == 1
assert "Secret lookup timed out" in stderr.getvalue()
PY

# Complete first install fills both sides, while the standalone Chart stays opt-in.
test_installed=false
export AUDIT_TEST_SECRET_STATE=ready
_openbkn_helm_upgrade_release agent-observability openbkn "${args[@]}"
assert_upgrades 2
python3 - "${AUDIT_TEST_RENDER}" <<'PY'
import json, sys
values = json.load(open(sys.argv[1]))["config"]
assert values["kafkaConsumers"]["audit"]["enabled"] is True
assert values["auditPublisher"]["enabled"] is True
assert values["kafkaConsumers"]["audit"]["existingSecret"]["name"] == "bkn-trace-evidence-kafka"
PY
test_installed=true
old_values='{"kafkaConsumers":{"audit":{"enabled":false,"brokers":[],"consumerGroup":"","saslMechanism":"","existingSecret":{"name":""}}},"auditPublisher":{"enabled":false,"brokers":[],"saslMechanism":"","existingSecret":{"name":""}}}'
_openbkn_helm_upgrade_release agent-observability openbkn "${args[@]}"
assert_upgrades 3
# An unchanged version still renders/checks, but does not roll out again.
old_values="$(python3 - "${AUDIT_TEST_RENDER}" "${SCRIPT_DIR}/scripts/lib" <<'PY'
import json, sys
sys.path.insert(0, sys.argv[2])
from audit_consumer import rendered_system_values
print(json.dumps(rendered_system_values(json.load(open(sys.argv[1])))))
PY
)"
should_skip_upgrade_same_chart_version() { return 0; }
_openbkn_agent_observability_has_durable_profile() { return 0; }
_openbkn_helm_upgrade_release agent-observability openbkn "${args[@]}"
assert_upgrades 3
_openbkn_helm_upgrade_release agent-observability openbkn "${args[@]}" --set auditPublisher.environment=staging
assert_upgrades 4
# Today's MQ config replaces saved derived connections, even at equal versions.
mq_test_host=new-kafka
_openbkn_helm_upgrade_release agent-observability openbkn "${args[@]}"
assert_upgrades 5
python3 - "${AUDIT_TEST_RENDER}" <<'PY'
import json, sys
values = json.load(open(sys.argv[1]))["config"]
for section in (values["auditPublisher"], values["kafkaConsumers"]["audit"]):
    assert section["brokers"] == ["new-kafka:9092"]
    assert section["saslMechanism"] == "PLAIN"
assert values["auditPublisher"]["environment"] == "test"
PY
# Current component-specific inputs still override shared MQ defaults.
_openbkn_helm_upgrade_release agent-observability openbkn "${args[@]}" \
    --set 'auditPublisher.brokers[0]=explicit-publisher:9092' \
    --set 'kafkaConsumers.audit.brokers[0]=explicit-consumer:9092'
assert_upgrades 6
python3 - "${AUDIT_TEST_RENDER}" <<'PY'
import json, sys
values = json.load(open(sys.argv[1]))["config"]
assert values["auditPublisher"]["brokers"] == ["explicit-publisher:9092"]
assert values["kafkaConsumers"]["audit"]["brokers"] == ["explicit-consumer:9092"]
PY
# Topic failure must stop before any Helm upgrade, including equal versions.
_openbkn_prepare_audit_topic() { return 1; }
if _openbkn_helm_upgrade_release agent-observability openbkn "${args[@]}"; then
    echo 'Topic preparation failure must stop installation' >&2; exit 1
fi
assert_upgrades 6
# A caller may invoke install_openbkn in an if-condition, disabling Bash's
# implicit errexit throughout the function. Failure must still propagate.
OFFLINE_MODE=true
_openbkn_resolve_latest_manifest() { :; }
_openbkn_require_version_manifest() { :; }
_openbkn_apply_default_set_values() { :; }
ensure_platform_prerequisites() { :; }
_openbkn_resolve_target_namespace() { printf openbkn; }
_openbkn_resolve_charts_dir() { printf '%s' "${test_dir}"; }
_openbkn_release_exists() { return 1; }
init_openbkn_databases() { :; }
bkn_mapfile_compat() { release_names=(agent-observability); }
_openbkn_prepare_trace_profile() { :; }
_openbkn_warn_unwired_evidence_producers() { :; }
_openbkn_resolve_release_version() { printf 0.1.0; }
_install_openbkn_release_local() { return 1; }
_openbkn_uninstall_retired_releases() { :; }
gen_install_status_json() { :; }
_read_access_address_field() { :; }
config_yaml_top_field() { :; }
_openbkn_should_show_bkn_safe_initial_password() { return 1; }
if install_openbkn >"${test_dir}/install.log" 2>&1; then
    echo 'Installation driver must propagate failed Audit preflight' >&2; exit 1
fi
echo "Audit consumer preservation and preflight checks passed"
