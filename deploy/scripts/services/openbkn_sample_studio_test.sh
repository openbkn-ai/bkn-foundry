#!/usr/bin/env bash
# Product installation supplies the sample service compatibility facts and URL.
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
fixture_backend_version="0.2.0-main.20261009001912.sha628dd24"
fixture_releases=$'bkn-backend\nvega-backend\nagent-operator-integration'
_openbkn_resolve_release_version() { printf '%s\n' "${fixture_backend_version}"; }
_openbkn_release_names() { printf '%s\n' "${fixture_releases}"; }
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

contains "platform.version=0.2.0" && ok || fail "backend version not normalized"
contains "platform.capabilities[0]=vega.catalog" && ok || fail "catalog capability missing"
contains "platform.capabilities[4]=execution.skill" && ok || fail "skill capability missing"

fixture_releases='bkn-backend'
_openbkn_release_extra_sets bkn-sample-studio openbkn
contains "platform.capabilities[0]=knowledge-network.import.kn-json" && ok || fail "backend import missing"
[[ "${CORE_RELEASE_EXTRA_SET_STRINGS[*]}" != *"execution.function"* ]] && ok || fail "absent execution advertised"
[[ "${CORE_RELEASE_EXTRA_SET_STRINGS[*]}" != *"vega.catalog"* ]] && ok || fail "absent catalog advertised"

fixture_backend_version='main'
_openbkn_release_extra_sets bkn-sample-studio openbkn
[[ "${CORE_RELEASE_EXTRA_SET_STRINGS[*]}" != *"platform.version="* ]] && ok || fail "unresolved version advertised"

fixture_backend_version='0.1.5'
_openbkn_release_extra_sets bkn-sample-studio openbkn
contains "platform.version=0.1.5" && ok || fail "legacy version changed"
[[ "${CORE_RELEASE_EXTRA_SET_STRINGS[*]}" != *"platform.capabilities"* ]] && ok || fail "legacy capabilities advertised"

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
CORE_SET_VALUES=('platform.version=0.2.1')
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
    [[ "${captured_helm_args[*]}" == *"platform.version=0.2.0"* ]] && ok || fail "${path}: missing resolved platform version"
    [[ "${captured_helm_args[*]}" == *"platform.capabilities[4]=execution.skill"* ]] && ok || fail "${path}: missing capabilities"
    [[ "${captured_helm_args[*]}" == *"platform.version=0.2.0"*"platform.version=0.2.1"* ]] && ok || fail "${path}: explicit override must win"
done
rm -f "${fixture_manifest}" "${cfg}"
echo "${PASS} passed"
exit "${ONE_FAILED}"
