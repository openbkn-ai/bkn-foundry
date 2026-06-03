
# Single-node k3s installation helpers (Linux).
# Keeps Traefik disabled; use install_ingress_nginx from ingress_nginx.sh for ingress.

K3S_INSTALL_URL="${K3S_INSTALL_URL:-https://rancher-mirror.rancher.cn/k3s/k3s-install.sh}"
INSTALL_K3S_MIRROR="${INSTALL_K3S_MIRROR:-cn}"
INSTALL_K3S_VERSION="${INSTALL_K3S_VERSION:-v1.30.6+k3s1}"
INSTALL_K3S_EXEC="${INSTALL_K3S_EXEC:-server --disable=traefik --write-kubeconfig-mode=644}"
# Passed to rancher k3s-install.sh. Empty = auto (see install_k3s); set true/false to force.
INSTALL_K3S_SKIP_SELINUX_RPM="${INSTALL_K3S_SKIP_SELINUX_RPM:-}"

# Pre-seed k3s containerd with system images that the rancher k3s installer
# does NOT bundle (busybox for local-path-provisioner helper-pod, ...).
# k3s auto-imports any *.tar / *.tar.gz / *.tar.zst in /var/lib/rancher/k3s/agent/images/
# on first agent boot. Must run BEFORE the rancher installer, otherwise the
# agent dir is owned by k3s and our copy may race the first reconcile.
_seed_k3s_system_images() {
    local target_dir=/var/lib/rancher/k3s/agent/images
    local src_dir
    src_dir="${SCRIPT_DIR}/images"
    if [[ ! -d "${src_dir}" ]]; then
        log_warn "k3s system-image cache ${src_dir} missing; skipping seed (helper-pod busybox may need to be pulled from docker.io)"
        return 0
    fi

    local -a tarballs=()
    local f
    for f in "${src_dir}"/*.tar "${src_dir}"/*.tar.gz "${src_dir}"/*.tar.zst; do
        [[ -f "${f}" ]] && tarballs+=("${f}")
    done
    if [[ ${#tarballs[@]} -eq 0 ]]; then
        log_warn "No tarballs in ${src_dir}; skipping k3s system-image seed"
        return 0
    fi

    mkdir -p "${target_dir}"
    for f in "${tarballs[@]}"; do
        cp -f "${f}" "${target_dir}/"
        log_info "Seeded k3s image: ${target_dir}/$(basename "${f}")"
    done
}

_export_k3s_kubeconfig() {
    if [[ -f /root/.kube/config ]]; then
        export KUBECONFIG=/root/.kube/config
    elif [[ -f /etc/rancher/k3s/k3s.yaml ]]; then
        export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
    fi
}

# Return 0 if kubectl can reach a Ready cluster using k3s kubeconfig paths.
k3s_is_running() {
    _export_k3s_kubeconfig

    if [[ -z "${KUBECONFIG:-}" ]]; then
        return 1
    fi

    if ! command -v kubectl >/dev/null 2>&1; then
        return 1
    fi

    if kubectl get nodes >/dev/null 2>&1; then
        return 0
    fi

    return 1
}

show_k3s_status() {
    _export_k3s_kubeconfig
    if [[ -z "${KUBECONFIG:-}" ]]; then
        log_warn "No k3s kubeconfig found (/root/.kube/config or /etc/rancher/k3s/k3s.yaml)."
        return 1
    fi
    if ! command -v kubectl >/dev/null 2>&1; then
        log_error "kubectl not found; k3s may be misconfigured."
        return 1
    fi
    log_info "kubectl context: ${KUBECONFIG}"
    echo ""
    kubectl get nodes -o wide 2>/dev/null || true
    echo ""
    kubectl get pods -A 2>/dev/null || true
}

install_k3s() {
    if k3s_is_running; then
        log_info "k3s cluster already running, skipping k3s install."
        return 0
    fi

    check_root

    if ! command -v curl >/dev/null 2>&1; then
        log_error "curl is required to install k3s."
        return 1
    fi

    log_info "Installing k3s (version ${INSTALL_K3S_VERSION})..."
    log_info "K3S_INSTALL_URL=${K3S_INSTALL_URL}"
    log_info "INSTALL_K3S_MIRROR=${INSTALL_K3S_MIRROR}"
    log_info "INSTALL_K3S_EXEC=${INSTALL_K3S_EXEC}"

    _seed_k3s_system_images

    # k3s-install.sh maps HCE VERSION_ID (e.g. 2.0) to el9 k3s-selinux, which requires
    # container-selinux >= 3:2.191 — often missing on Huawei/yum mirrors → dnf aborts before k3s starts.
    # Skip the SELinux RPM when SELinux is off, or on hce/openEuler (aligns with permissive/disabled lab setups).
    local _skip_selinux_rpm="${INSTALL_K3S_SKIP_SELINUX_RPM}"
    if [[ -z "${_skip_selinux_rpm}" ]]; then
        local _want_skip=false
        local os_id=""
        if [[ -f /etc/os-release ]]; then
            os_id="$(awk -F= '/^ID=/{gsub(/"/,"",$2); print $2; exit}' /etc/os-release)"
        fi
        case "${os_id}" in
            hce|openEuler|openeuler) _want_skip=true ;;
        esac
        if command -v getenforce &>/dev/null && [[ "$(getenforce 2>/dev/null)" == "Disabled" ]]; then
            _want_skip=true
        fi
        if [[ -f /etc/selinux/config ]] && grep -qE '^[[:space:]]*SELINUX[[:space:]]*=[[:space:]]*disabled' /etc/selinux/config 2>/dev/null; then
            _want_skip=true
        fi
        if [[ "${_want_skip}" == "true" ]]; then
            _skip_selinux_rpm=true
            log_info "INSTALL_K3S_SKIP_SELINUX_RPM=true (auto: hce/openEuler and/or SELinux disabled — avoid k3s-selinux dependency failure). Override with INSTALL_K3S_SKIP_SELINUX_RPM=false if you need the RPM after satisfying container-selinux)."
        fi
    else
        log_info "INSTALL_K3S_SKIP_SELINUX_RPM=${INSTALL_K3S_SKIP_SELINUX_RPM} (user-set)"
    fi

    curl -sfL "${K3S_INSTALL_URL}" \
        | INSTALL_K3S_VERSION="${INSTALL_K3S_VERSION}" \
            INSTALL_K3S_MIRROR="${INSTALL_K3S_MIRROR}" \
            INSTALL_K3S_EXEC="${INSTALL_K3S_EXEC}" \
            INSTALL_K3S_SKIP_SELINUX_RPM="${_skip_selinux_rpm:-false}" \
            sh -

    if [[ ! -f /etc/rancher/k3s/k3s.yaml ]]; then
        log_error "k3s install finished but /etc/rancher/k3s/k3s.yaml is missing."
        return 1
    fi

    mkdir -p /root/.kube
    install -m 0600 /etc/rancher/k3s/k3s.yaml /root/.kube/config
    export KUBECONFIG=/root/.kube/config

    if ! command -v kubectl >/dev/null 2>&1; then
        log_error "kubectl not available after k3s install."
        return 1
    fi

    log_info "Waiting for all nodes to be Ready..."
    kubectl wait --for=condition=Ready node --all --timeout=300s

    log_info "k3s install completed."
}

uninstall_k3s() {
    check_root

    local uninstaller=""
    for candidate in /usr/local/bin/k3s-uninstall.sh /usr/bin/k3s-uninstall.sh; do
        if [[ -x "${candidate}" ]]; then
            uninstaller="${candidate}"
            break
        fi
    done

    if [[ -z "${uninstaller}" ]]; then
        log_warn "k3s-uninstall.sh not found; k3s does not appear to be installed."
        return 0
    fi

    log_info "Running ${uninstaller} ..."
    bash "${uninstaller}"
}
