#!/usr/bin/env bash
# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
# Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

set -euo pipefail

chart_dir="${1:-charts/agent-observability}"
render_chart() {
    helm template "$@"
}

disabled="$(render_chart agent-observability "${chart_dir}")"
if grep -q 'business-provenance-bootstrap' <<<"${disabled}"; then
    echo "bootstrap Job must stay absent when Enterprise provenance is disabled" >&2
    exit 1
fi

enabled="$(render_chart agent-observability "${chart_dir}" \
    --set enterpriseBusinessProvenance.enabled=true \
    --set enterpriseBusinessProvenance.agentURL=http://bkn-agent:30800 \
    --set enterpriseBusinessProvenance.agentID=business_provenance_optimizer \
    --set enterpriseBusinessProvenance.agentName=BusinessProvenanceOptimizer \
    --set core.projection.enabled=true \
    --set core.projection.historicalProvenance.enabled=true \
    --set core.projection.grant.existingSecret=trace-projection-grant)"

# Projection grants are independent of the retired account bootstrap.
for required in \
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
    grep -Fq "${required}" <<<"${enabled}" || {
        echo "projection grant contract missing: ${required}" >&2
        exit 1
    }
done

# Even old values supplied by an existing deployment must not resurrect the Job.
legacy="$(render_chart agent-observability "${chart_dir}" \
    --set enterpriseBusinessProvenance.enabled=true \
    --set enterpriseBusinessProvenance.agentURL=http://bkn-agent:30800 \
    --set enterpriseBusinessProvenance.agentID=business_provenance_optimizer \
    --set enterpriseBusinessProvenance.agentName=BusinessProvenanceOptimizer \
    --set enterpriseBusinessProvenance.bootstrap.enabled=true \
    --set enterpriseBusinessProvenance.bootstrap.auth.existingSecret=obsolete-secret)"
for rendered in "${enabled}" "${legacy}"; do
    if grep -Eq 'business-provenance-bootstrap|BKN_PROVENANCE_OWNER_ID|BKN_PROVENANCE_BOOTSTRAP_TOKEN|obsolete-secret' <<<"${rendered}"; then
        echo "retired provenance account bootstrap must not render" >&2
        exit 1
    fi
done

historical_only="$(render_chart agent-observability "${chart_dir}" \
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

echo "business provenance chart without application account: PASS"
