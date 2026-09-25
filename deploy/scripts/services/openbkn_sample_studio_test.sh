#!/usr/bin/env bash
# bkn-sample-studio receives the recorded gateway URL as baseURL.
set -uo pipefail

ONE_FAILED=0
PASS=0
fail() { echo "FAIL: $*"; ONE_FAILED=1; }
ok() { PASS=$((PASS + 1)); }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=../lib/common.sh
source "${SCRIPT_DIR}/scripts/lib/common.sh"
# shellcheck source=../services/openbkn.sh
source "${SCRIPT_DIR}/scripts/services/openbkn.sh"

cfg="$(mktemp)"
cat > "${cfg}" <<'EOF'
accessAddress:
  host: gateway.example
  port: 443
  scheme: https
  path: /
EOF
CONFIG_YAML_PATH="${cfg}"
_openbkn_release_extra_sets bkn-sample-studio openbkn
if [[ "${CORE_RELEASE_EXTRA_SET_STRINGS[*]}" == "baseURL=https://gateway.example" ]]; then
    ok
else
    fail "gateway url: got[${CORE_RELEASE_EXTRA_SET_STRINGS[*]}]"
fi

printf 'accessAddress:\n  scheme: https\n' > "${cfg}"
_openbkn_release_extra_sets bkn-sample-studio openbkn
if [[ "${#CORE_RELEASE_EXTRA_SET_STRINGS[@]}" -eq 0 ]]; then
    ok
else
    fail "missing host should not set baseURL: got[${CORE_RELEASE_EXTRA_SET_STRINGS[*]}]"
fi

rm -f "${cfg}"
echo "${PASS} passed"
exit "${ONE_FAILED}"
