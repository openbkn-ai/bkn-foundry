#!/usr/bin/env bash
# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
# Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

set -euo pipefail

chart_dir="${1:-charts/agent-observability}"

disabled="$(helm template agent-observability "${chart_dir}")"
if grep -q 'business-provenance-bootstrap' <<<"${disabled}"; then
    echo "bootstrap Job must stay absent when Enterprise provenance is disabled" >&2
    exit 1
fi

enabled="$(helm template agent-observability "${chart_dir}" \
    --set enterpriseBusinessProvenance.enabled=true \
    --set enterpriseBusinessProvenance.agentURL=http://bkn-agent:30800 \
    --set enterpriseBusinessProvenance.agentID=business_provenance_optimizer \
    --set enterpriseBusinessProvenance.agentName=BusinessProvenanceOptimizer \
    --set core.projection.enabled=true \
    --set core.projection.historicalProvenance.enabled=true \
    --set core.projection.grant.existingSecret=trace-projection-grant)"

for required in \
    'name: agent-observability-business-provenance-bootstrap' \
    '"helm.sh/hook": post-install,post-upgrade' \
    'activeDeadlineSeconds: 420' \
    'value: "bdd59f76-19c3-58b0-bf5f-082c4c3cbddb"' \
    'value: "http://bkn-safe:3000/api/safe/v1"' \
    'value: "http://bkn-agent:30800/api/bkn-agent/v1"' \
    'name: "bkn-agent-provenance-bootstrap"' \
    'app.bootstrap.business_provenance' \
    'name: BKN_TRACE_PROJECTION_GRANT_ISSUER' \
    'value: "trace-core-projection"' \
    'name: BKN_TRACE_PROJECTION_GRANT_KEY_ID' \
    'value: "trace-projection-key"' \
    'name: BKN_TRACE_PROJECTION_GRANT_AUDIENCE' \
    'value: "bkn-projection-read"' \
    'name: BKN_TRACE_PROJECTION_GRANT_TTL' \
    'value: "24h5m"' \
    'name: "trace-projection-grant"' \
    'key: "private-key"'; do
    grep -q "${required}" <<<"${enabled}" || {
        echo "rendered bootstrap Job missing: ${required}" >&2
        exit 1
    }
done

historical_only="$(helm template agent-observability "${chart_dir}" \
    --set core.projection.enabled=true \
    --set core.projection.historicalProvenance.enabled=true \
    --set core.projection.grant.existingSecret=trace-projection-grant \
    --show-only templates/deployment.yaml)"
for required in \
    'name: BKN_TRACE_HISTORICAL_PROVENANCE_ENABLED' \
    'name: BKN_TRACE_PROJECTION_GRANT_PRIVATE_KEY' \
    'name: "trace-projection-grant"'; do
    grep -q "${required}" <<<"${historical_only}" || {
        echo "historical provenance configuration must not depend on bootstrap selection: ${required}" >&2
        exit 1
    }
done
grep -A1 'name: BKN_TRACE_HISTORICAL_PROVENANCE_ENABLED' <<<"${historical_only}" | grep -q 'value: "true"' || {
    echo "historical provenance enablement must render true next to its environment variable" >&2
    exit 1
}

private_registry="$(helm template agent-observability "${chart_dir}" \
    --set image.registry=registry.internal/openbkn \
    --set image.tag=observability-hotfix \
    --set enterpriseBusinessProvenance.enabled=true \
    --set enterpriseBusinessProvenance.agentURL=http://bkn-agent:30800 \
    --set enterpriseBusinessProvenance.agentID=business_provenance_optimizer \
    --set enterpriseBusinessProvenance.agentName=BusinessProvenanceOptimizer \
    --show-only templates/business-provenance-bootstrap-job.yaml)"
grep -q 'image: "registry.internal/openbkn/bkn-agent:__VERSION__"' <<<"${private_registry}" || {
    echo "bootstrap image must inherit the release registry but keep its own tag" >&2
    exit 1
}

long_name="rrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrr"
long_render="$(helm template "${long_name}" "${chart_dir}" \
    --set enterpriseBusinessProvenance.enabled=true \
    --set enterpriseBusinessProvenance.agentURL=http://bkn-agent:30800 \
    --set enterpriseBusinessProvenance.agentID=business_provenance_optimizer \
    --set enterpriseBusinessProvenance.agentName=BusinessProvenanceOptimizer \
    --show-only templates/business-provenance-bootstrap-job.yaml)"
job_name="$(awk '$1 == "name:" { print $2; exit }' <<<"${long_render}")"
if [[ -z "${job_name}" ]] || (( ${#job_name} > 63 )); then
    echo "bootstrap Job name must fit the DNS label limit: ${job_name}" >&2
    exit 1
fi

echo "business provenance bootstrap chart contract: PASS"
