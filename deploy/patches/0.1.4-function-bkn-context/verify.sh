#!/usr/bin/env bash
# Verify that the two patched control-plane deployments are available.
set -euo pipefail

namespace="openbkn"
timeout="10m"

usage() {
  cat <<'EOF'
Usage:
  verify.sh [--namespace <name>] [--timeout <duration>]

This checks Kubernetes readiness only. MCP acceptance is required afterwards:
start an Interaction, discover the published function toolbox, and call
"标准交期" with material_code=606-000989. The expected result is
leadtime_days=14 and Function exit_code=0.
EOF
}

die() { echo "ERROR: $*" >&2; exit 2; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --namespace|--timeout)
      [[ $# -ge 2 && -n ${2:-} ]] || die "$1 requires a value"
      [[ "$1" == --namespace ]] && namespace="$2" || timeout="$2"
      shift 2
      ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown argument: $1" ;;
  esac
done

command -v kubectl >/dev/null || die "kubectl is required"
kubectl -n "$namespace" rollout status deployment/agent-retrieval --timeout="$timeout"
kubectl -n "$namespace" rollout status deployment/agent-operator-integration --timeout="$timeout"
echo "MCP acceptance is required: use the two published-tool discovery tools and execute_published_tool."
