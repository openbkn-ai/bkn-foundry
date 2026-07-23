#!/usr/bin/env bash
# _core_should_show_bkn_safe_initial_password 的行为测试（无需集群）。
set -uo pipefail

ONE_FAILED=0
PASS=0
fail() { echo "FAIL: $*"; ONE_FAILED=1; }
ok() { PASS=$((PASS + 1)); }
check() {
    if [[ "$2" == "$3" ]]; then ok; else fail "$1: got[$2] want[$3]"; fi
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

# shellcheck source=../services/core.sh
source "${SCRIPT_DIR}/scripts/services/core.sh"

run_case() {
    local name="$1"
    local existed_before="$2"
    local initial_pwd="$3"
    local want_rc="$4"

    if _core_should_show_bkn_safe_initial_password "${existed_before}" "${initial_pwd}"; then
        check "${name}" "0" "${want_rc}"
    else
        check "${name}" "1" "${want_rc}"
    fi
}

# fresh install + password recorded => show it once
run_case "fresh-install-shows" "false" "abc123" "0"

# upgrade + password recorded => never show
run_case "upgrade-hides" "true" "abc123" "1"

# fresh install + empty password => nothing to show
run_case "fresh-install-empty-hides" "false" "" "1"

# upgrade + empty password => nothing to show
run_case "upgrade-empty-hides" "true" "" "1"

if [[ "${ONE_FAILED}" -eq 0 ]]; then
    echo "core_initial_password_test: all ${PASS} checks passed"
    exit 0
fi
echo "core_initial_password_test: FAILED"
exit 1
