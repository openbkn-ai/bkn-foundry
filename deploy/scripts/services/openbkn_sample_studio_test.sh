#!/usr/bin/env bash
# Verify the existing sample service URL and explicit Helm configuration wiring.
set -uo pipefail

ONE_FAILED=0
PASS=0
fail() { echo "FAIL: $*"; ONE_FAILED=1; }
ok() { PASS=$((PASS + 1)); }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=../lib/common.sh
source "${SCRIPT_DIR}/scripts/lib/common.sh"
# shellcheck source=../services/openbkn.sh
source "${SCRIPT_DIR}/scripts/services/openbkn.sh"

cfg="$(mktemp)"
cat > "${cfg}" <<'EOF'
accessAddress:
  host: gateway.example
  port: 443
  scheme: https
  path: /
EOF
CONFIG_YAML_PATH="${cfg}"
contains() {
    local item
    for item in "${CORE_RELEASE_EXTRA_SET_STRINGS[@]}"; do
        [[ "${item}" == "$1" ]] && return 0
    done
    return 1
}
_openbkn_release_extra_sets bkn-sample-studio openbkn
if contains "baseURL=https://gateway.example"; then
    ok
else
    fail "gateway url: got[${CORE_RELEASE_EXTRA_SET_STRINGS[*]}]"
fi

printf 'accessAddress:\n  scheme: https\n' > "${cfg}"
_openbkn_release_extra_sets bkn-sample-studio openbkn
if ! contains "baseURL=" && [[ "${CORE_RELEASE_EXTRA_SET_STRINGS[*]}" != *"baseURL="* ]]; then
    ok
else
    fail "missing host should not set baseURL: got[${CORE_RELEASE_EXTRA_SET_STRINGS[*]}]"
fi

[[ "${CORE_RELEASE_EXTRA_SET_STRINGS[*]}" != *"platform."* ]] && ok || fail "compatibility must not be inferred from releases"

# Exercise both installation paths with a real product manifest, capturing Helm
# writes so the test needs no cluster, registry or credentials.
source "${SCRIPT_DIR}/scripts/services/openbkn.sh"
fixture_manifest="$(mktemp)"
cat > "${fixture_manifest}" <<'EOF'
apiVersion: deploy.openbkn.ai/v1alpha1
kind: VersionSet
product: bkn-foundry
version: test
releases:
  bkn-backend:
    chart: bkn-backend
    version: 0.2.0-main.20261009001912.sha628dd24
  vega-backend:
    chart: vega-backend
    version: 0.2.0-main.20261009001912.sha628dd24
  agent-operator-integration:
    chart: agent-operator-integration
    version: 0.2.0-main.20261009001912.sha628dd24
  bkn-sample-studio:
    chart: bkn-sample-studio
    version: 0.1.0-main.20261011025845.sha600e202
EOF
CORE_VERSION_MANIFEST_FILE="${fixture_manifest}"
HELM_CHART_VERSION='test'
CORE_SET_VALUES=()
cat > "${cfg}" <<'EOF'
accessAddress:
  host: gateway.example
  port: 443
  scheme: https
  path: /
EOF
_openbkn_should_skip_upgrade() { return 1; }
find_cached_chart_tgz_by_version() { echo '/tmp/sample-chart.tgz'; }
helm() { return 1; }
_openbkn_helm_upgrade_release() { shift 2; captured_helm_args=("$@"); }
for path in local repo; do
    if [[ "${path}" == local ]]; then
        _install_openbkn_release_local bkn-sample-studio /tmp openbkn
    else
        _install_openbkn_release_repo bkn-sample-studio openbkn openbkn 0.1.0-main.20261011025845.sha600e202
    fi
    [[ "${captured_helm_args[*]}" == *"baseURL=https://gateway.example"* ]] && ok || fail "${path}: missing gateway URL"
    [[ "${captured_helm_args[*]}" != *"platform."* ]] && ok || fail "${path}: compatibility must not be inferred"

    # Existing explicit --set values pass through unchanged. No new declaration
    # protocol or deployment-derived compatibility facts are introduced.
    CORE_SET_VALUES=('platform.version=0.2.1' 'platform.capabilities[0]=vega.catalog')
    if [[ "${path}" == local ]]; then
        _install_openbkn_release_local bkn-sample-studio /tmp openbkn
    else
        _install_openbkn_release_repo bkn-sample-studio openbkn openbkn 0.1.0-main.20261011025845.sha600e202
    fi
    [[ "${captured_helm_args[*]}" == *"platform.version=0.2.1"* ]] && ok || fail "${path}: explicit version lost"
    [[ "${captured_helm_args[*]}" == *"platform.capabilities[0]=vega.catalog"* ]] && ok || fail "${path}: explicit capability lost"
    [[ "${captured_helm_args[*]}" != *"--set-string platform."* ]] && ok || fail "${path}: platform values must only use the explicit --set path"
    CORE_SET_VALUES=()
done
rm -f "${fixture_manifest}" "${cfg}"
echo "${PASS} passed"
exit "${ONE_FAILED}"
