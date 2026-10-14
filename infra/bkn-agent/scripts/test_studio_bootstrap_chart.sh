#!/usr/bin/env bash
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

set -euo pipefail

chart_dir="${1:-charts}"
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT

helm template bkn-agent "$chart_dir" > "$test_dir/default.yaml"
if grep -q 'name: studio-bootstrap' "$test_dir/default.yaml"; then
    echo 'Studio bootstrap must be disabled by default' >&2
    exit 1
fi

expect_failure() {
    local expected="$1"
    shift
    if helm template bkn-agent "$chart_dir" "$@" > "$test_dir/output.yaml" 2> "$test_dir/error.txt"; then
        echo "Expected chart rejection: $expected" >&2
        exit 1
    fi
    grep -Fq "$expected" "$test_dir/error.txt"
}

expect_failure 'studioBootstrap.ownerId must be a provisioned management principal' \
    --set studioBootstrap.enabled=true
expect_failure 'studioBootstrap.actorId is required for audit attribution' \
    --set studioBootstrap.enabled=true --set-string studioBootstrap.ownerId=management-principal
expect_failure 'studioBootstrap.dependencies.model must be ready, missing or unknown' \
    --set studioBootstrap.enabled=true --set-string studioBootstrap.ownerId=management-principal \
    --set-string studioBootstrap.actorId=deployment-operator --set-string studioBootstrap.dependencies.model=invalid

helm template bkn-agent "$chart_dir" --show-only templates/studio-bootstrap-job.yaml \
    --set studioBootstrap.enabled=true --set-string studioBootstrap.ownerId=management-principal \
    --set-string studioBootstrap.actorId=deployment-operator \
    --set-string 'studioBootstrap.installedFeatures[0]=graph_explorer_adapter' \
    --set-string studioBootstrap.dependencies.model=ready \
    --set-string studioBootstrap.dependencies.context_loader=missing \
    --set-string studioBootstrap.dependencies.studio_interactions=unknown \
    --set studioBootstrap.backoffLimit=2 --set studioBootstrap.activeDeadlineSeconds=90 \
    --set-string image.tag=test-baseline --set-string depServices.rds.host=test-database \
    > "$test_dir/job.yaml"

for expected in \
    'kind: Job' \
    '"helm.sh/hook": post-install,post-upgrade' \
    '"helm.sh/hook-delete-policy": before-hook-creation,hook-succeeded' \
    'backoffLimit: 2' \
    'activeDeadlineSeconds: 90' \
    'restartPolicy: Never' \
    'image: "ghcr.io/openbkn-ai/bkn-agent:test-baseline"' \
    'command: ["python", "-m", "app.bootstrap.studio"]' \
    '- initialize' \
    '- --owner-id' \
    '- "management-principal"' \
    '- --actor-id' \
    '- "deployment-operator"' \
    '- --installed-feature' \
    '- "graph_explorer_adapter"' \
    '- --dependency' \
    '- "model=ready"' \
    '- "context_loader=missing"' \
    '- "studio_interactions=unknown"' \
    'name: RDSHOST' \
    'value: "test-database"' \
    'name: RDSPORT' \
    'name: RDSDBNAME' \
    'name: RDSUSER' \
    'name: RDSPASS'; do
    grep -Fq -- "$expected" "$test_dir/job.yaml" || {
        echo "Studio bootstrap chart contract missing: $expected" >&2
        exit 1
    }
done

echo 'Studio bootstrap chart contract: PASS'
