#!/usr/bin/env bash
# Apply the OpenBKN 0.1.4 Function/BKN-context compatibility patch.
#
# This patch replaces only agent-retrieval, agent-operator-integration, and
# sandbox-control-plane container images. It performs no database migration and
# never imports a sample, toolbox, Skill, or knowledge network.
set -euo pipefail

namespace="openbkn"
registry="ghcr.io/openbkn-ai"
tag=""
chart_registry="oci://ghcr.io/openbkn-ai/charts"
chart_version="0.1.4-release"
timeout="10m"
dry_run=false
assume_yes=false

usage() {
  cat <<'EOF'
Usage:
  apply.sh --tag <patch-image-tag> [options]

Required:
  --tag <tag>                 Identical patch image tag for both services.

Options:
  --namespace <name>          Kubernetes namespace (default: openbkn).
  --registry <host/path>      Image registry path (default: ghcr.io/openbkn-ai).
  --chart-registry <oci-url>  Chart OCI prefix (default: oci://ghcr.io/openbkn-ai/charts).
  --chart-version <version>   Existing 0.1.4 chart version (default: 0.1.4-release).
  --timeout <duration>        Helm and rollout timeout (default: 10m).
  --dry-run                   Print the exact target images without changing the cluster.
  --yes                       Apply without an interactive confirmation.
  -h, --help                  Show this help.

This patch changes only agent-retrieval, agent-operator-integration, and
sandbox-control-plane. It has no schema/data migration. Use rollback.sh with
the original image registry/tag to revert the three deployments.
EOF
}

die() {
  echo "ERROR: $*" >&2
  exit 2
}

need_value() {
  [[ $# -ge 2 && -n $2 ]] || die "$1 requires a value"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --namespace|--registry|--tag|--chart-registry|--chart-version|--timeout)
      need_value "$1" "${2:-}"
      case "$1" in
        --namespace) namespace="$2" ;;
        --registry) registry="${2%/}" ;;
        --tag) tag="$2" ;;
        --chart-registry) chart_registry="${2%/}" ;;
        --chart-version) chart_version="$2" ;;
        --timeout) timeout="$2" ;;
      esac
      shift 2
      ;;
    --dry-run) dry_run=true; shift ;;
    --yes) assume_yes=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown argument: $1" ;;
  esac
done

[[ -n "$tag" ]] || die "--tag is required"

agent_retrieval_image="$registry/agent-retrieval:$tag"
operator_integration_image="$registry/agent-operator-integration:$tag"
sandbox_control_plane_image="$registry/sandbox-control-plane:$tag"

echo "patch=0.1.4-function-bkn-context"
echo "namespace=$namespace"
echo "agent-retrieval=$agent_retrieval_image"
echo "agent-operator-integration=$operator_integration_image"
echo "sandbox-control-plane=$sandbox_control_plane_image"

if [[ "$dry_run" == true ]]; then
  echo "mode=dry-run"
  exit 0
fi

command -v helm >/dev/null || die "helm is required"
command -v kubectl >/dev/null || die "kubectl is required"

for release in agent-retrieval agent-operator-integration sandbox; do
  helm status "$release" --namespace "$namespace" >/dev/null 2>&1 || \
    die "expected existing Helm release not found: $release in namespace $namespace"
done

if [[ "$assume_yes" != true ]]; then
  read -r -p "Upgrade only the three listed OpenBKN deployments? Type yes: " confirmation
  [[ "$confirmation" == yes ]] || die "cancelled"
fi

upgrade() {
  local release=$1 repository=$2
  helm upgrade --install "$release" "$chart_registry/$release" \
    --namespace "$namespace" \
    --version "$chart_version" \
    --reuse-values \
    --set "image.registry=$registry" \
    --set "image.service.repository=$repository" \
    --set-string "image.service.tag=$tag" \
    --wait --timeout "$timeout"
}

upgrade agent-retrieval agent-retrieval
upgrade agent-operator-integration agent-operator-integration

helm upgrade --install sandbox "$chart_registry/sandbox" \
  --namespace "$namespace" \
  --version "$chart_version" \
  --reuse-values \
  --set "image.registry=$registry" \
  --set "image.controlPlane.repository=sandbox-control-plane" \
  --set-string "image.controlPlane.tag=$tag" \
  --wait --timeout "$timeout"

kubectl -n "$namespace" rollout status deployment/agent-retrieval --timeout="$timeout"
kubectl -n "$namespace" rollout status deployment/agent-operator-integration --timeout="$timeout"
kubectl -n "$namespace" rollout status deployment/sandbox-control-plane --timeout="$timeout"

echo "patch apply complete"
echo "Next: run ./verify.sh --namespace $namespace and then the MCP acceptance in README.md."
