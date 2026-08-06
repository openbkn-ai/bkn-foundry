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

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=../services/openbkn.sh
source "${SCRIPT_DIR}/scripts/services/openbkn.sh"

# The profile builder is deliberately pure: install tests can validate the product
# contract without a cluster, credentials, or a Helm chart archive.
CORE_RELEASE_EXTRA_SETS=()
_openbkn_trace_profile_sets agent-observability
ao_sets="${CORE_RELEASE_EXTRA_SETS[*]:-}"
contains "AO uses durable Core" "${ao_sets}" "core.store=mariadb"
contains "AO requires Core DSN Secret" "${ao_sets}" "core.mariadb.existingSecret=bkn-trace-core-mariadb"
contains "AO persists evidence" "${ao_sets}" "evidence.store=opensearch"
contains "AO enables projection" "${ao_sets}" "core.projection.enabled=true"
contains "AO creates evidence index" "${ao_sets}" "evidence.indexManagement.createJob.enabled=true"
not_contains "AO has no query gateway Secret" "${ao_sets}" "queryAuth.existingSecret="

CORE_RELEASE_EXTRA_SETS=()
_openbkn_trace_profile_sets agent-retrieval
ar_sets="${CORE_RELEASE_EXTRA_SETS[*]:-}"
contains "retrieval targets internal Trace Core" "${ar_sets}" "observability.lifecycle.core_url=http://agent-observability-internal:8081"
contains "retrieval emits Trace spans" "${ar_sets}" "observability.trace.enabled=true"
contains "retrieval emits evidence internally" "${ar_sets}" "observability.evidence.ingest_url=http://agent-observability-internal:8081/api/agent-observability/v1/evidence/events"
not_contains "retrieval has no query gateway Secret" "${ar_sets}" "gateway_token_secret_name="
not_contains "retrieval has no ingest token Secret" "${ar_sets}" "ingest_token_secret_name="

if [[ "${FAILED}" -eq 0 ]]; then
    echo "openbkn_trace_profile_test: all ${PASS} checks passed"
    exit 0
fi
echo "openbkn_trace_profile_test: FAILED"
exit 1
