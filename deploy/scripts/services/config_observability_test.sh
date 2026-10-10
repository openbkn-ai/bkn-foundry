#!/usr/bin/env bash
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${SCRIPT_DIR}/scripts/services/config.sh"
test_dir="$(mktemp -d)"
trap 'rm -rf "${test_dir}"' EXIT
CONFIG_YAML_PATH="${test_dir}/config.yaml"
IMAGE_REGISTRY=registry.example.invalid
STORAGE_STORAGE_CLASS_NAME=test-storage
MARIADB_NAMESPACE=resource MARIADB_PASSWORD='' MARIADB_ROOT_PASSWORD=''
REDIS_NAMESPACE=resource OPENSEARCH_NAMESPACE=resource
OPENSEARCH_CLUSTER_NAME=os OPENSEARCH_NODE_GROUP=master OPENSEARCH_PROTOCOL=http
OPENSEARCH_RELEASE_NAME=os KAFKA_NAMESPACE=resource KAFKA_RELEASE_NAME=kafka
KAFKA_SASL_MECHANISM=PLAIN KAFKA_CLIENT_USER='' KAFKA_CLIENT_PASSWORD=''
KAFKA_AUTH_ENABLED=false INGRESS_NGINX_CLASS=nginx
ASSUME_YES=true BKN_SAFE_INITIAL_PASSWORD=synthetic-test-only
load_image_registry_from_config() { :; }
log_info() { :; }
log_warn() { :; }
log_error() { :; }
kubectl() { return 1; }
helm() { return 1; }
get_secret_b64_key() { :; }
first_service_with_port() { :; }
config_yaml_dep_field() { :; }
config_yaml_top_field() { :; }
get_access_address_field() {
  case "$1" in host) printf '127.0.0.1';; scheme) printf 'https';; port) printf '443';; path) printf '/';; esac
}
yaml_quote() { printf '"%s"' "$1"; }
generate_random_password() { printf 'synthetic-test-only'; }

cat >"${test_dir}/observability.yaml" <<'YAML'
observability:
  admissionBudget:
    profile: deployment-reviewed
    collectorMetricsEndpoint: "http://collector.example.invalid:8888/metrics"
    opensearchCapacityThreshold: 0.8
    opensearchHeapThreshold: "0.81"
    collectorQueueThreshold: 0.82
    storagePoolThreshold: "0.83"
  sourceCoverage:
    enabled: false
    metricsEndpoint: "http://legacy.example.invalid:8888/metrics"
  evidencePublisher:
    enabled: false
YAML
# Compare canonical YAML from Helm: mapping order/comments are not values.
canonical() {
  command helm template canonical "${SCRIPT_DIR}/../bkn-trace/agent-observability/charts/agent-observability" \
    -f "$1" --set image.registry=registry.example.invalid --show-only templates/deployment.yaml
}
canonical "${test_dir}/observability.yaml" >"${test_dir}/expected.yaml"
cat "${test_dir}/observability.yaml" >"${CONFIG_YAML_PATH}"
printf '\nunrelated: discarded\n' >>"${CONFIG_YAML_PATH}"
generate_config_yaml
canonical "${CONFIG_YAML_PATH}" >"${test_dir}/preserved.yaml"
diff -u "${test_dir}/expected.yaml" "${test_dir}/preserved.yaml"
grep -Eq '^    opensearchCapacityThreshold: 0[.]8$' "${CONFIG_YAML_PATH}"
grep -Eq '^    opensearchHeapThreshold: "0[.]81"$' "${CONFIG_YAML_PATH}"
grep -Eq '^    storagePoolThreshold: "0[.]83"$' "${CONFIG_YAML_PATH}"
[[ "$(grep -c '^    enabled: false$' "${CONFIG_YAML_PATH}")" == 2 ]]
if grep -q '^unrelated:' "${CONFIG_YAML_PATH}"; then
  echo 'unrelated values must not be preserved' >&2
  exit 1
fi
[[ "$(ls -l "${CONFIG_YAML_PATH}" | cut -c2-10)" == rw------- ]]
generate_config_yaml
canonical "${CONFIG_YAML_PATH}" >"${test_dir}/preserved.yaml"
diff -u "${test_dir}/expected.yaml" "${test_dir}/preserved.yaml"

# Feed the regenerated product configuration to the actual chart, not a YAML
# stand-in. Both numeric and quoted thresholds must reach the runtime env.
command helm template budget-test "${SCRIPT_DIR}/../bkn-trace/agent-observability/charts/agent-observability" \
  -f "${CONFIG_YAML_PATH}" --show-only templates/deployment.yaml >"${test_dir}/deployment.yaml"
assert_env() {
  grep -A1 "name: $1" "${test_dir}/deployment.yaml" | grep -Fq "value: \"$2\""
}
assert_env BKN_TRACE_ADMISSION_OPENSEARCH_CAPACITY_THRESHOLD 0.8
assert_env BKN_TRACE_ADMISSION_OPENSEARCH_HEAP_THRESHOLD 0.81
assert_env BKN_TRACE_ADMISSION_COLLECTOR_QUEUE_THRESHOLD 0.82
assert_env BKN_TRACE_ADMISSION_STORAGE_POOL_THRESHOLD 0.83
assert_env BKN_TRACE_ADMISSION_COLLECTOR_METRICS_ENDPOINT http://collector.example.invalid:8888/metrics

# Legal unindented flow contents/closing braces and quoted top-level keys.
for key in observability '"observability"' "'observability'"; do
  printf '%s: {\nadmissionBudget: {storagePoolThreshold: "0.75"}\n}\nunrelated: discarded\n' "$key" >"${CONFIG_YAML_PATH}"
  canonical "${CONFIG_YAML_PATH}" >"${test_dir}/expected.yaml"
  generate_config_yaml
  canonical "${CONFIG_YAML_PATH}" >"${test_dir}/preserved.yaml"
  diff -u "${test_dir}/expected.yaml" "${test_dir}/preserved.yaml"
done
# A distinct quoted key must not be mistaken for observability.
printf '"ob servability": {enabled: false}\n' >"${CONFIG_YAML_PATH}"
generate_config_yaml
if grep -q '^observability:' "${CONFIG_YAML_PATH}"; then
  echo 'absent observability must not be invented' >&2
  exit 1
fi
# Invalid YAML fails before overwriting reviewed input (without logging values).
printf 'observability: {\n' >"${CONFIG_YAML_PATH}"
cp "${CONFIG_YAML_PATH}" "${test_dir}/invalid.yaml"
if generate_config_yaml; then echo 'Invalid YAML was accepted' >&2; exit 1; fi
cmp "${CONFIG_YAML_PATH}" "${test_dir}/invalid.yaml"
printf 'namespace: openbkn\n' >"${CONFIG_YAML_PATH}"
generate_config_yaml
if grep -q '^observability:' "${CONFIG_YAML_PATH}"; then
  echo 'absent observability must not be invented' >&2
  exit 1
fi
# A new generated config inherits Chart defaults without inventing an
# observability block. Both the first render and regeneration must agree.
for iteration in 1 2; do
  command helm template budget-test "${SCRIPT_DIR}/../bkn-trace/agent-observability/charts/agent-observability" \
    -f "${CONFIG_YAML_PATH}" --show-only templates/deployment.yaml >"${test_dir}/deployment.yaml"
  assert_env BKN_TRACE_ADMISSION_OPENSEARCH_CAPACITY_THRESHOLD 0.90
  assert_env BKN_TRACE_ADMISSION_OPENSEARCH_HEAP_THRESHOLD 0.90
  assert_env BKN_TRACE_ADMISSION_COLLECTOR_QUEUE_THRESHOLD 0.90
  assert_env BKN_TRACE_ADMISSION_STORAGE_POOL_THRESHOLD 0.90
  generate_config_yaml
done
# Explicit invalid values must reach the runtime validator, not be silently
# replaced by defaults; partial values inherit defaults only for missing keys.
cat >"${CONFIG_YAML_PATH}" <<'YAML'
observability:
  admissionBudget:
    opensearchCapacityThreshold: 0
    opensearchHeapThreshold: "invalid"
    collectorQueueThreshold: "0.95"
YAML
generate_config_yaml
command helm template budget-test "${SCRIPT_DIR}/../bkn-trace/agent-observability/charts/agent-observability" \
  -f "${CONFIG_YAML_PATH}" --show-only templates/deployment.yaml >"${test_dir}/deployment.yaml"
assert_env BKN_TRACE_ADMISSION_OPENSEARCH_CAPACITY_THRESHOLD 0
assert_env BKN_TRACE_ADMISSION_OPENSEARCH_HEAP_THRESHOLD invalid
assert_env BKN_TRACE_ADMISSION_COLLECTOR_QUEUE_THRESHOLD 0.95
assert_env BKN_TRACE_ADMISSION_STORAGE_POOL_THRESHOLD 0.90
echo 'Foundry observability config regeneration checks passed'
