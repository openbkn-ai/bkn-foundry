#!/usr/bin/env bash
set -uo pipefail

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

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=../lib/common.sh
source "${SCRIPT_DIR}/scripts/lib/common.sh"
# shellcheck source=../services/redis.sh
source "${SCRIPT_DIR}/scripts/services/redis.sh"

tmp_cfg="$(mktemp)"
trap 'rm -f "${tmp_cfg}"' EXIT

log_info() { :; }
log_warn() { :; }
log_error() { :; }

# 1. CLI alias should resolve to the full openbkn-ai registry.
OFFLINE_MODE="false"
CORE_IMAGE_REGISTRY="swr"
CONFIG_YAML_PATH="${tmp_cfg}"
assert_eq "swr-alias" "$(resolve_openbkn_image_registry)" "swr.cn-east-3.myhuaweicloud.com/openbkn-ai"

CORE_IMAGE_REGISTRY="ghcr"
assert_eq "ghcr-alias" "$(resolve_openbkn_image_registry)" "ghcr.io/openbkn-ai"

# 2. Custom registry should pass through unchanged.
CORE_IMAGE_REGISTRY="registry.example.com/custom/openbkn-ai"
assert_eq "custom-registry" "$(resolve_openbkn_image_registry)" "registry.example.com/custom/openbkn-ai"

# 3. Config image.registry should be used when CLI does not set one.
CORE_IMAGE_REGISTRY=""
cat >"${tmp_cfg}" <<'EOF'
image:
  registry: custom.registry.local/openbkn-ai
EOF
assert_eq "config-registry" "$(resolve_openbkn_image_registry)" "custom.registry.local/openbkn-ai"

# 4. Offline mode always wins.
OFFLINE_MODE="true"
OFFLINE_REGISTRY="registry.openbkn.ai:5000"
CORE_IMAGE_REGISTRY="ghcr"
assert_eq "offline-registry" "$(resolve_openbkn_image_registry)" "registry.openbkn.ai:5000/openbkn-ai"

# 5. Compose helper keeps nested repo paths intact.
assert_eq "compose-image" "$(compose_image_ref "registry.example.com/ns" "bitnami/kafka" "3.9.0")" "registry.example.com/ns/bitnami/kafka:3.9.0"

# 6. Service helper should not let env-based registry override offline mode.
OFFLINE_MODE="true"
OFFLINE_REGISTRY="registry.openbkn.ai:5000"
REDIS_IMAGE_REGISTRY="ghcr"
REDIS_IMAGE=""
_redis_resolve_image_defaults
assert_eq "redis-offline-override" "${REDIS_IMAGE_REGISTRY}" "registry.openbkn.ai:5000/openbkn-ai"
assert_eq "redis-offline-image" "${REDIS_IMAGE}" "registry.openbkn.ai:5000/openbkn-ai/redis:1.11.2-main.20260718025853.shaf24e971"

if [[ "${FAIL}" -eq 0 ]]; then
    echo "registry_test: all ${PASS} checks passed"
    exit 0
fi

echo "registry_test: FAILED"
exit 1
