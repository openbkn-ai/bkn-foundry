#!/usr/bin/env bash
# Per-release image overrides from a release manifest — the enterprise install:
# same charts, same versions, a different image for the releases that have an
# enterprise build.
set -euo pipefail

PASS=0
FAILED=0

ok() { PASS=$((PASS + 1)); }
fail() { echo "FAIL: $*"; FAILED=$((FAILED + 1)); }
contains() {
    local label="$1" haystack="$2" needle="$3"
    if [[ "${haystack}" == *"${needle}"* ]]; then ok; else fail "${label}: missing [${needle}] in [${haystack}]"; fi
}
not_contains() {
    local label="$1" haystack="$2" needle="$3"
    if [[ "${haystack}" == *"${needle}"* ]]; then fail "${label}: unexpected [${needle}]"; else ok; fi
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=../lib/common.sh
source "${SCRIPT_DIR}/scripts/lib/common.sh"

log_info() { :; }
log_warn() { :; }
log_error() { :; }

MANIFEST="$(mktemp)"
trap 'rm -f "${MANIFEST}"' EXIT
cat > "${MANIFEST}" <<'EOF'
apiVersion: deploy.openbkn.ai/v1alpha1
kind: VersionSet
product: bkn-foundry
version: 0.1.3
source:
  type: oci
  registry: oci://ghcr.io/openbkn-ai/charts
releases:
  bkn-safe:
    chart: bkn-safe
    version: 0.1.3-main.aaa
  vega-backend:
    chart: vega-backend
    version: 0.1.3-main.bbb
    image:
      registry: swr.cn-east-3.myhuaweicloud.com/openbkn-ai-ee
      repository: vega-backend-ee
  agent-observability:
    chart: agent-observability
    version: 0.1.3-main.ccc
    image:
      registry: swr.cn-east-3.myhuaweicloud.com/openbkn-ai-ee
      repository: agent-observability-ee
      tag: 0.1.3-main.ddd
EOF

got="$(get_release_manifest_release_image_field "${MANIFEST}" bkn-foundry 0.1.3 vega-backend repository)"
contains "reads the enterprise repository" "${got}" "vega-backend-ee"
got="$(get_release_manifest_release_image_field "${MANIFEST}" bkn-foundry 0.1.3 vega-backend registry)"
contains "reads the enterprise registry" "${got}" "openbkn-ai-ee"

# No image block means no override — every other release must keep pulling the
# community image, which is the whole point of doing this per release.
got="$(get_release_manifest_release_image_field "${MANIFEST}" bkn-foundry 0.1.3 bkn-safe repository)"
if [[ -z "${got}" ]]; then ok; else fail "release without an image block must yield nothing, got [${got}]"; fi

# The block is optional per field: a manifest may pin the repository and let the
# chart's own tag stand.
got="$(get_release_manifest_release_image_field "${MANIFEST}" bkn-foundry 0.1.3 vega-backend tag)"
if [[ -z "${got}" ]]; then ok; else fail "absent image.tag must yield nothing, got [${got}]"; fi
got="$(get_release_manifest_release_image_field "${MANIFEST}" bkn-foundry 0.1.3 agent-observability tag)"
contains "reads image.tag when present" "${got}" "0.1.3-main.ddd"

# A release's own keys must never be read as image keys: version sits at the
# release level and would otherwise leak in through a loose match.
got="$(get_release_manifest_release_image_field "${MANIFEST}" bkn-foundry 0.1.3 vega-backend version)"
if [[ -z "${got}" ]]; then ok; else fail "release-level keys must not resolve as image keys, got [${got}]"; fi

# The image block of one release must not bleed into the next.
got="$(get_release_manifest_release_image_field "${MANIFEST}" bkn-foundry 0.1.3 agent-observability repository)"
contains "each release reads its own image" "${got}" "agent-observability-ee"
not_contains "does not read the previous release's image" "${got}" "vega-backend-ee"

# The existing readers must still see what they always did.
got="$(get_release_manifest_release_chart_name "${MANIFEST}" bkn-foundry 0.1.3 vega-backend)"
contains "chart name still resolves" "${got}" "vega-backend"
got="$(get_release_manifest_release_version "${MANIFEST}" bkn-foundry 0.1.3 vega-backend)"
contains "chart version still resolves" "${got}" "0.1.3-main.bbb"

if [[ ${FAILED} -gt 0 ]]; then
    echo "openbkn_manifest_image_test: FAILED (${FAILED} of $((PASS + FAILED)))"
    exit 1
fi
echo "openbkn_manifest_image_test: all ${PASS} checks passed"
