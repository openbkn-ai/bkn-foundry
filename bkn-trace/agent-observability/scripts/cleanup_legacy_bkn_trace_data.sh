#!/usr/bin/env bash
# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
# Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

set -euo pipefail

mode="preview"
if [[ ${1:-} == "--confirm" ]]; then
  mode="confirm"
  shift
fi
if [[ $# -ne 0 ]]; then
  echo "usage: $0 [--confirm]" >&2
  exit 2
fi

required=(
  BKN_TRACE_CLEANUP_DB_HOST
  BKN_TRACE_CLEANUP_DB_NAME
  BKN_TRACE_CLEANUP_DB_USER
  OPENSEARCH_ENDPOINT
  OPENSEARCH_TRACE_INDEX
  OPENSEARCH_EVIDENCE_INDEX
  BKN_TRACE_PROJECTION_INDEX
)
for name in "${required[@]}"; do
  if [[ -z ${!name:-} ]]; then
    echo "$name is required" >&2
    exit 2
  fi
  if [[ ${!name} == *'${'* || ${!name} == *'$('* ]]; then
    echo "$name contains an unresolved expression" >&2
    exit 2
  fi
done

endpoint=${OPENSEARCH_ENDPOINT%/}
if [[ ! $endpoint =~ ^https?://[^[:space:]]+$ ]] || [[ $endpoint == *'*'* || $endpoint == *'?'* || $endpoint == *'#'* ]]; then
  echo "OPENSEARCH_ENDPOINT must be one explicit http(s) endpoint without wildcards, query, or fragment" >&2
  exit 2
fi

database=${BKN_TRACE_CLEANUP_DB_NAME}
if [[ ! $database =~ ^[A-Za-z0-9_]+$ ]] || [[ $database =~ ^(mysql|information_schema|performance_schema|sys)$ ]]; then
  echo "BKN_TRACE_CLEANUP_DB_NAME must be one explicit application database" >&2
  exit 2
fi

indices=("$OPENSEARCH_TRACE_INDEX" "$OPENSEARCH_EVIDENCE_INDEX" "$BKN_TRACE_PROJECTION_INDEX")
for index in "${indices[@]}"; do
  if [[ ! $index =~ ^[A-Za-z0-9._-]+$ ]] || [[ $index == .* || $index == _all ]]; then
    echo "OpenSearch index must be one explicit non-system index: $index" >&2
    exit 2
  fi
done

for command in mysql curl jq; do
  command -v "$command" >/dev/null || { echo "$command is required" >&2; exit 2; }
done

db_port=${BKN_TRACE_CLEANUP_DB_PORT:-3306}
if [[ ! $db_port =~ ^[0-9]+$ ]]; then
  echo "BKN_TRACE_CLEANUP_DB_PORT must be numeric" >&2
  exit 2
fi
mysql_args=(
  "--host=$BKN_TRACE_CLEANUP_DB_HOST"
  "--port=$db_port"
  "--user=$BKN_TRACE_CLEANUP_DB_USER"
  "--database=$database"
  --batch
  --skip-column-names
)

curl_args=(-fsS)
query_curl_args=(-sS)
if [[ -n ${OPENSEARCH_USERNAME:-} || -n ${OPENSEARCH_PASSWORD:-} ]]; then
  if [[ -z ${OPENSEARCH_USERNAME:-} || -z ${OPENSEARCH_PASSWORD:-} ]]; then
    echo "OPENSEARCH_USERNAME and OPENSEARCH_PASSWORD must be provided together" >&2
    exit 2
  fi
  curl_args+=(--user "$OPENSEARCH_USERNAME:$OPENSEARCH_PASSWORD")
  query_curl_args+=(--user "$OPENSEARCH_USERNAME:$OPENSEARCH_PASSWORD")
fi
tables=(
  bkn_trace_operation_call_facts
  bkn_trace_receipts
  bkn_trace_operations
  bkn_trace_assembly_revisions
  bkn_trace_event_conflicts
  bkn_trace_projection_outbox
  bkn_trace_projection_checkpoints
  bkn_trace_dlq_replay_audit
  bkn_trace_dlq
  bkn_trace_evidence_event_ledger
  bkn_trace_idempotency_records
  bkn_trace_interactions
  bkn_trace_conversations
)

echo "mode=$mode database=$database"
existing_tables=()
for table in "${tables[@]}"; do
  exists=$(mysql "${mysql_args[@]}" --execute="SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = '$table'")
  if [[ $exists == "0" ]]; then
    echo "mariadb $table status=absent"
    continue
  fi
  existing_tables+=("$table")
  count=$(mysql "${mysql_args[@]}" --execute="SELECT COUNT(*) FROM $table")
  echo "mariadb $table count=$count"
done
existing_indices=()
absent_indices=()
for index in "${indices[@]}"; do
  if ! count_response=$(curl "${query_curl_args[@]}" "$endpoint/$index/_count"); then
    echo "OpenSearch count request failed for $index" >&2
    exit 1
  fi
  if [[ $(jq -r 'if .status == 404 then "absent" else empty end' <<<"$count_response") == "absent" ]]; then
    absent_indices+=("$index")
    echo "opensearch $index status=absent"
    continue
  fi
  if ! count=$(jq -er '.count' <<<"$count_response"); then
    echo "OpenSearch count response is invalid for $index" >&2
    exit 1
  fi
  existing_indices+=("$index")
  echo "opensearch $index count=$count"
done

if [[ $mode == "preview" ]]; then
  echo "preview only; rerun with --confirm to delete exactly the targets above"
  exit 0
fi

delete_sql="SET FOREIGN_KEY_CHECKS=0;"
for table in "${existing_tables[@]}"; do
  delete_sql+=" DELETE FROM $table;"
done
delete_sql+=" SET FOREIGN_KEY_CHECKS=1;"
mysql "${mysql_args[@]}" --execute="$delete_sql"
for index in "${existing_indices[@]}"; do
  response=$(curl "${curl_args[@]}" -X POST "$endpoint/$index/_delete_by_query?conflicts=proceed&refresh=true" -H 'Content-Type: application/json' --data '{"query":{"match_all":{}}}')
  if ! jq -e '(.failures // []) | length == 0' >/dev/null <<<"$response"; then
    echo "OpenSearch cleanup reported failures for $index" >&2
    exit 1
  fi
done

for table in "${existing_tables[@]}"; do
  remaining=$(mysql "${mysql_args[@]}" --execute="SELECT COUNT(*) FROM $table")
  if [[ $remaining != "0" ]]; then
    echo "MariaDB cleanup verification failed: table=$table remaining=$remaining" >&2
    exit 1
  fi
done
for index in "${existing_indices[@]}"; do
  remaining=$(curl "${curl_args[@]}" "$endpoint/$index/_count" | jq -er '.count')
  if [[ $remaining != "0" ]]; then
    echo "OpenSearch cleanup verification failed: index=$index remaining=$remaining" >&2
    exit 1
  fi
done
if [[ ${#absent_indices[@]} -gt 0 ]]; then
  echo "cleanup complete; all existing explicit BKN Trace targets are empty; absent indexes skipped: ${absent_indices[*]}"
else
  echo "cleanup complete; all explicit BKN Trace targets are empty"
fi
