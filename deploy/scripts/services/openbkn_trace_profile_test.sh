#!/usr/bin/env bash
# 完整 OpenBKN 安装必须启用可持久查询的 BKN Trace；轻量 Chart 默认值不能泄漏到产品安装。
set -uo pipefail

FAILED=0
PASS=0

fail() { echo "FAIL: $*"; FAILED=1; }
ok() { PASS=$((PASS + 1)); }
contains() {
    local label="$1" value="$2" expected="$3"
    if [[ "${value}" == *"${expected}"* ]]; then ok; else fail "${label}: missing [${expected}] in [${value}]"; fi
}
not_contains() {
    local label="$1" value="$2" unexpected="$3"
    if [[ "${value}" != *"${unexpected}"* ]]; then ok; else fail "${label}: unexpected [${unexpected}] in [${value}]"; fi
}
log_info() { :; }
get_set_value() {
    local key="$1"
    shift
    local item
    for item in "$@"; do
        if [[ "${item}" == "${key}="* ]]; then
            printf '%s\n' "${item#*=}"
            return 0
        fi
    done
    return 1
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CONFIG_REGISTRY_FILE="$(mktemp)"
trap 'rm -f "${CONFIG_REGISTRY_FILE}"' EXIT
# shellcheck source=../services/openbkn.sh
source "${SCRIPT_DIR}/scripts/services/openbkn.sh"

# The profile builder is deliberately pure: install tests can validate the product
# contract without a cluster, credentials, or a Helm chart archive.
CORE_RELEASE_EXTRA_SETS=()
_openbkn_trace_profile_sets agent-observability
ao_sets="${CORE_RELEASE_EXTRA_SETS[*]:-}"
contains "AO uses durable Core" "${ao_sets}" "core.store=mariadb"
contains "AO requires Core DSN Secret" "${ao_sets}" "core.mariadb.existingSecret=bkn-trace-core-mariadb"
contains "AO persists evidence" "${ao_sets}" "evidence.store=opensearch"
contains "AO enables projection" "${ao_sets}" "core.projection.enabled=true"
contains "AO creates evidence index" "${ao_sets}" "evidence.indexManagement.createJob.enabled=true"
contains "AO protects evidence producer ingest" "${ao_sets}" "evidence.ingestAuth.existingSecret=bkn-trace-evidence-ingest"
not_contains "AO has no query gateway Secret" "${ao_sets}" "queryAuth.existingSecret="

CORE_RELEASE_EXTRA_SETS=()
_openbkn_trace_profile_sets agent-retrieval
ar_sets="${CORE_RELEASE_EXTRA_SETS[*]:-}"
contains "retrieval targets internal Trace Core" "${ar_sets}" "observability.lifecycle.core_url=http://agent-observability-internal:8081"
contains "retrieval emits Trace spans" "${ar_sets}" "observability.trace.enabled=true"
contains "retrieval emits evidence through token-protected ingest" "${ar_sets}" "observability.evidence.ingest_url=http://agent-observability:8080/api/agent-observability/v1/evidence/events"
contains "retrieval uses evidence ingest Secret" "${ar_sets}" "observability.evidence.ingest_token_secret_name=bkn-trace-evidence-ingest"
not_contains "retrieval has no query gateway Secret" "${ar_sets}" "gateway_token_secret_name="

CORE_SET_VALUES=()
OFFLINE_MODE=true
OFFLINE_REGISTRY=registry.test:5000
_openbkn_apply_default_set_values
offline_sets="${CORE_SET_VALUES[*]:-}"
contains "offline installs rewrite application images" "${offline_sets}" "image.registry=registry.test:5000/openbkn-ai"
contains "offline installs rewrite the evidence index hook image" "${offline_sets}" "evidence.indexManagement.createJob.image.registry=registry.test:5000/openbkn-ai"

CORE_SET_VALUES=()
OFFLINE_MODE=false
CORE_IMAGE_REGISTRY=ghcr
CONFIG_YAML_PATH=/nonexistent/openbkn-config.yaml
_openbkn_apply_default_set_values
online_sets="${CORE_SET_VALUES[*]:-}"
contains "online registry flags rewrite application images" "${online_sets}" "image.registry=ghcr.io/openbkn-ai"
not_contains "online registry flags leave third-party hook images on their chart registry" "${online_sets}" "evidence.indexManagement.createJob.image.registry="

CORE_SET_VALUES=()
CORE_IMAGE_REGISTRY=""
_openbkn_apply_default_set_values
default_online_sets="${CORE_SET_VALUES[*]:-}"
contains "default online installs use the SWR application registry" "${default_online_sets}" "image.registry=swr.cn-east-3.myhuaweicloud.com/openbkn-ai"
not_contains "default online installs leave third-party hook images on their chart registry" "${default_online_sets}" "evidence.indexManagement.createJob.image.registry="

CORE_SET_VALUES=("image.registry=registry.example/openbkn")
CORE_IMAGE_REGISTRY=""
_openbkn_apply_default_set_values
explicit_sets="${CORE_SET_VALUES[*]:-}"
not_contains "application registry overrides do not imply a third-party mirror" "${explicit_sets}" "evidence.indexManagement.createJob.image.registry="

CORE_SET_VALUES=("evidence.indexManagement.createJob.image.registry=hooks.example")
OFFLINE_MODE=true
OFFLINE_REGISTRY=registry.test:5000
_openbkn_apply_default_set_values
explicit_hook_sets="${CORE_SET_VALUES[*]:-}"
contains "offline installs preserve explicit hook registries" "${explicit_hook_sets}" "evidence.indexManagement.createJob.image.registry=hooks.example"
not_contains "offline installs do not append a competing hook registry" "${explicit_hook_sets}" "evidence.indexManagement.createJob.image.registry=registry.test:5000/openbkn-ai"

OFFLINE_MODE=false

cat >"${CONFIG_REGISTRY_FILE}" <<'EOF'
image:
  registry: "registry.config/openbkn"
EOF
CORE_SET_VALUES=()
CONFIG_YAML_PATH="${CONFIG_REGISTRY_FILE}"
_openbkn_apply_default_set_values
config_sets="${CORE_SET_VALUES[*]:-}"
not_contains "config application registries do not imply a third-party mirror" "${config_sets}" "evidence.indexManagement.createJob.image.registry="

sync_script="$(<"${SCRIPT_DIR}/scripts/sync-k8s-images.sh")"
contains "offline sync includes the evidence index hook image" "${sync_script}" 'curlimages/curl:8.10.1'
contains "offline sync mirrors hooks into the OpenBKN namespace" "${sync_script}" 'target_image="${TARGET_REGISTRY}/openbkn-ai/${image}"'

if [[ "${FAILED}" -eq 0 ]]; then
    echo "openbkn_trace_profile_test: all ${PASS} checks passed"
    exit 0
fi
echo "openbkn_trace_profile_test: FAILED"
exit 1
