#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
collector_chart="$root_dir/otelcol-contribute-chart/charts/otelcol-contrib"
agent_chart="$root_dir/agent-observability/charts/agent-observability"
collector_rendered="$(mktemp)"
agent_rendered="$(mktemp)"
trap 'rm -f "$collector_rendered" "$agent_rendered"' EXIT

helm template trace-admission "$collector_chart" --set traceAdmission.clientID=trace-gateway --set traceAdmission.clientSecretSecret=trace-gateway-oauth --set traceAdmission.currentKeyID=trace-capture-policy-2026 --set traceAdmission.currentPublicKeySecret=trace-policy-public --set traceAdmission.workloadIdentity=spiffe://cluster-a/ns/openbkn/sa/otelcol --set traceAdmission.audienceClusterID=cluster-a >"$collector_rendered"

helm template agent-observability "$agent_chart" --set core.capturePolicySigning.existingSecret=trace-policy-signing --set core.capturePolicySigning.keyID=trace-capture-policy-2026 --set core.capturePolicySigning.audience=cluster-a >"$agent_rendered"

python3 - "$collector_rendered" "$agent_rendered" <<'PY'
import sys
import yaml

collector = [doc for doc in yaml.safe_load_all(open(sys.argv[1], encoding="utf-8")) if doc and doc.get("kind") == "Deployment"][0]
agent = [doc for doc in yaml.safe_load_all(open(sys.argv[2], encoding="utf-8")) if doc and doc.get("kind") == "Deployment"][0]

def env(deployment):
    return {item["name"]: item.get("value") for item in deployment["spec"]["template"]["spec"]["containers"][0]["env"] if "value" in item}

collector_env = env(collector)
agent_env = env(agent)
assert collector_env["TRACE_ADMISSION_AUDIENCE"] == agent_env["BKN_TRACE_CAPTURE_POLICY_AUDIENCE"]
assert collector_env["TRACE_ADMISSION_CURRENT_KEY_ID"] == agent_env["BKN_TRACE_CAPTURE_POLICY_SIGNING_KEY_ID"]
collector_names = {item["name"] for item in collector["spec"]["template"]["spec"]["containers"][0]["env"]}
agent_names = {item["name"] for item in agent["spec"]["template"]["spec"]["containers"][0]["env"]}
assert "TRACE_ADMISSION_CURRENT_PUBLIC_KEY" in collector_names, "collector public key must be Secret-backed"
assert "BKN_TRACE_CAPTURE_POLICY_SIGNING_KEY" in agent_names, "signer private key must be Secret-backed"
PY

echo "trace admission identity wiring: PASS"
