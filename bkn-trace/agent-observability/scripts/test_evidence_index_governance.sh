#!/usr/bin/env bash
set -euo pipefail

chart_dir="${1:-charts/agent-observability}"
render_chart() {
  helm template --kube-version 1.23.0 "$@"
}
if ! grep -Fq 'kubeVersion: ">=1.23.0-0"' "${chart_dir}/Chart.yaml"; then
  echo "chart must declare the Kubernetes version required by namespace label selectors" >&2
  exit 1
fi
default_rendered="$(render_chart agent-observability "${chart_dir}")"
if grep -Eq "BKN_TRACE_(EVIDENCE_INGEST|QUERY_GATEWAY)_TOKEN" <<<"${default_rendered}"; then
  echo "standalone chart must not inject a token without an existing Secret" >&2
  exit 1
fi
if grep -Fq 'kind: Secret' <<<"${default_rendered}"; then
  echo "standalone chart must not create a Secret by default" >&2
  exit 1
fi
if grep -Fq "BKN_TRACE_CORE_MARIADB_DSN" <<<"${default_rendered}"; then
  echo "default chart must not require the MariaDB Core secret" >&2
  exit 1
fi

if ! grep -Fq 'name: agent-observability-internal' <<<"${default_rendered}" ||
   ! grep -Fq 'port: internal-http' <<<"${default_rendered}"; then
  echo "chart must expose a private lifecycle Service" >&2
  exit 1
fi
long_name="rrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrr"
long_rendered="$(render_chart "${long_name}" "${chart_dir}")"
internal_name="$(awk '/^---$/{service=0; name=""} /^kind: Service$/{service=1} service && /^  name: /{name=$2} service && /targetPort: internal-http/{print name; exit}' <<<"${long_rendered}")"
if [[ -z "${internal_name}" ]] || (( ${#internal_name} > 63 )); then
  echo "private lifecycle Service name must fit the DNS label limit: ${internal_name}" >&2
  exit 1
fi
if ! grep -Fq 'kind: NetworkPolicy' <<<"${default_rendered}" ||
   ! grep -Fq 'app.kubernetes.io/name: agent-retrieval' <<<"${default_rendered}" ||
   ! grep -Fq 'app: agent-retrieval' <<<"${default_rendered}"; then
  echo "chart must restrict the private lifecycle port to agent-retrieval" >&2
  exit 1
fi
render_error="$(mktemp)"
trap 'rm -f "${render_error}"' EXIT
if render_chart agent-observability "${chart_dir}" \
  --set-json 'networkPolicy.allowedClients=[]' >/dev/null 2>"${render_error}"; then
  echo "enabled private lifecycle policy must fail closed without allowed clients" >&2
  exit 1
fi
if ! grep -Fq 'networkPolicy.allowedClients must contain at least one private lifecycle client' "${render_error}"; then
  echo "empty private lifecycle clients must fail for the intended reason" >&2
  exit 1
fi
if render_chart agent-observability "${chart_dir}" \
  --set-json 'networkPolicy.allowedClients=[{}]' >/dev/null 2>"${render_error}"; then
  echo "private lifecycle clients without pod labels must fail closed" >&2
  exit 1
fi
if ! grep -Fq 'networkPolicy.allowedClients[].podLabels is required' "${render_error}"; then
  echo "missing private lifecycle client labels must fail for the intended reason" >&2
  exit 1
fi

ingest_rendered="$(render_chart agent-observability "${chart_dir}" \
  --set evidence.ingestAuth.existingSecret=bkn-trace-evidence-ingest)"
if ! grep -Fq 'name: BKN_TRACE_EVIDENCE_INGEST_TOKEN' <<<"${ingest_rendered}" ||
   grep -Fq 'name: BKN_TRACE_QUERY_GATEWAY_TOKEN' <<<"${ingest_rendered}"; then
  echo "configured evidence ingest must use its token without restoring the lifecycle gateway token" >&2
  exit 1
fi
managed_ingest_rendered="$(render_chart agent-observability "${chart_dir}" \
  --set evidence.ingestAuth.existingSecret=bkn-trace-evidence-ingest \
  --set evidence.ingestAuth.createSecret=true)"
if ! grep -A12 -F 'kind: Secret' <<<"${managed_ingest_rendered}" | grep -Fq '"helm.sh/resource-policy": keep' ||
   ! grep -A20 -F 'kind: Secret' <<<"${managed_ingest_rendered}" | grep -Fq 'token:'; then
  echo "installer-managed ingest Secret must preserve the token key and keep policy" >&2
  exit 1
fi
if ! grep -A1 -Fq "name: BKN_TRACE_PROJECTION_ENABLED
              value: \"false\"" <<<"${default_rendered}"; then
  echo "Core projection must be disabled by default" >&2
  exit 1
fi
if grep -Fq "agent-observability-evidence-index-template" <<<"${default_rendered}"; then
  echo "evidence index template must not render by default" >&2
  exit 1
fi
if grep -Fq "agent-observability-evidence-index-setup" <<<"${default_rendered}"; then
  echo "evidence index setup job must not render by default" >&2
  exit 1
fi
if ! grep -A1 -Fq 'name: BKN_OBSERVABILITY_SOURCE_TIMEOUT
              value: "3s"' <<<"${default_rendered}"; then
  echo "observability source timeout must be rendered" >&2
  exit 1
fi
if ! grep -A1 -Fq 'name: BKN_OBSERVABILITY_MAX_CONCURRENT_SOURCES
              value: "4"' <<<"${default_rendered}"; then
  echo "observability source concurrency must be rendered" >&2
  exit 1
fi
if ! render_chart agent-observability "${chart_dir}" --set-json 'observability.archive=null' >/dev/null; then
  echo "existing releases without observability.archive must remain upgradeable" >&2
  exit 1
fi
if ! grep -A1 -Fq 'name: BKN_TRACE_DEPLOYMENT_TENANT_ID
              value: "openbkn-local"' <<<"${default_rendered}"; then
  echo "single-tenant deployments must inject the trusted deployment tenant" >&2
  exit 1
fi

legacy_rendered="$(render_chart agent-observability "${chart_dir}" \
  --set evidence.store=opensearch \
  --set evidence.index=bkn-trace-evidence-test \
  --set evidence.indexManagement.enabled=true \
  --set evidence.indexManagement.createJob.enabled=true)"

if grep -Eq 'agent-observability-evidence-index-(template|setup)|curlimages/curl|kind: Job' <<<"${legacy_rendered}"; then
  echo "legacy evidence index settings must not restore the duplicate Helm Hook" >&2
  exit 1
fi
