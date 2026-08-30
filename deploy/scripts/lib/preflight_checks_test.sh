#!/usr/bin/env bash
# Lightweight tests for preflight helpers (no cluster required)
set -euo pipefail
ONE_FAILED=0
fail() { echo "FAIL: $*"; ONE_FAILED=1; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=../lib/common.sh
source "${SCRIPT_DIR}/scripts/lib/common.sh"
# shellcheck source=../services/k8s.sh
source "${SCRIPT_DIR}/scripts/services/k8s.sh"
# shellcheck source=../services/k3s.sh
source "${SCRIPT_DIR}/scripts/services/k3s.sh"
# shellcheck source=./preflight_checks.sh
source "${SCRIPT_DIR}/scripts/lib/preflight_checks.sh"

# --- bkn_normalize_kube_distro (from common.sh) ---
[[ "$(bkn_normalize_kube_distro k8s)" == "kubeadm" ]] || fail "k8s -> kubeadm"
[[ "$(bkn_normalize_kube_distro kubeadm)" == "kubeadm" ]] || fail "kubeadm alias"
[[ "$(bkn_normalize_kube_distro k3s)" == "k3s" ]] || fail "k3s"
[[ "$(bkn_normalize_kube_distro "")" == "kubeadm" ]] || fail "empty -> kubeadm default (k8s)"

# --- Test resolve minor ---
out="$(PREFLIGHT_K8S_APT_MINOR=  preflight_resolve_k8s_apt_minor)"
[[ "${out}" =~ ^v[0-9]+\.[0-9]+$ ]] || fail "resolve default should be vM.N, got ${out}"

# --- Test JSON emit (empty) ---
PREFLIGHT_JSON_OK=()
PREFLIGHT_JSON_WARN=()
PREFLIGHT_JSON_FAIL=()
PREFLIGHT_JSON_FIXED=()
PREFLIGHT_JSON_DECLINED=()
json="$(emit_preflight_json 2>/dev/null)"
if command -v python3 &>/dev/null; then
  echo "$json" | python3 -c "import json,sys; d=json.load(sys.stdin); assert not d['ok'] and not d['warn'] and not d['fail']" || fail "empty json roundtrip"
else
  echo "skip: python3 not in PATH"
fi

# --- Onboard helpers: byte-compile (syntax-only; matches CPython 3.6+ subset) ---
ONBOARD_PY=(
  "${SCRIPT_DIR}/scripts/lib/onboard_patch_bkn_cm.py"
  "${SCRIPT_DIR}/scripts/lib/onboard_apply_config.py"
)
_onboard_py_compile() {
  local py="$1"
  echo "== onboard py_compile: ${py} ($("${py}" -V 2>&1)) =="
  "${py}" -m py_compile "${ONBOARD_PY[@]}" || fail "py_compile failed for ${py}"
}
if command -v python3 &>/dev/null; then
  _onboard_py_compile python3
  for py in ${EXTRA_PYTHONS:-}; do
    [[ -z "${py}" ]] && continue
    if command -v "${py}" &>/dev/null; then
      _onboard_py_compile "${py}"
    else
      echo "(skip: ${py} not on PATH)" >&2
    fi
  done
else
  echo "skip: onboard py_compile (python3 not in PATH)"
fi

# --- Test confirm: assume yes ---
PREFLIGHT_ASSUME_YES=true PREFLIGHT_ASSUME_NO=false preflight_confirm_fix "t" "a" "r" && true || fail "expect yes with ASSUME_YES"
PREFLIGHT_ASSUME_YES=false
PREFLIGHT_ASSUME_NO=true preflight_confirm_fix "t" "a" "r" && fail "expect no with ASSUME_NO" || true

# --- Test PREFLIGHT_FIX_ALLOW allowlist ---
PREFLIGHT_ASSUME_NO=false
PREFLIGHT_FIX_ALLOW="|t|"
PREFLIGHT_ASSUME_YES=false preflight_confirm_fix "t" "a" "r" && true || fail "allowlist t"
PREFLIGHT_FIX_ALLOW="|other|"
PREFLIGHT_ASSUME_NO=false PREFLIGHT_ASSUME_YES=false preflight_confirm_fix "t" "a" "r" && fail "not in allowlist" || true
PREFLIGHT_FIX_ALLOW=""

# --- Node floor: full version, not just the major ---
# 22.0.0 clears a `>= 22` test and still cannot install the CLI: npm resolves
# past a release whose `engines` the runtime misses and installs an older one
# without an error, which is how a deploy ends up running a stale openbkn while
# every check reports green.
[[ "${OPENBKN_NODE_MIN_SEMVER}" == "22.19.0" ]] || fail "node floor is ${OPENBKN_NODE_MIN_SEMVER}, expected 22.19.0"

bkn_semver_ge "22.19.0" "${OPENBKN_NODE_MIN_SEMVER}" || fail "22.19.0 must satisfy the floor"
bkn_semver_ge "22.22.0" "${OPENBKN_NODE_MIN_SEMVER}" || fail "22.22.0 must satisfy the floor"
bkn_semver_ge "24.15.0" "${OPENBKN_NODE_MIN_SEMVER}" || fail "24.15.0 must satisfy the floor"
bkn_semver_ge "22.11.0" "${OPENBKN_NODE_MIN_SEMVER}" && fail "22.11.0 must NOT satisfy the floor (major-only check let this through)"
bkn_semver_ge "22.0.0" "${OPENBKN_NODE_MIN_SEMVER}" && fail "22.0.0 must NOT satisfy the floor"
bkn_semver_ge "20.19.0" "${OPENBKN_NODE_MIN_SEMVER}" && fail "20.19.0 must NOT satisfy the floor"

# preflight_node_ok reads the running node, so drive it through a stub.
_node_stub_dir="$(mktemp -d)"
_stub_node() {
  printf '#!/usr/bin/env bash\necho "v%s"\n' "$1" > "${_node_stub_dir}/node"
  chmod +x "${_node_stub_dir}/node"
}
_stub_node "22.11.0"
PATH="${_node_stub_dir}:${PATH}" preflight_node_ok && fail "preflight_node_ok accepted 22.11.0" || true
_stub_node "22.19.0"
PATH="${_node_stub_dir}:${PATH}" preflight_node_ok || fail "preflight_node_ok rejected 22.19.0"
rm -rf "${_node_stub_dir}"

# The floor is a full version, so it must never reach an arithmetic test:
# `[[ $(( 10#$m )) -ge 22.19.0 ]]` is a runtime error, not a false result, and
# every caller that compared a major had to move to preflight_node_ok.
_pf="${SCRIPT_DIR}/scripts/lib/preflight_checks.sh"
grep -nE '\$\(\(.*\)\).*(-ge|-lt|-gt|-le) *\$\{PREFLIGHT_OPENBKN_MIN_NODE\}' "${_pf}" \
  && fail "preflight_checks.sh compares the Node floor arithmetically (it is a full version)" || true

# A bare `npm i -g` reinstalls whatever the global already resolves to.
grep -nE 'npm (i|install) -g @openbkn/bkn-sdk([^@]|$)' "${_pf}" \
  && fail "preflight_checks.sh installs @openbkn/bkn-sdk without @latest" || true

# The onboard script must enforce the same floor and pin the CLI install.
_onboard="${SCRIPT_DIR}/onboard.sh"
grep -q 'ONBOARD_MIN_NODE="\${ONBOARD_MIN_NODE:-\${OPENBKN_NODE_MIN_SEMVER}}"' "${_onboard}" \
  || fail "onboard.sh does not take its Node floor from OPENBKN_NODE_MIN_SEMVER"
grep -qE 'npm i -g @openbkn/bkn-sdk([^@]|$)' "${_onboard}" \
  && fail "onboard.sh installs @openbkn/bkn-sdk without @latest (a stale global blocks the upgrade)" || true

if [[ ${ONE_FAILED} -ne 0 ]]; then
  exit 1
fi
echo "OK preflight_checks_test.sh"
