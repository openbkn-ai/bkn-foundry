#!/usr/bin/env bash
# Regression tests for install-status image resolution.
set -euo pipefail

PASS=0
FAIL=0
ok() { PASS=$((PASS + 1)); }
fail() { echo "FAIL: $*"; FAIL=$((FAIL + 1)); }
assert_eq() {
    local name="$1" got="$2" want="$3"
    if [[ "${got}" == "${want}" ]]; then ok; else fail "${name}: got[${got}] want[${want}]"; fi
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

resolve_images() {
    local mode="$1"
    local kubectl_override="${2:-}"
    local nginx_override="${3:-}"
    env \
        "TEST_MODE=${mode}" \
        "TEST_KUBECTL_OVERRIDE=${kubectl_override}" \
        "TEST_NGINX_OVERRIDE=${nginx_override}" \
        bash -c '
            SCRIPT_DIR="$1"
            OFFLINE_MODE=false
            OFFLINE_REGISTRY=registry.test:5000
            if [[ -n "${TEST_KUBECTL_OVERRIDE}" ]]; then
                INSTALL_STATUS_KUBECTL_IMAGE="${TEST_KUBECTL_OVERRIDE}"
            fi
            if [[ -n "${TEST_NGINX_OVERRIDE}" ]]; then
                INSTALL_STATUS_NGINX_IMAGE="${TEST_NGINX_OVERRIDE}"
            fi
            source "${SCRIPT_DIR}/scripts/services/status.sh"
            # Simulate deploy.sh parsing --offline after status.sh is sourced.
            OFFLINE_MODE="${TEST_MODE}"
            _status_resolve_images
            printf "%s\n%s\n" "${INSTALL_STATUS_KUBECTL_IMAGE}" "${INSTALL_STATUS_NGINX_IMAGE}"
        ' bash "${SCRIPT_DIR}"
}

offline_images="$(resolve_images true)"
assert_eq "offline-kubectl" "$(sed -n '1p' <<<"${offline_images}")" \
    "registry.test:5000/openbkn-ai/portainer/kubectl-shell:latest"
assert_eq "offline-nginx" "$(sed -n '2p' <<<"${offline_images}")" \
    "registry.test:5000/openbkn-ai/library/nginx:1.27-alpine"

online_images="$(resolve_images false)"
assert_eq "online-kubectl" "$(sed -n '1p' <<<"${online_images}")" \
    "swr.cn-east-3.myhuaweicloud.com/openbkn-ai/portainer/kubectl-shell:latest"
assert_eq "online-nginx" "$(sed -n '2p' <<<"${online_images}")" \
    "swr.cn-east-3.myhuaweicloud.com/openbkn-ai/library/nginx:1.27-alpine"

override_images="$(resolve_images true custom/kubectl:tag custom/nginx:tag)"
assert_eq "kubectl-override" "$(sed -n '1p' <<<"${override_images}")" "custom/kubectl:tag"
assert_eq "nginx-override" "$(sed -n '2p' <<<"${override_images}")" "custom/nginx:tag"

if [[ "${FAIL}" -eq 0 ]]; then
    echo "status_test: all ${PASS} checks passed"
    exit 0
fi
echo "status_test: FAILED"
exit 1
