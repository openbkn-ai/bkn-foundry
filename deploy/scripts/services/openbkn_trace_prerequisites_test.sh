#!/usr/bin/env bash
# Regression tests for durable BKN Trace installation prerequisites.
set -euo pipefail

PASS=0
FAILED=0
CALLS=()
EXISTING_SECRETS=""
EXISTING_DSN_DATA="dHJhY2U6c2VjcmV0QHRjcChkYi5leGFtcGxlOjMzMDYpL2Jrbl90cmFjZT9jaGFyc2V0PXV0ZjhtYjQ="
EXISTING_TOKEN_DATA=""
KUBECTL_LOG="$(mktemp)"
LAST_ERROR=""

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=../lib/common.sh
source "${SCRIPT_DIR}/scripts/lib/common.sh"

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
    if grep -Fq -- "${expected}" "${KUBECTL_LOG}"; then
        ok
        return
    fi
    fail "${label}: missing [${expected}]"
}

kubectl() {
    CALLS+=("$*")
    printf '%s\n' "$*" >>"${KUBECTL_LOG}"
    case "$*" in
        *"get secret"*)
            local name
            name="$(printf '%s' "$*" | awk '{print $3}')"
            if [[ " ${EXISTING_SECRETS} " != *" ${name} "* ]]; then
                return 1
            fi
            if [[ "$*" == *"jsonpath={.data.dsn}"* ]]; then
                printf '%s' "${EXISTING_DSN_DATA}"
            elif [[ "$*" == *"jsonpath={.data.token}"* ]]; then
                printf '%s' "${EXISTING_TOKEN_DATA}"
            fi
            return 0
            ;;
        *"create secret"*)
            local stdin_value
            stdin_value="$(cat)"
            printf 'stdin:%s\n' "${stdin_value}" >>"${KUBECTL_LOG}"
            printf '%s\n' 'apiVersion: v1' 'kind: Secret'
            return 0
            ;;
        *"apply -f -"*)
            cat >/dev/null
            return 0
            ;;
        *) return 0 ;;
    esac
}
log_error() { LAST_ERROR="$*"; }
generate_random_password() { printf '%s' 'test-ingest-token'; }

CONFIG_YAML_PATH="$(mktemp)"
trap 'rm -f "${CONFIG_YAML_PATH}" "${KUBECTL_LOG}"' EXIT
# shellcheck source=../services/openbkn.sh
source "${SCRIPT_DIR}/scripts/services/openbkn.sh"

cat > "${CONFIG_YAML_PATH}" <<'EOF'
depServices:
  rds:
    source_type: "internal"
    host: "mariadb.resource.svc.cluster.local"
    port: "3306"
    user: "openbkn"
    password: "trace-password"
EOF
_openbkn_prepare_trace_profile openbkn
assert_contains "creates the Evidence ingest Secret before releases" "create secret generic bkn-trace-evidence-ingest"
assert_contains "keeps the Evidence token out of process arguments" "--from-file=token=/dev/stdin"
if grep -Fq -- "--from-literal=token=" "${KUBECTL_LOG}"; then
    fail "Evidence ingest token must not be exposed in kubectl process arguments"
else
    ok
fi
assert_contains "creates the Core DSN Secret" "create secret generic bkn-trace-core-mariadb"
assert_contains "uses the Trace database DSN" "/bkn_trace?charset=utf8mb4"
assert_contains "passes the DSN through stdin" "--from-file=dsn=/dev/stdin"
if grep -Fq -- "--from-literal=dsn=" "${KUBECTL_LOG}"; then
    fail "Core DSN must not be exposed in kubectl process arguments"
else
    ok
fi

CALLS=()
: >"${KUBECTL_LOG}"
EXISTING_SECRETS="bkn-trace-evidence-ingest"
EXISTING_TOKEN_DATA="dGVzdC10b2tlbg=="
_openbkn_prepare_trace_ingest_secret openbkn
if grep -Fq "create secret generic bkn-trace-evidence-ingest" "${KUBECTL_LOG}"; then
    fail "existing Evidence ingest Secret must be reused"
else
    ok
fi

CALLS=()
: >"${KUBECTL_LOG}"
EXISTING_TOKEN_DATA=""
if _openbkn_prepare_trace_ingest_secret openbkn; then
    fail "existing Evidence ingest Secret without token must fail"
else
    ok
fi

CALLS=()
: >"${KUBECTL_LOG}"
EXISTING_SECRETS=""
cat > "${CONFIG_YAML_PATH}" <<'EOF'
depServices:
  rds:
    host: "mariadb.resource.svc.cluster.local"
EOF
if _openbkn_prepare_trace_profile openbkn; then
    fail "missing RDS source_type must fail before installation"
elif [[ "${LAST_ERROR}" == *"set it to internal or external"* ]]; then
    ok
else
    fail "missing RDS source_type must report the actual configuration error"
fi

CALLS=()
: >"${KUBECTL_LOG}"
cat > "${CONFIG_YAML_PATH}" <<'EOF'
depServices:
  rds:
    source_type: internal
    host: mariadb.resource.svc.cluster.local
    port: 3306
    user: invalid:user
    password: valid@password/with?symbols
EOF
if _openbkn_prepare_trace_profile openbkn; then
    fail "ambiguous DSN user must fail with a clear error"
else
    ok
fi

CALLS=()
: >"${KUBECTL_LOG}"
cat > "${CONFIG_YAML_PATH}" <<'EOF'
depServices:
  rds:
    source_type: internal
    host: mariadb.resource.svc.cluster.local
    port: 3306
    user: openbkn
    password: valid@password/with?symbols
EOF
_openbkn_prepare_trace_profile openbkn
assert_contains "preserves valid DSN password symbols" "stdin:openbkn:valid@password/with?symbols@tcp("

CALLS=()
: >"${KUBECTL_LOG}"
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
: >"${KUBECTL_LOG}"
EXISTING_SECRETS="bkn-trace-core-mariadb"
EXISTING_DSN_DATA=""
if _openbkn_prepare_trace_profile openbkn; then
    fail "existing Core Secret without dsn key must fail"
else
    ok
fi

CALLS=()
: >"${KUBECTL_LOG}"
EXISTING_DSN_DATA="dHJhY2U6c2VjcmV0QHRjcChkYi5leGFtcGxlOjMzMDYpL290aGVyP2NoYXJzZXQ9dXRmOG1iNA=="
if _openbkn_prepare_trace_profile openbkn; then
    fail "external Core DSN must target the fixed bkn_trace database"
else
    ok
fi

CALLS=()
: >"${KUBECTL_LOG}"
EXISTING_DSN_DATA="dHJhY2U6c2VjcmV0QHRjcChkYi5leGFtcGxlOjMzMDYpL2Jrbl90cmFjZT9jaGFyc2V0PXV0ZjhtYjQ="
_openbkn_prepare_trace_profile openbkn
if grep -q "create secret generic bkn-trace-core-mariadb" "${KUBECTL_LOG}"; then
    fail "external Core DSN Secret must be reused"
else
    ok
fi

CALLS=()
: >"${KUBECTL_LOG}"
EXISTING_DSN_DATA="$(printf '%s' 'trace:pa?ss@tcp(db.example:3306)/bkn_trace?charset=utf8mb4' | base64)"
if _openbkn_prepare_trace_profile openbkn; then
    ok
else
    fail "external Core DSN must allow question marks in the password"
fi

CALLS=()
: >"${KUBECTL_LOG}"
cat > "${CONFIG_YAML_PATH}" <<'EOF'
depServices:
  rds:
    source_type: internal
    host: mariadb.resource.svc.cluster.local
    port: 3306
    user: openbkn
    password: refreshed-password
EOF
_openbkn_prepare_trace_profile openbkn
assert_contains "internal Core DSN Secret is refreshed from current RDS configuration" "create secret generic bkn-trace-core-mariadb"
assert_contains "internal Core DSN Secret is updated idempotently" "apply -f -"

if grep -Eq -- '^[[:space:]]{2}-[[:space:]]+bkn_trace([[:space:]]|$)' "${SCRIPT_DIR}/../data-migrator/config.monorepo.yaml"; then
    ok
else
    fail "data-migrator must pre-create the bkn_trace database"
fi

if [[ "${FAILED}" -eq 0 ]]; then
    echo "openbkn_trace_prerequisites_test: all ${PASS} checks passed"
    exit 0
fi
echo "openbkn_trace_prerequisites_test: ${FAILED} failures"
exit 1
