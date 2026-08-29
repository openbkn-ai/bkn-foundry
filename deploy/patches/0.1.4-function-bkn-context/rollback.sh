#!/usr/bin/env bash
# Roll back the two deployments changed by apply.sh. No data is deleted.
set -euo pipefail

namespace="openbkn"
registry="ghcr.io/openbkn-ai"
tag=""
chart_registry="oci://ghcr.io/openbkn-ai/charts"
chart_version="0.1.4-release"
timeout="10m"
assume_yes=false

usage() {
  cat <<'EOF'
Usage:
  rollback.sh --tag <original-image-tag> [options]

Use the exact registry/tag recorded before applying the patch. Standard 0.1.4
installations normally use --tag 0.1.4-release. This command only changes the
two patched deployments and does not delete data.
EOF
}

die() { echo "ERROR: $*" >&2; exit 2; }
need_value() { [[ $# -ge 2 && -n $2 ]] || die "$1 requires a value"; }

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
    --yes) assume_yes=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown argument: $1" ;;
  esac
done

[[ -n "$tag" ]] || die "--tag is required"
command -v helm >/dev/null || die "helm is required"
command -v kubectl >/dev/null || die "kubectl is required"

if [[ "$assume_yes" != true ]]; then
  read -r -p "Restore the two OpenBKN deployments to tag $tag? Type yes: " confirmation
  [[ "$confirmation" == yes ]] || die "cancelled"
fi

rollback() {
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

rollback agent-retrieval agent-retrieval
rollback agent-operator-integration agent-operator-integration
kubectl -n "$namespace" rollout status deployment/agent-retrieval --timeout="$timeout"
kubectl -n "$namespace" rollout status deployment/agent-operator-integration --timeout="$timeout"
echo "patch rollback complete"
