#!/usr/bin/env bash
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

# Optional: first business test user (login: test) with all roles from openbkn admin "role list"
# (on typical full stacks this is three business admin roles, e.g. 数据/AI/应用 管理员) + openbkn re-login
# for ADP toolset impex (built-in  admin  often lacks  CommonAdd  on agent-operator;  test  with roles does).
# shellcheck source=/dev/null

# ── bkn-safe test user ──────────────────────────────────────────────────────
# bkn-safe seeds only the admin; this provisions the business "test" account
# (login: test) with the three business-admin roles (数据/AI/应用管理员;
# source=business — NOT super-admin). bkn-safe's admin create+reset forces a
# password change, so we complete it once via the self-service change-password
# endpoint to leave test directly loginable. Idempotent. Response shapes are
# bkn-safe's ({"users":[...]}, {"roles":[...]}).

# Print test's user id (empty if absent).
onboard_bkn_safe_test_user_id() {
    openbkn admin --json user list --keyword test --limit 200 2>/dev/null | python3 -c "
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(1)
for e in d.get('users', []):
    if (e.get('account') or '') == 'test':
        print(e.get('id') or '')
        sys.exit(0)
sys.exit(1)"
}

# Assign every source=business role to <login> (idempotent; duplicates no-op).
onboard_bkn_safe_assign_business_roles() {
    local _login="${1:-test}" rjson rids _rid _ok=0 _n=0
    if ! rjson="$(openbkn admin --json role list --limit 1000 2>/dev/null)"; then
        log_warn "openbkn admin role list failed; cannot assign roles to ${_login}"
        return 1
    fi
    rids="$(echo "${rjson}" | python3 -c "
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(1)
for e in d.get('roles', []):
    if (e.get('source') or '') == 'business' and e.get('id'):
        print(e['id'])
" 2>/dev/null)" || true
    if [[ -z "${rids// }" ]]; then
        log_warn "No business roles found; none assigned to ${_login}"
        return 0
    fi
    while IFS= read -r _rid; do
        [[ -n "${_rid}" ]] || continue
        _n=$((_n + 1))
        openbkn admin user assign-role "${_login}" "${_rid}" >/dev/null 2>&1 && _ok=$((_ok + 1))
    done <<< "${rids}"
    log_info "User [test]: ${_ok}/${_n} business roles assigned (check: openbkn admin user roles test)"
    return 0
}

# Clear a pending must-change-password flag on [test] without changing the
# effective password: bounce final -> final.heal -> final through the
# self-service change-password endpoint (a successful self-service change
# clears the flag; admin reset would re-arm it). First hop 401 = the password
# is not the expected one (operator changed it) — leave the account alone.
# Heals users created by older onboard runs that left the flag set.
onboard_bkn_safe_heal_test_password_flag() {
    local _final="$1" _kurl="$2" _tmp _c1 _c2
    [[ -z "${_kurl}" ]] && return 0
    _tmp="${_final}.heal"
    _c1="$(curl -sk -o /dev/null -w '%{http_code}' -X POST "${_kurl%/}/api/safe/v1/auth/change-password" \
        -H 'Content-Type: application/json' \
        -d "{\"account\":\"test\",\"old_password\":\"${_final}\",\"new_password\":\"${_tmp}\"}" 2>/dev/null)"
    if [[ "${_c1}" != 20* ]]; then
        [[ "${_c1}" == "401" ]] || log_warn "test password-flag heal probe got http ${_c1}; if first login still demands a password change, reset manually: openbkn admin user reset-password <id> --password '<tmp>' then change-password to the final one."
        return 0
    fi
    _c2="$(curl -sk -o /dev/null -w '%{http_code}' -X POST "${_kurl%/}/api/safe/v1/auth/change-password" \
        -H 'Content-Type: application/json' \
        -d "{\"account\":\"test\",\"old_password\":\"${_tmp}\",\"new_password\":\"${_final}\"}" 2>/dev/null)"
    if [[ "${_c2}" != 20* ]]; then
        log_warn "test password left at ${_tmp} (restore hop got http ${_c2}); restore: curl -k -X POST ${_kurl%/}/api/safe/v1/auth/change-password -H 'Content-Type: application/json' -d '{\"account\":\"test\",\"old_password\":\"${_tmp}\",\"new_password\":\"${_final}\"}'"
        return 1
    fi
    log_info "User [test]: cleared pending must-change-password (password unchanged)"
    return 0
}

# Create + activate the business test user for bkn-safe. Gated on bkn-safe + an
# authenticated openbkn admin session. Honors ONBOARD_SKIP_TEST_USER and
# ONBOARD_TEST_USER_PASSWORD / ONBOARD_DEFAULT_TEST_USER_PASSWORD (default 111111).
onboard_provision_bkn_safe_test_user() {
    if [[ "${ONBOARD_SKIP_TEST_USER:-false}" == "true" ]]; then
        ONBOARD_REPORT_TEST_USER="skipped: ONBOARD_SKIP_TEST_USER / --skip-test-user"
        return 0
    fi
    type onboard_bkn_safe_detected &>/dev/null && onboard_bkn_safe_detected 2>/dev/null || {
        ONBOARD_REPORT_TEST_USER="n/a (bkn-safe not detected)"
        return 0
    }
    command -v openbkn &>/dev/null || return 0
    if ! openbkn admin --json user list --limit 1 &>/dev/null; then
        onboard_log_warn "openbkn admin not signed in; skipping test-user provisioning. Run: openbkn auth login <url> -u admin -p '<pw>' -k, then re-run: $0"
        ONBOARD_REPORT_TEST_USER="skipped: openbkn admin not signed in"
        return 0
    fi

    # Let the operator choose the test password (Enter = default). Skipped for
    # -y, non-TTY, or when ONBOARD_TEST_USER_PASSWORD is preset.
    if [[ -z "${ONBOARD_TEST_USER_PASSWORD:-}" && -t 0 && "${ONBOARD_ASSUME_YES:-false}" != "true" ]]; then
        local _p1 _p2
        read -r -s -p "  Password for business user [test] [Enter = ${ONBOARD_DEFAULT_TEST_USER_PASSWORD:-111111}]: " _p1
        echo ""
        if [[ -n "${_p1}" ]]; then
            read -r -s -p "  Confirm password: " _p2
            echo ""
            if [[ "${_p1}" == "${_p2}" ]]; then
                ONBOARD_TEST_USER_PASSWORD="${_p1}"
            else
                onboard_log_warn "Passwords do not match — using the default instead."
            fi
        fi
    fi

    local _final _tmp _kurl _id
    _final="${ONBOARD_TEST_USER_PASSWORD:-${ONBOARD_DEFAULT_TEST_USER_PASSWORD:-111111}}"
    _tmp="${_final}.init"   # admin-set temp; must differ from final
    _kurl="$(onboard_default_access_base_url 2>/dev/null)"

    _id="$(onboard_bkn_safe_test_user_id)"
    if [[ -n "${_id}" ]]; then
        log_info "User [test] already exists (id ${_id}); re-syncing business roles…"
        onboard_bkn_safe_assign_business_roles test || true
        # Older onboard runs could leave must-change-password set (the heal is a
        # no-op when the password was changed away from the default).
        onboard_bkn_safe_heal_test_password_flag "${_final}" "${_kurl}" || true
        ONBOARD_REPORT_TEST_USER="ready (existed; business roles re-synced; password unchanged)"
        return 0
    fi

    log_info "Creating business user [test] (bkn-safe)…"
    _id="$(openbkn admin --json user create --login test --display-name test 2>/dev/null \
        | python3 -c "import json,sys
try: print(json.load(sys.stdin).get('id',''))
except Exception: pass" 2>/dev/null)"
    [[ -z "${_id}" ]] && _id="$(onboard_bkn_safe_test_user_id)"
    if [[ -z "${_id}" ]]; then
        log_warn "Could not create or find user test"
        ONBOARD_REPORT_TEST_USER="error: create user test failed"
        return 0
    fi

    # admin reset -> temp (forces must-change-password), then self-service
    # change-password temp -> final clears the flag so test is directly loginable.
    if ! openbkn admin user reset-password "${_id}" --password "${_tmp}" >/dev/null 2>&1; then
        log_warn "openbkn admin reset-password for test failed"
        ONBOARD_REPORT_TEST_USER="error: reset-password test failed"
        return 0
    fi
    local _code
    _code="$(curl -sk -o /dev/null -w '%{http_code}' -X POST "${_kurl%/}/api/safe/v1/auth/change-password" \
        -H 'Content-Type: application/json' \
        -d "{\"account\":\"test\",\"old_password\":\"${_tmp}\",\"new_password\":\"${_final}\"}" 2>/dev/null)"
    if [[ "${_code}" != 20* ]]; then
        log_warn "Completing test's forced password change failed (http ${_code}); test may need a first-login change."
    fi
    onboard_bkn_safe_assign_business_roles test || true
    ONBOARD_REPORT_TEST_USER="created (bkn-safe; account test, password ${_final}, business roles assigned)"
    log_info "User [test] ready — login: openbkn auth login ${_kurl} -u test -p '${_final}' -k"
    return 0
}
