#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

check_source() {
  local release="$1" chart="$2" account="$3"
  local rendered
  rendered="$(helm template "$release" "$chart")"
  if ! grep -Fq "kind: ServiceAccount" <<<"$rendered" ||
     ! grep -Fq "name: $account" <<<"$rendered" ||
     ! grep -Fq "serviceAccountName: $account" <<<"$rendered" ||
     ! grep -Fq "automountServiceAccountToken: false" <<<"$rendered"; then
    echo "$release must render a dedicated, non-mounted ServiceAccount" >&2
    return 1
  fi
}

check_source ontology-query "$root/adp/bkn/ontology-query/helm/ontology-query" ontology-query
check_source mf-model-api "$root/infra/mf-model-api/charts" model-api
