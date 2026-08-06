#!/usr/bin/env bash
# Regression tests for durable BKN Trace installation prerequisites.
set -euo pipefail

PASS=0
FAILED=0
CALLS=()
EXISTING_SECRETS=""

ok() { PASS=$((PASS + 1)); }
fail() { echo "FAIL: $*"; FAILED=$((FAILED + 1)); }
assert_contains() {
    local label="$1" expected="$2" call
    for call in "${CALLS[@]-}"; do
        if [[ "${call}" == *"${expected}"* ]]; then
            ok
            return
        fi
    done
    fail "${label}: missing [${expected}]"
}

kubectl() {
    CALLS+=("$*")
    case "$*" in
        *"get secret"*)
            local name
            name="$(printf '%s' "$*" | awk '{print $3}')"
            [[ " ${EXISTING_SECRETS} " == *" ${name} "* ]]
            ;;
        *) return 0 ;;
    esac
}
log_error() { :; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CONFIG_YAML_PATH="$(mktemp)"
trap 'rm -f "${CONFIG_YAML_PATH}"' EXIT
# shellcheck source=../services/openbkn.sh
source "${SCRIPT_DIR}/scripts/services/openbkn.sh"

config_yaml_dep_field() {
    local section="$1" field="$2"
    awk -v section="${section}:" -v field="${field}:" '
        $1 == section { inside=1; next }
        inside && NF && $0 !~ /^    / { exit }
        inside && $1 == field { print $2; exit }
    ' "${CONFIG_YAML_PATH}"
}

cat > "${CONFIG_YAML_PATH}" <<'EOF'
depServices:
  rds:
    source_type: internal
    host: mariadb.resource.svc.cluster.local
    port: 3306
    user: openbkn
    password: trace-password
EOF
_openbkn_prepare_trace_profile openbkn
assert_contains "creates the Core DSN Secret" "create secret generic bkn-trace-core-mariadb"
assert_contains "uses the Trace database DSN" "/bkn_trace?charset=utf8mb4"

CALLS=()
cat > "${CONFIG_YAML_PATH}" <<'EOF'
depServices:
  rds:
    source_type: external
EOF
if _openbkn_prepare_trace_profile openbkn; then
    fail "external RDS without the Core DSN Secret must fail"
else
    ok
fi
assert_contains "checks for an external Core DSN Secret" "get secret bkn-trace-core-mariadb"

CALLS=()
EXISTING_SECRETS="bkn-trace-core-mariadb"
_openbkn_prepare_trace_profile openbkn
if printf '%s\n' "${CALLS[@]}" | grep -q "create secret generic"; then
    fail "existing Core DSN Secret must be reused"
else
    ok
fi

if [[ "${FAILED}" -eq 0 ]]; then
    echo "openbkn_trace_prerequisites_test: all ${PASS} checks passed"
    exit 0
fi
echo "openbkn_trace_prerequisites_test: ${FAILED} failures"
exit 1
