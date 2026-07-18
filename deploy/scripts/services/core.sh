
# Default bkn-foundry namespace
CORE_NAMESPACE="${CORE_NAMESPACE:-openbkn}"

# Set to true in parse_core_args when user passes --namespace/--namespace=… (overrides namespace: in YAML).
CORE_NAMESPACE_FROM_CLI="${CORE_NAMESPACE_FROM_CLI:-false}"

# Default local charts directory
CORE_LOCAL_CHARTS_DIR="${CORE_LOCAL_CHARTS_DIR:-}"
CORE_VERSION_MANIFEST_FILE="${CORE_VERSION_MANIFEST_FILE:-}"

# --registry=<swr|ghcr|FULL>: alias for image.registry. Empty means "not set on
# CLI"; the swr default is applied in _core_apply_default_set_values only when the
# user did not pass --registry nor an explicit --set image.registry=...
CORE_IMAGE_REGISTRY="${CORE_IMAGE_REGISTRY:-}"

# --dockerhub-mirror=<auto|host|off>: containerd registry mirror for docker.io
# (otel/hydra/postgres/minio) on CN/restricted nets. "off"/empty disables.
# Default "auto" probes a candidate list and picks the first mirror that serves
# this stack's docker.io images over the registry-mirror (?ns=docker.io) protocol
# (sentinel: oryd/hydra; docker.m.daocloud.io 403s namespaced repos there, so a
# fixed default isn't safe). Pass a host to pin one; "off" to disable.
CORE_DOCKERHUB_MIRROR="${CORE_DOCKERHUB_MIRROR:-auto}"

# --latest: when set and no --version_file is given, auto-generate a latest manifest
# via scripts/gen-dev-manifest.sh --latest and use it as the version_file.
CORE_USE_LATEST_MANIFEST="${CORE_USE_LATEST_MANIFEST:-false}"

# Global --set values array
declare -a CORE_SET_VALUES=()

# Core SQL module directories to initialize before installing Core releases.
declare -a CORE_SQL_MODULES=(
    "studio"
    "bkn"
    "vega"
    "agentoperator"
    "sandbox"
)

# Parse bkn-foundry command arguments
parse_core_args() {
    local action="$1"
    shift

    while [[ $# -gt 0 ]]; do
        case "$1" in
            --version=*)
                HELM_CHART_VERSION="${1#*=}"
                shift
                ;;
            --version)
                HELM_CHART_VERSION="$2"
                shift 2
                ;;
            --helm_repo=*)
                HELM_CHART_REPO_URL="${1#*=}"
                shift
                ;;
            --helm_repo)
                HELM_CHART_REPO_URL="$2"
                shift 2
                ;;
            --helm_repo_name=*)
                HELM_CHART_REPO_NAME="${1#*=}"
                shift
                ;;
            --helm_repo_name)
                HELM_CHART_REPO_NAME="$2"
                shift 2
                ;;
            --charts_dir=*)
                CORE_LOCAL_CHARTS_DIR="${1#*=}"
                shift
                ;;
            --charts_dir)
                CORE_LOCAL_CHARTS_DIR="$2"
                shift 2
                ;;
            --version_file=*)
                CORE_VERSION_MANIFEST_FILE="${1#*=}"
                shift
                ;;
            --version_file)
                CORE_VERSION_MANIFEST_FILE="$2"
                shift 2
                ;;
            --registry=*)
                CORE_IMAGE_REGISTRY="${1#*=}"
                shift
                ;;
            --registry)
                CORE_IMAGE_REGISTRY="$2"
                shift 2
                ;;
            --dockerhub-mirror=*)
                CORE_DOCKERHUB_MIRROR="${1#*=}"
                shift
                ;;
            --dockerhub-mirror)
                CORE_DOCKERHUB_MIRROR="$2"
                shift 2
                ;;
            --latest)
                CORE_USE_LATEST_MANIFEST="true"
                shift
                ;;
            --force-refresh)
                FORCE_REFRESH_CHARTS="true"
                shift
                ;;
            --namespace=*)
                CORE_NAMESPACE="${1#*=}"
                CORE_NAMESPACE_FROM_CLI="true"
                shift
                ;;
            --namespace)
                CORE_NAMESPACE="$2"
                CORE_NAMESPACE_FROM_CLI="true"
                shift 2
                ;;
            --config=*)
                CONFIG_YAML_PATH="${1#*=}"
                shift
                ;;
            --config)
                CONFIG_YAML_PATH="$2"
                shift 2
                ;;
            --set=*)
                CORE_SET_VALUES+=("${1#*=}")
                shift
                ;;
            --set)
                CORE_SET_VALUES+=("$2")
                shift 2
                ;;
            --api_server_address=*)
                API_SERVER_ADVERTISE_ADDRESS="${1#*=}"
                shift
                ;;
            --api_server_address)
                API_SERVER_ADVERTISE_ADDRESS="$2"
                shift 2
                ;;
            --access_address=*)
                OPENBKN_ACCESS_ADDRESS="${1#*=}"
                shift
                ;;
            --access_address)
                OPENBKN_ACCESS_ADDRESS="$2"
                shift 2
                ;;
            -y|--yes)
                ASSUME_YES="true"
                shift
                ;;
            --force-upgrade)
                FORCE_UPGRADE="true"
                shift
                ;;
            *)
                log_error "Unknown argument: $1"
                return 1
                ;;
        esac
    done
}

# Target namespace: explicit --namespace overrides `namespace:` in CONFIG_YAML_PATH (avoids uninstall missing releases when YAML drifted from the cluster).
_core_resolve_target_namespace() {
    if [[ "${CORE_NAMESPACE_FROM_CLI:-false}" == true ]]; then
        printf '%s' "${CORE_NAMESPACE}"
        return 0
    fi
    local yaml_ns
    yaml_ns="$(bkn_values_namespace_from_config)"
    if [[ -n "${yaml_ns}" ]]; then
        printf '%s' "${yaml_ns}"
    else
        printf '%s' "${CORE_NAMESPACE}"
    fi
}

# Resolve local charts directory for bkn-foundry
_core_resolve_charts_dir() {
    if [[ -n "${CORE_LOCAL_CHARTS_DIR}" ]]; then
        if [[ -d "${CORE_LOCAL_CHARTS_DIR}" ]]; then
            echo "${CORE_LOCAL_CHARTS_DIR}"
        fi
    fi
}

_core_download_charts_dir() {
    if [[ -n "${CORE_LOCAL_CHARTS_DIR}" ]]; then
        ensure_charts_dir "${CORE_LOCAL_CHARTS_DIR}"
        return 0
    fi

    ensure_charts_dir "$(resolve_shared_charts_dir)"
}

_core_auto_resolve_version_manifest() {
    if [[ -n "${CORE_VERSION_MANIFEST_FILE:-}" ]]; then
        return 0
    fi

    local embedded_manifest
    if [[ -n "${HELM_CHART_VERSION:-}" ]]; then
        embedded_manifest="$(resolve_embedded_release_manifest "bkn-foundry" "${HELM_CHART_VERSION}")"
        if [[ -n "${embedded_manifest}" && -f "$(dirname "${embedded_manifest}")/DEPRECATED" ]]; then
            log_warn "Release ${HELM_CHART_VERSION} is DEPRECATED:"
            sed 's/^/  /' "$(dirname "${embedded_manifest}")/DEPRECATED" >&2 || true
        fi
    else
        embedded_manifest="$(resolve_latest_embedded_release_manifest "bkn-foundry")"
    fi
    if [[ -n "${embedded_manifest}" ]]; then
        CORE_VERSION_MANIFEST_FILE="${embedded_manifest}"
    fi
}

_core_require_version_manifest() {
    _core_auto_resolve_version_manifest

    if [[ -z "${CORE_VERSION_MANIFEST_FILE:-}" ]]; then
        log_error "No release manifest found for bkn-foundry. Provide --version or --version_file."
        return 1
    fi
}

_core_resolve_release_version() {
    local release_name="$1"
    _core_require_version_manifest || return 1
    resolve_release_chart_version "${CORE_VERSION_MANIFEST_FILE:-}" "bkn-foundry" "${HELM_CHART_VERSION:-}" "${release_name}" "${HELM_CHART_VERSION:-}"
}

_core_resolve_chart_name() {
    local release_name="$1"
    _core_require_version_manifest || return 1
    resolve_release_chart_name "${CORE_VERSION_MANIFEST_FILE:-}" "bkn-foundry" "${HELM_CHART_VERSION:-}" "${release_name}" "${release_name}"
}

_core_release_names() {
    _core_require_version_manifest || return 1
    get_release_manifest_release_names "${CORE_VERSION_MANIFEST_FILE}" "bkn-foundry" "${HELM_CHART_VERSION:-}"
}

init_core_databases() {
    local sql_base_dir
    sql_base_dir="$(resolve_versioned_sql_dir "bkn-foundry" "${HELM_CHART_VERSION:-}")"

    if ! is_rds_internal; then
        warn_external_rds_sql_required "BKN Foundry" "${sql_base_dir}"
        log_warn "Skipping automatic BKN Foundry database initialization (external RDS)"
        return 0
    fi

    # If the manifest declares a stage:pre data-migrator release, the chart
    # hook owns DB init; the script must not also run the SQL files.
    _core_require_version_manifest || return 1
    if should_skip_db_init_for_manifest "${CORE_VERSION_MANIFEST_FILE}"; then
        log_info "bkn-foundry manifest ${CORE_VERSION_MANIFEST_FILE} has pre-stage data-migrator, skipping SQL initialization"
        return 0
    fi

    local -a sql_modules=()
    bkn_mapfile_compat sql_modules list_versioned_sql_modules "bkn-foundry" "${HELM_CHART_VERSION:-}"
    if [[ ${#sql_modules[@]} -eq 0 ]]; then
        log_info "Skipping BKN Foundry database initialization: no SQL module directories found in ${sql_base_dir}"
        return 0
    fi

    local module_name
    local sql_dir
    for module_name in "${sql_modules[@]}"; do
        sql_dir="${sql_base_dir}/${module_name}"

        if ! init_module_database_if_present "${module_name}" "${sql_dir}" "${module_name}"; then
            log_error "Failed to initialize database for module: ${module_name}"
            return 1
        fi
    done
}

download_core() {
    log_info "Downloading BKN Foundry charts..."
    ensure_helm_available
    _core_resolve_latest_manifest || return 1
    _core_require_version_manifest || return 1

    HELM_CHART_REPO_NAME="${HELM_CHART_REPO_NAME:-openbkn}"
    HELM_CHART_REPO_URL="${HELM_CHART_REPO_URL:-https://openbkn-ai.github.io/helm-repo/}"

    local charts_dir
    charts_dir="$(_core_download_charts_dir)"

    parse_manifest_source "${CORE_VERSION_MANIFEST_FILE:-}"
    ensure_chart_source "${HELM_CHART_REPO_NAME}" "${HELM_CHART_REPO_URL}"

    local -a release_names=()
    bkn_mapfile_compat release_names _core_release_names
    local release_name
    local release_version
    local chart_name
    for release_name in "${release_names[@]}"; do
        release_version="$(_core_resolve_release_version "${release_name}")"
        chart_name="$(_core_resolve_chart_name "${release_name}")"
        download_chart_to_cache "${charts_dir}" "${HELM_CHART_REPO_NAME}" "${chart_name}" "${release_version}" "${FORCE_REFRESH_CHARTS:-false}"
    done
}

# Find local chart tgz for a given release name
_core_find_local_chart() {
    local charts_dir="$1"
    local chart_name="$2"
    find_cached_chart_tgz "${charts_dir}" "${chart_name}"
}

# Per-release extra --set values, filled into CORE_RELEASE_EXTRA_SETS.
# bkn-safe: pass the per-install platform initial password recorded in
# config.yaml by generate_config_yaml (seeded admin + users created without an
# explicit password — no baked-in default). Applied BEFORE CORE_SET_VALUES so an
# explicit --set config.initialPassword=... still wins.
CORE_RELEASE_EXTRA_SETS=()
_core_release_extra_sets() {
    local release_name="$1"
    CORE_RELEASE_EXTRA_SETS=()
    if [[ "${release_name}" == "bkn-safe" ]]; then
        local initial_pwd
        initial_pwd="$(config_yaml_top_field bknSafe initialPassword)"
        if [[ -n "${initial_pwd}" ]]; then
            CORE_RELEASE_EXTRA_SETS+=("config.initialPassword=${initial_pwd}")
        else
            log_warn "bknSafe.initialPassword not recorded in ${CONFIG_YAML_PATH} — bkn-safe will generate an admin password and log it once (re-run 'deploy.sh config generate' to record one)."
        fi
    fi
}

# Install a single bkn-foundry release from a local .tgz
_install_core_release_local() {
    local release_name="$1"
    local charts_dir="$2"
    local namespace="$3"
    local requested_version
    local chart_name

    requested_version="$(_core_resolve_release_version "${release_name}")"
    chart_name="$(_core_resolve_chart_name "${release_name}")"

    local chart_tgz=""
    if [[ -n "${requested_version}" ]]; then
        chart_tgz="$(find_cached_chart_tgz_by_version "${charts_dir}" "${chart_name}" "${requested_version}" || true)"
    fi
    if [[ -z "${chart_tgz}" ]]; then
        chart_tgz="$(_core_find_local_chart "${charts_dir}" "${chart_name}")"
    fi

    if [[ -z "${chart_tgz}" ]]; then
        log_error "✗ Local chart not found for ${release_name} (${chart_name}) in ${charts_dir}"
        return 1
    fi

    local target_version
    target_version="${requested_version}"
    if [[ -z "${target_version}" ]]; then
        target_version="$(get_local_chart_version "${chart_tgz}")"
    fi
    if should_skip_upgrade_same_chart_version "${release_name}" "${namespace}" "${chart_name}" "${target_version}"; then
        return 0
    fi

    log_info "Installing ${release_name} from local chart: $(basename "${chart_tgz}")..."

    local -a helm_args=(
        "upgrade" "--install" "${release_name}" "${chart_tgz}"
        "--namespace" "${namespace}"
        "-f" "${CONFIG_YAML_PATH}"
        "--wait" "--timeout=600s"
    )

    # Per-release values first, then all --set values (so explicit --set wins)
    local set_value
    _core_release_extra_sets "${release_name}"
    for set_value in "${CORE_RELEASE_EXTRA_SETS[@]}"; do
        helm_args+=("--set" "${set_value}")
    done
    for set_value in "${CORE_SET_VALUES[@]}"; do
        helm_args+=("--set" "${set_value}")
    done

    if helm "${helm_args[@]}"; then
        log_info "✓ ${release_name} installed successfully"
    else
        log_error "✗ Failed to install ${release_name}"
        return 1
    fi
}

# Install a single bkn-foundry release from a Helm repository
_install_core_release_repo() {
    local release_name="$1"
    local namespace="$2"
    local helm_repo_name="$3"
    local release_version="$4"
    local chart_name
    chart_name="$(_core_resolve_chart_name "${release_name}")"

    local chart_ref
    chart_ref="$(build_chart_ref "${helm_repo_name}" "${chart_name}")"

    local target_version="${release_version}"
    if [[ -z "${target_version}" ]]; then
        target_version=$(get_repo_chart_latest_version "${helm_repo_name}" "${chart_name}")
    fi

    if should_skip_upgrade_same_chart_version "${release_name}" "${namespace}" "${chart_name}" "${target_version}"; then
        return 0
    fi

    # Clean up any pending state before installing
    local current_status
    current_status=$(helm status "${release_name}" -n "${namespace}" -o json 2>/dev/null | grep -o '"status":"[^"]*"' | head -1 | cut -d'"' -f4)
    if [[ -n "${current_status}" && "${current_status}" != "deployed" && "${current_status}" != "failed" ]]; then
        log_info "Cleaning up ${release_name} (status: ${current_status})..."
        helm uninstall "${release_name}" -n "${namespace}" 2>/dev/null || true
    fi

    log_info "Installing ${release_name} from ${chart_ref}..."

    local -a helm_args=(
        "upgrade" "--install" "${release_name}"
        "${chart_ref}"
        "--namespace" "${namespace}"
        "-f" "${CONFIG_YAML_PATH}"
    )

    if [[ -n "${release_version}" ]]; then
        helm_args+=("--version" "${release_version}")
    fi

    helm_args+=("--devel")

    # Per-release values first, then all --set values (so explicit --set wins)
    local set_value
    _core_release_extra_sets "${release_name}"
    for set_value in "${CORE_RELEASE_EXTRA_SETS[@]}"; do
        helm_args+=("--set" "${set_value}")
    done
    for set_value in "${CORE_SET_VALUES[@]}"; do
        helm_args+=("--set" "${set_value}")
    done

    if helm "${helm_args[@]}"; then
        log_info "✓ ${release_name} installed successfully"
    else
        log_error "✗ Failed to install ${release_name}"
        return 1
    fi
}

# Resolve a --registry shorthand to a full registry/namespace string.
#   swr  -> swr.cn-east-3.myhuaweicloud.com/openbkn-ai
#   ghcr -> ghcr.io/openbkn-ai
#   *    -> used verbatim (treated as a full registry/namespace)
# In offline mode, all registries resolve to ${OFFLINE_REGISTRY}/openbkn-ai
_core_resolve_registry() {
    local raw="$1"
    # In offline mode, always use offline registry
    if [[ "${OFFLINE_MODE}" == "true" ]]; then
        echo "${OFFLINE_REGISTRY}/openbkn-ai"
        return
    fi
    case "${raw}" in
        swr)  echo "swr.cn-east-3.myhuaweicloud.com/openbkn-ai" ;;
        ghcr) echo "ghcr.io/openbkn-ai" ;;
        *)    echo "${raw}" ;;
    esac
}

# True if the active CONFIG_YAML_PATH sets image.registry (so we must not
# clobber it with the swr default — e.g. mac-config.yaml pins its own registry).
_core_config_sets_image_registry() {
    [[ -n "${CONFIG_YAML_PATH:-}" && -f "${CONFIG_YAML_PATH}" ]] || return 1
    awk '
        /^image:[[:space:]]*$/ {inimg=1; next}
        /^[^[:space:]#]/        {inimg=0}
        inimg && /^[[:space:]]+registry:[[:space:]]*[^[:space:]#]/ {found=1; exit}
        END {exit found?0:1}
    ' "${CONFIG_YAML_PATH}"
}

# Inject default --set values for bkn-foundry if user did not override them.
# Currently: businessDomain.enabled defaults to false at install time.
_core_apply_default_set_values() {
    # image.registry precedence in ONLINE mode: explicit --set image.registry=… wins;
    # else an explicit --registry flag is applied; else if CONFIG_YAML_PATH already sets
    # image.registry we respect it (don't clobber a config's registry, e.g. the
    # Mac dev mac-config.yaml); else default to swr.
    #
    # In OFFLINE mode (--offline flag): Always force offline registry via --set,
    # which takes precedence over config.yaml's image.registry setting.

    # Highest priority: offline mode
    if [[ "${OFFLINE_MODE}" == "true" ]]; then
        local _reg_resolved
        _reg_resolved="$(_core_resolve_registry "offline")"
        CORE_SET_VALUES+=("image.registry=${_reg_resolved}")
        log_info "Offline mode: Forcing image.registry=${_reg_resolved} via --set (overrides config.yaml)"
    elif get_set_value "image.registry" "${CORE_SET_VALUES[@]}" >/dev/null 2>&1; then
        : # user passed --set image.registry=… explicitly; do not override
    elif [[ -n "${CORE_IMAGE_REGISTRY}" ]]; then
        local _reg_resolved
        _reg_resolved="$(_core_resolve_registry "${CORE_IMAGE_REGISTRY}")"
        CORE_SET_VALUES+=("image.registry=${_reg_resolved}")
        log_info "Image registry applied: --set image.registry=${_reg_resolved} (from --registry=${CORE_IMAGE_REGISTRY})"
    elif _core_config_sets_image_registry; then
        log_info "Image registry: using image.registry from ${CONFIG_YAML_PATH} (pass --registry=swr|ghcr to override)."
    else
        local _reg_resolved
        _reg_resolved="$(_core_resolve_registry "swr")"
        CORE_SET_VALUES+=("image.registry=${_reg_resolved}")
        log_info "Image registry default applied: --set image.registry=${_reg_resolved} (override with --registry=ghcr or --set image.registry=...)."
    fi

    if ! get_set_value "businessDomain.enabled" "${CORE_SET_VALUES[@]}" >/dev/null 2>&1; then
        CORE_SET_VALUES+=("businessDomain.enabled=false")
        log_info "Default applied: --set businessDomain.enabled=false (override with --set businessDomain.enabled=true)"
    fi

    # Lightweight resource overrides for resource-constrained environments (mac kind / k3s).
    # All four envs are empty by default → k8s/kubeadm path stays at chart defaults
    # (most app charts ship limits=4-8Gi which is over-provisioned for dev).
    # Defaults are layered upstream:
    #   - mac dev: see deploy/dev/lib/mac_common.sh (mac_common_init)
    #   - k3s    : see bkn_apply_k3s_lightweight_defaults in common.sh
    # Apply uniformly to every Core release; per-release tuning (e.g. larger limit for
    # ontology-query) can be added later if a service consistently OOMs at install time.
    local _core_resource_set
    _core_resource_set=0
    if [[ -n "${OPENBKN_CORE_REQ_CPU:-}" ]]; then
        CORE_SET_VALUES+=("resources.requests.cpu=${OPENBKN_CORE_REQ_CPU}")
        _core_resource_set=1
    fi
    if [[ -n "${OPENBKN_CORE_REQ_MEM:-}" ]]; then
        CORE_SET_VALUES+=("resources.requests.memory=${OPENBKN_CORE_REQ_MEM}")
        _core_resource_set=1
    fi
    if [[ -n "${OPENBKN_CORE_LIM_CPU:-}" ]]; then
        CORE_SET_VALUES+=("resources.limits.cpu=${OPENBKN_CORE_LIM_CPU}")
        _core_resource_set=1
    fi
    if [[ -n "${OPENBKN_CORE_LIM_MEM:-}" ]]; then
        CORE_SET_VALUES+=("resources.limits.memory=${OPENBKN_CORE_LIM_MEM}")
        _core_resource_set=1
    fi
    if [[ "${_core_resource_set}" == "1" ]]; then
        log_info "bkn-foundry resource overrides applied (uniform): req cpu=${OPENBKN_CORE_REQ_CPU:-<chart>} mem=${OPENBKN_CORE_REQ_MEM:-<chart>} / lim cpu=${OPENBKN_CORE_LIM_CPU:-<chart>} mem=${OPENBKN_CORE_LIM_MEM:-<chart>}"
    fi
}

# Configure a containerd registry mirror so kubelet pulls docker.io third-party
# images (otel/hydra/postgres/minio) via the given mirror host. Needed in CN/
# restricted networks where docker.io is unreachable. Best-effort: never fails the
# install — logs a warning and returns 0 when it cannot act.
#   $1 = mirror host (e.g. docker.m.daocloud.io). "off"/empty disables (caller-gated).
setup_dockerhub_mirror() {
    local mirror_host="$1"
    if [[ -z "${mirror_host}" || "${mirror_host}" == "off" ]]; then
        return 0
    fi

    # Bail out early (before any network probe) when we cannot act: not root, or
    # containerd has no certs.d config_path. Keeps Mac/kind & non-root runs fast.
    if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
        log_warn "dockerhub-mirror: not root (EUID != 0); skipping containerd mirror setup."
        return 0
    fi

    # containerd must be configured with a certs.d config_path for per-host hosts.toml
    # to take effect. If not present, skip (don't fail) and tell the user.
    local containerd_config="/etc/containerd/config.toml"
    local certs_d=""
    if [[ -f "${containerd_config}" ]]; then
        # Parse config_path value, supporting both single and double quotes.
        # Handles: config_path = '/path', config_path = "/path", config_path = '/path'
        certs_d="$(grep -E '^\s*config_path\s*=' "${containerd_config}" 2>/dev/null \
            | head -1 | sed -E "s/.*=\s*['\"]?([^'\"]*)['\"]?\s*$/\1/" | tr -d '[:space:]')"
    fi
    # Reject empty string or relative path (must be absolute for certs.d)
    if [[ -z "${certs_d}" ]] || [[ "${certs_d}" != /* ]]; then
        log_warn "dockerhub-mirror: containerd config_path (certs.d dir) not found or invalid in ${containerd_config}; a certs.d config_path is required for the mirror — skipping (set it and re-run, or pass --dockerhub-mirror=off)."
        return 0
    fi

    # --dockerhub-mirror=auto: probe candidate mirrors and pick the first that
    # serves this stack's docker.io images over the registry-mirror (?ns=docker.io)
    # protocol. Sentinel = oryd/hydra (some mirrors, e.g. docker.m.daocloud.io,
    # 403 namespaced repos over that protocol). Override the list via
    # OPENBKN_DOCKERHUB_MIRROR_CANDIDATES (space-separated).
    if [[ "${mirror_host}" == "auto" ]]; then
        local _candidates="${OPENBKN_DOCKERHUB_MIRROR_CANDIDATES:-docker.1panel.live docker.m.daocloud.io docker.1ms.run dockerproxy.net}"
        local _accept="application/vnd.docker.distribution.manifest.v2+json,application/vnd.oci.image.manifest.v1+json,application/vnd.docker.distribution.manifest.list.v2+json,application/vnd.oci.image.index.v1+json"
        local _cand _picked="" _code
        for _cand in ${_candidates}; do
            _code="$(curl -s -m 8 -o /dev/null -w '%{http_code}' -H "Accept: ${_accept}" \
                "https://${_cand}/v2/oryd/hydra/manifests/v26.2.0?ns=docker.io" 2>/dev/null)"
            log_info "dockerhub-mirror=auto: probe ${_cand} -> ${_code}"
            if [[ "${_code}" == "200" ]]; then _picked="${_cand}"; break; fi
        done
        if [[ -z "${_picked}" ]]; then
            log_warn "dockerhub-mirror=auto: no candidate mirror reachable for docker.io; skipping (pass --dockerhub-mirror=<host>, or =off)."
            return 0
        fi
        mirror_host="${_picked}"
        log_info "dockerhub-mirror=auto: selected ${mirror_host}"
    fi

    local hosts_dir="${certs_d}/docker.io"
    local hosts_file="${hosts_dir}/hosts.toml"
    mkdir -p "${hosts_dir}"
    cat > "${hosts_file}" <<EOF
server = "https://docker.io"

[host."https://${mirror_host}"]
  capabilities = ["pull", "resolve"]
EOF
    log_info "dockerhub-mirror: wrote ${hosts_file} (docker.io -> https://${mirror_host}); hosts.toml is read per-pull, no containerd restart needed."
}

# Resolve the working manifest for install/download. Default (no --version /
# --version_file / --latest): the newest NON-DEPRECATED embedded release
# manifest; when none exists, fall back to following the newest main build per
# chart (same resolution as --latest). Deprecated releases stay reachable via
# an explicit --version=<x.y.z>.
_core_resolve_latest_manifest() {
    if [[ "${CORE_USE_LATEST_MANIFEST:-false}" != "true" ]]; then
        if [[ -n "${HELM_CHART_VERSION:-}" || -n "${CORE_VERSION_MANIFEST_FILE:-}" ]]; then
            return 0
        fi
        local newest_release
        newest_release="$(resolve_latest_embedded_release_manifest "bkn-foundry")"
        if [[ -n "${newest_release}" ]]; then
            CORE_VERSION_MANIFEST_FILE="${newest_release}"
            log_info "Defaulting to newest release manifest: ${newest_release} (pass --latest to follow main builds instead)."
            return 0
        fi
        log_info "No usable release manifest (all deprecated or none present) — following the newest main builds (pass --version=<release> to pick one explicitly)."
        CORE_USE_LATEST_MANIFEST="true"
    fi
    if [[ -n "${CORE_VERSION_MANIFEST_FILE:-}" ]]; then
        log_info "--latest ignored: --version_file is set (${CORE_VERSION_MANIFEST_FILE})."
        return 0
    fi

    local gen_script="${SCRIPT_DIR}/scripts/gen-dev-manifest.sh"
    local tmp_manifest
    tmp_manifest="$(mktemp -t bkn-latest-manifest.XXXXXX.yaml 2>/dev/null || mktemp)"
    log_info "--latest: generating latest manifest via ${gen_script} -> ${tmp_manifest}"
    if ! "${gen_script}" --latest --out="${tmp_manifest}"; then
        log_error "--latest: failed to generate latest manifest via ${gen_script}"
        return 1
    fi
    CORE_VERSION_MANIFEST_FILE="${tmp_manifest}"
    log_info "--latest: using generated manifest ${CORE_VERSION_MANIFEST_FILE}"
}

# Install BKN Foundry services via Helm
install_core() {
    log_info "Installing BKN Foundry services via Helm..."
    _core_resolve_latest_manifest || return 1
   if [[ "${OFFLINE_MODE}" != "true" ]]; then
        setup_dockerhub_mirror "${CORE_DOCKERHUB_MIRROR}"
    fi

    _core_require_version_manifest || return 1
    _core_apply_default_set_values

    if ! ensure_platform_prerequisites; then
        log_error "Failed to ensure platform prerequisites for BKN Foundry"
        diagnose_cluster_context
        return 1
    fi

    # macOS kind / BYOK: platform bootstrap is skipped, so ensure_data_services is not run above.
    # Install the same bundled data layer as `deploy.sh data-services install` unless opted out.
    if [[ "${OPENBKN_SKIP_PLATFORM_BOOTSTRAP:-false}" == "true" ]] && [[ "${OPENBKN_SKIP_DATA_SERVICES_BUNDLE:-false}" != "true" ]]; then
        log_info "Bring-your-own cluster: ensuring bundled data services before bkn-foundry (skip with OPENBKN_SKIP_DATA_SERVICES_BUNDLE=true)"
        if ! ensure_data_services; then
            log_error "Failed to ensure data services before BKN Foundry"
            return 1
        fi
    fi

    local namespace
    namespace="$(_core_resolve_target_namespace)"

    kubectl create namespace "${namespace}" 2>/dev/null || true

    local charts_dir
    charts_dir="$(_core_resolve_charts_dir)"

    local use_local=false
    if [[ -n "${charts_dir}" && -d "${charts_dir}" ]]; then
        use_local=true
        log_info "Using local bkn-foundry charts from: ${charts_dir}"
    else
        log_info "No explicit local bkn-foundry charts directory provided, using remote chart source."
        log_info "  Version:   ${HELM_CHART_VERSION}"
        if [[ -n "${CORE_VERSION_MANIFEST_FILE:-}" ]]; then
            log_info "  Version Manifest: ${CORE_VERSION_MANIFEST_FILE}"
        fi
        HELM_CHART_REPO_NAME="${HELM_CHART_REPO_NAME:-openbkn}"
        HELM_CHART_REPO_URL="${HELM_CHART_REPO_URL:-https://openbkn-ai.github.io/helm-repo/}"
        parse_manifest_source "${CORE_VERSION_MANIFEST_FILE:-}"
        log_chart_source "${HELM_CHART_REPO_NAME}" "${HELM_CHART_REPO_URL}"
        ensure_chart_source "${HELM_CHART_REPO_NAME}" "${HELM_CHART_REPO_URL}"
    fi

    log_info "Target namespace: ${namespace}"

    if ! init_core_databases; then
        log_error "Failed to initialize BKN Foundry databases"
        return 1
    fi

    local -a release_names=()
    bkn_mapfile_compat release_names _core_release_names

    local release_version
    for release_name in "${release_names[@]}"; do
        release_version="$(_core_resolve_release_version "${release_name}")"
        if [[ "${use_local}" == "true" ]]; then
            _install_core_release_local "${release_name}" "${charts_dir}" "${namespace}"
        else
            _install_core_release_repo "${release_name}" "${namespace}" "${HELM_CHART_REPO_NAME}" "${release_version}"
        fi
    done

    log_info "BKN Foundry services installation completed."

    # Publish the non-sensitive install-status snapshot + /install-status endpoint.
    # Best-effort: never fails the install.
    gen_install_status_json || true

    log_info "Context Loader toolset is auto-imported by agent-retrieval at startup (no manual onboard step needed)."

    local _host _port _scheme
    _host="$(_read_access_address_field "host" 2>/dev/null || true)"
    _port="$(_read_access_address_field "port" 2>/dev/null || true)"
    _scheme="$(_read_access_address_field "scheme" 2>/dev/null || true)"
    _host="${_host:-localhost}"
    _port="${_port:-443}"
    _scheme="${_scheme:-https}"

    echo ""
    echo "============================================"
    echo "  Verify your installation (open in a browser):"
    echo ""
    if [[ "${_port}" == "443" || "${_port}" == "80" ]]; then
        echo "    ${_scheme}://${_host}/install-status"
    else
        echo "    ${_scheme}://${_host}:${_port}/install-status"
    fi
    # Platform account credentials (NOT database passwords): the console admin
    # initial password, which is also the initial password handed to users
    # created without an explicit one. Highlighted — it is the one thing the
    # operator must take away from this summary.
    local _initial_pwd
    _initial_pwd="$(config_yaml_top_field bknSafe initialPassword)"
    if [[ -n "${_initial_pwd}" ]]; then
        echo ""
        echo "  Console sign-in (a password change is forced on first login):"
        echo ""
        echo -e "    ${YELLOW}user:     admin${NC}"
        echo -e "    ${YELLOW}password: ${_initial_pwd}${NC}"
        echo ""
        echo "  New users created without an explicit password start with this same"
        echo "  initial password (also returned once in the create response)."
        echo "  Recorded as bknSafe.initialPassword in ${CONFIG_YAML_PATH}"
    fi
    echo ""
    echo "============================================"
}

# Uninstall BKN Foundry services
uninstall_core() {
    log_info "Uninstalling BKN Foundry services..."

    local namespace
    namespace="$(_core_resolve_target_namespace)"
    log_info "Helm target namespace: ${namespace}"

    local -a release_names=()
    bkn_mapfile_compat release_names _core_release_names
    for ((i=${#release_names[@]}-1; i>=0; i--)); do
        local release_name="${release_names[$i]}"
        log_info "Uninstalling ${release_name}..."
        local helm_err
        if helm_err=$(helm uninstall "${release_name}" -n "${namespace}" 2>&1); then
            log_info "✓ ${release_name} uninstalled"
        else
            # Do not confuse "wrong namespace / no Helm metadata" with a silent no-op:
            log_warn "⚠ ${release_name} uninstall skipped: ${helm_err}"
        fi
    done

    # Clean up sandbox session pods created at runtime by sandbox-control-plane.
    # These pods are scheduled via K8s API and are not owned by any Helm release,
    # so `helm uninstall` cannot reclaim them.
    log_warn "Deleting sandbox session pods (label: sandbox-type=execution)"
    kubectl delete pod -n "${namespace}" -l sandbox-type=execution --ignore-not-found >/dev/null 2>&1 || true

    log_info "Deleting leftover bkn-foundry Jobs in ${namespace} (e.g. data-migrator / chart hooks)"
    bkn_delete_jobs_name_match_ere_in_ns "${namespace}" 'migrator|data-migrator'

    log_info "BKN Foundry services uninstallation completed."
}

# Show BKN Foundry services status
show_core_status() {
    log_info "BKN Foundry services status:"

    local namespace
    namespace="$(_core_resolve_target_namespace)"

    log_info "Namespace: ${namespace}"
    log_info ""

    local -a release_names=()
    bkn_mapfile_compat release_names _core_release_names
    for release_name in "${release_names[@]}"; do
        if helm status "${release_name}" -n "${namespace}" >/dev/null 2>&1; then
            local status
            status=$(helm status "${release_name}" -n "${namespace}" -o json 2>/dev/null \
                | grep -o '"status":"[^"]*"' | head -1 | cut -d'"' -f4)
            log_info "  ✓ ${release_name}: ${status}"
        else
            log_info "  ✗ ${release_name}: not installed"
        fi
    done
}
