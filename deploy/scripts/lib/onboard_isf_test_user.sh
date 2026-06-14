#!/usr/bin/env bash
# Optional: first business test user (login: test) with all roles from openbkn admin "role list"
# (on typical full stacks this is three business admin roles, e.g. 数据/AI/应用 管理员) + openbkn re-login
# for ADP toolset impex (built-in  admin  often lacks  CommonAdd  on agent-operator;  test  with roles does).
# shellcheck source=/dev/null

# Last password for openbkn HTTP sign-in as test (set in this shell after reset-password or env).
__ONBOARD_TEST_USER_KWEAVER_PASSWORD=""
# Set to "true" once user [test] has been ensured (created/synced + roles + openbkn session as test) within this run.
# Lets later steps (Context Loader impex) skip re-assigning roles and re-logging in.
__ONBOARD_TEST_USER_PREPARED="false"

# Match onboard.sh: ISF (full) install present.
onboard_isf_full_install() {
    local has_isf="false"
    if command -v helm &>/dev/null; then
        if helm list -A 2>/dev/null \
            | awk 'NR>1 {print $1}' \
            | grep -qE '^(authentication|hydra|user-management|eacp|isfweb|isf-data-migrator|policy-management|audit-log|authorization|sharemgnt|oauth2-ui|ingress-informationsecurityfabric)$'; then
            has_isf="true"
        fi
    fi
    if [[ "${has_isf}" != "true" ]] && kubectl get ns 2>/dev/null | awk '{print $1}' | grep -qiE '^(isf|information-security-fabric)$'; then
        has_isf="true"
    fi
    [[ "${has_isf}" == "true" ]]
}

# True if a user with account (login) "test" already exists.
onboard_user_test_exists() {
    local _js
    if ! _js="$(openbkn admin --json user list --keyword test --limit 200 2>/dev/null)"; then
        return 1
    fi
    echo "${_js}" | python3 -c "
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(1)
for e in d.get('entries', []):
    if (e.get('account') or '') == 'test':
        sys.exit(0)
sys.exit(1)" || return 1
    return 0
}

# Assign every role from role list to account (idempotent: duplicate assign may no-op on server).
onboard_assign_all_listed_roles_to_user() {
    local _login="${1:-test}"
    local rjson rids _rid _n _fail _ok
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
for e in d.get('entries', []):
    i = e.get('id') or e.get('Id')
    if i:
        print(i)
" 2>/dev/null)" || true
    if [[ -z "${rids// }" ]]; then
        log_warn "No roles in role list; no roles to assign to ${_login}"
        return 0
    fi
    _fail=0
    _n=0
    _ok=0
    while IFS= read -r _rid; do
        [[ -n "${_rid}" ]] || continue
        _n=$((_n + 1))
        if openbkn admin user assign-role "${_login}" "${_rid}" 2>/dev/null; then
            _ok=$((_ok + 1))
        else
            log_warn "assign-role ${_login} <- ${_rid} failed (may already be bound)"
            _fail=$((_fail + 1))
        fi
    done <<< "${rids}"
    log_info "Role assign for ${_login}: ok ${_ok}, failed/duplicate ${_fail} (of ${_n} role ids; usually all business admin roles in role list). Check: openbkn admin user roles ${_login}"
    return 0
}

# Set kweaver-usable password for user  test  (ISF:  user create  leaves platform 123456 until  reset-password ).
# Default password for onboard: ONBOARD_DEFAULT_TEST_USER_PASSWORD (default 111111). Fills __ONBOARD_TEST_USER_KWEAVER_PASSWORD.
onboard_set_test_user_password() {
    local _defp
    _defp="${ONBOARD_DEFAULT_TEST_USER_PASSWORD:-111111}"
    if [[ -n "${ONBOARD_TEST_USER_PASSWORD:-}" ]]; then
        if ! openbkn admin user reset-password -u test -p "${ONBOARD_TEST_USER_PASSWORD}" -y 2>/dev/null; then
            log_warn "openbkn admin user reset-password (ONBOARD_TEST_USER_PASSWORD) failed; try: openbkn admin user reset-password -u test -p '...' -y"
            return 1
        fi
        __ONBOARD_TEST_USER_KWEAVER_PASSWORD="${ONBOARD_TEST_USER_PASSWORD}"
        return 0
    fi
    if [[ "${ONBOARD_ASSUME_YES:-false}" == "true" ]]; then
        if ! openbkn admin user reset-password -u test -p "${_defp}" -y 2>/dev/null; then
            log_warn "openbkn admin user reset-password (default ${_defp}) failed; set ONBOARD_TEST_USER_PASSWORD and re-run, or: openbkn admin user reset-password -u test -p '...' -y"
            return 1
        fi
        __ONBOARD_TEST_USER_KWEAVER_PASSWORD="${_defp}"
        log_info "User test: password set to default ${_defp} (-y, non-interactive). Override: ONBOARD_TEST_USER_PASSWORD=..."
        return 0
    fi
    if ! (type onboard_is_bootstrap_tty &>/dev/null && onboard_is_bootstrap_tty); then
        log_warn "Not a TTY: set ONBOARD_TEST_USER_PASSWORD=... or use  $0 -y  (default password ${_defp} for  test )"
        return 1
    fi
    log_info "Set password for user [test] (press Enter to use the default ${_defp})…"
    read -r -s -p "  Password for user test (Enter = ${_defp}): " __ONBOARD_TEST_USER_KWEAVER_PASSWORD
    echo
    __ONBOARD_TEST_USER_KWEAVER_PASSWORD="${__ONBOARD_TEST_USER_KWEAVER_PASSWORD:-${_defp}}"
    if ! openbkn admin user reset-password -u test -p "${__ONBOARD_TEST_USER_KWEAVER_PASSWORD}" -y 2>/dev/null; then
        log_warn "openbkn admin user reset-password failed; set ONBOARD_TEST_USER_PASSWORD=... or: openbkn admin user reset-password -u test -p '...' -y"
        return 1
    fi
    return 0
}

# Create login=test, set password, assign all roles in role list.
onboard_create_test_user_with_all_roles() {
    log_info "Creating user [test] and assigning all roles from openbkn admin 'role list' (typically three business admin roles)…"
    local uerr
    uerr="$(mktemp 2>/dev/null || echo /tmp/onboard-uc.$$)"
    if ! openbkn admin --json user create --login test >/dev/null 2> "${uerr}"; then
        if grep -qiE 'already|exists|exist|重复|已存在' "${uerr}" 2>/dev/null; then
            log_info "User test may already exist; continuing…"
        else
            log_warn "openbkn admin user create failed: $(tr '\n' ' ' < "${uerr}" | head -c 400)"
            rm -f "${uerr}"
            return 1
        fi
    fi
    rm -f "${uerr}" 2>/dev/null || true
    if ! onboard_set_test_user_password; then
        log_warn "Password not set; openbkn re-login for impex may fail until you: openbkn admin user reset-password -u test -p '...' -y"
        __ONBOARD_TEST_USER_KWEAVER_PASSWORD="${__ONBOARD_TEST_USER_KWEAVER_PASSWORD:-${ONBOARD_TEST_USER_PASSWORD:-${ONBOARD_DEFAULT_TEST_USER_PASSWORD:-111111}}}"
    fi
    if ! onboard_assign_all_listed_roles_to_user test; then
        return 1
    fi
    return 0
}

# openbkn auth as test (HTTP) for impex. Requires access URL; password from __/env or prompt.
# shellcheck disable=SC2120
onboard_kweaver_relogin_isf_test() {
    local kurl="${1:-}"
    if [[ -z "${kurl}" ]] && type onboard_default_access_base_url &>/dev/null; then
        kurl="$(onboard_default_access_base_url)"
    fi
    if [[ -z "${kurl}" ]]; then
        log_warn "openbkn re-login as test: no access URL; set access URL or run from onboard.sh"
        return 1
    fi
    if [[ -n "${ONBOARD_KWEAVER_IMPEX_NO_RELLOGIN:-}" ]]; then
        log_info "ONBOARD_KWEAVER_IMPEX_NO_RELLOGIN set; skipping openbkn auth as test (using existing ~/.kweaver session)"
        return 0
    fi
    local _pw _defp
    _defp="${ONBOARD_DEFAULT_TEST_USER_PASSWORD:-111111}"
    _pw="${__ONBOARD_TEST_USER_KWEAVER_PASSWORD:-${ONBOARD_TEST_USER_PASSWORD:-}}"
    if [[ -z "${_pw}" && -t 0 && -t 1 ]] && (type onboard_is_bootstrap_tty &>/dev/null && onboard_is_bootstrap_tty); then
        read -r -s -p "openbkn: password for user 'test' (ADP impex; empty = ${_defp}): " _pw
        echo
    fi
    _pw="${_pw:-${_defp}}"
    log_info "Signing openbkn in as user [test] (token saved under ~/.kweaver)…"
    if ! openbkn auth login "${kurl}" -u test -p "${_pw}" -k; then
        log_warn "openbkn: sign-in as test failed. Re-run: openbkn auth login ${kurl} -u test -p '<password>' -k"
        return 1
    fi
    return 0
}

# After user test is created or synced: sign openbkn SDK in as test for the next steps.
# Sets __ONBOARD_TEST_USER_PREPARED=true so Context Loader can skip the duplicate work.
onboard_isf_relogin_kweaver_cli_as_test_for_downstream() {
    type onboard_isf_full_install &>/dev/null || return 0
    onboard_isf_full_install 2>/dev/null || return 0
    command -v openbkn &>/dev/null || return 0
    if ! onboard_user_test_exists 2>/dev/null; then
        return 0
    fi
    local kurl=""
    type onboard_default_access_base_url &>/dev/null && kurl="$(onboard_default_access_base_url)"
    log_info "Switching openbkn CLI to user [test] for the next steps (Context Loader and model registration)…"
    if ! onboard_kweaver_relogin_isf_test "${kurl}"; then
        onboard_log_err "openbkn: could not sign in as test. Set ONBOARD_TEST_USER_PASSWORD or run: openbkn auth login ${kurl} -u test -p '<password>' -k  then re-run: $0"
        exit 1
    fi
    __ONBOARD_TEST_USER_PREPARED="true"
    return 0
}

# After openbkn is usable; only when full ISF + openbkn admin and user chose to run.
onboard_offer_isf_test_user() {
    if [[ "${ONBOARD_SKIP_ISF_TEST_USER:-false}" == "true" ]]; then
        ONBOARD_REPORT_ISF_TEST_USER="skipped: ONBOARD_SKIP_ISF_TEST_USER / --skip-isf-test-user"
        return 0
    fi
    onboard_isf_full_install || {
        ONBOARD_REPORT_ISF_TEST_USER="n/a (not an ISF / full install)"
        return 0
    }
    command -v openbkn &>/dev/null || {
        ONBOARD_REPORT_ISF_TEST_USER="error: openbkn admin not on PATH"
        onboard_log_err "ISF: openbkn admin not found. Install: npm i -g @openbkn/bkn-sdk@alpha, then: export PATH=\"\$(npm config get prefix 2>/dev/null)/bin:\$PATH\"  and re-run: $0"
        exit 1
    }

    if ! openbkn admin --json user list --limit 1 &>/dev/null; then
        ONBOARD_REPORT_ISF_TEST_USER="error: openbkn admin not authenticated"
        onboard_log_err "ISF: openbkn admin cannot list users (not signed in). This should not happen after the auth step. Run: openbkn admin auth login <https://access-url> -u admin -p '...' -k, then: $0"
        exit 1
    fi

    if onboard_user_test_exists; then
        log_info "User [test] already exists. Syncing roles from openbkn admin 'role list'…"
        onboard_assign_all_listed_roles_to_user test || true
        ONBOARD_REPORT_ISF_TEST_USER="ready (existed; all roles re-synced; check: openbkn admin user roles test)"
        log_info "If you changed test's password, set ONBOARD_TEST_USER_PASSWORD before re-running (or enter it below)."
        onboard_isf_relogin_kweaver_cli_as_test_for_downstream
        return 0
    fi

    if [[ "${ONBOARD_ASSUME_YES}" == "true" ]]; then
        log_info "ONBOARD: creating user test, password/roles (-y)…"
        if ! onboard_create_test_user_with_all_roles; then
            ONBOARD_REPORT_ISF_TEST_USER="error: create/configure user test failed"
            onboard_log_err "ISF: could not create or configure user test. Fix the errors above, then re-run: $0"
            exit 1
        fi
        ONBOARD_REPORT_ISF_TEST_USER="created (-y; password = ${ONBOARD_DEFAULT_TEST_USER_PASSWORD:-111111}, override ONBOARD_TEST_USER_PASSWORD; all roles assigned)"
        onboard_isf_relogin_kweaver_cli_as_test_for_downstream
        return 0
    fi
    if ! (type onboard_is_bootstrap_tty &>/dev/null && onboard_is_bootstrap_tty); then
        ONBOARD_REPORT_ISF_TEST_USER="skipped: not a TTY (use -y or create user manually)"
        log_info "Not a TTY: skipping interactive offer to create user test. Re-run in a terminal, or use -y, or: openbkn admin user create --login test && …"
        return 0
    fi
    echo ""
    read -r -p "Create user [test] and grant all roles for ADP import? [Y/n] (Enter = Y): " _otu
    if [[ "${_otu}" =~ ^[Nn] ]]; then
        ONBOARD_REPORT_ISF_TEST_USER="skipped: user declined to create test"
        log_info "Skipped. You can: openbkn admin user create --login test && openbkn admin user reset-password -u test --prompt-password -y && (assign all role ids from role list)"
        return 0
    fi
    if ! onboard_create_test_user_with_all_roles; then
        ONBOARD_REPORT_ISF_TEST_USER="error: create/configure user test failed"
        onboard_log_err "ISF: could not create or configure user test. Fix the errors above, then re-run: $0"
        exit 1
    fi
    ONBOARD_REPORT_ISF_TEST_USER="created (password set; all roles assigned; openbkn signed in as test)"
    onboard_isf_relogin_kweaver_cli_as_test_for_downstream
}

# Before Context Loader impex (ISF): make sure user [test] is ready and openbkn is signed in as test.
# Skips role-sync + relogin if the test-user step already did them in this run.
# Returns 0 to proceed (no ISF: always 0).
onboard_ensure_isf_test_for_kweaver_impex() {
    type onboard_isf_full_install &>/dev/null || return 0
    onboard_isf_full_install 2>/dev/null || return 0
    if ! command -v openbkn &>/dev/null; then
        return 0
    fi
    if [[ "${__ONBOARD_TEST_USER_PREPARED:-false}" == "true" ]]; then
        return 0
    fi
    if ! command -v openbkn &>/dev/null; then
        log_warn "Context Loader (ISF): openbkn admin not on PATH; cannot prepare user [test]. Install: npm i -g @openbkn/bkn-sdk@alpha, then re-run."
        return 1
    fi
    if ! openbkn admin --json user list --limit 1 &>/dev/null; then
        log_warn "Context Loader (ISF): openbkn admin is not signed in. Run: openbkn admin auth login <url> -u admin -p '<password>' -k, then re-run."
        return 1
    fi
    if ! onboard_user_test_exists; then
        log_warn "Context Loader (ISF): user [test] is missing. Create it first (test-user step or: openbkn admin user create --login test + roles), then re-run."
        return 1
    fi
    log_info "Context Loader (ISF): syncing roles to [test] and signing openbkn in as [test]…"
    onboard_assign_all_listed_roles_to_user test || true
    local _url=""
    if type onboard_default_access_base_url &>/dev/null; then
        _url="$(onboard_default_access_base_url)"
    fi
    if ! onboard_kweaver_relogin_isf_test "${_url}"; then
        return 1
    fi
    __ONBOARD_TEST_USER_PREPARED="true"
    return 0
}

# ── bkn-safe test user ──────────────────────────────────────────────────────
# bkn-safe seeds only the admin; this provisions the business "test" account
# (login: test) with the three business-admin roles (数据/AI/应用管理员;
# source=business — NOT super-admin). bkn-safe's admin create+reset forces a
# password change, so we complete it once via the self-service change-password
# endpoint to leave test directly loginable. Idempotent. Response shapes are
# bkn-safe's ({"users":[...]}, {"roles":[...]}), distinct from the ISF helpers.

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
# authenticated openbkn admin session. Honors ONBOARD_SKIP_ISF_TEST_USER and
# ONBOARD_TEST_USER_PASSWORD / ONBOARD_DEFAULT_TEST_USER_PASSWORD (default 111111).
onboard_provision_bkn_safe_test_user() {
    if [[ "${ONBOARD_SKIP_ISF_TEST_USER:-false}" == "true" ]]; then
        ONBOARD_REPORT_ISF_TEST_USER="skipped: ONBOARD_SKIP_ISF_TEST_USER / --skip-isf-test-user"
        return 0
    fi
    type onboard_bkn_safe_detected &>/dev/null && onboard_bkn_safe_detected 2>/dev/null || {
        ONBOARD_REPORT_ISF_TEST_USER="n/a (bkn-safe not detected)"
        return 0
    }
    command -v openbkn &>/dev/null || return 0
    if ! openbkn admin --json user list --limit 1 &>/dev/null; then
        onboard_log_warn "openbkn admin not signed in; skipping test-user provisioning. Run: openbkn auth login <url> -u admin -p '<pw>' -k, then re-run: $0"
        ONBOARD_REPORT_ISF_TEST_USER="skipped: openbkn admin not signed in"
        return 0
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
        ONBOARD_REPORT_ISF_TEST_USER="ready (existed; business roles re-synced; password unchanged)"
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
        ONBOARD_REPORT_ISF_TEST_USER="error: create user test failed"
        return 0
    fi

    # admin reset -> temp (forces must-change-password), then self-service
    # change-password temp -> final clears the flag so test is directly loginable.
    if ! openbkn admin user reset-password "${_id}" --password "${_tmp}" >/dev/null 2>&1; then
        log_warn "openbkn admin reset-password for test failed"
        ONBOARD_REPORT_ISF_TEST_USER="error: reset-password test failed"
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
    ONBOARD_REPORT_ISF_TEST_USER="created (bkn-safe; account test, password ${_final}, business roles assigned)"
    log_info "User [test] ready — login: openbkn auth login ${_kurl} -u test -p '${_final}' -k"
    return 0
}
