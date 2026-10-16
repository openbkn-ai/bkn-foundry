#!/usr/bin/env bash
# Copyright openbkn.ai
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${SCRIPT_DIR}/scripts/services/openbkn.sh"
test_dir="$(mktemp -d)"
trap 'rm -rf "${test_dir}"' EXIT
log_info() { :; }
log_error() { :; }
_openbkn_drop_literal_env_now_from_secret() { :; }
_openbkn_adopt_unowned_resources() { return 1; }
installed=false
old_values='{}'
services='{"items":[{"metadata":{"name":"custom-gateway","namespace":"edge"},"spec":{"selector":{"app":"edge"},"ports":[{"name":"http","port":8080,"targetPort":"web","nodePort":32080}]}}]}'
kubectl() {
    printf '%s\n' "$*" >>"${test_dir}/discovery-calls"
    [[ "${services}" != unavailable ]] || return 1
    printf '%s' "${services}"
}
helm() {
    case "$1" in
        list) if [[ "${installed}" == true ]]; then printf 'sandbox\n'; fi ;;
        get) [[ " $* " != *' --all '* ]] || return 1; printf '%s' "${old_values}" ;;
        install) command helm "$@" >"${test_dir}/render.json"; cat "${test_dir}/render.json" ;;
        upgrade) printf 'upgrade\n' >>"${test_dir}/upgrades" ;;
        *) return 1 ;;
    esac
}
chart="${SCRIPT_DIR}/../infra/sandbox/deploy/helm/sandbox"
args=(upgrade --install sandbox "${chart}" --namespace openbkn)
_openbkn_helm_upgrade_release sandbox openbkn "${args[@]}"
python3 - "${test_dir}/render.json" <<'PY'
import json, sys
r = json.load(open(sys.argv[1]))
assert 'BKN_BASE_URL: "http://custom-gateway.edge.svc:8080"' in r['manifest']
p = r['config']['networkPolicy']['gateway']
assert p['namespace'] == 'edge' and p['podSelector'] == {'app': 'edge'} and p['port'] == 'web'
assert 'app.kubernetes.io/name: ingress-nginx' not in r['manifest']
assert 'BKN_SANDBOX_MCP_URL: "http://agent-retrieval:30780/' in r['manifest']
PY
# An explicit current or saved override needs no cluster-wide Service discovery.
rm "${test_dir}/discovery-calls"
services=unavailable
_openbkn_helm_upgrade_release sandbox openbkn "${args[@]}" --set controlPlane.env.BKN_BASE_URL=http://explicit:80
[[ ! -f "${test_dir}/discovery-calls" ]]
installed=true
old_values='{"controlPlane":{"env":{"BKN_BASE_URL":"http://saved:80"}}}'
_openbkn_helm_upgrade_release sandbox openbkn "${args[@]}"
python3 - "${test_dir}/render.json" <<'PY'
import json, sys
assert 'BKN_BASE_URL: "http://saved:80"' in json.load(open(sys.argv[1]))['manifest']
PY
_openbkn_helm_upgrade_release sandbox openbkn "${args[@]}" --set controlPlane.env.BKN_BASE_URL=http://current:80
python3 - "${test_dir}/render.json" <<'PY'
import json, sys
assert 'BKN_BASE_URL: "http://current:80"' in json.load(open(sys.argv[1]))['manifest']
PY
[[ ! -f "${test_dir}/discovery-calls" ]]
# A rendered system fallback is lower priority than the explicit origin.
_openbkn_helm_upgrade_release sandbox openbkn "${args[@]}" --set controlPlane.gateway.baseURL=http://discovered:80
python3 - "${test_dir}/render.json" <<'PY'
import json, sys
assert 'BKN_BASE_URL: "http://saved:80"' in json.load(open(sys.argv[1]))['manifest']
PY
# No gateway, multiple gateways and discovery failure must stop before upgrade;
# the default MCP URL must never silently become the REST base.
old_values='{}'
for services in unavailable '{"items":[]}' '{"items":[{"metadata":{"name":"a","namespace":"edge"},"spec":{"selector":{"app":"edge"},"ports":[{"name":"http","port":80}]}},{"metadata":{"name":"b","namespace":"edge"},"spec":{"selector":{"app":"edge"},"ports":[{"name":"http","port":80}]}}]}'; do
    if _openbkn_helm_upgrade_release sandbox openbkn "${args[@]}" 2>/dev/null; then
        echo 'missing or ambiguous gateway must fail before rollout' >&2
        exit 1
    fi
done
for bad_url in 'http://gateway/api/agent-retrieval/v1/mcp/' 'http://user:password@gateway' 'ftp://gateway' 'http://gateway:0'; do
    if _openbkn_helm_upgrade_release sandbox openbkn "${args[@]}" --set "controlPlane.env.BKN_BASE_URL=${bad_url}" 2>/dev/null; then
        echo 'invalid REST origin must fail before rollout' >&2
        exit 1
    fi
done
[[ "$(wc -l <"${test_dir}/upgrades")" -eq 5 ]]
# Both charts render separate REST and MCP rules; a REST gateway never changes
# the namespace selected for a cross-namespace MCP Service.
for chart_name in sandbox sandbox_standalone; do
    command helm template sandbox "${SCRIPT_DIR}/../infra/sandbox/deploy/helm/${chart_name}" \
        --set controlPlane.env.BKN_BASE_URL=http://gateway.edge.svc:80 \
        --set controlPlane.env.BKN_SANDBOX_MCP_URL=http://agent-retrieval.knowledge.svc:30780/api/agent-retrieval/v1/mcp/ \
        --set networkPolicy.bkn.enabled=true --set networkPolicy.gateway.enabled=true \
        --set networkPolicy.gateway.namespace=edge --set networkPolicy.gateway.podSelector.app=edge >"${test_dir}/chart.yaml"
    grep -q 'kubernetes.io/metadata.name: "knowledge"' "${test_dir}/chart.yaml"
    grep -q 'kubernetes.io/metadata.name: "edge"' "${test_dir}/chart.yaml"
    grep -q 'port: 30780' "${test_dir}/chart.yaml"
    grep -q 'port: http' "${test_dir}/chart.yaml"
    if command helm template sandbox "${SCRIPT_DIR}/../infra/sandbox/deploy/helm/${chart_name}" \
        --set networkPolicy.bkn.enabled=true \
        --set controlPlane.env.BKN_SANDBOX_MCP_URL=http://agent-retrieval:30779/api/agent-retrieval/v1/mcp/ >/dev/null 2>&1; then
        echo 'trusted Retrieval listener must remain blocked' >&2
        exit 1
    fi
done
echo 'Sandbox gateway discovery, override, upgrade and separate egress checks passed'
