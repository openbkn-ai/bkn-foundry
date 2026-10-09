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
touch "${test_dir}/upgrades"
assert_upgrades() { [[ "$(wc -l <"${test_dir}/upgrades")" -eq "$1" ]]; }
test_installed=true
old_values='{"kafkaConsumers":{"audit":{"enabled":true,"brokers":["kafka:9092"],"consumerGroup":"audit-ledger-v1","topic":"openbkn.audit.v1","saslMechanism":"PLAIN","existingSecret":{"name":"audit-kafka","usernameKey":"username","passwordKey":"password"},"password":"must-not-be-preserved"}},"unrelated":"must-not-be-preserved"}'
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
    --set core.store=mariadb --set core.mariadb.existingSecret=core-db)

# An enabled consumer survives upgrades, without carrying unrelated settings.
_openbkn_helm_upgrade_release agent-observability openbkn "${args[@]}"
assert_upgrades 1
python3 - "${AUDIT_TEST_RENDER}" <<'PY'
import json, sys
config = json.load(open(sys.argv[1]))["config"]
audit = config["kafkaConsumers"]["audit"]
assert audit["enabled"] and audit["brokers"] == ["kafka:9092"]
assert audit["consumerGroup"] == "audit-ledger-v1"
assert "password" not in audit and "unrelated" not in config
PY

# Explicit CLI and config-file changes override the preserved settings.
printf '{"kafkaConsumers":{"audit":{"enabled":false}}}' >"${test_dir}/disabled.json"
export AUDIT_TEST_SECRET_STATE=missing
_openbkn_helm_upgrade_release agent-observability openbkn "${args[@]}" -f "${test_dir}/disabled.json"
_openbkn_helm_upgrade_release agent-observability openbkn "${args[@]}" --set kafkaConsumers.audit.enabled=false
assert_upgrades 3

for state in missing missing-key missing-dsn; do
    export AUDIT_TEST_SECRET_STATE="${state}"
    if _openbkn_helm_upgrade_release agent-observability openbkn "${args[@]}" 2>/dev/null; then
        echo "enabled Audit consumer must reject ${state} Secret" >&2
        exit 1
    fi
done
assert_upgrades 3

# Helm treats a non-empty string as enabled too; it must not bypass preflight.
export AUDIT_TEST_SECRET_STATE=missing
if _openbkn_helm_upgrade_release agent-observability openbkn "${args[@]}" --set-string kafkaConsumers.audit.enabled=true 2>/dev/null; then
    echo "string-valued Audit enablement must still validate Secret references" >&2
    exit 1
fi
assert_upgrades 3

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

# First install remains opt-in and does not read Audit credentials when disabled.
test_installed=false
_openbkn_helm_upgrade_release agent-observability openbkn "${args[@]}"
assert_upgrades 4
echo "Audit consumer preservation and preflight checks passed"
