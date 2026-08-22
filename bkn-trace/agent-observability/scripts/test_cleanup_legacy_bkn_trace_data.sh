#!/usr/bin/env bash
# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
# Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

set -euo pipefail

script_dir=$(cd "$(dirname "$0")" && pwd)
target="$script_dir/cleanup_legacy_bkn_trace_data.sh"

if env -i PATH="$PATH" bash "$target" >/dev/null 2>&1; then
  echo "cleanup must reject missing targets" >&2
  exit 1
fi

common=(
  BKN_TRACE_CLEANUP_DB_HOST=127.0.0.1
  BKN_TRACE_CLEANUP_DB_NAME=bkn_trace
  BKN_TRACE_CLEANUP_DB_USER=trace
  OPENSEARCH_ENDPOINT=http://127.0.0.1:9200
  OPENSEARCH_TRACE_INDEX=ss4o_traces-local
  OPENSEARCH_EVIDENCE_INDEX=bkn-trace-evidence-local
  BKN_TRACE_PROJECTION_INDEX=bkn-trace-core-local
)

validation_bin=$(mktemp -d)
printf '#!/usr/bin/env bash\necho 0\n' >"$validation_bin/mysql"
printf '#!/usr/bin/env bash\necho "{\\"count\\":0}"\n' >"$validation_bin/curl"
chmod +x "$validation_bin/mysql" "$validation_bin/curl"

if env "${common[@]}" PATH="$validation_bin:$PATH" BKN_TRACE_CLEANUP_DB_NAME='*' bash "$target" >/dev/null 2>&1; then
  echo "cleanup must reject wildcard database names" >&2
  exit 1
fi
if env "${common[@]}" PATH="$validation_bin:$PATH" OPENSEARCH_TRACE_INDEX='trace-*' bash "$target" >/dev/null 2>&1; then
  echo "cleanup must reject wildcard index names" >&2
  exit 1
fi
if env "${common[@]}" PATH="$validation_bin:$PATH" OPENSEARCH_ENDPOINT='http://127.0.0.1:9200/*' bash "$target" >/dev/null 2>&1; then
  echo "cleanup must reject wildcard OpenSearch endpoints" >&2
  exit 1
fi
if env "${common[@]}" PATH="$validation_bin:$PATH" BKN_TRACE_PROJECTION_INDEX='${UNRESOLVED}' bash "$target" >/dev/null 2>&1; then
  echo "cleanup must reject unresolved expressions" >&2
  exit 1
fi

while read -r table; do
  if [[ $table != bkn_trace_* ]]; then
    echo "cleanup target inventory escaped the BKN Trace namespace: $table" >&2
    exit 1
  fi
done < <(grep -oE 'DELETE FROM [A-Za-z0-9_]+' "$target" | awk '{print $3}')

fake_bin=$(mktemp -d)
call_log=$(mktemp)
fake_state=$(mktemp)
rm -f "$fake_state"
trap 'rm -rf "$validation_bin" "$fake_bin"; rm -f "$call_log" "$fake_state"' EXIT
printf '%s\n' \
  '#!/usr/bin/env bash' \
  'printf "mysql %s\n" "$*" >>"$CALL_LOG"' \
  'if [[ $* == *"information_schema.tables"* ]]; then' \
  '  if [[ ${MISSING_OPERATION_FACTS:-} == 1 && $* == *"bkn_trace_operation_call_facts"* ]]; then echo 0; else echo 1; fi' \
  '  exit 0' \
  'fi' \
  'if [[ $* == *"DELETE FROM"* ]]; then touch "$FAKE_STATE"; exit 0; fi' \
  'if [[ -e $FAKE_STATE ]]; then echo 0; else echo 3; fi' \
  >"$fake_bin/mysql"
printf '%s\n' \
  '#!/usr/bin/env bash' \
  'printf "curl %s\n" "$*" >>"$CALL_LOG"' \
  'if [[ ${MISSING_TRACE_INDEX:-} == 1 && $* == *"ss4o_traces-local/_count"* ]]; then echo "{\"error\":{\"reason\":\"missing\"},\"status\":404}"; exit 0; fi' \
  'if [[ $* == *"_delete_by_query"* ]]; then echo "{\"failures\":[]}"; exit 0; fi' \
  'if [[ -e $FAKE_STATE ]]; then echo "{\"count\":0}"; else echo "{\"count\":3}"; fi' \
  >"$fake_bin/curl"
chmod +x "$fake_bin/mysql" "$fake_bin/curl"

env "${common[@]}" PATH="$fake_bin:$PATH" CALL_LOG="$call_log" FAKE_STATE="$fake_state" bash "$target" >/dev/null
if grep -Eq 'DELETE FROM|_delete_by_query' "$call_log"; then
  echo "preview mode performed a destructive call" >&2
  exit 1
fi

if ! legacy_preview=$(env "${common[@]}" PATH="$fake_bin:$PATH" CALL_LOG="$call_log" FAKE_STATE="$fake_state" MISSING_OPERATION_FACTS=1 bash "$target" 2>&1); then
  echo "preview must support the upgrade-from-0.1.3 schema where operation_call_facts is absent" >&2
  exit 1
fi
if [[ $legacy_preview != *"bkn_trace_operation_call_facts status=absent"* ]]; then
  echo "preview must report an explicitly allowed table that is absent" >&2
  exit 1
fi

if ! missing_index_preview=$(env "${common[@]}" PATH="$fake_bin:$PATH" CALL_LOG="$call_log" FAKE_STATE="$fake_state" MISSING_TRACE_INDEX=1 bash "$target" 2>&1); then
  echo "preview must support an explicitly targeted OpenSearch index that is already absent" >&2
  exit 1
fi
if [[ $missing_index_preview != *"opensearch ss4o_traces-local status=absent"* ]]; then
  echo "preview must report an explicitly targeted OpenSearch index that is absent" >&2
  exit 1
fi

: >"$call_log"
if ! missing_index_confirm=$(env "${common[@]}" PATH="$fake_bin:$PATH" CALL_LOG="$call_log" FAKE_STATE="$fake_state" MISSING_TRACE_INDEX=1 bash "$target" --confirm 2>&1); then
  echo "confirmed cleanup must skip an explicitly targeted OpenSearch index that is already absent" >&2
  exit 1
fi
if [[ $missing_index_confirm != *"absent indexes skipped: ss4o_traces-local"* ]]; then
  echo "confirmed cleanup must summarize absent OpenSearch targets" >&2
  exit 1
fi
if grep -q 'ss4o_traces-local/_delete_by_query' "$call_log"; then
  echo "confirmed cleanup must not delete an absent OpenSearch index" >&2
  exit 1
fi
if ! grep -q 'bkn-trace-evidence-local/_delete_by_query' "$call_log" || ! grep -q 'bkn-trace-core-local/_delete_by_query' "$call_log"; then
  echo "confirmed cleanup must retain the remaining explicit OpenSearch targets" >&2
  exit 1
fi

: >"$call_log"
env "${common[@]}" PATH="$fake_bin:$PATH" CALL_LOG="$call_log" FAKE_STATE="$fake_state" bash "$target" --confirm >/dev/null
if ! grep -q 'DELETE FROM bkn_trace_operation_call_facts' "$call_log" || ! grep -q '_delete_by_query' "$call_log"; then
  echo "confirmed cleanup did not execute the explicit Trace targets" >&2
  exit 1
fi

echo "cleanup safety contract passed"
