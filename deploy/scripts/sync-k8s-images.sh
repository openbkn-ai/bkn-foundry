#!/bin/bash
# Kubernetes Images Sync Script for Offline Deployment
# Usage: ./sync-k8s-images.sh <target_registry> [k8s_version]
# Example: ./sync-k8s-images.sh node1:5000 v1.28.15

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib/common.sh"

# Default Kubernetes version
K8S_VERSION="${2:-v1.28.15}"
K8S_VERSION_SHORT="${K8S_VERSION#v}"

# Source registry (Aliyun mirror)
SOURCE_REGISTRY="registry.aliyuncs.com/google_containers"

# Target registry
TARGET_REGISTRY="${1:-}"
TARGET_NAMESPACE="google_containers"

if [[ -z "${TARGET_REGISTRY}" ]]; then
    echo "Usage: $0 <target_registry> [k8s_version]"
    echo ""
    echo "Arguments:"
    echo "  target_registry  Target offline registry (e.g., node1:5000)"
    echo "  k8s_version      Kubernetes version (default: v1.28.15)"
    echo ""
    echo "Examples:"
    echo "  $0 node1:5000"
    echo "  $0 node1:5000 v1.28.15"
    echo "  $0 192.168.1.100:5000 v1.27.10"
    exit 1
fi

log_info "=== Kubernetes Images Sync Script ==="
log_info "Source Registry: ${SOURCE_REGISTRY}"
log_info "Target Registry: ${TARGET_REGISTRY}"
log_info "Kubernetes Version: ${K8S_VERSION}"
log_info ""

# Required images for Kubernetes
K8S_IMAGES=(
    "kube-apiserver:${K8S_VERSION}"
    "kube-controller-manager:${K8S_VERSION}"
    "kube-scheduler:${K8S_VERSION}"
    "kube-proxy:${K8S_VERSION}"
    "pause:3.9"
    "pause:3.6"
    "pause:3.10"
    "coredns:v1.10.1"
    "etcd:3.5.9-0"
)

# Required images for Flannel CNI
FLANNEL_IMAGES=(
    "openbkn-ai/flannel/flannel:v0.25.5"
    "openbkn-ai/flannel/flannel-cni-plugin:v1.5.1-flannel1"
)

# Required images for Ingress-Nginx
INGRESS_NGINX_IMAGES=(
    "openbkn-ai/ingress-nginx/controller:v1.14.1"
    "openbkn-ai/ingress-nginx/kube-webhook-certgen:v1.6.1"
)

# Required images for Local Path Provisioner
LOCALPV_IMAGES=(
    "openbkn-ai/rancher/local-path-provisioner:v0.0.32"
    "openbkn-ai/busybox:1.36.1"
)

# Required images for MariaDB
MARIADB_IMAGES=(
    "openbkn-ai/mariadb:11.4.7"
)

# Required images for Redis
REDIS_IMAGES=(
    "openbkn-ai/redis:1.11.2-main.20260718025853.shaf24e971"
)

# Required images for Kafka
KAFKA_IMAGES=(
    "openbkn-ai/bitnami/kafka:3.9.0-debian-12-r10"
)

# Required images for OpenSearch
OPENSEARCH_IMAGES=(
    "openbkn-ai/opensearchproject/opensearch:2.19.4"
    "openbkn-ai/busybox:1.36.1"
)

# Required images for other components
OTHER_IMAGES=(
    "openbkn-ai/portainer/kubectl-shell:latest"
)

# Required images for OpenBKN Applications
# NOTE: OpenBKN application images are defined in Helm Charts
# The actual image names depend on the specific version being deployed
# Common images include:
#   - openbkn-ai/bkn-core:<version>
#   - openbkn-ai/bkn-auth:<version>
#   - openbkn-ai/bkn-business-domain:<version>
#   - openbkn-ai/bkn-foundry-web:<version>
# You need to sync these images based on your deployment
OPENBKN_APP_IMAGES=(
    # Add your OpenBKN application images here, e.g.:
    # "openbkn-ai/bkn-core:v1.0.0"
    # "openbkn-ai/bkn-auth:v1.0.0"
)

log_info "Images to sync:"
echo "Kubernetes Components:"
for img in "${K8S_IMAGES[@]}"; do
    echo "  - ${img}"
done
echo ""
echo "Flannel CNI:"
for img in "${FLANNEL_IMAGES[@]}"; do
    echo "  - ${img}"
done
echo ""
echo "Ingress-Nginx:"
for img in "${INGRESS_NGINX_IMAGES[@]}"; do
    echo "  - ${img}"
done
echo ""
echo "Local Path Provisioner:"
for img in "${LOCALPV_IMAGES[@]}"; do
    echo "  - ${img}"
done
echo ""
echo "MariaDB:"
for img in "${MARIADB_IMAGES[@]}"; do
    echo "  - ${img}"
done
echo ""
echo "Redis:"
for img in "${REDIS_IMAGES[@]}"; do
    echo "  - ${img}"
done
echo ""
echo "Kafka:"
for img in "${KAFKA_IMAGES[@]}"; do
    echo "  - ${img}"
done
echo ""
echo "OpenSearch:"
for img in "${OPENSEARCH_IMAGES[@]}"; do
    echo "  - ${img}"
done
echo ""
echo "Other Components:"
for img in "${OTHER_IMAGES[@]}"; do
    echo "  - ${img}"
done
echo ""
echo "OpenBKN Applications (configure based on your deployment):"
if [[ ${#OPENBKN_APP_IMAGES[@]} -gt 0 ]]; then
    for img in "${OPENBKN_APP_IMAGES[@]}"; do
        echo "  - ${img}"
    done
else
    echo "  (No OpenBKN application images configured)"
    echo "  Please add your images to OPENBKN_APP_IMAGES array in this script"
fi
echo ""

# Check if docker or podman is available
if command -v docker &>/dev/null; then
    CONTAINER_RUNTIME="docker"
elif command -v podman &>/dev/null; then
    CONTAINER_RUNTIME="podman"
else
    log_error "Neither docker nor podman is available. Please install one of them."
    exit 1
fi

log_info "Using container runtime: ${CONTAINER_RUNTIME}"

# Function to sync a single image
sync_image() {
    local source_image="$1"
    local target_image="$2"

    log_info "Syncing ${source_image} -> ${target_image}"

    # Pull from source
    if ! ${CONTAINER_RUNTIME} pull "${source_image}"; then
        log_error "Failed to pull ${source_image}"
        return 1
    fi

    # Tag for target
    if ! ${CONTAINER_RUNTIME} tag "${source_image}" "${target_image}"; then
        log_error "Failed to tag ${source_image}"
        return 1
    fi

    # Push to target
    if ! ${CONTAINER_RUNTIME} push "${target_image}"; then
        log_error "Failed to push ${target_image}"
        return 1
    fi

    log_info "✓ ${source_image} synced successfully"
    return 0
}

# Test connectivity to target registry
log_info "Testing connectivity to ${TARGET_REGISTRY}..."
if ! curl -s "http://${TARGET_REGISTRY}/v2/" >/dev/null 2>&1; then
    log_warn "Cannot connect to http://${TARGET_REGISTRY}/v2/"
    log_warn "If your registry uses HTTPS or requires authentication, please configure it manually."
    log_warn "Continuing anyway..."
else
    log_info "✓ Target registry is accessible"
fi

# Initialize counters
FAILED_IMAGES=()
SYNCED_COUNT=0
FAILED_COUNT=0

# Sync Kubernetes images
log_info "Syncing Kubernetes images..."
for image in "${K8S_IMAGES[@]}"; do
    source_image="${SOURCE_REGISTRY}/${image}"
    target_image="${TARGET_REGISTRY}/${TARGET_NAMESPACE}/${image}"

    if sync_image "${source_image}" "${target_image}"; then
        SYNCED_COUNT=$((SYNCED_COUNT + 1))
    else
        FAILED_IMAGES+=("${source_image}")
        FAILED_COUNT=$((FAILED_COUNT + 1))
    fi
done

# Sync Flannel images from SWR
log_info "Syncing Flannel CNI images..."
FLANNEL_SOURCE="swr.cn-east-3.myhuaweicloud.com"
for image in "${FLANNEL_IMAGES[@]}"; do
    source_image="${FLANNEL_SOURCE}/${image}"
    target_image="${TARGET_REGISTRY}/${image}"

    if sync_image "${source_image}" "${target_image}"; then
        SYNCED_COUNT=$((SYNCED_COUNT + 1))
    else
        FAILED_IMAGES+=("${source_image}")
        FAILED_COUNT=$((FAILED_COUNT + 1))
    fi
done

# Sync Ingress-Nginx images from SWR
log_info "Syncing Ingress-Nginx images..."
INGRESS_SOURCE="swr.cn-east-3.myhuaweicloud.com"
for image in "${INGRESS_NGINX_IMAGES[@]}"; do
    source_image="${INGRESS_SOURCE}/${image}"
    target_image="${TARGET_REGISTRY}/${image}"

    if sync_image "${source_image}" "${target_image}"; then
        SYNCED_COUNT=$((SYNCED_COUNT + 1))
    else
        FAILED_IMAGES+=("${source_image}")
        FAILED_COUNT=$((FAILED_COUNT + 1))
    fi
done

# Sync Local Path Provisioner images from SWR
log_info "Syncing Local Path Provisioner images..."
LOCALPV_SOURCE="swr.cn-east-3.myhuaweicloud.com"
for image in "${LOCALPV_IMAGES[@]}"; do
    source_image="${LOCALPV_SOURCE}/${image}"
    target_image="${TARGET_REGISTRY}/${image}"

    if sync_image "${source_image}" "${target_image}"; then
        SYNCED_COUNT=$((SYNCED_COUNT + 1))
    else
        FAILED_IMAGES+=("${source_image}")
        FAILED_COUNT=$((FAILED_COUNT + 1))
    fi
done

# Sync MariaDB images from SWR
log_info "Syncing MariaDB images..."
MARIADB_SOURCE="swr.cn-east-3.myhuaweicloud.com"
for image in "${MARIADB_IMAGES[@]}"; do
    source_image="${MARIADB_SOURCE}/${image}"
    target_image="${TARGET_REGISTRY}/${image}"

    if sync_image "${source_image}" "${target_image}"; then
        SYNCED_COUNT=$((SYNCED_COUNT + 1))
    else
        FAILED_IMAGES+=("${source_image}")
        FAILED_COUNT=$((FAILED_COUNT + 1))
    fi
done

# Sync Redis images from SWR
log_info "Syncing Redis images..."
REDIS_SOURCE="swr.cn-east-3.myhuaweicloud.com"
for image in "${REDIS_IMAGES[@]}"; do
    source_image="${REDIS_SOURCE}/${image}"
    target_image="${TARGET_REGISTRY}/${image}"

    if sync_image "${source_image}" "${target_image}"; then
        SYNCED_COUNT=$((SYNCED_COUNT + 1))
    else
        FAILED_IMAGES+=("${source_image}")
        FAILED_COUNT=$((FAILED_COUNT + 1))
    fi
done

# Sync Kafka images from SWR
log_info "Syncing Kafka images..."
KAFKA_SOURCE="swr.cn-east-3.myhuaweicloud.com"
for image in "${KAFKA_IMAGES[@]}"; do
    source_image="${KAFKA_SOURCE}/${image}"
    target_image="${TARGET_REGISTRY}/${image}"

    if sync_image "${source_image}" "${target_image}"; then
        SYNCED_COUNT=$((SYNCED_COUNT + 1))
    else
        FAILED_IMAGES+=("${source_image}")
        FAILED_COUNT=$((FAILED_COUNT + 1))
    fi
done

# Sync OpenSearch images from SWR
log_info "Syncing OpenSearch images..."
OPENSEARCH_SOURCE="swr.cn-east-3.myhuaweicloud.com"
for image in "${OPENSEARCH_IMAGES[@]}"; do
    source_image="${OPENSEARCH_SOURCE}/${image}"
    target_image="${TARGET_REGISTRY}/${image}"

    if sync_image "${source_image}" "${target_image}"; then
        SYNCED_COUNT=$((SYNCED_COUNT + 1))
    else
        FAILED_IMAGES+=("${source_image}")
        FAILED_COUNT=$((FAILED_COUNT + 1))
    fi
done

# Sync other images from SWR
log_info "Syncing other component images..."
OTHER_SOURCE="swr.cn-east-3.myhuaweicloud.com"
for image in "${OTHER_IMAGES[@]}"; do
    source_image="${OTHER_SOURCE}/${image}"
    target_image="${TARGET_REGISTRY}/${image}"

    if sync_image "${source_image}" "${target_image}"; then
        SYNCED_COUNT=$((SYNCED_COUNT + 1))
    else
        FAILED_IMAGES+=("${source_image}")
        FAILED_COUNT=$((FAILED_COUNT + 1))
    fi
done

# Sync OpenBKN application images from SWR (if configured)
if [[ ${#OPENBKN_APP_IMAGES[@]} -gt 0 ]]; then
    log_info "Syncing OpenBKN application images..."
    OPENBKN_SOURCE="swr.cn-east-3.myhuaweicloud.com"
    for image in "${OPENBKN_APP_IMAGES[@]}"; do
        source_image="${OPENBKN_SOURCE}/${image}"
        target_image="${TARGET_REGISTRY}/${image}"

        if sync_image "${source_image}" "${target_image}"; then
            SYNCED_COUNT=$((SYNCED_COUNT + 1))
        else
            FAILED_IMAGES+=("${source_image}")
            FAILED_COUNT=$((FAILED_COUNT + 1))
        fi
    done
else
    log_info "Skipping OpenBKN application images (none configured)"
    log_info "To sync OpenBKN app images, edit this script and add them to OPENBKN_APP_IMAGES array"
fi

echo ""
log_info "=== Sync Summary ==="
log_info "Total images synced: ${SYNCED_COUNT}"
log_info "Failed: ${FAILED_COUNT}"

if [[ ${FAILED_COUNT} -gt 0 ]]; then
    log_error "Failed images:"
    for img in "${FAILED_IMAGES[@]}"; do
        echo "  - ${img}"
    done
    exit 1
fi

echo ""
log_info "✓ All images synced successfully!"
log_info ""
log_info "Next steps:"
log_info "1. Verify images in your offline registry:"
log_info "   curl http://${TARGET_REGISTRY}/v2/${TARGET_NAMESPACE}/kube-apiserver/tags/list"
log_info "   curl http://${TARGET_REGISTRY}/v2/openbkn-ai/flannel/flannel/tags/list"
log_info "   curl http://${TARGET_REGISTRY}/v2/openbkn-ai/ingress-nginx/controller/tags/list"
log_info "   curl http://${TARGET_REGISTRY}/v2/openbkn-ai/rancher/local-path-provisioner/tags/list"
log_info "   curl http://${TARGET_REGISTRY}/v2/openbkn-ai/mariadb/tags/list"
log_info "   curl http://${TARGET_REGISTRY}/v2/openbkn-ai/redis/tags/list"
log_info "   curl http://${TARGET_REGISTRY}/v2/openbkn-ai/bitnami/kafka/tags/list"
log_info "   curl http://${TARGET_REGISTRY}/v2/openbkn-ai/opensearchproject/opensearch/tags/list"
log_info ""
log_info "2. Deploy Kubernetes in offline mode:"
log_info "   sudo bash ./deploy.sh --offline k8s install"
log_info ""
log_info "   Or with custom registry:"
log_info "   sudo bash ./deploy.sh --offline=${TARGET_REGISTRY} k8s install"
log_info ""
log_info "3. Deploy data services in offline mode:"
log_info "   sudo bash ./deploy.sh --offline foundry install --minimum"
log_info "   Or deploy individual services:"
log_info "   sudo bash ./deploy.sh --offline mariadb install"
log_info "   sudo bash ./deploy.sh --offline redis install"
log_info "   sudo bash ./deploy.sh --offline kafka install"
log_info "   sudo bash ./deploy.sh --offline opensearch install"