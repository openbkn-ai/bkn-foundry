#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/opensearch.sh"

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

log_info() { :; }
log_warn() { :; }
log_error() { :; }
resolve_openbkn_image_registry() { printf '%s' "ghcr.io/openbkn-ai"; }
compose_image_ref() { printf '%s/%s:%s' "$1" "$2" "$3"; }

reset_env() {
    OPENSEARCH_IMAGE=""
    OPENSEARCH_IMAGE_REGISTRY=""
    OPENSEARCH_IMAGE_REPOSITORY="opensearch"
    OPENSEARCH_IMAGE_TAG="2.19.4-main.20260818163046.shaaeb5d56"
    OPENSEARCH_INIT_IMAGE=""
    OPENSEARCH_INIT_IMAGE_REPOSITORY="busybox"
    OPENSEARCH_INIT_IMAGE_TAG="1.36.1"
    OPENSEARCH_RELEASE_NAME="opensearch"
    OPENSEARCH_NAMESPACE="resource"
    OPENSEARCH_CLUSTER_NAME="opensearch-cluster"
    OPENSEARCH_NODE_GROUP="master"
    KUBECTL_SET_IMAGE_CALL=""
}

kubectl() {
    if [[ "$1" == "get" && "$2" == "statefulset" ]]; then
        printf '%s' "${CURRENT_IMAGE}"
    elif [[ "$1" == "set" && "$2" == "image" && "$3" == "statefulset" ]]; then
        KUBECTL_SET_IMAGE_CALL="$*"
    fi
}

reset_env
CURRENT_IMAGE="ghcr.io/openbkn-ai/opensearchproject/opensearch:2.19.4"
_opensearch_upgrade_legacy_image
[[ "${KUBECTL_SET_IMAGE_CALL}" == *"set image statefulset opensearch-cluster-master"* ]] || fail "legacy image must update the OpenSearch StatefulSet"
[[ "${KUBECTL_SET_IMAGE_CALL}" == *"opensearch=ghcr.io/openbkn-ai/opensearch:2.19.4-main.20260818163046.shaaeb5d56"* ]] || fail "upgrade must set the platform image"

reset_env
CURRENT_IMAGE="ghcr.io/openbkn-ai/opensearch:2.19.4-main.20260818163046.shaaeb5d56"
_opensearch_upgrade_legacy_image
[[ -z "${KUBECTL_SET_IMAGE_CALL}" ]] || fail "platform image must not be upgraded again"

reset_env
CURRENT_IMAGE="ghcr.io/acme/opensearch:2.19.4"
OPENSEARCH_IMAGE="ghcr.io/acme/opensearch:2.19.4"
_opensearch_upgrade_legacy_image
[[ -z "${KUBECTL_SET_IMAGE_CALL}" ]] || fail "explicit image must not be overwritten"

echo "PASS: OpenSearch legacy image upgrade guard"
