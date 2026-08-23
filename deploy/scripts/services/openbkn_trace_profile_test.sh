#!/usr/bin/env bash
# 完整 OpenBKN 安装必须启用可持久查询的 BKN Trace；轻量 Chart 默认值不能泄漏到产品安装。
set -uo pipefail

FAILED=0
PASS=0

fail() { echo "FAIL: $*"; FAILED=1; }
ok() { PASS=$((PASS + 1)); }
contains() {
    local label="$1" value="$2" expected="$3"
    if [[ "${value}" == *"${expected}"* ]]; then ok; else fail "${label}: missing [${expected}] in [${value}]"; fi
}
not_contains() {
    local label="$1" value="$2" unexpected="$3"
    if [[ "${value}" != *"${unexpected}"* ]]; then ok; else fail "${label}: unexpected [${unexpected}] in [${value}]"; fi
}
log_info() { :; }
log_warn() { :; }
get_set_value() {
    local key="$1"
    shift
    local item
    for item in "$@"; do
        if [[ "${item}" == "${key}="* ]]; then
            printf '%s\n' "${item#*=}"
            return 0
        fi
    done
    return 1
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CONFIG_REGISTRY_FILE="$(mktemp)"
trap 'rm -f "${CONFIG_REGISTRY_FILE}"' EXIT
# shellcheck source=../services/openbkn.sh
source "${SCRIPT_DIR}/scripts/services/openbkn.sh"

# The production installer loads common.sh before this module. Keep this unit
# test cluster-free while exercising the same config contract.
config_yaml_dep_field() {
    local section="$1" field="$2"
    awk -v sec="${section}:" -v fld="${field}:" '
        !in_block && $1 == sec && substr($0, 1, 2) == "  " {in_block=1; next}
        in_block && NF && $0 !~ /^    / {exit}
        in_block && $1 == fld {gsub(/'"'"'|"/, "", $2); print $2; exit}
    ' "${CONFIG_REGISTRY_FILE}" 2>/dev/null
}

# The profile builder is deliberately pure: install tests can validate the product
# contract without a cluster, credentials, or a Helm chart archive.
CORE_RELEASE_EXTRA_SETS=()
_openbkn_trace_profile_sets agent-observability
ao_sets="${CORE_RELEASE_EXTRA_SETS[*]:-}"
contains "AO uses durable Core" "${ao_sets}" "core.store=mariadb"
contains "AO requires Core DSN Secret" "${ao_sets}" "core.mariadb.existingSecret=bkn-trace-core-mariadb"
contains "AO persists evidence" "${ao_sets}" "evidence.store=opensearch"
contains "AO enables projection" "${ao_sets}" "core.projection.enabled=true"
contains "AO creates the Collector timestamp repair pipeline first" "${ao_sets}" "opensearch.traceTimestampPipeline=bkn-trace-span-timestamp-v1"
not_contains "AO leaves index initialization to runtime" "${ao_sets}" "evidence.indexManagement"
contains "AO protects evidence producer ingest" "${ao_sets}" "evidence.ingestAuth.existingSecret=bkn-trace-evidence-ingest"
not_contains "AO has no query gateway Secret" "${ao_sets}" "queryAuth.existingSecret="

# The installer must not silently retain Chart defaults when the chart version
# is unchanged. That is precisely when a previously direct-installed release
# would otherwise keep its volatile Core store through a later normal install.
should_skip_upgrade_same_chart_version() { return 0; }
HELM_VALUES=''
HELM_GET_VALUES_EXIT=0
helm() {
    if [[ "$*" == "get values agent-observability -n openbkn --all -o json" || "$*" == "get values otelcol-contrib -n openbkn --all -o json" ]]; then
        if [[ "${HELM_GET_VALUES_EXIT}" -ne 0 ]]; then
            return "${HELM_GET_VALUES_EXIT}"
        fi
        printf '%s\n' "${HELM_VALUES}"
        return 0
    fi
    return 0
}
HELM_VALUES='{"core":{"store":"memory","projection":{"enabled":false}},"evidence":{"store":"memory"}}'
if ! declare -F _openbkn_should_skip_upgrade >/dev/null; then
    fail "installer must expose a runtime-profile reconciliation guard"
elif _openbkn_should_skip_upgrade agent-observability openbkn agent-observability 0.1.4; then
    fail "installer must reconcile a volatile agent-observability runtime profile"
else
    ok
fi

HELM_VALUES='{"core":{"store":"mariadb","projection":{"enabled":true}},"evidence":{"store":"opensearch","ingestAuth":{"existingSecret":""}}}'
if _openbkn_should_skip_upgrade agent-observability openbkn agent-observability 0.1.4; then
    fail "installer must reconcile a profile without the evidence ingest Secret"
else
    ok
fi

HELM_VALUES='{"core":{"store":"mariadb","projection":{"enabled":true}},"evidence":{"store":"opensearch","ingestAuth":{"existingSecret":"bkn-trace-evidence-ingest"}}}'
if _openbkn_should_skip_upgrade agent-observability openbkn agent-observability 0.1.4; then
    fail "installer must reconcile a durable profile that lacks the timestamp pipeline"
else
    ok
fi

HELM_VALUES='{"core":{"store":"mariadb","projection":{"enabled":true}},"evidence":{"store":"opensearch","ingestAuth":{"existingSecret":"bkn-trace-evidence-ingest"}},"opensearch":{"traceTimestampPipeline":"bkn-trace-span-timestamp-v1"}}'
if _openbkn_should_skip_upgrade agent-observability openbkn agent-observability 0.1.4; then
    ok
else
    fail "installer should retain the version-skip optimization for a fully reconciled runtime profile"
fi

CORE_SET_VALUES=("core.store=memory")
HELM_VALUES='{"core":{"store":"memory","projection":{"enabled":false}},"evidence":{"store":"memory"}}'
if _openbkn_should_skip_upgrade agent-observability openbkn agent-observability 0.1.4; then
    ok
else
    fail "an explicit volatile override must retain the normal version-skip decision"
fi
CORE_SET_VALUES=("opensearch.traceTimestampPipeline=custom-trace-pipeline")
HELM_VALUES='{"core":{"store":"memory","projection":{"enabled":false}},"evidence":{"store":"memory"},"opensearch":{"traceTimestampPipeline":"custom-trace-pipeline"}}'
if _openbkn_should_skip_upgrade agent-observability openbkn agent-observability 0.1.4; then
    fail "a custom timestamp pipeline must not bypass durable Core profile reconciliation"
else
    ok
fi
CORE_SET_VALUES=("")

HELM_VALUES='{not-json}'
if _openbkn_should_skip_upgrade agent-observability openbkn agent-observability 0.1.4; then
    ok
else
    fail "invalid Helm values must preserve the version-skip decision"
fi
HELM_GET_VALUES_EXIT=1
if _openbkn_should_skip_upgrade agent-observability openbkn agent-observability 0.1.4; then
    ok
else
    fail "a failed Helm values read must preserve the version-skip decision"
fi
HELM_GET_VALUES_EXIT=0

CORE_SET_VALUES=()
HELM_VALUES='{"opensearchExporter":{"pipeline":""}}'
if _openbkn_should_skip_upgrade otelcol-contrib openbkn otelcol-contrib 0.1.4; then
    fail "installer must reconcile a Collector without the timestamp repair pipeline"
else
    ok
fi

HELM_VALUES='{"opensearchExporter":{"pipeline":"bkn-trace-span-timestamp-v1"}}'
if _openbkn_should_skip_upgrade otelcol-contrib openbkn otelcol-contrib 0.1.4; then
    ok
else
    fail "installer should retain the version-skip optimization for a reconciled Collector"
fi

HELM_VALUES='{not-json}'
if _openbkn_should_skip_upgrade otelcol-contrib openbkn otelcol-contrib 0.1.4; then
    ok
else
    fail "invalid Collector Helm values must preserve the version-skip decision"
fi

CORE_SET_VALUES=("core.store=memory" "core.store=mariadb")
HELM_VALUES='{"core":{"store":"memory","projection":{"enabled":false}},"evidence":{"store":"memory"}}'
if _openbkn_should_skip_upgrade agent-observability openbkn agent-observability 0.1.4; then
    fail "the last explicit --set value must decide whether a volatile profile is intentional"
else
    ok
fi
CORE_SET_VALUES=("")

# Both installer paths must use the reconciliation guard. Keep this test
# cluster-free by stubbing only the final Helm upgrade operation.
UPGRADE_CALLS=0
CONFIG_YAML_PATH="${CONFIG_REGISTRY_FILE}"
ORIGINAL_RELEASE_EXTRA_SETS="$(declare -f _openbkn_release_extra_sets)"
_openbkn_helm_upgrade_release() { UPGRADE_CALLS=$((UPGRADE_CALLS + 1)); }
_openbkn_release_extra_sets() { CORE_RELEASE_EXTRA_SETS=("test.profile=true"); CORE_RELEASE_EXTRA_SET_STRINGS=(""); }
_openbkn_resolve_release_version() { printf '%s' '0.1.4'; }
_openbkn_resolve_chart_name() { printf '%s' "$1"; }
build_chart_ref() { printf '%s' "$1/$2"; }
find_cached_chart_tgz_by_version() { printf '%s' '/tmp/agent-observability-0.1.4.tgz'; }
HELM_VALUES='{"core":{"store":"memory","projection":{"enabled":false}},"evidence":{"store":"memory"}}'
_install_openbkn_release_local agent-observability /tmp openbkn
if [[ "${UPGRADE_CALLS}" -eq 1 ]]; then
    ok
else
    fail "local installer must upgrade a volatile runtime profile"
fi

_install_openbkn_release_repo agent-observability openbkn openbkn 0.1.4
if [[ "${UPGRADE_CALLS}" -eq 2 ]]; then
    ok
else
    fail "repository installer must upgrade a volatile runtime profile"
fi

HELM_VALUES='{"core":{"store":"mariadb","projection":{"enabled":true}},"evidence":{"store":"opensearch","ingestAuth":{"existingSecret":"bkn-trace-evidence-ingest"}},"opensearch":{"traceTimestampPipeline":"bkn-trace-span-timestamp-v1"}}'
_install_openbkn_release_local agent-observability /tmp openbkn
_install_openbkn_release_repo agent-observability openbkn openbkn 0.1.4
if [[ "${UPGRADE_CALLS}" -eq 2 ]]; then
    ok
else
    fail "durable runtime profiles must skip both installer paths"
fi

HELM_VALUES='{not-json}'
_install_openbkn_release_local agent-observability /tmp openbkn
_install_openbkn_release_repo agent-observability openbkn openbkn 0.1.4
if [[ "${UPGRADE_CALLS}" -eq 2 ]]; then
    ok
else
    fail "unreadable Helm values must not roll out either installer path"
fi
eval "${ORIGINAL_RELEASE_EXTRA_SETS}"

cat >"${CONFIG_REGISTRY_FILE}" <<'EOF'
depServices:
  opensearch:
    host: opensearch-secure.resource.svc.cluster.local
    port: 9200
    protocol: https
EOF
CORE_RELEASE_EXTRA_SETS=()
_openbkn_trace_profile_sets agent-observability
secure_ao_sets="${CORE_RELEASE_EXTRA_SETS[*]:-}"
contains "secure AO uses the OpenSearch endpoint from config" "${secure_ao_sets}" "opensearch.endpoint=https://opensearch-secure.resource.svc.cluster.local:9200"
contains "secure AO enables OpenSearch auth" "${secure_ao_sets}" "opensearch.auth.enabled=true"
contains "secure AO references the shared OpenSearch Secret" "${secure_ao_sets}" "opensearch.auth.existingSecret=bkn-trace-opensearch"
not_contains "secure AO never forwards an OpenSearch password to Helm" "${secure_ao_sets}" "password="

CORE_RELEASE_EXTRA_SETS=()
_openbkn_trace_profile_sets otelcol-contrib
secure_collector_sets="${CORE_RELEASE_EXTRA_SETS[*]:-}"
contains "Collector routes spans through the timestamp repair pipeline" "${secure_collector_sets}" "opensearchExporter.pipeline=bkn-trace-span-timestamp-v1"
contains "secure Collector uses the OpenSearch endpoint from config" "${secure_collector_sets}" "opensearchExporter.http.endpoint=https://opensearch-secure.resource.svc.cluster.local:9200"
contains "secure Collector verifies the OpenSearch TLS peer" "${secure_collector_sets}" "opensearchExporter.http.tls.insecure=false"
contains "secure Collector enables OpenSearch auth" "${secure_collector_sets}" "opensearchExporter.auth.enabled=true"
contains "secure Collector references the shared OpenSearch Secret" "${secure_collector_sets}" "opensearchExporter.auth.existingSecret=bkn-trace-opensearch"
not_contains "secure Collector never forwards an OpenSearch password to Helm" "${secure_collector_sets}" "password="

# The installer must apply the Collector profile, not merely expose a helper
# that unit tests can call directly.
CORE_RELEASE_EXTRA_SETS=()
CORE_RELEASE_EXTRA_SET_STRINGS=()
_openbkn_release_extra_sets otelcol-contrib openbkn
installed_collector_sets="${CORE_RELEASE_EXTRA_SETS[*]:-}"
contains "installer applies secure Collector endpoint" "${installed_collector_sets}" "opensearchExporter.http.endpoint=https://opensearch-secure.resource.svc.cluster.local:9200"
contains "installer applies secure Collector auth Secret" "${installed_collector_sets}" "opensearchExporter.auth.existingSecret=bkn-trace-opensearch"

otel_values="$(<"${SCRIPT_DIR}/../bkn-trace/otelcol-contribute-chart/charts/otelcol-contrib/values.yaml")"
contains "collector uses the Trace profile dataset" "${otel_values}" "dataset: default"
contains "collector uses the Trace profile namespace" "${otel_values}" "namespace: namespace"
contains "collector uses SS4O index names" "${otel_values}" "mode: ss4o"
collector_dataset="$(awk '/^  dataset:/ { print $2; exit }' <<<"${otel_values}")"
collector_namespace="$(awk '/^  namespace:/ { print $2; exit }' <<<"${otel_values}")"
expected_trace_index="ss4o_traces-${collector_dataset}-${collector_namespace}"
expected_log_index="ss4o_logs-${collector_dataset}-${collector_namespace}"
contains "AO profile reads the Collector Trace index" "${ao_sets}" "opensearch.traceIndex=${expected_trace_index}"
contains "AO profile reads the Collector log index" "${ao_sets}" "opensearch.logIndex=${expected_log_index}"

agent_observability_values="$(<"${SCRIPT_DIR}/../bkn-trace/agent-observability/charts/agent-observability/values.yaml")"
contains "standalone AO reads the Collector Trace index" "${agent_observability_values}" "traceIndex: ${expected_trace_index}"
contains "standalone AO reads the Collector log index" "${agent_observability_values}" "logIndex: ${expected_log_index}"

CORE_RELEASE_EXTRA_SETS=()
_openbkn_trace_profile_sets agent-retrieval
ar_sets="${CORE_RELEASE_EXTRA_SETS[*]:-}"
contains "retrieval targets internal Trace Core" "${ar_sets}" "observability.lifecycle.core_url=http://agent-observability-internal:8081"
contains "retrieval emits Trace spans" "${ar_sets}" "observability.trace.enabled=true"
contains "retrieval emits searchable runtime logs" "${ar_sets}" "observability.log.enabled=true"
contains "retrieval emits evidence through token-protected ingest" "${ar_sets}" "observability.evidence.ingest_url=http://agent-observability:8080/api/agent-observability/v1/evidence/events"
contains "retrieval uses evidence ingest Secret" "${ar_sets}" "observability.evidence.ingest_token_secret_name=bkn-trace-evidence-ingest"
not_contains "retrieval has no query gateway Secret" "${ar_sets}" "gateway_token_secret_name="

# vega-backend is an Evidence producer on an older chart generation: same three
# facts, different keys. Its chart defaults ingestTokenSecretName to empty and
# the template only injects the token env when that name is set, so leaving it
# unwired means Evidence posted with no token — which the receiver rejects.
# Through _openbkn_release_extra_sets, not _openbkn_trace_profile_sets: the
# outer function gates which releases reach the inner case at all, so testing
# the inner one directly passes while the release stays unwired in a real
# install. That is exactly how vega-backend was missed the first time.
CORE_RELEASE_EXTRA_SETS=()
_openbkn_release_extra_sets vega-backend openbkn
vega_sets="${CORE_RELEASE_EXTRA_SETS[*]:-}"
contains "vega posts evidence to the ingest route" "${vega_sets}" "bknTrace.evidence.ingestUrl=http://agent-observability:8080/api/agent-observability/v1/evidence/events"
contains "vega posts artifacts to the artifact route" "${vega_sets}" "bknTrace.evidence.artifactIngestUrl=http://agent-observability:8080/api/agent-observability/v1/evidence/artifacts"
contains "vega uses the evidence ingest Secret" "${vega_sets}" "bknTrace.evidence.ingestTokenSecretName=bkn-trace-evidence-ingest"
# The chart still defaults this key to the pre-rename name; the receiver reads
# "token", so the installer has to override it or the two never meet.
contains "vega reads the token key the receiver writes" "${vega_sets}" "bknTrace.evidence.ingestTokenSecretKey=token"
not_contains "vega does not keep the pre-rename key" "${vega_sets}" "ingestTokenSecretKey=ingest-token"

# A full OpenBKN installation must wire every declared producer through the
# real installer entrypoint. bkn-backend and ontology-query are durable
# producers, so the shared trusted-delivery Secret and both outbox switches are
# required in addition to the ingest route. The remaining two post directly.
CORE_RELEASE_EXTRA_SETS=()
_openbkn_release_extra_sets bkn-backend openbkn
bkn_backend_sets="${CORE_RELEASE_EXTRA_SETS[*]:-}"
contains "bkn-backend posts evidence to the ingest route" "${bkn_backend_sets}" "bknTrace.evidence.ingestUrl=http://agent-observability:8080/api/agent-observability/v1/evidence/events"
contains "bkn-backend uses the evidence ingest Secret" "${bkn_backend_sets}" "bknTrace.evidence.ingestTokenSecretName=bkn-trace-evidence-ingest"
contains "bkn-backend reads the token key the receiver writes" "${bkn_backend_sets}" "bknTrace.evidence.ingestTokenSecretKey=token"
contains "bkn-backend enables its durable outbox" "${bkn_backend_sets}" "bknTrace.producerOutbox.enabled=true"
contains "bkn-backend starts its durable outbox worker" "${bkn_backend_sets}" "bknTrace.producerOutbox.workerEnabled=true"
contains "bkn-backend enables delivered outbox cleanup" "${bkn_backend_sets}" "bknTrace.producerOutbox.cleanup.enabled=true"
contains "bkn-backend uses the trusted delivery Secret" "${bkn_backend_sets}" "bknTrace.producerOutbox.queryGatewayTokenSecretName=bkn-trace-evidence-ingest"
contains "bkn-backend reads the trusted delivery token key" "${bkn_backend_sets}" "bknTrace.producerOutbox.queryGatewayTokenSecretKey=token"

CORE_RELEASE_EXTRA_SETS=()
_openbkn_release_extra_sets ontology-query openbkn
ontology_query_sets="${CORE_RELEASE_EXTRA_SETS[*]:-}"
contains "ontology-query posts evidence to the ingest route" "${ontology_query_sets}" "bknTrace.evidence.ingestUrl=http://agent-observability:8080/api/agent-observability/v1/evidence/events"
contains "ontology-query uses the evidence ingest Secret" "${ontology_query_sets}" "bknTrace.evidence.ingestTokenSecretName=bkn-trace-evidence-ingest"
contains "ontology-query reads the token key the receiver writes" "${ontology_query_sets}" "bknTrace.evidence.ingestTokenSecretKey=token"
contains "ontology-query enables its durable outbox" "${ontology_query_sets}" "bknTrace.producerOutbox.enabled=true"
contains "ontology-query starts its durable outbox worker" "${ontology_query_sets}" "bknTrace.producerOutbox.workerEnabled=true"
contains "ontology-query enables delivered outbox cleanup" "${ontology_query_sets}" "bknTrace.producerOutbox.cleanup.enabled=true"
contains "ontology-query uses the trusted delivery Secret" "${ontology_query_sets}" "bknTrace.producerOutbox.queryGatewayTokenSecretName=bkn-trace-evidence-ingest"
contains "ontology-query reads the trusted delivery token key" "${ontology_query_sets}" "bknTrace.producerOutbox.queryGatewayTokenSecretKey=token"

CORE_RELEASE_EXTRA_SETS=()
_openbkn_release_extra_sets agent-operator-integration openbkn
operator_sets="${CORE_RELEASE_EXTRA_SETS[*]:-}"
contains "operator integration posts evidence to the ingest route" "${operator_sets}" "observability.evidence.ingest_url=http://agent-observability:8080/api/agent-observability/v1/evidence/events"
contains "operator integration uses the evidence ingest Secret" "${operator_sets}" "observability.evidence.ingest_token_secret_name=bkn-trace-evidence-ingest"
contains "operator integration reads the token key the receiver writes" "${operator_sets}" "observability.evidence.ingest_token_secret_key=token"

CORE_RELEASE_EXTRA_SETS=()
_openbkn_release_extra_sets bkn-agent openbkn
bkn_agent_sets="${CORE_RELEASE_EXTRA_SETS[*]:-}"
contains "bkn-agent posts evidence to the ingest route" "${bkn_agent_sets}" "observability.bknTraceEvidenceIngestUrl=http://agent-observability:8080/api/agent-observability/v1/evidence/events"
contains "bkn-agent posts artifacts to the artifact route" "${bkn_agent_sets}" "observability.bknTraceArtifactIngestUrl=http://agent-observability:8080/api/agent-observability/v1/evidence/artifacts"
contains "bkn-agent uses the evidence ingest Secret" "${bkn_agent_sets}" "observability.bknTraceEvidenceIngestTokenSecretName=bkn-trace-evidence-ingest"
contains "bkn-agent reads the token key the receiver writes" "${bkn_agent_sets}" "observability.bknTraceEvidenceIngestTokenSecretKey=token"

# The full release manifest contains all declared producers. A new producer
# must either be wired above or make this installer-level assertion fail.
LAST_WARN=""
log_warn() { LAST_WARN="$*"; }
_openbkn_warn_unwired_evidence_producers "${_OPENBKN_TRACE_EVIDENCE_PRODUCERS[@]}" bkn-safe
if [[ -n "${LAST_WARN}" ]]; then
    fail "no warning when every present producer is wired: ${LAST_WARN}"
else
    ok
fi

# Keep the guard tied to the release entrypoint. If a declared producer is
# omitted from its outer gate, its actual extra-set result is empty even though
# the producer name itself is known to the installer.
LAST_WARN=""
_openbkn_release_extra_sets() { CORE_RELEASE_EXTRA_SETS=(); }
_openbkn_warn_unwired_evidence_producers bkn-agent
contains "names a declared producer without actual wiring" "${LAST_WARN}" "bkn-agent"

CORE_SET_VALUES=()
OFFLINE_MODE=true
OFFLINE_REGISTRY=registry.test:5000
_openbkn_apply_default_set_values
offline_sets="${CORE_SET_VALUES[*]:-}"
contains "offline installs rewrite application images" "${offline_sets}" "image.registry=registry.test:5000/openbkn-ai"
not_contains "offline installs do not rewrite deleted Hook images" "${offline_sets}" "evidence.indexManagement"

CORE_SET_VALUES=()
OFFLINE_MODE=false
CORE_IMAGE_REGISTRY=ghcr
CONFIG_YAML_PATH=/nonexistent/openbkn-config.yaml
_openbkn_apply_default_set_values
online_sets="${CORE_SET_VALUES[*]:-}"
contains "online registry flags rewrite application images" "${online_sets}" "image.registry=ghcr.io/openbkn-ai"
not_contains "online registry flags do not configure deleted Hook images" "${online_sets}" "evidence.indexManagement"

CORE_SET_VALUES=()
CORE_IMAGE_REGISTRY=""
_openbkn_apply_default_set_values
default_online_sets="${CORE_SET_VALUES[*]:-}"
contains "default online installs use the SWR application registry" "${default_online_sets}" "image.registry=swr.cn-east-3.myhuaweicloud.com/openbkn-ai"
not_contains "default online installs do not configure deleted Hook images" "${default_online_sets}" "evidence.indexManagement"

CORE_SET_VALUES=("image.registry=registry.example/openbkn")
CORE_IMAGE_REGISTRY=""
_openbkn_apply_default_set_values
explicit_sets="${CORE_SET_VALUES[*]:-}"
not_contains "application registry overrides do not imply a third-party mirror" "${explicit_sets}" "evidence.indexManagement.createJob.image.registry="

cat >"${CONFIG_REGISTRY_FILE}" <<'EOF'
image:
  registry: "registry.config/openbkn"
EOF
CORE_SET_VALUES=()
CONFIG_YAML_PATH="${CONFIG_REGISTRY_FILE}"
_openbkn_apply_default_set_values
config_sets="${CORE_SET_VALUES[*]:-}"
not_contains "config application registries do not imply a third-party mirror" "${config_sets}" "evidence.indexManagement.createJob.image.registry="

sync_script="$(<"${SCRIPT_DIR}/scripts/sync-k8s-images.sh")"
not_contains "offline sync excludes the deleted Hook image" "${sync_script}" 'curlimages/curl:8.10.1'
not_contains "offline sync drops the Hook image array" "${sync_script}" 'HOOK_IMAGES'

if [[ "${FAILED}" -eq 0 ]]; then
    echo "openbkn_trace_profile_test: all ${PASS} checks passed"
    exit 0
fi
echo "openbkn_trace_profile_test: FAILED"
exit 1
