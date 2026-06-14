#!/usr/bin/env bash
# BKN Foundry — onboard: register models, BKN, rollout (run after `deploy.sh` install)
# Requires: openbkn, kubectl, python3, PyYAML (pip3 install pyyaml) for --config; interactive is lighter.
# Run from the deploy/ directory (symmetric with preflight.sh).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Auto-migrate legacy ~/.kweaver-ai to ~/.openbkn-ai (one-time, when target absent).
if [[ -d "${HOME}/.kweaver-ai" && ! -e "${HOME}/.openbkn-ai" ]]; then
    if mv "${HOME}/.kweaver-ai" "${HOME}/.openbkn-ai" 2>/dev/null; then
        echo "[migrate] moved ${HOME}/.kweaver-ai -> ${HOME}/.openbkn-ai" >&2
    else
        echo "[migrate][warn] failed to move ${HOME}/.kweaver-ai -> ${HOME}/.openbkn-ai" >&2
    fi
fi

# Same as deploy.sh: generated install config lives under $HOME/.openbkn-ai/config.yaml.
# Prefer it when CONFIG_YAML_PATH is unset so accessAddress matches the machine that ran deploy
# (vendored deploy/conf/config.yaml is only a template). Legacy ~/.kweaver-ai is honored as a fallback
# when the auto-migration above could not move the directory (e.g. perms).
if [[ -z "${CONFIG_YAML_PATH:-}" ]]; then
    for _ob_rt in "${HOME}/.openbkn-ai/config.yaml" "${HOME}/.kweaver-ai/config.yaml"; do
        if [[ -f "${_ob_rt}" ]]; then
            export CONFIG_YAML_PATH="${_ob_rt}"
            break
        fi
    done
    unset _ob_rt
fi
# shellcheck source=scripts/lib/common.sh
source "${SCRIPT_DIR}/scripts/lib/common.sh"

# Linux: deploy.sh persists accessAddress / depServices to $HOME/.openbkn-ai/config.yaml of the user that
# ran it (root when invoked via sudo). When onboard runs as a non-root user without that file, it falls
# back to the vendored deploy/conf/config.yaml template — accessAddress diverges from deploy. Hint the
# operator. /root/.openbkn-ai/config.yaml cannot be stat'd from a regular shell (perm 700), so we trigger
# whenever the current user lacks the runtime yaml. Skipped on macOS (kind dev path) or when silenced.
if [[ "$(uname -s 2>/dev/null || true)" != "Darwin" ]] \
        && [[ "${EUID:-$(id -u)}" -ne 0 ]] \
        && [[ -z "${ONBOARD_SUDO_HINT_DISABLED:-}" ]] \
        && [[ ! -f "${HOME}/.openbkn-ai/config.yaml" ]] \
        && [[ ! -f "${HOME}/.kweaver-ai/config.yaml" ]] \
        && [[ -z "${CONFIG_YAML_PATH:-}" ]]; then
    printf '\033[0;33m[onboard][hint] No %s found for user %s.\n' "${HOME}/.openbkn-ai/config.yaml" "${USER:-$(id -un)}" >&2
    printf '              If deploy.sh ran via sudo, accessAddress/depServices live at /root/.openbkn-ai/config.yaml (root home, mode 700).\n' >&2
    printf '              Re-run onboard with sudo so it reads the same yaml:\n' >&2
    printf '                  sudo bash ./onboard.sh %s\n' "$*" >&2
    printf '              Or pin it explicitly:\n' >&2
    printf '                  sudo -E env CONFIG_YAML_PATH=/root/.openbkn-ai/config.yaml bash ./onboard.sh\n' >&2
    printf '              Otherwise onboard falls back to deploy/conf/config.yaml (template) and may show a different access URL.\n' >&2
    printf '              Set ONBOARD_SUDO_HINT_DISABLED=1 to silence.\033[0m\n' >&2
fi

# macOS kind dev: vendored deploy/conf lacks accessAddress; switch to mac-config when still using defaults.
_onboard_default_conf="${SCRIPT_DIR}/conf/config.yaml"
_onboard_default_home="${HOME}/.openbkn-ai/config.yaml"
_onboard_mac_cfg="${SCRIPT_DIR}/dev/conf/mac-config.yaml"
if [[ "$(uname -s 2>/dev/null || true)" == "Darwin" ]] && [[ -f "${_onboard_mac_cfg}" ]]; then
    if [[ "${CONFIG_YAML_PATH:-}" == "${_onboard_default_conf}" ]] || [[ "${CONFIG_YAML_PATH:-}" == "${_onboard_default_home}" ]]; then
        export CONFIG_YAML_PATH="${_onboard_mac_cfg}"
    fi
fi

# Top-level "namespace:" from CONFIG_YAML_PATH (Helm values); default NAMESPACE=openbkn unless set in env or yaml.
onboard_namespace_from_config_yaml() {
    local cfg="${CONFIG_YAML_PATH:-}"
    if [[ -z "${cfg}" ]] || [[ ! -f "${cfg}" ]]; then
        return 0
    fi
    awk '$1=="namespace:" {gsub(/['\''"]/,"",$2); print $2; exit}' "${cfg}" 2>/dev/null
}

# Apply helm values namespace unless NAMESPACE was set in the parent environment.
if [[ -z "${NAMESPACE+x}" ]] || [[ "${NAMESPACE}" == "openbkn" ]]; then
    _ns_cfg="$(onboard_namespace_from_config_yaml || true)"
    if [[ -n "${_ns_cfg}" ]]; then
        export NAMESPACE="${_ns_cfg}"
    else
        export NAMESPACE="${NAMESPACE:-openbkn}"
    fi
else
    export NAMESPACE="${NAMESPACE}"
fi
unset _ns_cfg

# shellcheck source=scripts/lib/onboard_models.sh
source "${SCRIPT_DIR}/scripts/lib/onboard_models.sh"
# shellcheck source=scripts/lib/onboard_oss_storage.sh
source "${SCRIPT_DIR}/scripts/lib/onboard_oss_storage.sh"

CONFIG_FILE=""
BKN_NAME=""
ENABLE_BKN_ONLY="false"
SKIP_BKN="false"
INTERACTIVE="true"
ONBOARD_ASSUME_YES="false"
ONBOARD_SKIP_TEST_USER="${ONBOARD_SKIP_TEST_USER:-${ONBOARD_SKIP_ISF_TEST_USER:-false}}"  # ONBOARD_SKIP_ISF_TEST_USER: deprecated alias
# Populated by onboard_kweaver_tls_insecure_args_to_array (usually empty or -k).
declare -a ONBOARD_TLS_INSECURE_ARGS=()

# openbkn auth: HTTP sign-in defaults (full install). Console account is usually  admin  /  openbkn  if not changed.
# Override in CI. Used when you press Enter at username/password prompts.
: "${ONBOARD_DEFAULT_KWEAVER_USER:=admin}"
: "${ONBOARD_DEFAULT_KWEAVER_PASSWORD:=openbkn}"

# First business user  test  (after  openbkn admin user create ) — platform default is 123456 until  reset-password;
# we set this for onboard default /  -y  / empty Enter. Override: ONBOARD_TEST_USER_PASSWORD; rename default: ONBOARD_DEFAULT_TEST_USER_PASSWORD
: "${ONBOARD_DEFAULT_TEST_USER_PASSWORD:=111111}"

# Same requirement as @openbkn/bkn-sdk on npm (node >= 22). https://www.npmjs.com/package/@openbkn/bkn-sdk
ONBOARD_MIN_NODE_MAJOR="${ONBOARD_MIN_NODE_MAJOR:-22}"

# Node/kweaver bootstrap prompts use the terminal (even with --config); -y skips those prompts; no TTY + no -y = error.
onboard_is_bootstrap_tty() {
    [[ -t 0 && -t 1 ]]
}

# shellcheck source=scripts/lib/onboard_test_user.sh
source "${SCRIPT_DIR}/scripts/lib/onboard_test_user.sh"
# shellcheck source=scripts/lib/onboard_report.sh
source "${SCRIPT_DIR}/scripts/lib/onboard_report.sh"

# Primary IPv4 of this host (for default BKN Foundry access URL). Override: ONBOARD_DEFAULT_ACCESS_IP=...
onboard_default_local_ipv4() {
    if [[ -n "${ONBOARD_DEFAULT_ACCESS_IP:-}" ]]; then
        echo "${ONBOARD_DEFAULT_ACCESS_IP}"
        return
    fi
    python3 -c "
import re
import socket
import subprocess
import sys

def main() -> None:
    for remote in ('8.8.8.8', '1.1.1.1'):
        try:
            s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
            s.settimeout(0.4)
            s.connect((remote, 80))
            print(s.getsockname()[0])
            s.close()
            return
        except Exception:
            pass
    try:
        out = subprocess.check_output(
            ['ip', '-4', 'route', 'get', '1.1.1.1'], text=True, stderr=subprocess.DEVNULL, timeout=2
        )
        m = re.search(r'\\bsrc (\\d+\\.\\d+\\.\\d+\\.\\d+)', out)
        if m:
            print(m.group(1))
            return
    except Exception:
        pass
    try:
        out = subprocess.check_output(
            ['ipconfig', 'getifaddr', 'en0'], text=True, stderr=subprocess.DEVNULL, timeout=2
        ).strip()
        if out and out != '0.0.0.0':
            print(out)
            return
    except Exception:
        pass
    print('127.0.0.1')

if __name__ == '__main__':
    main()
" 2>/dev/null || echo "127.0.0.1"
}

# Default access base for  openbkn auth login  (this machine, HTTPS + primary IPv4 unless overridden).
# Set ONBOARD_DEFAULT_ACCESS_BASE to a full URL to skip auto IP.
# Otherwise, when ONBOARD_SKIP_CONFIG_ACCESS_URL!=true, prefers accessAddress.host|port|scheme|path
# from CONFIG_YAML_PATH (same file as deploy.sh / Helm values; mac.sh sets CONFIG_YAML_PATH to
# deploy/dev/conf/mac-config.yaml).
onboard_default_access_base_url() {
    if [[ -n "${ONBOARD_DEFAULT_ACCESS_BASE:-}" ]]; then
        echo "${ONBOARD_DEFAULT_ACCESS_BASE%/}"
        return
    fi
    local _from_cfg=""
    if [[ "${ONBOARD_SKIP_CONFIG_ACCESS_URL:-false}" != "true" ]] && type get_access_address_base_url &>/dev/null; then
        _from_cfg="$(get_access_address_base_url 2>/dev/null || true)"
    fi
    if [[ -n "${_from_cfg}" ]]; then
        echo "${_from_cfg%/}"
        return
    fi
    local ip _scheme _port
    ip="$(onboard_default_local_ipv4)"
    _scheme="${ONBOARD_DEFAULT_ACCESS_SCHEME:-https}"
    _port="${ONBOARD_DEFAULT_ACCESS_PORT:-}"
    if [[ -n "${_port}" ]] && [[ "${_scheme}" =~ ^[Hh][Tt][Tt][Pp]$ ]] && [[ "${_port}" == "80" ]]; then
        _port=""
    fi
    if [[ -n "${_port}" ]] && [[ "${_scheme}" =~ ^[Hh][Tt][Tt][Pp][Ss]$ ]] && [[ "${_port}" == "443" ]]; then
        _port=""
    fi
    if [[ -n "${_port}" ]]; then
        echo "${_scheme}://${ip}:${_port}"
    else
        echo "${_scheme}://${ip}"
    fi
}

# openbkn / openbkn admin: optional --insecure/-k only for HTTPS URLs (self-signed dev certs).
# For plain http:// bases, unconditional -k has been observed to break some login flows (--no-auth)
# against HTTP-only ingress backends (404 Not Found). Also: never emit trailing whitespace from
# command substitution — Word-splitting turns it into an extra empty argv and confuses the CLI.
# Override: ONBOARD_FORCE_INSECURE_LOGIN=true forces -k even for HTTP (rare; debugging only).
# Populate global array ONBOARD_TLS_INSECURE_ARGS (empty or (-k)).
onboard_kweaver_tls_insecure_args_to_array() {
    ONBOARD_TLS_INSECURE_ARGS=()
    local _base="$1"
    if [[ "${ONBOARD_FORCE_INSECURE_LOGIN:-false}" == "true" ]]; then
        ONBOARD_TLS_INSECURE_ARGS=(-k)
        return 0
    fi
    case "${_base}" in
        https://*|HTTPS://*)
            ONBOARD_TLS_INSECURE_ARGS=(-k)
            ;;
        *)
            ;;
    esac
}

usage() {
    echo "Usage: sudo bash ./onboard.sh [options]   # Linux (matches sudo deploy.sh)"
    echo "       bash ./dev/mac.sh onboard           # macOS dev (kind path; no sudo)"
    echo "  Requires: Node 22+ (see @openbkn/bkn-sdk on npm), openbkn, kubectl, python3; run from deploy/"
    echo "  Config YAML: unset CONFIG_YAML_PATH and onboard uses \$HOME/.openbkn-ai/config.yaml when that file exists (same as deploy.sh); otherwise scripts/lib/common.sh default (deploy/conf/config.yaml)."
    echo "  Why sudo on Linux: deploy.sh runs as root and writes \$HOME/.openbkn-ai/config.yaml under /root/.openbkn-ai/ (mode 700); onboard.sh also writes \$HOME/.kweaver auth state. sudo keeps both pointing at the same root home (silence the startup hint with ONBOARD_SUDO_HINT_DISABLED=1; not needed on macOS dev)."
    echo "  (no flags)                Interactive: nvm+Node 22 and npm -g (Y/n) in your terminal, then models/BKN"
    echo "  -y, --yes                 Auto nvm+Node 22, npm -g, [test] user+roles (no Y/n)"
    echo "  --config=PATH            YAML: deploy/conf/models.yaml.example; model prompts off, but nvm/kweaver still Y/n in a TTY (use -y to skip those asks)"
    echo "  --skip-test-user         Do not offer: openbkn admin user test + all roles"
    echo ""
    echo "  ADP impex / auth:  openbkn call  uses ~/.kweaver from  openbkn auth login ."
    echo "    - Full install:  openbkn admin  / console  admin  for user ops. ADP impex uses user  test  with all  role list"
    echo "      roles (typically three business admins), then  openbkn auth  as  test .  -y  uses password  ${ONBOARD_DEFAULT_TEST_USER_PASSWORD:-111111}  (override: ONBOARD_TEST_USER_PASSWORD) ."
    echo "    - Minimum install:  openbkn auth login  only; openbkn admin is not required."
    echo "  --namespace=NS           Override K8s namespace (default: NAMESPACE env, else namespace: in CONFIG_YAML_PATH, else openbkn)"
    echo "  --enable-bkn-search      Only patch bkn/ontology ConfigMaps and rollout"
    echo "  --bkn-embedding-name=X   Required with --enable-bkn-search (registered model_name)"
    echo "  --skip-bkn               With --config: register models but skip BKN + rollout"
    echo "  -h, --help"
    echo ""
    echo "  Environment: ONBOARD_SKIP_NODE_INSTALL=true  skip nvm in onboard (fail if Node < ${ONBOARD_MIN_NODE_MAJOR})"
    echo "                ONBOARD_SKIP_KWEAVER_INSTALL=true  never run npm -g for openbkn in onboard"
    echo "                ONBOARD_SKIP_KWEAVER_ADMIN_INSTALL=true  do not auto/offer  npm -g  openbkn admin  (also skipped with  -y )"
    echo "                ONBOARD_SKIP_TEST_USER=true  same as --skip-test-user"
    echo "                ONBOARD_TEST_USER_PASSWORD=...  override default password for  test  (default: ONBOARD_DEFAULT_TEST_USER_PASSWORD, built-in 111111)"
    echo "                ONBOARD_DEFAULT_TEST_USER_PASSWORD=...  first-user  test  password (default 111111;  -y  non-interactive)"
    echo "                ONBOARD_KWEAVER_IMPEX_NO_RELLOGIN=1  skip  openbkn auth  as  test  before impex (use current openbkn session)"
    echo "                ONBOARD_NO_COMPLETION_REPORT=1  do not print the English completion report at the end"
    echo "                ONBOARD_FORCE_INSECURE_LOGIN=true  always pass -k (--insecure) to openbkn auth login (even for http:// bases; default false)"
    echo "                ONBOARD_SKIP_CONFIG_ACCESS_URL=true  do not derive default URL from CONFIG_YAML_PATH accessAddress"
    echo "  Default BKN Foundry access URL (openbkn auth): accessAddress in CONFIG_YAML_PATH when present;"
    echo "                on macOS, if CONFIG_YAML_PATH is still deploy/conf/config.yaml (~/.openbkn-ai not used yet),"
    echo "                onboard uses deploy/dev/conf/mac-config.yaml when that file exists (same as mac.sh)."
    echo "                Else host primary IPv4 + ONBOARD_DEFAULT_ACCESS_SCHEME (https by default)."
    echo "                Set ONBOARD_DEFAULT_ACCESS_BASE to force a URL; ONBOARD_DEFAULT_ACCESS_PORT / SCHEME override fallback IP path."
    echo "  openbkn auth: you confirm URL. Full install: HTTP defaults user=admin pass=openbkn (if still default); override with ONBOARD_DEFAULT_KWEAVER_USER / _PASSWORD. Enter keeps defaults. Minimum: default --no-auth; Enter to accept."
    echo "  openbkn admin shares the openbkn session (no separate login). First login forces a password change; onboard clears it via /api/safe/v1/auth/change-password (or do it once: openbkn auth change-password <url> -u admin). Then openbkn re-logs in as user test for impex and model steps."
    echo "  Node: onboard is not a login shell — it auto-loads nvm/fnm/asdf/Volta and Homebrew paths so an already-configured Node 22+ is found without re-asking. ONBOARD_SKIP_NVM_INIT=true skips that; ONBOARD_NVM_VERSION=22 (default) is used after  nvm.sh  load."
    echo "  (preflight on the server: sudo bash ./preflight.sh --fix still optional; this script can install Node in your *user* account via nvm.)"
}

for _ob_arg in "$@"; do
    case "${_ob_arg}" in
        -h | --help) usage; exit 0 ;;
        --config=*) INTERACTIVE="false" ;;
        -y | --yes) ONBOARD_ASSUME_YES="true" ;;
        --skip-test-user|--skip-isf-test-user) ONBOARD_SKIP_TEST_USER="true" ;;
    esac
done

onboard_node_major() {
    if ! command -v node &>/dev/null; then
        echo 0
        return
    fi
    local v
    v="$(node -v 2>/dev/null)"
    v="${v#v}"
    v="${v%%.*}"
    if [[ "${v}" =~ ^[0-9]+$ ]]; then
        echo "${v}"
    else
        echo 0
    fi
}

# This script is not a login shell: ~/.zshrc / .bashrc are not sourced, so nvm's node is often missing
# from PATH even when the user already "configured" it in a terminal. Load common version managers
# and standard locations before we decide to prompt for nvm install.
onboard_bootstrap_node_path() {
    if [[ "${ONBOARD_SKIP_NVM_INIT:-false}" == "true" ]]; then
        return 0
    fi
    # Volta
    if [[ -d "${HOME}/.volta/bin" ]]; then
        case ":${PATH}:" in *":${HOME}/.volta/bin:"*) ;; *) export PATH="${HOME}/.volta/bin:${PATH}" ;; esac
    fi
    # asdf
    if [[ -f "${HOME}/.asdf/asdf.sh" ]]; then
        # shellcheck source=/dev/null
        . "${HOME}/.asdf/asdf.sh" 2>/dev/null && hash -r 2>/dev/null || true
    fi
    # fnm
    if command -v fnm &>/dev/null; then
        # shellcheck disable=SC1091
        eval "$(fnm env 2>/dev/null)" && hash -r 2>/dev/null || true
    fi
    # nvm (most common)
    export NVM_DIR="${NVM_DIR:-$HOME/.nvm}"
    if [[ -s "${NVM_DIR}/nvm.sh" ]]; then
        # shellcheck source=/dev/null
        if . "${NVM_DIR}/nvm.sh" 2>/dev/null; then
            nvm use "${ONBOARD_NVM_VERSION:-22}" 2>/dev/null || nvm use default 2>/dev/null || nvm use node 2>/dev/null || true
            hash -r 2>/dev/null || true
        fi
    fi
    # Homebrew (macOS): node@22 is often in PATH once these dirs are prepended
    for _nd in /opt/homebrew/bin /usr/local/bin; do
        if [[ -x "${_nd}/node" ]]; then
            case ":${PATH}:" in *":${_nd}:"*) ;; *) export PATH="${_nd}:${PATH}" ;; esac
        fi
    done
}

# Install nvm + Node 22 in the current user (no sudo; same idea as preflight's node-22 fix, user-local).
onboard_install_node22_nvm() {
    if ! command -v curl &>/dev/null; then
        log_error "curl is required to install nvm. Install curl, or install Node ${ONBOARD_MIN_NODE_MAJOR}+ from https://nodejs.org/"
        return 1
    fi
    if ! command -v bash &>/dev/null; then
        log_error "bash is required to run the nvm installer."
        return 1
    fi
    export NVM_DIR="${NVM_DIR:-$HOME/.nvm}"
    if [[ ! -s "${NVM_DIR}/nvm.sh" ]]; then
        log_info "Installing nvm into ${NVM_DIR}…"
        if ! curl -fsSL https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.1/install.sh | bash; then
            log_error "nvm install.sh failed (network, proxy, or missing deps). See https://github.com/nvm-sh/nvm"
            return 1
        fi
    fi
    # shellcheck source=/dev/null
    if ! . "${NVM_DIR}/nvm.sh"; then
        log_error "Could not source \${NVM_DIR}/nvm.sh (${NVM_DIR})"
        return 1
    fi
    if ! nvm install 22; then
        log_error "nvm install 22 failed"
        return 1
    fi
    nvm use 22
    nvm alias default 22 2>/dev/null || true
    hash -r 2>/dev/null || true
    return 0
}

# If not sudo bash ./preflight.sh --fix, still help: offer (or with -y, run) nvm+Node 22 in this user.
onboard_ensure_node_22() {
    local mj
    onboard_bootstrap_node_path
    mj="$(onboard_node_major)"
    if command -v node &>/dev/null && [[ -n "${mj}" && $(( 10#${mj} )) -ge ${ONBOARD_MIN_NODE_MAJOR} ]]; then
        log_info "Using $(node -v) ($(command -v node))"
        return 0
    fi

    if [[ "${ONBOARD_SKIP_NODE_INSTALL:-false}" == "true" ]]; then
        log_error "Node is $(node -v 2>/dev/null || echo missing) but Node.js ${ONBOARD_MIN_NODE_MAJOR}+ is required. Unset ONBOARD_SKIP_NODE_INSTALL or install Node manually."
        exit 1
    fi

    # Interactive on a TTY: always ask, including when --config is set. No TTY: must pass -y to auto-install.
    if [[ "${ONBOARD_ASSUME_YES}" == "true" ]]; then
        log_info "Node ${ONBOARD_MIN_NODE_MAJOR}+ not active; installing via nvm (-y)…"
    elif onboard_is_bootstrap_tty; then
        echo ""
        read -r -p "Node.js ${ONBOARD_MIN_NODE_MAJOR}+ is required for openbkn/onboard. Install nvm and Node 22 in this user account now? [Y/n]: " _obn
        if [[ "${_obn}" =~ ^[Nn] ]]; then
            log_error "Install Node ${ONBOARD_MIN_NODE_MAJOR}+ (e.g. nvm install 22), or use another machine with Node 22+ on PATH, or run: sudo bash ./preflight.sh --fix on the host where you need system-wide Node."
            exit 1
        fi
    else
        log_error "Node ${ONBOARD_MIN_NODE_MAJOR}+ required (or missing). In a real terminal you get a Y/n prompt; without a TTY pass  $0 -y  (e.g. CI), or install Node / nvm first. Or: sudo bash ./preflight.sh --fix (onboard-tooling) on a server."
        exit 1
    fi

    if ! onboard_install_node22_nvm; then
        exit 1
    fi
    mj="$(onboard_node_major)"
    if ! command -v node &>/dev/null || [[ -z "${mj}" || $(( 10#${mj} )) -lt ${ONBOARD_MIN_NODE_MAJOR} ]]; then
        log_error "Node is still < ${ONBOARD_MIN_NODE_MAJOR} in this process. In a new terminal run:  source \"\$NVM_DIR/nvm.sh\" && nvm use 22  then:  $0  again."
        exit 1
    fi
    # After a fresh nvm install in this function, the success message is similar
    if command -v node &>/dev/null; then
        log_info "Using Node $(node -v) ($(command -v node))"
    fi
}

onboard_ensure_kweaver_cli() {
    if command -v openbkn &>/dev/null; then
        return 0
    fi
    if ! command -v npm &>/dev/null; then
        log_error "openbkn not in PATH and npm not found. With nvm+Node, npm should exist; re-open a shell and re-run."
        exit 1
    fi
    if [[ "${ONBOARD_SKIP_KWEAVER_INSTALL:-false}" == "true" ]]; then
        log_error "openbkn not in PATH. Install: npm i -g @openbkn/bkn-sdk@alpha  (or unset ONBOARD_SKIP_KWEAVER_INSTALL to allow this script to run npm -g.)"
        exit 1
    fi
    if [[ "${ONBOARD_ASSUME_YES}" == "true" ]]; then
        log_info "Installing @openbkn/bkn-sdk globally (-y)…"
    elif onboard_is_bootstrap_tty; then
        echo ""
        read -r -p "openbkn CLI not in PATH. Install @openbkn/bkn-sdk globally now? (npm i -g) [Y/n]: " _obk
        if [[ "${_obk}" =~ ^[Nn] ]]; then
            log_error "openbkn is required. Run:  npm i -g @openbkn/bkn-sdk@alpha"
            exit 1
        fi
    else
        log_error "openbkn not in PATH. In a TTY you get a Y/n prompt; without a TTY use  $0 -y  or install: npm i -g @openbkn/bkn-sdk@alpha"
        exit 1
    fi
    if ! npm i -g @openbkn/bkn-sdk@alpha; then
        log_error "npm i -g @openbkn/bkn-sdk@alpha failed. Check registry/proxy, or EACCES (avoid sudo; use nvm user prefix.)"
        exit 1
    fi
    hash -r 2>/dev/null || true
    if ! command -v openbkn &>/dev/null; then
        log_error "openbkn still not on PATH. Add npm global bin to PATH, e.g.:  export PATH=\"\$(npm config get prefix 2>/dev/null)/bin:\$PATH\""
        exit 1
    fi
    log_info "openbkn: $(openbkn --version 2>/dev/null | head -1)"
}

# Same shell as nvm/node: global CLIs (openbkn, openbkn admin) live under $(npm config get prefix)/bin — prepend so a just-installed -g is visible.
onboard_prepend_npm_global_bin_to_path() {
    local pfx
    pfx="$(npm config get prefix 2>/dev/null)" || true
    if [[ -n "${pfx}" && -d "${pfx}/bin" ]]; then
        case ":${PATH}:" in
            *":${pfx}/bin:"*) ;;
            *) export PATH="${pfx}/bin${PATH:+:${PATH}}" ;;
        esac
    fi
    hash -r 2>/dev/null || true
}

onboard_ensure_node_22
onboard_ensure_kweaver_cli
if ! command -v kubectl &>/dev/null; then
    log_error "kubectl not found"
    exit 1
fi
if ! command -v python3 &>/dev/null; then
    log_error "python3 not found"
    exit 1
fi
# Verify (and best-effort install) yq OR PyYAML now, so we never get stuck
# halfway through onboard at the BKN ConfigMap patch step. Skip when the
# operator has explicitly opted out of BKN with --skip-bkn.
__onboard_skip_bkn_early=false
for __arg in "$@"; do
    case "${__arg}" in
        --skip-bkn) __onboard_skip_bkn_early=true ;;
    esac
done
if [[ "${__onboard_skip_bkn_early}" != "true" ]]; then
    if ! onboard_ensure_yaml_dep; then
        log_error "onboard.sh needs PyYAML or yq to patch the BKN ConfigMap. Install one of the commands above (or pass --skip-bkn if you really want to onboard without touching BKN), then re-run."
        exit 1
    fi
fi
unset __onboard_skip_bkn_early __arg

while [[ $# -gt 0 ]]; do
    case "$1" in
        -h | --help)
            usage
            exit 0
            ;;
        -y | --yes)
            ONBOARD_ASSUME_YES="true"
            shift
            ;;
        --skip-test-user|--skip-isf-test-user)
            ONBOARD_SKIP_TEST_USER="true"
            shift
            ;;
        --config=*)
            CONFIG_FILE="${1#*=}"
            INTERACTIVE="false"
            shift
            ;;
        --namespace=*)
            NAMESPACE="${1#*=}"
            shift
            ;;
        --bkn-embedding-name=*)
            BKN_NAME="${1#*=}"
            shift
            ;;
        --enable-bkn-search) ENABLE_BKN_ONLY="true"; shift ;;
        --skip-bkn)          SKIP_BKN="true"; shift ;;
        *)
            log_error "Unknown: $1"
            usage
            exit 1
            ;;
    esac
done

# Bash 3.2 (macOS) printf has no %q; single-quote each arg for safe copy-paste in logs.
onboard_argv_q() {
    local _a _esc _out=""
    for _a in "$@"; do
        _esc="${_a//\'/\'\\\'\'}"
        [[ -n "${_out}" ]] && _out+=" "
        _out+="'${_esc}'"
    done
    printf '%s' "${_out}"
}

# For [onboard] logs: absolute path + first line of --version (often semver only, e.g. 0.6.4).
onboard_kweaver_admin_version_summary() {
    local _bin _ver
    _bin="$(command -v openbkn 2>/dev/null)" || return 1
    _ver="$(openbkn admin --version 2>/dev/null | head -n1 | tr -d '\r')"
    _ver="${_ver//$'\n'/ }"
    [[ -z "${_ver}" ]] && _ver="?"
    printf '%s' "${_bin} (version ${_ver})"
}

# bkn-safe seeds the built-in admin (and reset users) with must_change_password
# set, so the first headless `openbkn auth login -u/-p` never completes: the login
# provider returns the change-password page instead of accepting the hydra login,
# and the device-code flow then hangs until it times out. Onboard always runs
# against a fresh first login, so clear the flag up front by bouncing the password
# through the tokenless self-service change-password endpoint (pw -> pw.heal ->
# pw). A successful self-service change clears must_change_password and leaves the
# effective password unchanged. A non-2xx first hop (typically 401) means the
# password is not the one we hold (operator already changed it) — leave the
# account alone and let the login proceed with whatever it is. Idempotent: on an
# already-cleared account it just changes the password to itself twice.
onboard_bkn_safe_clear_must_change() {
    local _account="$1" _pw="$2" _kurl="$3" _ep _tmp _c1 _c2
    local _ins=()
    [[ -z "${_account}" || -z "${_pw}" || -z "${_kurl}" ]] && return 0
    command -v curl &>/dev/null || return 0
    case "${_kurl}" in https://*) _ins=(-k);; esac
    _ep="${_kurl%/}/api/safe/v1/auth/change-password"
    _tmp="${_pw}.heal"
    _c1="$(curl -s "${_ins[@]}" -o /dev/null -w '%{http_code}' -X POST "${_ep}" \
        -H 'Content-Type: application/json' \
        -d "{\"account\":\"${_account}\",\"old_password\":\"${_pw}\",\"new_password\":\"${_tmp}\"}" 2>/dev/null)"
    if [[ "${_c1}" != 2* ]]; then
        [[ "${_c1}" == "401" ]] || onboard_log_info "openbkn auth: must-change heal probe for [${_account}] got http ${_c1} (skipping)"
        return 0
    fi
    _c2="$(curl -s "${_ins[@]}" -o /dev/null -w '%{http_code}' -X POST "${_ep}" \
        -H 'Content-Type: application/json' \
        -d "{\"account\":\"${_account}\",\"old_password\":\"${_tmp}\",\"new_password\":\"${_pw}\"}" 2>/dev/null)"
    if [[ "${_c2}" != 2* ]]; then
        log_warn "openbkn auth: [${_account}] password left at <pw>.heal (restore hop http ${_c2}); restore via ${_ep} old='<pw>.heal' new='<pw>'"
        return 1
    fi
    onboard_log_info "openbkn auth: cleared pending must-change-password for [${_account}] (password unchanged)"
    return 0
}

onboard_kweaver_auth_login_echo_cmd() {
    local _url="$1"
    shift
    onboard_log_info "Running: $(onboard_argv_q openbkn auth login "${_url}" "$@")"
}

# After access URL is chosen: bkn-safe → credential login (defaults admin / openbkn if unchanged); else minimum → --no-auth (Enter) or HTTP.
# Env: ONBOARD_DEFAULT_KWEAVER_USER, ONBOARD_DEFAULT_KWEAVER_PASSWORD, ONBOARD_ASSUME_YES (non-interactive: bkn-safe=credentials, min=--no-auth).
onboard_kweaver_auth_login_for_url() {
    local _kurl="$1"
    local _u _p _duser _dpass
    _duser="${ONBOARD_DEFAULT_KWEAVER_USER:-admin}"
    _dpass="${ONBOARD_DEFAULT_KWEAVER_PASSWORD:-openbkn}"
    onboard_kweaver_tls_insecure_args_to_array "${_kurl}"
    local _kv
    _kv="$(openbkn --version 2>/dev/null | grep -Eo '[vV]?[0-9]+\.[0-9]+\.[0-9]+' | tail -1 || true)"
    [[ -z "${_kv}" ]] && _kv="?"
    onboard_log_info "openbkn CLI: $(command -v openbkn 2>/dev/null || echo missing) (${_kv}) CONFIG_YAML_PATH=${CONFIG_YAML_PATH:-unset}"

    # bkn-safe (current auth stack): credential login via the
    # openbkn device-code flow (openbkn auth login -u/-p — no --http-signin). The
    # admin is seeded with a platform initial password (default "openbkn"); set
    # ONBOARD_DEFAULT_KWEAVER_PASSWORD if it has been changed. A first-login forced
    # password change must be completed once (browser or `openbkn auth change-password`)
    # before non-interactive -u/-p login can succeed.
    if type onboard_bkn_safe_detected &>/dev/null && onboard_bkn_safe_detected; then
        local _su _sp
        _su="${ONBOARD_DEFAULT_KWEAVER_USER:-admin}"
        _sp="${ONBOARD_DEFAULT_KWEAVER_PASSWORD:-openbkn}"
        if [[ "${ONBOARD_ASSUME_YES}" == "true" ]]; then
            onboard_log_info "openbkn auth: bkn-safe detected — credential device-flow login (-y): ${_su}"
            onboard_bkn_safe_clear_must_change "${_su}" "${_sp}" "${_kurl}" || true
            onboard_kweaver_auth_login_echo_cmd "${_kurl}" -u "${_su}" -p "***" "${ONBOARD_TLS_INSECURE_ARGS[@]+"${ONBOARD_TLS_INSECURE_ARGS[@]}"}"
            if ! openbkn auth login "${_kurl}" -u "${_su}" -p "${_sp}" "${ONBOARD_TLS_INSECURE_ARGS[@]+"${ONBOARD_TLS_INSECURE_ARGS[@]}"}" ; then
                return 1
            fi
            return 0
        fi
        read -r -p "  Username [Enter = ${_su}]: " _u
        _u="${_u:-${_su}}"
        read -r -s -p "  Password [Enter = ${_sp}] " _p
        echo
        _p="${_p:-${_sp}}"
        onboard_bkn_safe_clear_must_change "${_u}" "${_p}" "${_kurl}" || true
        onboard_kweaver_auth_login_echo_cmd "${_kurl}" -u "${_u}" -p "***" "${ONBOARD_TLS_INSECURE_ARGS[@]+"${ONBOARD_TLS_INSECURE_ARGS[@]}"}"
        if ! openbkn auth login "${_kurl}" -u "${_u}" -p "${_p}" "${ONBOARD_TLS_INSECURE_ARGS[@]+"${ONBOARD_TLS_INSECURE_ARGS[@]}"}" ; then
            return 1
        fi
        return 0
    fi

    if [[ "${ONBOARD_ASSUME_YES}" == "true" ]]; then
        onboard_log_info "openbkn auth: minimum install — --no-auth (default, -y)"
        onboard_kweaver_auth_login_echo_cmd "${_kurl}" --no-auth "${ONBOARD_TLS_INSECURE_ARGS[@]+"${ONBOARD_TLS_INSECURE_ARGS[@]}"}"
        if ! openbkn auth login "${_kurl}" --no-auth "${ONBOARD_TLS_INSECURE_ARGS[@]+"${ONBOARD_TLS_INSECURE_ARGS[@]}"}" ; then
            return 1
        fi
        return 0
    fi
    echo ""
    read -r -p "Minimum install: use --no-auth (typical) [Y/n] (Enter = Y): " _mna
    if [[ -z "${_mna}" || ! "${_mna}" =~ ^[Nn] ]]; then
        onboard_kweaver_auth_login_echo_cmd "${_kurl}" --no-auth "${ONBOARD_TLS_INSECURE_ARGS[@]+"${ONBOARD_TLS_INSECURE_ARGS[@]}"}"
        if ! openbkn auth login "${_kurl}" --no-auth "${ONBOARD_TLS_INSECURE_ARGS[@]+"${ONBOARD_TLS_INSECURE_ARGS[@]}"}" ; then
            return 1
        fi
        return 0
    fi
    read -r -p "  Username [Enter = ${_duser}]: " _u
    _u="${_u:-${_duser}}"
    read -r -s -p "  Password [Enter = ${_dpass} if default unchanged on console] " _p
    echo
    _p="${_p:-${_dpass}}"
    onboard_kweaver_auth_login_echo_cmd "${_kurl}" -u "${_u}" -p "***" "${ONBOARD_TLS_INSECURE_ARGS[@]+"${ONBOARD_TLS_INSECURE_ARGS[@]}"}"
    if ! openbkn auth login "${_kurl}" -u "${_u}" -p "${_p}" "${ONBOARD_TLS_INSECURE_ARGS[@]+"${ONBOARD_TLS_INSECURE_ARGS[@]}"}" ; then
        return 1
    fi
    return 0
}

# When openbkn bkn list fails, interactively let the user log in or retry; non-interactive (or -y) exits.
onboard_ensure_kweaver_auth() {
    while true; do
        if openbkn bkn list &>/dev/null; then
            return 0
        fi
        # -y (with or without --config): auto-login with defaults. Checked BEFORE the
        # non-interactive hard-exit below so `--config … -y` can authenticate unattended.
        if [[ "${ONBOARD_ASSUME_YES}" == "true" ]]; then
            _durl="$(onboard_default_access_base_url)"
            onboard_log_warn "openbkn bkn list failed (-y). Trying  openbkn auth login ${_durl}  with defaults…"
            if ! onboard_kweaver_auth_login_for_url "${_durl}"; then
                onboard_log_err "openbkn auth login failed. Set ONBOARD_DEFAULT_ACCESS_BASE / ONBOARD_DEFAULT_KWEAVER_USER / _PASSWORD, or run openbkn auth login manually, then re-run: $0 -y"
                exit 1
            fi
            if ! openbkn bkn list &>/dev/null; then
                onboard_log_err "openbkn bkn list still fails after auth login. Check the platform, then re-run: $0 -y"
                exit 1
            fi
            return 0
        fi
        if [[ "${INTERACTIVE}" != "true" ]]; then
            _durl="$(onboard_default_access_base_url)"
            onboard_kweaver_tls_insecure_args_to_array "${_durl}"
            onboard_log_err "openbkn bkn list failed. Run: $(onboard_argv_q openbkn auth login "${_durl}" "${ONBOARD_TLS_INSECURE_ARGS[@]+"${ONBOARD_TLS_INSECURE_ARGS[@]}"}") (or set ONBOARD_DEFAULT_ACCESS_BASE=...)"
            exit 1
        fi
        onboard_log_warn "openbkn bkn list failed (not logged in or platform unreachable)."
        echo ""
        echo "Choose:"
        echo "  1) Run login: URL (Enter = this host IP), then credential login or minimum (--no-auth) — see -h for defaults"
        echo "  2) Retry (after you ran login in another terminal)"
        echo "  3) Quit"
        read -r -p "Select [1-3] (default: 1): " _kwa
        _kwa="${_kwa:-1}"
        case "${_kwa}" in
            1)
                _def_url="$(onboard_default_access_base_url)"
                read -r -p "Access base URL [Enter = ${_def_url}]: " _kurl
                _kurl="${_kurl:-${_def_url}}"
                if ! onboard_kweaver_auth_login_for_url "${_kurl}"; then
                    onboard_log_warn "openbkn auth login failed. If you saw engine, SyntaxError, or RegExp issues under node_modules, upgrade Node (see npm @openbkn/bkn-sdk engines), then reinstall the CLI."
                    onboard_log_warn "Otherwise: set ONBOARD_DEFAULT_ACCESS_* or run login manually, then choose 2 to retry."
                fi
                ;;
            2) : ;;
            3) exit 1 ;;
            *) onboard_log_warn "Invalid choice, try again." ;;
        esac
    done
}


onboard_probe() {
    onboard_ensure_kweaver_auth
    if [[ "${INTERACTIVE}" == "true" ]]; then
        if ! kubectl get ns &>/dev/null; then
            onboard_log_err "kubectl: cannot list namespaces (check KUBECONFIG / cluster access)"
            exit 1
        fi
    else
        if ! kubectl get "namespace/${NAMESPACE}" &>/dev/null; then
            onboard_log_err "kubectl: namespace ${NAMESPACE} not found (export NAMESPACE=...)"
            exit 1
        fi
    fi
    if [[ "${INTERACTIVE}" == "true" ]]; then
        onboard_log_info "OK: openbkn + kubectl (enter namespace in the next prompt if not ${NAMESPACE})"
    else
        onboard_log_info "OK: openbkn + kubectl (ns=${NAMESPACE})"
    fi
    onboard_prepend_npm_global_bin_to_path
    onboard_recommend_admin_cli
    # bkn-safe seeds the admin + OAuth clients itself; onboard additionally
    # provisions the business "test" account (login: test, business-admin roles)
    # for ADP/business use. Skip with ONBOARD_SKIP_TEST_USER / --skip-test-user.
    onboard_provision_bkn_safe_test_user
    onboard_provision_oss_default_storage "${NAMESPACE}"
}

# onboard_bkn_safe_detected — true when the bkn-safe auth stack is present
# (helm release "bkn-safe" or a bkn-safe Deployment in any namespace). bkn-safe
# now installs into the shared openbkn namespace, so match the release/deploy
# name rather than a dedicated namespace.
onboard_bkn_safe_detected() {
    if command -v helm &>/dev/null; then
        helm list -A 2>/dev/null | awk 'NR>1 {print $1}' | grep -qE '^bkn-safe$' && return 0
    fi
    kubectl get deploy -A 2>/dev/null | awk '{print $2}' | grep -qE '^bkn-safe$' && return 0
    return 1
}

# Detect the bkn-safe auth stack and print admin guidance. bkn-safe self-seeds
# the admin (account "admin", platform initial password — forced change on first
# login) and the OAuth clients (client-seed Job), so no openbkn admin / test-user
# provisioning is needed. (no install-type detection needed.)
onboard_recommend_admin_cli() {
    local has_safe="false"
    onboard_bkn_safe_detected && has_safe="true"

    if [[ "${has_safe}" == "true" ]]; then
        onboard_log_info "Auth stack: bkn-safe (+ bundled hydra). The admin user is seeded automatically (account 'admin', platform initial password — you must change it on first login). OAuth clients are seeded by the in-cluster client-seed Job."
        onboard_log_info "Admin ops use the openbkn CLI: 'openbkn auth login <url> -u admin -p <pw> -k' (device flow), then 'openbkn admin user|role|...' or bkn-safe's token-gated /api/safe/v1/admin API."
    else
        onboard_log_info "bkn-safe not detected on this cluster — auth stack may not be installed yet."
    fi
}

onboard_do_bkn_bash() {
    local emb_name="$1"
    onboard_upsert_cm_embedded_yaml "${NAMESPACE}" "bkn-backend-cm" "${emb_name}" || return 1
    onboard_upsert_cm_embedded_yaml "${NAMESPACE}" "ontology-query-cm" "${emb_name}" || return 1
    onboard_bkn_rollout "${NAMESPACE}" || return 1
}

# ---- main --------------------------------------------------------------------
if [[ "${ENABLE_BKN_ONLY}" == "true" ]]; then
    if [[ -z "${BKN_NAME}" ]]; then
        onboard_log_err "Use --bkn-embedding-name=<model_name> with --enable-bkn-search"
        exit 1
    fi
    # Prefer Python (same as full config) if PyYAML available; else bash+yq
    onboard_probe
    export KWE_POST_NS="${NAMESPACE}" KWE_POST_BKN="${BKN_NAME}"
    if python3 -c "import yaml" 2>/dev/null && PYTHONPATH="${SCRIPT_DIR}/scripts/lib" python3 -c "import onboard_apply_config" 2>/dev/null; then
        PYTHONPATH="${SCRIPT_DIR}/scripts/lib" python3 -c "
import sys
from onboard_apply_config import patch_bkn_cms_and_rollout
import os
sys.exit(patch_bkn_cms_and_rollout(os.environ['KWE_POST_NS'], os.environ['KWE_POST_BKN']))
"
    else
        onboard_log_warn "PyYAML or module missing; using bash path (needs PyYAML in onboard_upsert)"
        onboard_do_bkn_bash "${BKN_NAME}"
    fi
    ONBOARD_REPORT_MAIN_MODE="bkn-only"
    onboard_log_info "Done (BKN only)."
    onboard_print_completion_report
    exit 0
fi

onboard_probe

if [[ -n "${CONFIG_FILE}" ]]; then
    if [[ ! -f "${CONFIG_FILE}" ]]; then
        onboard_log_err "Config not found: ${CONFIG_FILE}"
        exit 1
    fi
    if ! python3 -c "import yaml" 2>/dev/null; then
        onboard_log_err "For --config, install PyYAML: pip3 install pyyaml"
        exit 1
    fi
    exec python3 "${SCRIPT_DIR}/scripts/lib/onboard_apply_config.py" \
        "${CONFIG_FILE}" \
        "${NAMESPACE}" \
        "${SKIP_BKN}"
fi

# Interactive (bash path for registration; BKN uses upsert in models.sh)
if [[ "${INTERACTIVE}" == "true" ]]; then
    if [[ "${ONBOARD_ASSUME_YES}" == "true" ]]; then
        _y_llm_count="$(onboard_get_existing_llm_names | grep -c . || true)"
        _y_sm_count="$(onboard_get_existing_small_model_names | grep -c . || true)"
        _y_bkn_default="$(onboard_bkn_cm_current_default_name "${NAMESPACE}" 2>/dev/null || true)"
        if [[ -n "${_y_bkn_default}" ]]; then
            ONBOARD_REPORT_BKN_CM="skipped — already patched (defaultSmallModelName=${_y_bkn_default})"
        else
            ONBOARD_REPORT_BKN_CM="skipped (-y; not applied without explicit --config)"
        fi
        ONBOARD_REPORT_MODELS="skipped (-y); on platform: ${_y_llm_count} LLM, ${_y_sm_count} small/embedding"
        onboard_log_info "Skipping interactive model registration (-y). On platform: ${_y_llm_count} LLM, ${_y_sm_count} small/embedding. For non-interactive registration, use --config=models.yaml (see -h) or --enable-bkn-search."
        ONBOARD_REPORT_MAIN_MODE="interactive"
        onboard_log_info "Done."
        onboard_print_completion_report
        exit 0
    fi
    if ! python3 -c "import yaml" 2>/dev/null; then
        onboard_log_warn "PyYAML not installed: BKN ConfigMap patch will fail. pip3 install pyyaml"
    fi
    onboard_log_info "Interactive model registration (empty line skips section)"
    read -r -p "Namespace [${NAMESPACE}]: " ns
    [[ -n "${ns}" ]] && NAMESPACE="${ns}"
    if ! kubectl get "namespace/${NAMESPACE}" &>/dev/null; then
        onboard_log_err "namespace ${NAMESPACE} not found"
        exit 1
    fi

    _POSTI_EXISTING_LLM="$(onboard_get_existing_llm_names)"
    _POSTI_EXISTING_SM="$(onboard_get_existing_small_model_names)"
    _existing_llm_count="$(printf '%s\n' "${_POSTI_EXISTING_LLM}" | grep -c . || true)"
    _existing_sm_count="$(printf '%s\n' "${_POSTI_EXISTING_SM}" | grep -c . || true)"
    _bkn_current_default="$(onboard_bkn_cm_current_default_name "${NAMESPACE}" 2>/dev/null || true)"

    # LLM section: ask whether to add another if any already exist; otherwise prompt directly.
    llm_n=""
    _add_llm="true"
    if [[ "${_existing_llm_count}" -gt 0 ]]; then
        onboard_log_info "LLM already registered on this platform: ${_existing_llm_count}."
        read -r -p "Register another LLM now? [y/N]: " _add_llm_ans
        if [[ ! "${_add_llm_ans}" =~ ^[Yy] ]]; then
            _add_llm="false"
        fi
    fi
    if [[ "${_add_llm}" == "true" ]]; then
        read -r -p "LLM model_name (Enter to skip): " llm_n
        if [[ -n "${llm_n}" ]]; then
            read -r -p "LLM model_series (e.g. deepseek) [others]: " llm_s
            read -r -p "max_model_len [8192]: " llm_ml
            read -r -p "api_key: " -s llm_key
            echo
            read -r -p "api_model: " llm_am
            read -r -p "api_url: " llm_url
            onboard_ensure_llm "${llm_n}" "${llm_s:-others}" "${llm_ml:-8192}" "${llm_key}" "${llm_am}" "${llm_url}" "llm"
        fi
        _POSTI_EXISTING_LLM="$(onboard_get_existing_llm_names)"
        _POSTI_EXISTING_SM="$(onboard_get_existing_small_model_names)"
    fi

    # Embedding / small-model section: same pattern. If a new one is added, ask whether to set as BKN default.
    em_n=""
    bkn_default_name=""
    _new_em_set_default="false"
    _add_em="true"
    if [[ "${_existing_sm_count}" -gt 0 ]]; then
        if [[ -n "${_bkn_current_default}" ]]; then
            onboard_log_info "Embedding / small models already registered: ${_existing_sm_count}. BKN default is currently [${_bkn_current_default}] (defaultSmallModelEnabled=true)."
        else
            onboard_log_info "Embedding / small models already registered: ${_existing_sm_count}. BKN default is not set yet."
        fi
        read -r -p "Register another embedding / small model now? [y/N]: " _add_em_ans
        if [[ ! "${_add_em_ans}" =~ ^[Yy] ]]; then
            _add_em="false"
        fi
    fi
    if [[ "${_add_em}" == "true" ]]; then
        read -r -p "Embedding model_name (Enter to skip): " em_n
        if [[ -n "${em_n}" ]]; then
            read -r -p "api_key: " -s em_key
            echo
            read -r -p "api_model: " em_am
            read -r -p "api_url: " em_url
            read -r -p "embedding_dim [1024]: " em_dim
            if [[ -n "${_bkn_current_default}" ]]; then
                read -r -p "BKN default is currently [${_bkn_current_default}]. Set [${em_n}] as the new BKN default (will patch ConfigMaps and restart bkn-backend / ontology-query)? [y/N]: " em_bkn
                if [[ "${em_bkn}" =~ ^[Yy] ]]; then
                    _new_em_set_default="true"
                fi
            else
                read -r -p "Set [${em_n}] as the BKN default (no default is set yet)? [Y/n]: " em_bkn
                if [[ ! "${em_bkn}" =~ ^[Nn] ]]; then
                    _new_em_set_default="true"
                fi
            fi
            onboard_ensure_small_model "${em_n}" "embedding" "${em_key}" "${em_url}" "${em_am}" 32 512 "${em_dim:-1024}"
            if [[ "${_new_em_set_default}" == "true" ]]; then
                bkn_default_name="${em_n}"
            fi
        fi
    fi
    if [[ -n "${llm_n:-}" ]]; then
        onboard_test_llm "$(onboard_get_id_for_llm "${llm_n}")"
    fi
    if [[ -n "${em_n:-}" ]]; then
        onboard_test_small "$(onboard_get_id_for_small "${em_n}")"
    fi

    _models_status_parts=()
    if [[ -n "${llm_n:-}" ]]; then
        _models_status_parts+=("registered LLM ${llm_n}")
    elif [[ "${_existing_llm_count}" -gt 0 ]]; then
        _models_status_parts+=("LLM unchanged (${_existing_llm_count} on platform)")
    else
        _models_status_parts+=("no LLM entered")
    fi
    if [[ -n "${em_n:-}" ]]; then
        if [[ "${_new_em_set_default}" == "true" ]]; then
            _models_status_parts+=("registered embedding ${em_n} (set as new BKN default)")
        else
            _models_status_parts+=("registered embedding ${em_n}")
        fi
    elif [[ "${_existing_sm_count}" -gt 0 ]]; then
        _models_status_parts+=("embedding/small unchanged (${_existing_sm_count} on platform)")
    else
        _models_status_parts+=("no embedding entered")
    fi
    ONBOARD_REPORT_MODELS=""
    for _p in "${_models_status_parts[@]}"; do
        if [[ -z "${ONBOARD_REPORT_MODELS}" ]]; then
            ONBOARD_REPORT_MODELS="${_p}"
        else
            ONBOARD_REPORT_MODELS="${ONBOARD_REPORT_MODELS}; ${_p}"
        fi
    done

    # If the user did NOT register a new embedding but embeddings already exist on the platform,
    # offer to (re)apply one of them as the BKN default. This covers two cases:
    #   1) BKN default is unset — first-time wiring of an already-registered embedding.
    #   2) BKN default is set — operator wants to switch to a different existing embedding.
    if [[ "${SKIP_BKN}" != "true" && -z "${bkn_default_name}" && "${_existing_sm_count}" -gt 0 ]]; then
        if [[ -z "${_bkn_current_default}" ]]; then
            read -r -p "Use one of the already-registered embeddings as the BKN default (will patch ConfigMaps and restart bkn-backend / ontology-query)? [Y/n]: " _use_existing_em
            _use_existing_em_apply="true"
            if [[ "${_use_existing_em}" =~ ^[Nn] ]]; then
                _use_existing_em_apply="false"
            fi
        else
            read -r -p "BKN default is currently [${_bkn_current_default}]. Switch to a different already-registered embedding? [y/N]: " _use_existing_em
            _use_existing_em_apply="false"
            if [[ "${_use_existing_em}" =~ ^[Yy] ]]; then
                _use_existing_em_apply="true"
            fi
        fi
        if [[ "${_use_existing_em_apply}" == "true" ]]; then
            _existing_em_names="$(onboard_get_existing_small_model_names | sed '/^$/d')"
            if [[ -z "${_existing_em_names}" ]]; then
                onboard_log_info "Could not list existing embedding/small models — skipping ConfigMap patch."
            else
                _default_pick="$(printf '%s\n' "${_existing_em_names}" | head -n1)"
                echo "Available embedding / small models:"
                printf '  - %s\n' ${_existing_em_names}
                read -r -p "Embedding model_name to set as BKN default [${_default_pick}]: " _pick_em
                _pick_em="${_pick_em:-${_default_pick}}"
                if printf '%s\n' "${_existing_em_names}" | grep -Fxq "${_pick_em}"; then
                    bkn_default_name="${_pick_em}"
                else
                    onboard_log_info "[${_pick_em}] not in the existing embedding list — skipping ConfigMap patch."
                fi
            fi
        fi
    fi

    if [[ "${SKIP_BKN}" == "true" ]]; then
        onboard_log_info "Done (skip BKN not used in interactive; omit model to skip BKN patch)."
        ONBOARD_REPORT_BKN_CM="skipped (--skip-bkn)"
    elif [[ -n "${bkn_default_name}" ]]; then
        # User explicitly chose a (new) default — patch and restart, even if CMs were already patched (the name changed).
        onboard_do_bkn_bash "${bkn_default_name}" || exit 1
        if [[ -n "${_bkn_current_default}" && "${_bkn_current_default}" != "${bkn_default_name}" ]]; then
            ONBOARD_REPORT_BKN_CM="re-patched (defaultSmallModelName ${_bkn_current_default} -> ${bkn_default_name}); bkn-backend & ontology-query restarted"
        else
            ONBOARD_REPORT_BKN_CM="patched (defaultSmallModelName=${bkn_default_name}); bkn-backend & ontology-query restarted"
        fi
    elif [[ -n "${_bkn_current_default}" ]]; then
        onboard_log_info "BKN ConfigMaps unchanged — already patched (defaultSmallModelName=${_bkn_current_default}). No restart needed."
        ONBOARD_REPORT_BKN_CM="unchanged — already patched (defaultSmallModelName=${_bkn_current_default})"
    else
        onboard_log_info "No BKN default embedding selected; ConfigMap not patched."
        ONBOARD_REPORT_BKN_CM="skipped (no default embedding selected)"
    fi
    ONBOARD_REPORT_MAIN_MODE="interactive"
    onboard_log_info "Done."
    onboard_print_completion_report
    exit 0
fi

usage
exit 1
