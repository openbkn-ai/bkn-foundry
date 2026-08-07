#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
agent_chart="${root_dir}/agent-observability/charts/agent-observability"
collector_chart="${root_dir}/otelcol-contribute-chart/charts/otelcol-contrib"
secret_name="bkn-trace-opensearch"
sentinel_password="must-not-appear-in-rendered-manifests"

assert_contains() {
  local rendered="$1"
  local needle="$2"
  if ! grep -Fq -- "${needle}" <<<"${rendered}"; then
    echo "expected rendered chart to contain: ${needle}" >&2
    exit 1
  fi
}

assert_not_contains() {
  local rendered="$1"
  local needle="$2"
  if grep -Fq -- "${needle}" <<<"${rendered}"; then
    echo "rendered chart must not contain: ${needle}" >&2
    exit 1
  fi
}

assert_requires_secret() {
  local chart_name="$1"
  local chart_path="$2"
  local auth_path="$3"
  local stderr_file
  stderr_file="$(mktemp)"

  if helm template "${chart_name}" "${chart_path}" --set "${auth_path}.enabled=true" >/dev/null 2>"${stderr_file}"; then
    rm -f "${stderr_file}"
    echo "${chart_name} must fail closed when OpenSearch auth has no existingSecret" >&2
    exit 1
  fi
  if ! grep -Fq "opensearch.auth.existingSecret is required" "${stderr_file}"; then
    rm -f "${stderr_file}"
    echo "${chart_name} must report the missing OpenSearch existingSecret" >&2
    exit 1
  fi
  rm -f "${stderr_file}"
}

agent_default="$(helm template agent-observability "${agent_chart}")"
collector_default="$(helm template otelcol-contrib "${collector_chart}")"
assert_not_contains "${agent_default}" "OPENSEARCH_AUTH_USERNAME"
assert_not_contains "${agent_default}" "OPENSEARCH_AUTH_PASSWORD"
assert_not_contains "${collector_default}" "OPENSEARCH_AUTH_USERNAME"
assert_not_contains "${collector_default}" "OPENSEARCH_AUTH_PASSWORD"

assert_requires_secret agent-observability "${agent_chart}" opensearch.auth
assert_requires_secret otelcol-contrib "${collector_chart}" opensearchExporter.auth

agent_secure="$(helm template agent-observability "${agent_chart}" \
  --set evidence.store=opensearch \
  --set opensearch.auth.enabled=true \
  --set opensearch.auth.existingSecret="${secret_name}" \
  --set-string opensearch.auth.password="${sentinel_password}")"
assert_contains "${agent_secure}" "name: OPENSEARCH_AUTH_USERNAME"
assert_contains "${agent_secure}" "name: OPENSEARCH_AUTH_PASSWORD"
assert_contains "${agent_secure}" "${secret_name}"
assert_not_contains "${agent_secure}" "${sentinel_password}"

collector_secure="$(helm template otelcol-contrib "${collector_chart}" \
  --set opensearchExporter.auth.enabled=true \
  --set opensearchExporter.auth.existingSecret="${secret_name}" \
  --set-string opensearchExporter.auth.password="${sentinel_password}")"
assert_contains "${collector_secure}" "name: OPENSEARCH_AUTH_USERNAME"
assert_contains "${collector_secure}" "name: OPENSEARCH_AUTH_PASSWORD"
assert_contains "${collector_secure}" "${secret_name}"
assert_contains "${collector_secure}" '${env:OPENSEARCH_AUTH_USERNAME}'
assert_contains "${collector_secure}" '${env:OPENSEARCH_AUTH_PASSWORD}'
assert_not_contains "${collector_secure}" "${sentinel_password}"
