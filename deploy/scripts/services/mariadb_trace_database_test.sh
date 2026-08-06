#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=../services/mariadb.sh
source "${SCRIPT_DIR}/scripts/services/mariadb.sh"

manifest="$(mktemp)"
sql_log="$(mktemp)"
trap 'rm -f "${manifest}" "${sql_log}"' EXIT
printf '%s\n' 'pre-stage: core-data-migrator' >"${manifest}"

MARIADB_NAMESPACE=openbkn
MARIADB_DATABASE=openbkn
MARIADB_ROOT_PASSWORD=test-root-password
CORE_VERSION_MANIFEST_FILE="${manifest}"
ISF_VERSION_MANIFEST_FILE=/nonexistent/isf-manifest.yaml

grant_mariadb_user_permissions() { :; }
should_skip_db_init_for_manifest() { return 0; }
log_info() { :; }
log_warn() { :; }
sleep() { :; }
kubectl() {
    if [[ "$*" == *"get pods"* ]]; then
        printf '%s' 'mariadb-0'
        return 0
    fi
    if [[ "$*" == *"mariadb-admin ping"* ]]; then
        return 0
    fi
    if [[ "$*" == *"exec -i"* ]]; then
        cat >"${sql_log}"
        return 0
    fi
    return 0
}

setup_mariadb_databases

for database in openbkn safe bkn_trace; do
    if ! grep -Fq "CREATE DATABASE IF NOT EXISTS \`${database}\` CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci" "${sql_log}"; then
        echo "database fallback must create ${database} with the canonical utf8mb4 charset" >&2
        exit 1
    fi
done

echo "mariadb_trace_database_test: passed"
