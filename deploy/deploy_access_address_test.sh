#!/usr/bin/env bash
# Regression test: the port selected for accessAddress must reach ingress-nginx.
set -euo pipefail

PASS=0
FAIL=0

ok() { PASS=$((PASS + 1)); }
fail() { echo "FAIL: $*"; FAIL=$((FAIL + 1)); }
assert_eq() {
    local name="$1"
    local got="$2"
    local want="$3"
    if [[ "${got}" == "${want}" ]]; then
        ok
    else
        fail "${name}: got[${got}] want[${want}]"
    fi
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG_YAML_PATH="$(mktemp)"
trap 'rm -f "${CONFIG_YAML_PATH}"' EXIT

# deploy.sh only invokes main when executed directly, so its helpers can be
# sourced without contacting a cluster.
# shellcheck source=../deploy.sh
source "${SCRIPT_DIR}/deploy.sh"

INGRESS_NGINX_HTTP_PORT=80
INGRESS_NGINX_HTTPS_PORT=443

# Interactive protocol changes without an explicit port must switch to the
# new protocol's default, otherwise hostNetwork would try to bind both
# protocols to the same port.
assert_eq "protocol-change-uses-new-default-port" \
    "$(_port_after_interactive_protocol_selection "443" "https" "" "http")" \
    "80"
assert_eq "explicit-port-wins-over-protocol-default" \
    "$(_port_after_interactive_protocol_selection "443" "https" "8080" "http")" \
    "8080"
assert_eq "unchanged-protocol-keeps-current-port" \
    "$(_port_after_interactive_protocol_selection "8443" "https" "" "")" \
    "8443"

_sync_and_upsert_access_address "foundry.example" "8443" "/" "https"
assert_eq "https-custom-port-reaches-ingress" "${INGRESS_NGINX_HTTPS_PORT}" "8443"
assert_eq "https-does-not-change-http-port" "${INGRESS_NGINX_HTTP_PORT}" "80"
assert_eq "https-port-is-persisted" "$(_read_access_address_field port)" "8443"

_sync_and_upsert_access_address "foundry.example" "8080" "/api" "http"
assert_eq "http-custom-port-reaches-ingress" "${INGRESS_NGINX_HTTP_PORT}" "8080"
assert_eq "http-does-not-change-https-port" "${INGRESS_NGINX_HTTPS_PORT}" "8443"
assert_eq "http-scheme-is-persisted" "$(_read_access_address_field scheme)" "http"

if [[ "${FAIL}" -eq 0 ]]; then
    echo "deploy_access_address_test: all ${PASS} checks passed"
    exit 0
fi

echo "deploy_access_address_test: FAILED"
exit 1
