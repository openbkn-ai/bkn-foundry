#!/usr/bin/env bash
set -euo pipefail

chart_dir="${1:-charts/agent-observability}"
default_rendered="$(helm template agent-observability "${chart_dir}")"
if grep -Fq "BKN_TRACE_CORE_MARIADB_DSN" <<<"${default_rendered}"; then
  echo "default chart must not require the MariaDB Core secret" >&2
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
if ! grep -A1 -Fq 'name: BKN_TRACE_DEPLOYMENT_TENANT_ID
              value: "openbkn-local"' <<<"${default_rendered}"; then
  echo "single-tenant deployments must inject the trusted deployment tenant" >&2
  exit 1
fi

rendered="$(helm template agent-observability "${chart_dir}" \
  --set evidence.store=opensearch \
  --set evidence.index=bkn-trace-evidence-test \
  --set evidence.indexManagement.enabled=true \
  --set evidence.indexManagement.createJob.enabled=true)"

assert_contains() {
  local needle="$1"
  if ! grep -Fq "$needle" <<<"${rendered}"; then
    echo "expected rendered chart to contain: ${needle}" >&2
    exit 1
  fi
}

assert_contains "kind: ConfigMap"
assert_contains "agent-observability-evidence-index-template"
assert_contains '"helm.sh/hook": pre-install,pre-upgrade'
assert_contains '"helm.sh/hook-weight": "-5"'
assert_contains '"helm.sh/hook-delete-policy": before-hook-creation'
assert_contains '"ingested_at":'
assert_contains '"type": "date"'
assert_contains '"max_result_window": 10000'
assert_contains "kind: Job"
assert_contains "agent-observability-evidence-index-setup"
assert_contains '"helm.sh/hook-weight": "0"'
assert_contains "bkn-trace-evidence-test"
