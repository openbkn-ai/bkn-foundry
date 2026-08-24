#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<EOF
usage: $0 [--confirm --expected-context CONTEXT --backup-confirmed --writes-quiesced]

Without --confirm the script only previews the cleanup targets.
Confirmed cleanup is irreversible and leaves agent-observability at 0 replicas
so the OpenBKN 0.1.4 rollout can restore it.
EOF
}

mode=preview
expected_context=
backup_confirmed=false
writes_quiesced=false
confirmation_option_seen=false
while (( $# > 0 )); do
  case "$1" in
    --confirm)
      [[ $mode == preview ]] || { echo "--confirm was provided more than once" >&2; exit 2; }
      mode=confirm
      shift
      ;;
    --expected-context)
      [[ -z $expected_context ]] || { echo "--expected-context was provided more than once" >&2; exit 2; }
      (( $# >= 2 )) || { echo "--expected-context requires a value" >&2; exit 2; }
      expected_context=$2
      confirmation_option_seen=true
      shift 2
      ;;
    --backup-confirmed)
      [[ $backup_confirmed == false ]] || { echo "--backup-confirmed was provided more than once" >&2; exit 2; }
      backup_confirmed=true
      confirmation_option_seen=true
      shift
      ;;
    --writes-quiesced)
      [[ $writes_quiesced == false ]] || { echo "--writes-quiesced was provided more than once" >&2; exit 2; }
      writes_quiesced=true
      confirmation_option_seen=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage
      exit 2
      ;;
  esac
done

if [[ $mode == confirm ]]; then
  [[ -n $expected_context ]] || { echo "--expected-context is required with --confirm" >&2; exit 2; }
  [[ $backup_confirmed == true ]] || { echo "--backup-confirmed is required with --confirm" >&2; exit 2; }
  [[ $writes_quiesced == true ]] || { echo "--writes-quiesced is required with --confirm" >&2; exit 2; }
elif [[ $confirmation_option_seen == true ]]; then
  echo "confirmation options require --confirm" >&2
  exit 2
fi

config_file=${BKN_TRACE_CLEANUP_CONFIG:-/root/.openbkn-ai/config.yaml}
deployment=${BKN_TRACE_CLEANUP_DEPLOYMENT:-agent-observability}
dependency_namespace=${BKN_TRACE_CLEANUP_DEPENDENCY_NAMESPACE:-resource}
database=${BKN_TRACE_CLEANUP_DB_NAME:-bkn_trace}
scale_timeout=${BKN_TRACE_CLEANUP_SCALE_TIMEOUT:-180s}
quiesce_seconds=${BKN_TRACE_CLEANUP_QUIESCE_SECONDS:-10}
opensearch_ca_file=${BKN_TRACE_CLEANUP_OPENSEARCH_CA_FILE:-}
opensearch_insecure=${BKN_TRACE_CLEANUP_OPENSEARCH_INSECURE:-false}

[[ $scale_timeout =~ ^[1-9][0-9]*[smh]$ ]] || { echo "BKN_TRACE_CLEANUP_SCALE_TIMEOUT must be a positive duration such as 180s" >&2; exit 2; }
[[ $quiesce_seconds =~ ^[0-9]+$ ]] || { echo "BKN_TRACE_CLEANUP_QUIESCE_SECONDS must be a non-negative integer" >&2; exit 2; }
[[ $opensearch_insecure == true || $opensearch_insecure == false ]] || { echo "BKN_TRACE_CLEANUP_OPENSEARCH_INSECURE must be true or false" >&2; exit 2; }
if [[ -n $opensearch_ca_file && $opensearch_insecure == true ]]; then
  echo "BKN_TRACE_CLEANUP_OPENSEARCH_CA_FILE and BKN_TRACE_CLEANUP_OPENSEARCH_INSECURE=true cannot be used together" >&2
  exit 2
fi

for required_command in kubectl jq awk; do
  command -v "$required_command" >/dev/null || { echo "$required_command is required" >&2; exit 2; }
done
[[ -r $config_file ]] || { echo "cleanup config is not readable: $config_file" >&2; exit 2; }

yaml_value() {
  local section=$1 key=$2 value label
  label=${section:+$section.}$key
  value=$(awk -v wanted_section="$section" -v wanted_key="$key" '
    function trim(value) {
      sub(/^[[:space:]]+/, "", value)
      sub(/[[:space:]]+$/, "", value)
      return value
    }
    function scalar(value) {
      value = trim(value)
      if (value ~ /^\047.*\047$/ || value ~ /^".*"$/) {
        value = substr(value, 2, length(value) - 2)
      }
      return value
    }
    /^[^[:space:]#][^:]*:/ {
      top = $0
      sub(/:.*/, "", top)
      top = trim(top)
      section = ""
    }
    /^  [^[:space:]#][^:]*:/ {
      if (top == "depServices") {
        section = $0
        sub(/:.*/, "", section)
        section = trim(section)
      }
    }
    {
      line = $0
      sub(/\r$/, "", line)
      indent = match(line, /[^ ]/) - 1
      candidate = line
      sub(/^[[:space:]]*/, "", candidate)
      name = candidate
      sub(/:.*/, "", name)
      name = trim(name)
      if (wanted_section == "" && indent == 0 && name == wanted_key) {
        sub(/^[^:]*:/, "", candidate)
        print scalar(candidate)
        exit
      }
      if (wanted_section != "" && top == "depServices" && section == wanted_section && indent == 4 && name == wanted_key) {
        sub(/^[^:]*:/, "", candidate)
        print scalar(candidate)
        exit
      }
    }
  ' "$config_file")
  [[ -n $value && $value != null ]] || { echo "required config value is missing: $label" >&2; exit 2; }
  printf '%s' "$value"
}

application_namespace=$(yaml_value '' namespace)
mariadb_host=$(yaml_value rds host)
mariadb_port=$(yaml_value rds port)
mariadb_password=$(yaml_value rds root_password)
opensearch_host=$(yaml_value opensearch host)
opensearch_port=$(yaml_value opensearch port)
opensearch_protocol=$(yaml_value opensearch protocol)
opensearch_user=$(yaml_value opensearch user)
opensearch_password=$(yaml_value opensearch password)

dns_label_pattern='^[a-z0-9]([-a-z0-9]*[a-z0-9])?$'
[[ $application_namespace =~ $dns_label_pattern ]] || { echo "invalid application namespace" >&2; exit 2; }
[[ $dependency_namespace =~ $dns_label_pattern ]] || { echo "invalid dependency namespace" >&2; exit 2; }
[[ $deployment =~ $dns_label_pattern ]] || { echo "invalid deployment name" >&2; exit 2; }
[[ $database =~ ^[A-Za-z0-9_]+$ && ! $database =~ ^(mysql|information_schema|performance_schema|sys)$ ]] || {
  echo "BKN_TRACE_CLEANUP_DB_NAME must be one explicit application database" >&2; exit 2;
}
[[ $mariadb_port =~ ^[0-9]+$ && $opensearch_port =~ ^[0-9]+$ ]] || { echo "configured ports must be numeric" >&2; exit 2; }
[[ $opensearch_protocol == http || $opensearch_protocol == https ]] || { echo "OpenSearch protocol must be http or https" >&2; exit 2; }
if [[ $opensearch_protocol == http && ( -n $opensearch_ca_file || $opensearch_insecure == true ) ]]; then
  echo "OpenSearch TLS options require protocol=https" >&2
  exit 2
fi
if [[ $opensearch_insecure == true ]]; then
  echo "WARNING: OpenSearch TLS certificate verification is disabled explicitly" >&2
fi

service_name() {
  local host=$1 kind=$2
  if [[ $host =~ ^([a-z0-9]([-a-z0-9]*[a-z0-9])?)\.${dependency_namespace}\.svc(\.cluster\.local)?$ ]]; then
    printf '%s' "${BASH_REMATCH[1]}"
  else
    echo "$kind host must be a service in namespace $dependency_namespace: $host" >&2
    exit 2
  fi
}
mariadb_service=$(service_name "$mariadb_host" MariaDB)
opensearch_service=$(service_name "$opensearch_host" OpenSearch)

current_context=$(kubectl config current-context)
[[ -n $current_context ]] || { echo "kubectl current context is empty" >&2; exit 2; }
if [[ $mode == confirm && $current_context != "$expected_context" ]]; then
  echo "kubectl context mismatch: expected=$expected_context actual=$current_context" >&2
  exit 2
fi

deployment_json=$(kubectl -n "$application_namespace" get deployment "$deployment" -o json)
original_replicas=$(jq -er '.spec.replicas // 1 | select(type == "number" and . >= 0 and floor == .)' <<<"$deployment_json") || {
  echo "deployment $deployment has an invalid replica count" >&2; exit 2;
}
pod_selector=$(jq -er '.spec.selector.matchLabels | to_entries | map("\(.key)=\(.value)") | join(",") | select(length > 0)' <<<"$deployment_json") || {
  echo "deployment $deployment has no usable matchLabels selector" >&2; exit 2;
}
deployment_env() {
  local name=$1 value
  value=$(jq -er --arg name "$name" '[.spec.template.spec.containers[].env[]? | select(.name == $name) | .value][0] // empty' <<<"$deployment_json") || {
    echo "deployment $deployment is missing a literal $name value" >&2; exit 2;
  }
  [[ -n $value ]] || { echo "deployment $deployment is missing a literal $name value" >&2; exit 2; }
  printf '%s' "$value"
}
trace_index=$(deployment_env OPENSEARCH_TRACE_INDEX)
evidence_index=$(deployment_env OPENSEARCH_EVIDENCE_INDEX)
projection_alias=$(deployment_env BKN_TRACE_PROJECTION_INDEX)

validate_index_name() {
  local index=$1
  [[ $index =~ ^[A-Za-z0-9._-]+$ && $index != .* && $index != _all ]] || {
    echo "OpenSearch target must be one explicit non-system name: $index" >&2; exit 2;
  }
}
validate_index_name "$trace_index"
validate_index_name "$evidence_index"
validate_index_name "$projection_alias"
[[ $trace_index != "$evidence_index" ]] || { echo "Trace and Evidence indexes must be different" >&2; exit 2; }

ready_pod_for_service() {
  local service=$1 kind=$2 pod
  pod=$(kubectl -n "$dependency_namespace" get endpointslice -l "kubernetes.io/service-name=$service" -o json |
    jq -er '[.items[].endpoints[]? | select(.conditions.ready != false) | select(.targetRef.kind == "Pod") | .targetRef.name][0] // empty') || {
      echo "no Ready Pod found for $kind service $dependency_namespace/$service" >&2; exit 2;
    }
  [[ -n $pod ]] || { echo "no Ready Pod found for $kind service $dependency_namespace/$service" >&2; exit 2; }
  printf '%s' "$pod"
}
mariadb_pod=$(ready_pod_for_service "$mariadb_service" MariaDB)
opensearch_pod=$(ready_pod_for_service "$opensearch_service" OpenSearch)

kubectl -n "$dependency_namespace" exec "$mariadb_pod" -- sh -c 'command -v mariadb >/dev/null || command -v mysql >/dev/null' || {
  echo "MariaDB Pod $mariadb_pod has neither mariadb nor mysql client" >&2; exit 2;
}
kubectl -n "$dependency_namespace" exec "$opensearch_pod" -- sh -c 'command -v curl >/dev/null && command -v sed >/dev/null' || {
  echo "OpenSearch Pod $opensearch_pod must contain curl and sed" >&2; exit 2;
}

mysql_exec() {
  local sql=$1
  printf '%s\n' "$mariadb_password" | kubectl -n "$dependency_namespace" exec -i "$mariadb_pod" -- sh -c '
    IFS= read -r MYSQL_PWD; export MYSQL_PWD
    client=$(command -v mariadb || command -v mysql)
    "$client" --host=127.0.0.1 --port="$1" --user=root --database="$2" --batch --skip-column-names --execute="$3"
  ' sh "$mariadb_port" "$database" "$sql"
}

opensearch_raw() {
  local method=$1 path=$2 data=${3:-}
  printf '%s\n%s\n' "$opensearch_user" "$opensearch_password" |
    kubectl -n "$dependency_namespace" exec -i "$opensearch_pod" -- sh -c '
      IFS= read -r username; IFS= read -r password
      method=$1; protocol=$2; port=$3; data=$4; path=$5; tls_mode=$6; ca_file=$7
      config_escape() { sed -e "s/\\\\/\\\\\\\\/g" -e "s/\"/\\\\\"/g"; }
      username=$(printf "%s" "$username" | config_escape)
      password=$(printf "%s" "$password" | config_escape)
      set -- -sS -X "$method" --config -
      if [ -n "$data" ]; then set -- "$@" -H "Content-Type: application/json" --data "$data"; fi
      if [ "$tls_mode" = insecure ]; then set -- "$@" --insecure; fi
      if [ "$tls_mode" = ca ]; then set -- "$@" --cacert "$ca_file"; fi
      printf "user = \"%s:%s\"\\n" "$username" "$password" |
        curl "$@" --write-out "\n%{http_code}" "$protocol://127.0.0.1:$port$path"
    ' sh "$method" "$opensearch_protocol" "$opensearch_port" "$data" "$path" "$opensearch_tls_mode" "$opensearch_ca_file"
}

opensearch_tls_mode=verify
[[ -n $opensearch_ca_file ]] && opensearch_tls_mode=ca
[[ $opensearch_insecure == true ]] && opensearch_tls_mode=insecure

split_opensearch_response() {
  local raw=$1
  OPENSEARCH_STATUS=${raw##*$'\n'}
  OPENSEARCH_BODY=${raw%$'\n'*}
  [[ $OPENSEARCH_STATUS =~ ^[0-9]{3}$ ]] || { echo "OpenSearch response is missing an HTTP status" >&2; return 2; }
}

opensearch_json_2xx() {
  local method=$1 path=$2 data=${3:-} raw
  raw=$(opensearch_raw "$method" "$path" "$data") || { echo "OpenSearch request failed: method=$method path=$path" >&2; return 2; }
  split_opensearch_response "$raw"
  [[ $OPENSEARCH_STATUS =~ ^2[0-9][0-9]$ ]] || {
    echo "OpenSearch request failed: method=$method path=$path http_status=$OPENSEARCH_STATUS" >&2; return 2;
  }
  printf '%s' "$OPENSEARCH_BODY"
}

projection_target() {
  local raw count target
  raw=$(opensearch_raw GET "/_alias/$projection_alias") || {
    echo "failed to query projection alias: $projection_alias" >&2; return 2;
  }
  split_opensearch_response "$raw" || return
  case "$OPENSEARCH_STATUS" in
    200) ;;
    404) return 1 ;;
    *) echo "failed to query projection alias: alias=$projection_alias http_status=$OPENSEARCH_STATUS" >&2; return 2 ;;
  esac
  count=$(jq -er 'keys | length' <<<"$OPENSEARCH_BODY")
  [[ $count == 1 ]] || { echo "projection alias must point to exactly one physical index: alias=$projection_alias targets=$count" >&2; return 2; }
  target=$(jq -er 'keys[0]' <<<"$OPENSEARCH_BODY")
  validate_index_name "$target"
  [[ $target != "$projection_alias" ]] || { echo "projection alias name is a concrete index: $projection_alias" >&2; return 2; }
  printf '%s' "$target"
}

tables=(
  bkn_trace_archive_jobs bkn_trace_operation_call_facts bkn_trace_receipts bkn_trace_operations
  bkn_trace_assembly_revisions bkn_trace_event_conflicts bkn_trace_projection_outbox
  bkn_trace_projection_checkpoints bkn_trace_dlq_replay_audit bkn_trace_dlq
  bkn_trace_evidence_event_ledger bkn_trace_idempotency_records
  bkn_trace_interactions bkn_trace_conversations bkn_trace_log_source_coverage
  bkn_trace_ee_provenance_analyses
)
table_is_allowed() {
  local candidate=$1 allowed
  for allowed in "${tables[@]}"; do
    [[ $candidate == "$allowed" ]] && return 0
  done
  return 1
}

database_inventory() {
  mysql_exec "SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE() ORDER BY table_name"
}

validate_database_inventory() {
  local inventory table
  local -a unexpected_tables=()
  inventory=$(database_inventory)
  while IFS= read -r table; do
    [[ -z $table ]] || table_is_allowed "$table" || unexpected_tables+=("$table")
  done <<<"$inventory"
  if (( ${#unexpected_tables[@]} != 0 )); then
    printf 'refusing cleanup because database %s contains table(s) outside the allowlist:\n' "$database" >&2
    printf '  %s\n' "${unexpected_tables[@]}" >&2
    return 2
  fi
}

index_count() {
  local index=$1 body
  body=$(opensearch_json_2xx GET "/$index/_count") || return
  jq -er '.count | select(type == "number" and . >= 0)' <<<"$body"
}

index_count_or_absent() {
  local index=$1 raw body
  raw=$(opensearch_raw GET "/$index/_count") || {
    echo "OpenSearch request failed: method=GET path=/$index/_count" >&2
    return 2
  }
  split_opensearch_response "$raw" || return
  case "$OPENSEARCH_STATUS" in
    200)
      body=$OPENSEARCH_BODY
      jq -er '.count | select(type == "number" and . >= 0)' <<<"$body"
      ;;
    404) return 1 ;;
    *)
      echo "OpenSearch request failed: method=GET path=/$index/_count http_status=$OPENSEARCH_STATUS" >&2
      return 2
      ;;
  esac
}

snapshot_index() {
  local index=$1 expected_status=$2 count status
  if count=$(index_count_or_absent "$index"); then
    [[ $expected_status == present ]] || {
      echo "OpenSearch index appeared during cleanup admission: $index" >&2
      return 2
    }
    printf 'index=%s count=%s\n' "$index" "$count"
    return
  else
    status=$?
  fi
  if [[ $status == 1 && $expected_status == absent ]]; then
    printf 'index=%s status=absent\n' "$index"
    return
  fi
  if [[ $status == 1 ]]; then
    echo "OpenSearch index disappeared during cleanup admission: $index" >&2
    return 2
  fi
  return "$status"
}

physical_projection_index=
projection_status=absent
trace_index_status=present
evidence_index_status=present
validate_database_inventory
if physical_projection_index=$(projection_target); then
  projection_status=present
else
  status=$?
  [[ $status == 1 ]] || exit "$status"
fi

existing_tables=()
while IFS= read -r table; do
  [[ -z $table ]] || existing_tables+=("$table")
done < <(database_inventory)
if trace_index_count=$(index_count_or_absent "$trace_index"); then
  :
else
  status=$?
  if [[ $status == 1 ]]; then
    trace_index_status=absent
  else
    exit "$status"
  fi
fi
if evidence_index_count=$(index_count_or_absent "$evidence_index"); then :; else
  status=$?
  if [[ $status == 1 ]]; then evidence_index_status=absent; else exit "$status"; fi
fi

echo "mode=$mode kubectl_context=$current_context config=$config_file"
echo "application_namespace=$application_namespace deployment=$deployment original_replicas=$original_replicas"
echo "mariadb namespace=$dependency_namespace service=$mariadb_service pod=$mariadb_pod database=$database user=root"
echo "opensearch namespace=$dependency_namespace service=$opensearch_service pod=$opensearch_pod tls_mode=$opensearch_tls_mode"
for table in ${existing_tables[*]-}; do
  count=$(mysql_exec "SELECT COUNT(*) FROM $table") || exit $?
  echo "mariadb $table count=$count action=drop_table"
done
if [[ $trace_index_status == present ]]; then
  echo "opensearch $trace_index count=$trace_index_count action=delete_documents"
else
  echo "opensearch $trace_index status=absent action=already_clean"
fi
if [[ $evidence_index_status == present ]]; then
  echo "opensearch $evidence_index count=$evidence_index_count action=delete_documents"
else
  echo "opensearch $evidence_index status=absent action=already_clean"
fi
if [[ $projection_status == present ]]; then
  count=$(index_count "$physical_projection_index") || exit $?
  echo "opensearch projection_alias=$projection_alias physical_index=$physical_projection_index count=$count action=delete_physical_index"
else
  echo "opensearch projection_alias=$projection_alias status=absent action=already_clean"
fi

if [[ $mode == preview ]]; then
  echo "preview only; confirmed cleanup requires an external Trace write freeze and an upgrade backup"
  exit 0
fi

scaled_down=false
cleanup_exit() {
  local exit_status=$?
  if [[ $exit_status -ne 0 && $scaled_down == true ]]; then
    echo "cleanup failed after scale-down; deployment remains at 0 replicas" >&2
    echo "after resolving the failure, restore it with: kubectl -n $application_namespace scale deployment/$deployment --replicas=$original_replicas" >&2
  fi
  exit "$exit_status"
}
trap cleanup_exit EXIT

if [[ $original_replicas != 0 ]]; then
  kubectl -n "$application_namespace" scale deployment "$deployment" --replicas=0 >/dev/null
  scaled_down=true
fi
kubectl -n "$application_namespace" wait --for=delete pod -l "$pod_selector" --timeout="$scale_timeout" >/dev/null
stopped_json=$(kubectl -n "$application_namespace" get deployment "$deployment" -o json)
for field in replicas readyReplicas availableReplicas updatedReplicas; do
  value=$(jq -r --arg field "$field" '.status[$field] // 0' <<<"$stopped_json")
  [[ $value == 0 ]] || { echo "deployment did not fully stop: status.$field=$value" >&2; exit 2; }
done

storage_snapshot() {
  local inventory table projection_now status count
  validate_database_inventory
  inventory=$(database_inventory)
  printf 'database=%s\n' "$database"
  while IFS= read -r table; do
    if [[ -n $table ]]; then
      count=$(mysql_exec "SELECT COUNT(*) FROM $table") || return
      printf 'table=%s count=%s\n' "$table" "$count"
    fi
  done <<<"$inventory"
  snapshot_index "$trace_index" "$trace_index_status" || return
  snapshot_index "$evidence_index" "$evidence_index_status" || return
  if projection_now=$(projection_target); then
    [[ $projection_status == present && $projection_now == "$physical_projection_index" ]] || {
      echo "projection alias changed during cleanup admission" >&2; return 2;
    }
    count=$(index_count "$projection_now") || return
    printf 'projection=%s count=%s\n' "$projection_now" "$count"
  else
    status=$?
    [[ $status == 1 && $projection_status == absent ]] || return 2
    printf 'projection=absent\n'
  fi
}

snapshot_before=$(storage_snapshot)
if (( quiesce_seconds > 0 )); then sleep "$quiesce_seconds"; fi
snapshot_after=$(storage_snapshot)
if [[ $snapshot_before != "$snapshot_after" ]]; then
  echo "Trace storage changed during the quiescence observation window; refusing cleanup" >&2
  diff -u <(printf '%s\n' "$snapshot_before") <(printf '%s\n' "$snapshot_after") >&2 || true
  exit 2
fi
echo "quiescence verified: observation_seconds=$quiesce_seconds"

drop_sql="SET FOREIGN_KEY_CHECKS=0;"
for table in ${existing_tables[*]-}; do drop_sql+=" DROP TABLE IF EXISTS $table;"; done
drop_sql+=" SET FOREIGN_KEY_CHECKS=1;"
mysql_exec "$drop_sql"

cleanup_indexes=()
[[ $trace_index_status == present ]] && cleanup_indexes+=("$trace_index")
[[ $evidence_index_status == present ]] && cleanup_indexes+=("$evidence_index")
if (( ${#cleanup_indexes[@]} > 0 )); then
  for index in "${cleanup_indexes[@]}"; do
    response=$(opensearch_json_2xx POST "/$index/_delete_by_query?conflicts=proceed&refresh=true" '{"query":{"match_all":{}}}')
    jq -e '(.failures // []) | length == 0' >/dev/null <<<"$response" || {
      echo "OpenSearch cleanup reported failures for $index" >&2; exit 1;
    }
  done
fi
if [[ $projection_status == present ]]; then
  opensearch_json_2xx DELETE "/$physical_projection_index" >/dev/null
fi

remaining_tables=$(mysql_exec "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE()")
[[ $remaining_tables == 0 ]] || {
  echo "MariaDB cleanup verification failed: database=$database remaining_tables=$remaining_tables" >&2; exit 1;
}
verify_index_clean() {
  local index=$1 remaining status
  if remaining=$(index_count_or_absent "$index"); then
    [[ $remaining == 0 ]] || {
      echo "OpenSearch cleanup verification failed: index=$index remaining=$remaining" >&2
      return 1
    }
    return
  else
    status=$?
  fi
  [[ $status == 1 ]] && return
  return "$status"
}
verify_index_clean "$trace_index"
verify_index_clean "$evidence_index"

verify_opensearch_absent() {
  local path=$1 label=$2 raw
  raw=$(opensearch_raw GET "$path") || { echo "$label verification request failed" >&2; return 2; }
  split_opensearch_response "$raw" || return
  case "$OPENSEARCH_STATUS" in
    404) return 0 ;;
    200) echo "$label cleanup verification failed: resource still exists" >&2; return 1 ;;
    *) echo "$label cleanup verification failed: http_status=$OPENSEARCH_STATUS" >&2; return 2 ;;
  esac
}
if [[ $projection_status == present ]]; then
  verify_opensearch_absent "/$physical_projection_index" "Projection physical index"
fi
verify_opensearch_absent "/_alias/$projection_alias" "Projection alias"

trap - EXIT
echo "cleanup complete; MariaDB has no tables, Trace/Evidence data are empty, and the Projection physical index is absent"
echo "deployment $application_namespace/$deployment remains at 0 replicas; continue with the OpenBKN 0.1.4 rollout"
