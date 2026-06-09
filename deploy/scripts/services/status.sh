
# BKN Foundry install-status — two views over one collector
# (scripts/lib/install_status.py):
#   show_install_status()      — Layer 1: live, detailed table for operators on
#                                the server (`deploy.sh core status`).
#   gen_install_status_json()  — Layer 2: regenerate the non-sensitive JSON
#                                snapshot + publish the static /install-status
#                                ingress endpoint. Called at the end of
#                                install_core, and reusable standalone.
#
# Depends on core.sh helpers (_core_resolve_target_namespace,
# _core_auto_resolve_version_manifest, CORE_VERSION_MANIFEST_FILE) — source AFTER
# core.sh in deploy.sh.

INSTALL_STATUS_PY="${SCRIPT_DIR}/scripts/lib/install_status.py"
INSTALL_STATUS_DIR="${SCRIPT_DIR}/conf/install-status"
INSTALL_STATUS_ENDPOINT_TPL="${INSTALL_STATUS_DIR}/endpoint.yaml"
INSTALL_STATUS_NGINX_CONF="${INSTALL_STATUS_DIR}/nginx.conf"
INSTALL_STATUS_INDEX_HTML="${INSTALL_STATUS_DIR}/index.html"

# Detect the ingress-nginx IngressClass to bind the endpoint to.
_status_detect_ingress_class() {
    local cls=""
    cls="$(kubectl get ingressclass \
        -o jsonpath='{.items[?(@.spec.controller=="k8s.io/ingress-nginx")].metadata.name}' \
        2>/dev/null | awk '{print $1}' || true)"
    echo "${cls:-${INGRESS_NGINX_CLASS:-class-443}}"
}

# Resolve the release manifest path (auto-embedded if not set on the CLI).
_status_require_manifest() {
    _core_auto_resolve_version_manifest || true
    if [[ -z "${CORE_VERSION_MANIFEST_FILE:-}" || ! -f "${CORE_VERSION_MANIFEST_FILE}" ]]; then
        log_error "No release manifest resolved; cannot collect install status."
        return 1
    fi
    return 0
}

# Layer 1 — detailed live table (expected vs deployed version, app version,
# helm revision/status, workload ready, drift/missing flags).
show_install_status() {
    local namespace
    namespace="$(_core_resolve_target_namespace)"
    _status_require_manifest || return 1

    if ! command -v python3 >/dev/null 2>&1; then
        log_error "python3 is required for install status."
        return 1
    fi

    python3 "${INSTALL_STATUS_PY}" \
        --namespace "${namespace}" \
        --manifest "${CORE_VERSION_MANIFEST_FILE}" \
        --config "${CONFIG_YAML_PATH:-}" \
        --product "bkn-foundry" \
        --format table
}

# Apply the static endpoint (nginx + service + ingress + nginx conf ConfigMap).
# Idempotent: safe to re-run on every install.
_status_apply_endpoint() {
    local namespace="$1"
    if [[ ! -f "${INSTALL_STATUS_ENDPOINT_TPL}" ]]; then
        log_warn "install-status endpoint template missing: ${INSTALL_STATUS_ENDPOINT_TPL}"
        return 0
    fi

    # nginx conf + dashboard HTML ConfigMap (built from real files, not inlined
    # YAML — keeps them un-escaped and editable). Idempotent.
    if [[ -f "${INSTALL_STATUS_NGINX_CONF}" && -f "${INSTALL_STATUS_INDEX_HTML}" ]]; then
        kubectl create configmap install-status-nginx \
            --from-file=nginx.conf="${INSTALL_STATUS_NGINX_CONF}" \
            --from-file=index.html="${INSTALL_STATUS_INDEX_HTML}" \
            -n "${namespace}" \
            --dry-run=client -o yaml 2>/dev/null \
            | kubectl apply -f - >/dev/null 2>&1 \
            || log_warn "Failed to apply install-status-nginx ConfigMap."
    else
        log_warn "install-status nginx.conf / index.html missing under ${INSTALL_STATUS_DIR}."
    fi

    local ingress_class
    ingress_class="$(_status_detect_ingress_class)"
    sed -e "s|__NAMESPACE__|${namespace}|g" \
        -e "s|__INGRESS_CLASS__|${ingress_class}|g" \
        "${INSTALL_STATUS_ENDPOINT_TPL}" \
        | kubectl apply -f - >/dev/null 2>&1 || {
            log_warn "Failed to apply install-status endpoint manifests."
            return 0
        }
}

# Layer 2 — regenerate the non-sensitive JSON snapshot and publish the endpoint.
# Never fails the install: best-effort, warns on error.
gen_install_status_json() {
    local namespace
    namespace="$(_core_resolve_target_namespace)"

    if ! command -v python3 >/dev/null 2>&1; then
        log_warn "python3 not found; skipping install-status snapshot."
        return 0
    fi
    if ! _status_require_manifest; then
        log_warn "Skipping install-status snapshot (no manifest)."
        return 0
    fi

    local tmp
    tmp="$(mktemp)"
    if ! python3 "${INSTALL_STATUS_PY}" \
            --namespace "${namespace}" \
            --manifest "${CORE_VERSION_MANIFEST_FILE}" \
            --config "${CONFIG_YAML_PATH:-}" \
            --product "bkn-foundry" \
            --format json > "${tmp}" 2>/dev/null; then
        log_warn "Failed to generate install-status JSON; skipping."
        rm -f "${tmp}"
        return 0
    fi

    _status_apply_endpoint "${namespace}"

    # Refresh only the data ConfigMap; nginx reads the mounted file per request,
    # so no pod restart is needed.
    if kubectl create configmap install-status-data \
            --from-file=install-status.json="${tmp}" \
            -n "${namespace}" \
            --dry-run=client -o yaml 2>/dev/null \
            | kubectl apply -f - >/dev/null 2>&1; then
        log_info "install-status published (ns ${namespace}): page /install-status · json /install-status.json"
    else
        log_warn "Failed to publish install-status data ConfigMap."
    fi
    rm -f "${tmp}"
}
