#!/usr/bin/env bash
# Lightweight tests for onboard openbkn install/upgrade guards (no network or cluster required).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ONBOARD_SCRIPT="${SCRIPT_DIR}/onboard.sh"
ONE_FAILED=0

fail() {
    echo "FAIL: $*"
    ONE_FAILED=1
}

# Load the production function bodies without executing onboard.sh's main flow.
eval "$(awk '/^onboard_ensure_bkn_cli\(\)/,/^}/' "${ONBOARD_SCRIPT}")"
eval "$(awk '/^onboard_upgrade_bkn_cli\(\)/,/^}/' "${ONBOARD_SCRIPT}")"

log_info() { :; }
log_warn() { echo "warn:$*"; }
log_error() { echo "error:$*" >&2; }
onboard_is_bootstrap_tty() { return 1; }

# Existing CLI: attempt the upgrade; failure is non-fatal and warns.
out="$(
    openbkn() { echo "0.1.2"; }
    npm() { echo "npm-called"; return 1; }
    ONBOARD_SKIP_OPENBKN_INSTALL=false OFFLINE_MODE=false ENABLE_BKN_ONLY=false \
        onboard_upgrade_bkn_cli
)"
[[ "${out}" == *"npm-called"* ]] || fail "installed CLI should attempt npm upgrade"
[[ "${out}" == *"warn:npm i -g @openbkn/bkn-sdk@latest failed"* ]] || fail "upgrade failure should warn"

# Explicit skip, offline mode, and BKN-only mode must never invoke npm.
# Patterns are written `(x)` rather than `x)`: bash 3.2, still the macOS system
# shell and a supported way to run these scripts, otherwise reads the pattern's
# `)` as the end of the enclosing `$( )` and the whole loop never runs.
for mode in skip offline bkn-only; do
    out="$(
        openbkn() { :; }
        npm() { echo "unexpected-npm"; return 99; }
        case "${mode}" in
            (skip)
                ONBOARD_SKIP_OPENBKN_INSTALL=true OFFLINE_MODE=false ENABLE_BKN_ONLY=false \
                    onboard_upgrade_bkn_cli
                ;;
            (offline)
                ONBOARD_SKIP_OPENBKN_INSTALL=false OFFLINE_MODE=true ENABLE_BKN_ONLY=false \
                    onboard_upgrade_bkn_cli
                ;;
            (bkn-only)
                ONBOARD_SKIP_OPENBKN_INSTALL=false OFFLINE_MODE=false ENABLE_BKN_ONLY=true \
                    onboard_upgrade_bkn_cli
                ;;
        esac
    )"
    [[ -z "${out}" ]] || fail "${mode} should not invoke npm (got: ${out})"
done

# Missing CLI without TTY/-y remains a hard failure and must not invoke npm.
set +e
out="$(
    {
        command() {
            if [[ "${1:-}" == "-v" && "${2:-}" == "openbkn" ]]; then return 1; fi
            if [[ "${1:-}" == "-v" && "${2:-}" == "npm" ]]; then return 0; fi
            builtin command "$@"
        }
        npm() { echo "unexpected-npm"; return 99; }
        ONBOARD_SKIP_OPENBKN_INSTALL=false ONBOARD_ASSUME_YES=false OFFLINE_MODE=false \
            onboard_ensure_bkn_cli
    } 2>&1
)"
rc=$?
set -e
[[ "${rc}" -ne 0 ]] || fail "missing CLI without TTY/-y should fail"
[[ "${out}" != *"unexpected-npm"* ]] || fail "missing CLI consent failure should not invoke npm"

# Offline mode with a missing CLI must fail immediately without npm registry access.
set +e
out="$(
    {
        command() {
            if [[ "${1:-}" == "-v" && "${2:-}" == "openbkn" ]]; then return 1; fi
            builtin command "$@"
        }
        npm() { echo "unexpected-npm"; return 99; }
        ONBOARD_SKIP_OPENBKN_INSTALL=false ONBOARD_ASSUME_YES=true OFFLINE_MODE=true \
            onboard_ensure_bkn_cli
    } 2>&1
)"
rc=$?
set -e
[[ "${rc}" -ne 0 ]] || fail "offline mode with a missing CLI should fail"
[[ "${out}" != *"unexpected-npm"* ]] || fail "offline missing-CLI failure should not invoke npm"

if [[ "${ONE_FAILED}" -ne 0 ]]; then
    exit 1
fi
echo "OK onboard_cli_test.sh"
