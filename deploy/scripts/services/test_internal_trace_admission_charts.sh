#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/../../.." && pwd)"
check() {
 local chart="$1" prefix="$2" userkey="$3" passkey="$4"
 local rendered
 rendered=$(helm template test "$root/$chart" --set "$prefix.enabled=true" --set "$prefix.brokers=kafka:9092" --set "$prefix.$userkey=kafka-auth" --set "$prefix.$passkey=kafka-auth")
 [[ "$rendered" == *"TRACE_ADMISSION_POLICY_URL"* ]] || exit 1
 [[ "$rendered" != *"TRACE_ADMISSION_TOKEN_URL"* ]] || exit 1
 [[ "$rendered" != *"TRACE_ADMISSION_CURRENT_PUBLIC_KEY"* ]] || exit 1
 [[ "$rendered" == *"internal/trace-evidence/configuration"* ]] || exit 1
}
check adp/bkn/bkn-backend/helm/bkn-backend bknTrace.evidencePublisher usernameSecretName passwordSecretName
check adp/bkn/ontology-query/helm/ontology-query bknTrace.evidencePublisher usernameSecretName passwordSecretName
check adp/context-loader/agent-retrieval/helm/agent-retrieval observability.evidencePublisher usernameSecretName passwordSecretName
check adp/execution-factory/operator-integration/helm/agent-operator-integration observability.evidence.publisher credentials_secret_name credentials_secret_name
echo 'internal Trace Admission publisher charts: PASS'
rendered=$(helm template test "$root/bkn-trace/agent-observability/charts/agent-observability" --namespace openbkn --show-only templates/network-policy.yaml)
[[ "$rendered" == *"module: bkn-backend"* ]] || exit 1
[[ "$rendered" == *"module: ontology-query"* ]] || exit 1
[[ "$rendered" == *"app.kubernetes.io/name: otelcol-contrib"* ]] || exit 1
echo 'internal control network allowlist: PASS'
