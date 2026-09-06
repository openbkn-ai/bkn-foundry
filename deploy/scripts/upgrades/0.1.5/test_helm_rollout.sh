#!/usr/bin/env bash
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
ontology_chart="${repo_root}/adp/bkn/ontology-query/helm/ontology-query"
bkn_chart="${repo_root}/adp/bkn/bkn-backend/helm/bkn-backend"
vega_chart="${repo_root}/adp/vega/vega-backend/helm/vega-backend"
execution_chart="${repo_root}/adp/execution-factory/operator-integration/helm/agent-operator-integration"
safe_chart="${repo_root}/bkn-safe/charts/bkn-safe"

ontology_default="$(helm template ontology-query "${ontology_chart}")"
grep -A1 'name: KN_PROXY_MODE' <<<"${ontology_default}" | grep -q 'value: "off"'
grep -A1 'name: KN_PROXY_KN_ALLOWLIST' <<<"${ontology_default}" | grep -q 'value: ""'

ontology_canary="$(helm template ontology-query "${ontology_chart}" \
  --set knProxy.mode=allowlist \
  --set 'knProxy.knowledgeNetworks={kn-1,kn-2}')"
grep -A1 'name: KN_PROXY_MODE' <<<"${ontology_canary}" | grep -q 'value: "allowlist"'
grep -A1 'name: KN_PROXY_KN_ALLOWLIST' <<<"${ontology_canary}" | grep -q 'value: "kn-1,kn-2"'

for chart in "${bkn_chart}" "${vega_chart}" "${execution_chart}"; do
  if helm template rollout "${chart}" | grep -q '^kind: NetworkPolicy$'; then
    echo "NetworkPolicy must be disabled by default for ${chart}" >&2
    exit 1
  fi
  rendered="$(helm template rollout "${chart}" --set knProxy.networkPolicy.enabled=true)"
  grep -q '^kind: NetworkPolicy$' <<<"${rendered}"
  grep -q 'module: bkn-backend' <<<"${rendered}"
  grep -q 'module: ontology-query' <<<"${rendered}"
  grep -q 'app: agent-operator-integration' <<<"${rendered}"
  grep -q 'app.kubernetes.io/name: ingress-nginx' <<<"${rendered}"
done

safe_default="$(helm template bkn-safe "${safe_chart}")"
if grep -q '^kind: NetworkPolicy$' <<<"${safe_default}"; then
  echo "bkn-safe NetworkPolicy must be disabled by default" >&2
  exit 1
fi
if grep -q '/api/safe/in/v1' <<<"${safe_default}"; then
  echo "bkn-safe managed-proxy control routes must not be exposed through Ingress" >&2
  exit 1
fi
safe_policy="$(helm template bkn-safe "${safe_chart}" \
  --set networkPolicy.enabled=true \
  --set-json 'networkPolicy.ingress=[{"from":[{"podSelector":{"matchLabels":{"module":"bkn-backend"}}},{"podSelector":{"matchLabels":{"module":"ontology-query"}}},{"podSelector":{"matchLabels":{"app":"agent-operator-integration"}}}],"ports":[{"protocol":"TCP","port":3000}]}]')"
grep -q '^kind: NetworkPolicy$' <<<"${safe_policy}"
grep -q 'module: bkn-backend' <<<"${safe_policy}"
grep -q 'module: ontology-query' <<<"${safe_policy}"
grep -q 'app: agent-operator-integration' <<<"${safe_policy}"

bkn_ingress="$(helm template bkn-backend "${bkn_chart}" --show-only templates/ingress.yaml)"
vega_ingress="$(helm template vega-backend "${vega_chart}" --show-only templates/ingress.yaml)"
execution_ingress="$(helm template agent-operator-integration "${execution_chart}" --show-only templates/ingress.yaml)"
if grep -Eq '/api/(bkn-backend|ontology-manager)/in/v1' <<<"${bkn_ingress}"; then
  echo "BKN internal routes must not be exposed through Ingress" >&2
  exit 1
fi
if grep -q '/api/vega-backend/in/v1' <<<"${vega_ingress}"; then
  echo "Vega internal proxy routes must not be exposed through Ingress" >&2
  exit 1
fi
if grep -q '/api/agent-operator-integration/internal-v1' <<<"${execution_ingress}"; then
  echo "Execution-factory internal proxy routes must not be exposed through Ingress" >&2
  exit 1
fi

echo "Helm managed-proxy rollout checks passed."
