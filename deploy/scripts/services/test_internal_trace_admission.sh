#!/usr/bin/env bash
set -euo pipefail
log_warn() { :; }
log_error() { echo "$*" >&2; }
source "$(dirname "$0")/openbkn.sh"
CORE_RELEASE_EXTRA_SET_STRINGS=()
_openbkn_trace_admission_values bknTrace.evidencePublisher
[[ "${CORE_RELEASE_EXTRA_SET_STRINGS[*]}" == *"agent-observability-internal:8081/api/agent-observability/v1/internal/trace-evidence/configuration"* ]] || exit 1
[[ "${CORE_RELEASE_EXTRA_SET_STRINGS[*]}" != *"clientID"* ]] || exit 1
[[ "${CORE_RELEASE_EXTRA_SET_STRINGS[*]}" != *"PublicKey"* ]] || exit 1
CORE_RELEASE_EXTRA_SET_STRINGS=()
_openbkn_trace_admission_values_operator observability.evidence.publisher
[[ "${CORE_RELEASE_EXTRA_SET_STRINGS[*]}" != *"client_id"* ]] || exit 1
echo 'internal Trace Admission installer profile: PASS'
