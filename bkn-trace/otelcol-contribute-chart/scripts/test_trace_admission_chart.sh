#!/usr/bin/env bash
set -euo pipefail

chart_dir="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../charts/otelcol-contrib" && pwd)}"
rendered="$(mktemp)"
trap 'rm -f "$rendered"' EXIT

helm template trace-admission "$chart_dir" \
  --set traceAdmission.clientID=trace-gateway \
  --set traceAdmission.clientSecretSecret=trace-gateway-oauth \
  --set traceAdmission.currentKeyID=trace-policy-2026q3 \
  --set traceAdmission.currentPublicKeySecret=trace-policy-public \
  --set traceAdmission.workloadIdentity=spiffe://cluster-a/ns/openbkn/sa/otelcol \
  >"$rendered"

grep -q 'traceadmission' "$rendered"
grep -q 'TRACE_ADMISSION_CLIENT_SECRET' "$rendered"
grep -q 'agent-observability-internal:8081/api/agent-observability/v1/internal/trace-evidence/policy' "$rendered"
grep -q 'agent-observability-internal:8081/api/agent-observability/v1/internal/trace-evidence/endpoints:heartbeat' "$rendered"
grep -q 'agent-observability-internal:8081/api/agent-observability/v1/internal/trace-evidence/operations' "$rendered"
grep -q 'agent-observability:8080/api/agent-observability/v1/trace-evidence-configuration' "$rendered"

python3 - "$rendered" <<'PY'
import sys
import yaml

documents = list(yaml.safe_load_all(open(sys.argv[1], encoding="utf-8")))
configmaps = [doc for doc in documents if doc and doc.get("kind") == "ConfigMap"]
assert configmaps, "collector ConfigMap was not rendered"
config = yaml.safe_load(configmaps[0]["data"]["collector-config.yaml"])
assert "traceadmission" in config["service"]["pipelines"]["traces"]["processors"]
assert "traceadmission" not in config["service"]["pipelines"]["logs"]["processors"]
secret = "trace-gateway-oauth"
assert secret not in configmaps[0]["data"]["collector-config.yaml"], "Secret name leaked into ConfigMap"
PY

disabled_error="$(mktemp)"
trap 'rm -f "$rendered" "$disabled_error"' EXIT
if helm template trace-admission "$chart_dir" \
  --set traceAdmission.enabled=false \
  --set traceAdmission.clientID=trace-gateway \
  --set traceAdmission.clientSecretSecret=trace-gateway-oauth \
  --set traceAdmission.currentKeyID=trace-policy-2026q3 \
  --set traceAdmission.currentPublicKeySecret=trace-policy-public \
  --set traceAdmission.workloadIdentity=spiffe://cluster-a/ns/openbkn/sa/otelcol \
  > /dev/null 2>"$disabled_error"; then
  echo "traceAdmission.enabled=false must be rejected" >&2
  exit 1
fi
grep -Fq 'traceAdmission.enabled=false is unsupported' "$disabled_error"

echo "trace admission chart wiring: PASS"
