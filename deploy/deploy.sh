#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

CONF_DIR="${CONF_DIR:-${HOME}/.openbkn-ai}"
CONFIG_YAML_PATH="${CONFIG_YAML_PATH:-${CONF_DIR}/config.yaml}"

# Global flag: skip all interactive prompts and use defaults
ASSUME_YES="${ASSUME_YES:-false}"

# Global flag: bypass "skip when chart version unchanged" optimization so that
# helm upgrade re-renders templates with the latest values.yaml. Use this after
# editing config.yaml on a previously-installed cluster.
FORCE_UPGRADE="${FORCE_UPGRADE:-false}"

# Fix paths to use script's conf directory, not user home
FLANNEL_MANIFEST_PATH="${SCRIPT_DIR}/conf/kube-flannel.yml"
LOCALPV_MANIFEST_PATH="${SCRIPT_DIR}/conf/local-path-storage.yaml"
HELM_INSTALL_SCRIPT_PATH="${SCRIPT_DIR}/conf/get-helm-3"

# Source all service libraries
source "${SCRIPT_DIR}/scripts/lib/common.sh"
source "${SCRIPT_DIR}/scripts/services/config.sh"
source "${SCRIPT_DIR}/scripts/services/k8s.sh"
source "${SCRIPT_DIR}/scripts/services/k3s.sh"
source "${SCRIPT_DIR}/scripts/services/storage.sh"
source "${SCRIPT_DIR}/scripts/services/mariadb.sh"
source "${SCRIPT_DIR}/scripts/services/redis.sh"
source "${SCRIPT_DIR}/scripts/services/kafka.sh"
# source "${SCRIPT_DIR}/scripts/services/mongodb.sh"  # MongoDB disabled
source "${SCRIPT_DIR}/scripts/services/ingress_nginx.sh"
source "${SCRIPT_DIR}/scripts/services/opensearch.sh"
source "${SCRIPT_DIR}/scripts/services/core.sh"
source "${SCRIPT_DIR}/scripts/services/status.sh"

usage() {
    echo "Kubernetes Infrastructure Initialization Script"
    echo ""
    echo "Usage: $0 <module> [action]"
    echo ""
    echo "Modules and Actions:"
    echo "  k8s install                   Initialize K8s master node with CNI and DNS"
    echo "  k8s reset                     Reset Kubernetes cluster state (kubeadm reset -f + cleanup)"
    echo "  k8s status                    Show cluster status"
    echo "  k3s install                   Install single-node k3s (Linux), Traefik disabled; uses ingress-nginx"
    echo "  k3s uninstall                 Run k3s-uninstall.sh (removes k3s)"
    echo "  k3s status                    Show cluster status (nodes and pods)"
    echo "  mariadb install               Install single-node MariaDB 11"
    echo "  mariadb uninstall             Uninstall MariaDB (blocked if bkn-foundry installed)"
    echo "  mariadb uninstall --force     Force uninstall MariaDB even if bkn-foundry installed (WARNING)"
    echo "  mariadb uninstall --delete-data  Uninstall MariaDB and delete PVC (data loss!)"
    echo "  redis install                 Install single-node Redis 7"
    echo "  redis uninstall               Uninstall Redis (PVCs will be deleted by default)"
    echo "  kafka install                 Install single-node Kafka"
    echo "  kafka uninstall               Uninstall Kafka (PVCs will be deleted by default)"
    echo "  data-services install         Install MariaDB, Redis, Kafka, OpenSearch (cluster must exist)"
    echo "  data-services uninstall       Uninstall those bundles (ingress only if AUTO_INSTALL_INGRESS_NGINX=true)"
    echo "  opensearch install            Install single-node OpenSearch"
    echo "  opensearch uninstall          Uninstall OpenSearch (optionally purge PVC)"
    echo "  ingress-nginx install         Install ingress-nginx-controller"
    echo "  ingress-nginx uninstall       Uninstall ingress-nginx-controller"
    echo "  bkn-foundry install          Install BKN Foundry services; auto-installs K8s/data services if missing"
    echo "  bkn-foundry install          On BYOK (OPENBKN_SKIP_PLATFORM_BOOTSTRAP=true), runs ensure_data_services first unless OPENBKN_SKIP_DATA_SERVICES_BUNDLE=true"
    echo "  bkn-foundry download         Download/update BKN Foundry charts into deploy/.tmp/charts"
    echo "  bkn-foundry uninstall        Uninstall BKN Foundry services"
    echo "  bkn-foundry status           Show BKN Foundry services status"
    echo "                                Use --set to pass custom values to all charts"
    echo "  all install                   Run full initialization (k8s + mariadb + redis + ingress-nginx)"
    echo ""
    echo "Examples:"
    echo "  $0 k8s install                # Initialize K8s master node with default settings"
    echo "  $0 k8s reset                  # Reset cluster state before re-install"
    echo "  $0 k8s status                 # Show cluster status"
    echo "  $0 k3s install                # Install single-node k3s + ingress-nginx (Linux)"
    echo "  $0 --distro=k3s bkn-foundry install  # k3s path; default is k8s/kubeadm (omit flag or KUBE_DISTRO=k8s)"
    echo "  POD_CIDR=10.0.0.0/16 $0 k8s install  # Initialize with custom POD_CIDR"
    echo "  $0 mariadb install            # Install MariaDB"
    echo "  $0 mariadb uninstall          # Uninstall MariaDB (fails if bkn-foundry installed)"
    echo "  $0 mariadb uninstall --force  # Force uninstall (WARNING: breaks bkn-foundry)"
    echo "  $0 mariadb uninstall --delete-data  # Uninstall MariaDB and delete PVC (data loss!)"
    echo "  $0 redis install              # Install Redis"
    echo "  $0 redis uninstall            # Uninstall Redis"
    echo "  $0 redis uninstall                         # Uninstall Redis (PVCs deleted by default)"
    echo "  REDIS_PURGE_PVC=false $0 redis uninstall   # Uninstall Redis but keep PVCs"
    echo "  $0 kafka install              # Install Kafka"
    echo "  $0 kafka uninstall                         # Uninstall Kafka (PVCs deleted by default)"
    echo "  KAFKA_PURGE_PVC=false $0 kafka uninstall   # Uninstall Kafka but keep PVCs"
    echo "  AUTO_INSTALL_INGRESS_NGINX=false $0 data-services install  # After kind/kubeadm + ingress already exist"
    echo "  $0 data-services uninstall                        # Tear down bundled data-layer charts"
    echo "  $0 data-services uninstall --delete-data           # Same; also purge MariaDB PVC (data loss!)"
    echo "  $0 opensearch install         # Install OpenSearch"
    echo "  $0 opensearch uninstall       # Uninstall OpenSearch"
    echo "  OPENSEARCH_PURGE_PVC=true $0 opensearch uninstall  # Uninstall OpenSearch and delete PVC (data loss!)"
    echo "  $0 ingress-nginx install      # Install ingress-nginx-controller"
    echo "  $0 ingress-nginx uninstall    # Uninstall ingress-nginx-controller"
    echo "  $0 config generate            # Generate/update ~/.openbkn-ai/config.yaml"
    echo "                                Console admin initial password: prompted on first generation (Enter = random);"
    echo "                                preset with BKN_SAFE_INITIAL_PASSWORD=...; recorded as bknSafe.initialPassword."
    echo "  $0 all install                # Full initialization with all components"
    echo ""
    echo "Global Options (must appear BEFORE <module> <action>, e.g. $0 --distro=k8s bkn-foundry install):"
    echo "                                Trailing flags like ... install --distro=k8s are NOT parsed here;"
    echo "                                use env KUBE_DISTRO=k8s or move --distro (same rule as -y, --force-upgrade)."
    echo "  -y, --yes                     Skip all interactive prompts and use defaults"
    echo "  --offline                     Enable offline deployment mode (use offline registry)"
    echo "                                Default offline registry: registry.openbkn.ai:5000"
    echo "  --offline=<registry>          Enable offline mode with custom registry"
    echo "                                Example: --offline=node1:5000"
    echo "  --force-upgrade               Always run helm upgrade even if installed chart version equals target."
    echo "                                Use this after editing config.yaml on a previously-installed cluster."
    echo "  --distro=k8s|k3s              Cluster bootstrap when modules auto-ensure K8s (default: k8s = kubeadm stack)."
    echo "                                Same as env KUBE_DISTRO=k8s|k3s (legacy: kubeadm means k8s). Use k3s for single-node lightweight."
    echo "  --config=<path>               Specify config.yaml path (values file for helm installs). May appear"
    echo "                                before <module> (global) or on the module command line (e.g. bkn-foundry)."
    echo "                                Default: ~/.openbkn-ai/config.yaml or \$CONFIG_YAML_PATH env var"
    echo "  --charts_dir=<path>           Use a specific local chart directory for download/install"
    echo "                                install only uses local charts when this option is explicitly set"
    echo "  --version_file=<path>         Use an aggregate release manifest to resolve exact chart versions"
    echo "                                (default auto path: deploy/release-manifests/<version>/<product>.yaml)"
    echo "  --registry=<swr|ghcr|FULL>    Alias for image.registry (bkn-foundry install). swr/ghcr expand to the"
    echo "                                openbkn-ai registries; anything else is used verbatim. Default: swr"
    echo "                                (an explicit --set image.registry=... always wins)."
    echo "  --dockerhub-mirror=<auto|host|off> Containerd docker.io mirror for third-party images (otel/hydra/postgres)"
    echo "                                on CN/restricted nets. Default 'auto' probes candidates and picks a working one;"
    echo "                                pass a host to pin, 'off' to disable. Requires root + containerd certs.d config_path."
    echo "  --latest                      (default behavior; kept for compatibility) follow newest main builds"
    echo "                                Default install already does this; pass --version=<release> for a pinned set."
    echo "  --access_address=<addr>       BKN Foundry access address: host, host:port, or scheme://host:port/path"
    echo "                                Example: --access_address=10.0.0.5 or --access_address=https://openbkn.example.com:443"
    echo "  --api_server_address=<ip>     Kubernetes API server advertise address (must be a local interface IP)"
    echo "                                Defaults to auto-detect from hostname -I"
    echo "                                Equivalent to: --set auth.enabled=false --set businessDomain.enabled=false"
    echo "                                auth.enabled=false also SKIPS installing the bkn-safe auth stack"
    echo "                                (bkn-safe + bundled hydra + its postgres). Keep it: --set bknSafe.install=true"
    echo "  --set <key>=<value>           Pass custom values to helm charts (can be used multiple times)"
    echo "                                Example: --set auth.enabled=false --set image.tag=latest"
    echo ""
    echo "Environment (optional, bkn-foundry install):"
    echo "  (Context Loader ADP import moved to deploy/onboard.sh after openbkn auth — openbkn call impex; see onboard -h.)"
    echo "  DEPLOY_BUSINESS_DOMAIN        x-business-domain for openbkn/onboard (default: bd_public)."
    echo "  OPENBKN_CORE_REQ_CPU         CPU requests for all Core releases (e.g. 200m, 1)"
    echo "  OPENBKN_CORE_REQ_MEM         Memory requests for all Core releases (e.g. 512Mi, 1Gi)"
    echo "  OPENBKN_CORE_LIM_CPU         CPU limits for all Core releases (e.g. 2, 4)"
    echo "  OPENBKN_CORE_LIM_MEM         Memory limits for all Core releases (e.g. 2Gi, 4Gi)"
    echo "                                Example: OPENBKN_CORE_REQ_CPU=200m OPENBKN_CORE_REQ_MEM=512Mi $0 bkn-foundry install"
    echo ""
    echo "  $0 --offline k8s install                        # Offline mode: use default offline registry"
    echo "  $0 --offline=node1:5000 k8s install             # Offline mode: use custom offline registry"
    echo "  $0 bkn-foundry install --set image.registry=my-registry.com --set image.tag=v1.0.0  # Custom image settings"
    echo "  $0 bkn-foundry install --registry=swr           # Default: newest main builds + Huawei SWR registry (CN-friendly)"
    echo "  $0 bkn-foundry install --version=0.1.0          # Pinned release manifest (release-manifests/<version>/)"
    echo "  $0 bkn-foundry download --charts_dir=/path/to/charts # Download Core charts into a specific local directory"
    echo "  $0 bkn-foundry install --charts_dir=/path/to/charts  # Install Core from a local charts directory"
    echo "  $0 bkn-foundry download --version=0.4.0  # Auto-uses ./release-manifests/0.4.0/bkn-foundry.yaml when present"
    echo "  $0 bkn-foundry download --version=0.4.0 --version_file=./release-manifests/0.4.0/bkn-foundry.yaml"
    echo "  $0 bkn-foundry install --config=/root/.openbkn-ai/config.yaml --helm_repo_name=openbkn"
}

_detect_node_ip() {
    local node_ip
    local os
    os="$(uname -s 2>/dev/null || true)"
    # macOS (kind / Docker Desktop): no hostname -I / ip addr; use default route interface.
    if [[ "${os}" == "Darwin" ]]; then
        local iface
        iface="$(route -n get default 2>/dev/null | awk '/interface:/{print $2}' | head -1)"
        if [[ -n "${iface}" ]]; then
            node_ip="$(ipconfig getifaddr "${iface}" 2>/dev/null || true)"
        fi
        if [[ -z "${node_ip}" ]]; then
            for iface in en0 en1; do
                node_ip="$(ipconfig getifaddr "${iface}" 2>/dev/null || true)"
                [[ -n "${node_ip}" ]] && break
            done
        fi
        if [[ -z "${node_ip}" ]]; then
            node_ip="127.0.0.1"
        fi
        echo "${node_ip}"
        return 0
    fi

    node_ip="$(hostname -I 2>/dev/null | tr ' ' '\n' | grep -v '^127\.' | head -1 | tr -d '\n' || true)"
    if [[ -z "${node_ip}" ]] || [[ "${node_ip}" == "127.0.0.1" ]]; then
        node_ip="$(ip addr show 2>/dev/null | grep -oE 'inet [0-9]+(\.[0-9]+){3}' | awk '{print $2}' | grep -v '^127\.' | head -1 || true)"
    fi
    if [[ -z "${node_ip}" ]]; then
        node_ip="10.x.x.x"
    fi
    echo "${node_ip}"
}

_read_access_address_field() {
    local field="$1"
    if [[ ! -f "${CONFIG_YAML_PATH}" ]]; then
        return 0
    fi
    awk -v key="${field}:" '
        $1=="accessAddress:" {in_block=1; next}
        in_block && $1==key {print $2; exit}
        in_block && $0 ~ /^[^ ]/ {in_block=0}
    ' "${CONFIG_YAML_PATH}" 2>/dev/null | sed -e 's/^"//; s/"$//' -e "s/^'//; s/'$//"
}

_upsert_access_address() {
    local host="$1"
    local port="$2"
    local path="$3"
    local scheme="$4"
    local tmp
    local src

    mkdir -p "$(dirname "${CONFIG_YAML_PATH}")"
    tmp="$(mktemp)"
    src="${CONFIG_YAML_PATH}"
    if [[ ! -f "${src}" ]]; then
        src="/dev/null"
    fi

    awk -v host="${host}" -v port="${port}" -v path="${path}" -v scheme="${scheme}" '
        BEGIN {in_block=0; replaced=0}
        {
            if ($1=="accessAddress:") {
                print "accessAddress:"
                print "  host: " host
                print "  port: " port
                print "  scheme: " scheme
                print "  path: " path
                in_block=1
                replaced=1
                next
            }

            if (in_block==1) {
                if ($0 ~ /^[^ ]/) {
                    in_block=0
                    print $0
                }
                next
            }

            print $0
        }
        END {
            if (replaced==0) {
                print "accessAddress:"
                print "  host: " host
                print "  port: " port
                print "  scheme: " scheme
                print "  path: " path
            }
        }
    ' "${src}" > "${tmp}"

    mv "${tmp}" "${CONFIG_YAML_PATH}"
}

confirm_access_address_before_install() {
    local confirm_switch="${CONFIRM_ACCESS_ADDRESS:-true}"
    local config_missing_before="false"
    if [[ ! -f "${CONFIG_YAML_PATH}" ]]; then
        config_missing_before="true"
    fi
    if [[ "${confirm_switch}" == "false" ]]; then
        # Still materialize CONFIG_YAML_PATH when missing so installs read namespace/accessAddress from one file.
        if [[ "${config_missing_before}" == "true" ]] && [[ "${AUTO_GENERATE_CONFIG:-true}" == "true" ]]; then
            log_info "Config not found, generating: ${CONFIG_YAML_PATH}"
            generate_config_yaml
        fi
        return 0
    fi

    local raw_host raw_port raw_path raw_scheme
    raw_host="$(_read_access_address_field "host")"
    raw_port="$(_read_access_address_field "port")"
    raw_path="$(_read_access_address_field "path")"
    raw_scheme="$(_read_access_address_field "scheme")"

    local host port path scheme

    # --access_address supports: "host", "host:port", or "scheme://host:port/path"
    if [[ -n "${OPENBKN_ACCESS_ADDRESS:-}" ]]; then
        local addr="${OPENBKN_ACCESS_ADDRESS}"
        if [[ "${addr}" == *"://"* ]]; then
            scheme="${addr%%://*}"
            local remainder="${addr#*://}"
            if [[ "${remainder}" == *":"* ]]; then
                host="${remainder%%:*}"
                remainder="${remainder#*:}"
                port="${remainder%%/*}"
                local after_port="${remainder#*/}"
                if [[ "${after_port}" != "${remainder}" ]]; then
                    path="/${after_port}"
                fi
            else
                host="${remainder%%/*}"
            fi
        elif [[ "${addr}" == *":"* ]]; then
            host="${addr%%:*}"
            port="${addr#*:}"
        else
            host="${addr}"
        fi
        port="${port:-${raw_port:-${INGRESS_NGINX_HTTPS_PORT:-443}}}"
        path="${path:-${raw_path:-/}}"
        scheme="${scheme:-${raw_scheme:-https}}"
    else
        host="${raw_host:-$(_detect_node_ip)}"
        port="${raw_port:-${INGRESS_NGINX_HTTPS_PORT:-443}}"
        path="${raw_path:-/}"
        scheme="${raw_scheme:-https}"
    fi

    local url="${scheme}://${host}:${port}${path}"

    # If provided via CLI arg, skip interactive confirmation
    if [[ -n "${OPENBKN_ACCESS_ADDRESS:-}" ]]; then
        log_info "Using accessAddress from --access_address: ${url}"
        # For first-time initialization, generate full config first.
        if [[ "${config_missing_before}" == "true" ]]; then
            log_info "Config not found, generating: ${CONFIG_YAML_PATH}"
            generate_config_yaml
        fi
        # Then upsert the confirmed accessAddress into full config.
        _upsert_access_address "${host}" "${port}" "${path}" "${scheme}"
        return 0
    fi

    echo ""
    echo "============================================"
    echo "  BKN Foundry Access Address Configuration"
    echo "============================================"
    echo "  Current detected values:"
    echo "    Host     : ${host}"
    echo "    Port     : ${port}"
    echo "    URL Root : ${path}"
    echo "    Protocol : ${scheme}  (http or https)"
    echo "    URL      : ${url}"
    echo "============================================"

    if [[ "${ASSUME_YES}" == "true" ]]; then
        log_info "Using defaults (-y)."
    elif [[ -t 0 ]]; then
        echo ""
        echo "Press Enter to keep the default, or type a new value."
        echo ""
        local input_host input_port input_path input_scheme
        read -r -p "  Host     [${host}]: " input_host
        read -r -p "  Port     [${port}]: " input_port
        read -r -p "  URL Root [${path}]: " input_path
        read -r -p "  Protocol [${scheme}]: " input_scheme

        host="${input_host:-${host}}"
        port="${input_port:-${port}}"
        path="${input_path:-${path}}"
        scheme="${input_scheme:-${scheme}}"
    else
        log_info "Non-interactive mode detected, using defaults."
    fi

    # For first-time initialization, generate full config first.
    if [[ "${config_missing_before}" == "true" ]]; then
        log_info "Config not found, generating: ${CONFIG_YAML_PATH}"
        generate_config_yaml
    fi

    # Then upsert the confirmed accessAddress into full config.
    _upsert_access_address "${host}" "${port}" "${path}" "${scheme}"
    log_info "accessAddress written to ${CONFIG_YAML_PATH}: ${scheme}://${host}:${port}${path}"
}

# Pure Helm/kubectl data-layer installs (MariaDB, Redis, …): host root is not required on macOS
# or when using an existing cluster (kind, BYOK). Linux "full platform" installs still use root.
require_root_for_helm_cluster_addons_only() {
    local os
    os="$(uname -s 2>/dev/null || true)"
    if [[ "${os}" == "Darwin" ]]; then
        return 0
    fi
    if [[ "${OPENBKN_BYOK_CLUSTER:-false}" == "true" ]] || [[ "${OPENBKN_SKIP_PLATFORM_BOOTSTRAP:-false}" == "true" ]]; then
        return 0
    fi
    check_root
}

# Main function
main() {
    # Parse global flags before module/action
    while [[ $# -gt 0 ]]; do
        case "$1" in
            -y|--yes) ASSUME_YES="true"; shift ;;
            --force-upgrade) FORCE_UPGRADE="true"; shift ;;
            --offline)
                export OFFLINE_MODE="true"
                shift
                ;;
            --offline=*)
                export OFFLINE_MODE="true"
                export OFFLINE_REGISTRY="${1#*=}"
                shift
                ;;
            --config=*)
                CONFIG_YAML_PATH="${1#*=}"
                shift
                ;;
            --config)
                CONFIG_YAML_PATH="$2"
                shift 2
                ;;
            --distro=k3s|--distro=k8s|--distro=kubeadm)
                export KUBE_DISTRO="${1#*=}"
                shift
                ;;
            --distro)
                export KUBE_DISTRO="$2"
                shift 2
                ;;
            *) break ;;
        esac
    done

    # Non-interactive apt post-hooks (Ubuntu needrestart prompts on some lib upgrades)
    if [[ "${ASSUME_YES}" == "true" ]]; then
        export DEBIAN_FRONTEND=noninteractive
        export NEEDRESTART_MODE=a
    fi

    export KUBE_DISTRO="$(bkn_normalize_kube_distro "${KUBE_DISTRO:-k8s}")"

    local module="${1:-}"
    local action="${2:-}"
    shift 2 2>/dev/null || true

    # If no arguments, show usage
    if [[ -z "${module}" ]]; then
        usage
        exit 0
    fi

    if [[ "${module}" == "config" ]]; then
        case "${action}" in
            generate)
                check_root
                generate_config_yaml
                ;;
            *)
                log_error "Unknown config action: ${action}"
                usage
                exit 1
                ;;
        esac
        return 0
    fi

    # Handle storage module
    if [[ "${module}" == "storage" ]]; then
        case "${action}" in
            install|init)
                check_root
                install_localpv
                ;;
            *)
                log_error "Unknown storage action: ${action}"
                usage
                exit 1
                ;;
        esac
        return 0
    fi
    
    # Handle k8s module
    if [[ "${module}" == "k8s" ]]; then
        while [[ $# -gt 0 ]]; do
            case "$1" in
                --api_server_address=*) API_SERVER_ADVERTISE_ADDRESS="${1#*=}"; shift ;;
                --api_server_address)   API_SERVER_ADVERTISE_ADDRESS="$2"; shift 2 ;;
                --force-upgrade)        FORCE_UPGRADE="true"; shift ;;
                --access_address=*)     OPENBKN_ACCESS_ADDRESS="${1#*=}"; shift ;;
                --access_address)       OPENBKN_ACCESS_ADDRESS="$2"; shift 2 ;;
                -y|--yes)               ASSUME_YES="true"; shift ;;
                *) shift ;;
            esac
        done
        case "${action}" in
            install|init)
                check_root
                # Pre-install dependencies (containerd, k8s, helm) before k8s init
                log_info "Pre-installing dependencies..."
                detect_package_manager
                install_containerd
                install_kubernetes
                install_helm
                
                check_prerequisites
                init_k8s_master
                allow_master_scheduling
                install_cni
                wait_for_dns

                if [[ "${AUTO_INSTALL_LOCALPV}" == "true" ]]; then
                    if [[ -z "$(kubectl get storageclass --no-headers 2>/dev/null)" ]]; then
                        install_localpv
                    fi
                fi

                if [[ "${AUTO_INSTALL_INGRESS_NGINX}" == "true" ]]; then
                    if ! command -v helm >/dev/null 2>&1; then
                        log_error "Helm is required to install ingress-nginx. Please run: $0 k8s install"
                        exit 1
                    fi
                    install_ingress_nginx
                fi

                if [[ "${AUTO_GENERATE_CONFIG}" == "true" ]]; then
                    generate_config_yaml
                fi
                show_status
                ;;
            reset)
                reset_k8s
                ;;
            status)
                show_status
                ;;
            *)
                log_error "Unknown k8s action: ${action}"
                usage
                exit 1
                ;;
        esac
        return 0
    fi

    # Handle k3s module (single-node k3s on Linux; kubeadm path unchanged in k8s module)
    if [[ "${module}" == "k3s" ]]; then
        case "${action}" in
            install|init)
                check_root
                install_helm || exit 1
                install_k3s || exit 1
                if [[ "${AUTO_INSTALL_INGRESS_NGINX}" == "true" ]]; then
                    install_ingress_nginx || exit 1
                fi
                if [[ "${AUTO_GENERATE_CONFIG}" == "true" ]]; then
                    generate_config_yaml || exit 1
                fi
                show_k3s_status
                ;;
            uninstall)
                check_root
                uninstall_k3s
                ;;
            status)
                show_k3s_status
                ;;
            *)
                log_error "Unknown k3s action: ${action}"
                usage
                exit 1
                ;;
        esac
        return 0
    fi

    # Bundle: platform data services only (for bring-your-own kube, e.g. kind on macOS).
    if [[ "${module}" == "data-services" ]]; then
        case "${action}" in
            install|init)
                require_root_for_helm_cluster_addons_only
                ensure_data_services || exit 1
                ;;
            uninstall)
                require_root_for_helm_cluster_addons_only
                uninstall_platform_data_services "$@"
                ;;
            *)
                log_error "Unknown data-services action: ${action}"
                usage
                exit 1
                ;;
        esac
        return 0
    fi
    
    # Handle mariadb module
    if [[ "${module}" == "mariadb" ]]; then
        case "${action}" in
            install|init)
                require_root_for_helm_cluster_addons_only
                install_mariadb
                ;;
            uninstall)
                require_root_for_helm_cluster_addons_only
                # Parse additional arguments
                # shift 2
                while [[ $# -gt 0 ]]; do
                    case "$1" in
                        --force)
                            MARIADB_UNINSTALL_FORCE=true
                            export MARIADB_UNINSTALL_FORCE
                            shift
                            ;;
                        --delete-data)
                            MARIADB_PURGE_PVC=true
                            export MARIADB_PURGE_PVC
                            shift
                            ;;
                        *)
                            shift
                            ;;
                    esac
                done
                uninstall_mariadb
                ;;
            *)
                log_error "Unknown mariadb action: ${action}"
                usage
                exit 1
                ;;
        esac
        return 0
    fi
    
    # Handle redis module
    if [[ "${module}" == "redis" ]]; then
        case "${action}" in
            install|init)
                require_root_for_helm_cluster_addons_only
                install_redis
                ;;
            uninstall)
                require_root_for_helm_cluster_addons_only
                uninstall_redis
                ;;
            fix-acl)
                fix_redis_acl
                ;;
            *)
                log_error "Unknown redis action: ${action}"
                usage
                exit 1
                ;;
        esac
        return 0
    fi

    # Handle opensearch module
    if [[ "${module}" == "opensearch" ]]; then
        case "${action}" in
            install|init)
                require_root_for_helm_cluster_addons_only
                install_opensearch
                ;;
            uninstall)
                require_root_for_helm_cluster_addons_only
                uninstall_opensearch
                ;;
            *)
                log_error "Unknown opensearch action: ${action}"
                usage
                exit 1
                ;;
        esac
        return 0
    fi

    # Handle mongodb module (disabled)
    # if [[ "${module}" == "mongodb" ]]; then
    #     case "${action}" in
    #         install|init)
    #             check_root
    #             # install_mongodb  # MongoDB disabled
    #             ;;
    #         uninstall)
    #             check_root
    #             # uninstall_mongodb  # MongoDB disabled
    #             ;;
    #         *)
    #             log_error "Unknown mongodb action: ${action}"
    #             usage
    #             exit 1
    #             ;;
    #     esac
    #     return 0
    # fi

    # Handle kafka module
    if [[ "${module}" == "kafka" ]]; then
        case "${action}" in
            install|init)
                require_root_for_helm_cluster_addons_only
                install_kafka
                ;;
            uninstall)
                require_root_for_helm_cluster_addons_only
                uninstall_kafka
                ;;
            *)
                log_error "Unknown kafka action: ${action}"
                usage
                exit 1
                ;;
        esac
        return 0
    fi
    
    # Handle ingress-nginx module
    if [[ "${module}" == "ingress-nginx" ]]; then
        case "${action}" in
            install|init)
                check_root
                install_ingress_nginx
                ;;
            uninstall)
                check_root
                uninstall_ingress_nginx
                ;;
            *)
                log_error "Unknown ingress-nginx action: ${action}"
                usage
                exit 1
                ;;
        esac
        return 0
    fi
    
    # Handle bkn-foundry module
    if [[ "${module}" == "bkn-foundry" ]] || [[ "${module}" == "foundry" ]] || [[ "${module}" == "bkn-foundry" ]] || [[ "${module}" == "core" ]]; then
        case "${action}" in
            install|init)
                parse_core_args "install" "$@"
                confirm_access_address_before_install
                install_core
                ;;
            download)
                parse_core_args "download" "$@"
                download_core
                ;;
            uninstall)
                parse_core_args "uninstall" "$@"
                uninstall_core
                ;;
            status)
                parse_core_args "status" "$@"
                show_install_status
                ;;
            publish-status)
                parse_core_args "status" "$@"
                gen_install_status_json
                ;;
            *)
                log_error "Unknown bkn-foundry action: ${action}"
                usage
                exit 1
                ;;
        esac
        return 0
    fi
    

    # Handle etrino module
    if [[ "${module}" == "etrino" ]]; then
        local etrino_script="${SCRIPT_DIR}/scripts/services/etrino.sh"
        if [[ ! -f "${etrino_script}" ]]; then
            log_error "Etrino script not found at ${etrino_script}"
            exit 1
        fi

        case "${action}" in
            install|init)
                if [[ "${OPENBKN_SKIP_PLATFORM_BOOTSTRAP:-false}" != "true" ]]; then
                    check_root
                fi
                CONFIG_FILE="${CONFIG_YAML_PATH}" bash "${etrino_script}" install "$@"
                ;;
            status)
                CONFIG_FILE="${CONFIG_YAML_PATH}" bash "${etrino_script}" status "$@"
                ;;
            uninstall)
                if [[ "${OPENBKN_SKIP_PLATFORM_BOOTSTRAP:-false}" != "true" ]]; then
                    check_root
                fi
                CONFIG_FILE="${CONFIG_YAML_PATH}" bash "${etrino_script}" uninstall "$@"
                ;;
            *)
                log_error "Unknown etrino action: ${action}"
                usage
                exit 1
                ;;
        esac
        return 0
    fi

    # Handle all/infra module (infrastructure: k8s + data services)
    # 'all' is an alias for 'infra' for backward compatibility
    if [[ "${module}" == "all" ]] || [[ "${module}" == "infra" ]]; then
        case "${action}" in
            install|init)
                check_root
                log_info "=========================================="
                log_info "  Deploying Infrastructure (K8s + Data Services)"
                log_info "=========================================="
                
                # Pre-install dependencies (containerd, k8s, helm) before k8s init
                log_info "Pre-installing dependencies..."
                detect_package_manager
                install_containerd
                install_kubernetes
                install_helm
                
                check_prerequisites
                init_k8s_master
                allow_master_scheduling
                install_cni
                wait_for_dns

                if [[ "${AUTO_INSTALL_LOCALPV}" == "true" ]]; then
                    if [[ -z "$(kubectl get storageclass --no-headers 2>/dev/null)" ]]; then
                        install_localpv
                    fi
                fi
                install_mariadb
                install_redis
                install_kafka
                # install_mongodb  # MongoDB disabled
                if [[ "${AUTO_INSTALL_INGRESS_NGINX}" == "true" ]]; then
                    install_ingress_nginx
                fi
                install_opensearch
                if [[ "${AUTO_GENERATE_CONFIG}" == "true" ]]; then
                    generate_config_yaml
                fi
                show_status
                log_info "Infrastructure deployment completed!"
                ;;
            reset)
                check_root
                log_info "Resetting infrastructure..."
                uninstall_opensearch || true
                uninstall_ingress_nginx || true
                # uninstall_mongodb || true  # MongoDB disabled
                uninstall_kafka || true
                uninstall_redis || true
                uninstall_mariadb || true
                reset_k8s
                log_info "Infrastructure reset completed!"
                ;;
            *)
                log_error "Unknown infra action: ${action}"
                usage
                exit 1
                ;;
        esac
        return 0
    fi
    
    # Handle bkn module (application services)
    if [[ "${module}" == "bkn" ]] || [[ "${module}" == "bkn" ]]; then
        case "${action}" in
            init)
                check_root
                shift 2
                log_info "=========================================="
                log_info "  Deploying BKN Foundry Application Services"
                log_info "=========================================="
                
                # Parse common args for all bkn services
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
                        *)
                            shift
                            ;;
                    esac
                done
                
                # Install all BKN Foundry services in order
                install_core

                log_info "BKN Foundry application services deployment completed!"
                ;;
            uninstall)
                check_root
                log_info "Uninstalling BKN Foundry application services..."
                uninstall_core || true
                log_info "BKN Foundry application services uninstalled!"
                ;;
            status)
                log_info "BKN Foundry application services status:"
                show_core_status
                ;;
            *)
                log_error "Unknown bkn action: ${action}"
                usage
                exit 1
                ;;
        esac
        return 0
    fi
    
    # Handle full module (complete deployment: infra + bkn)
    if [[ "${module}" == "full" ]]; then
        case "${action}" in
            init)
                check_root
                shift 2
                log_info "╔════════════════════════════════════════════════════════════════╗"
                log_info "║       Full Deployment: Infrastructure + BKN Foundry Services       ║"
                log_info "╚════════════════════════════════════════════════════════════════╝"
                
                # Save args for bkn
                local bkn_args=("$@")
                
                # Step 1: Deploy infrastructure
                log_info ""
                log_info "Step 1/2: Deploying Infrastructure..."
                log_info ""
                
                detect_package_manager
                install_containerd
                install_kubernetes
                install_helm
                
                check_prerequisites
                init_k8s_master
                allow_master_scheduling
                install_cni
                wait_for_dns

                if [[ "${AUTO_INSTALL_LOCALPV}" == "true" ]]; then
                    if [[ -z "$(kubectl get storageclass --no-headers 2>/dev/null)" ]]; then
                        install_localpv
                    fi
                fi
                install_mariadb
                install_redis
                install_kafka
                # install_mongodb  # MongoDB disabled
                if [[ "${AUTO_INSTALL_INGRESS_NGINX}" == "true" ]]; then
                    install_ingress_nginx
                fi
                install_opensearch
                if [[ "${AUTO_GENERATE_CONFIG}" == "true" ]]; then
                    generate_config_yaml
                fi

                # Step 2: Deploy BKN Foundry services
                log_info ""
                log_info "Step 2/2: Deploying BKN Foundry Application Services..."
                log_info ""
                
                # Parse bkn args
                for arg in "${bkn_args[@]}"; do
                    case "$arg" in
                        --version=*)
                            HELM_CHART_VERSION="${arg#*=}"
                            ;;
                        --helm_repo=*)
                            HELM_CHART_REPO_URL="${arg#*=}"
                            ;;
                    esac
                done
                
                install_studio
                install_bkn
                install_vega
                install_agentoperator
                install_sandboxruntime

                show_status
                log_info ""
                log_info "╔════════════════════════════════════════════════════════════════╗"
                log_info "║                   Full Deployment Completed!                   ║"
                log_info "╚════════════════════════════════════════════════════════════════╝"
                ;;
            reset)
                check_root
                log_info "Full reset: Uninstalling all components..."
                
                # Uninstall BKN Foundry services first
                uninstall_sandboxruntime || true
                uninstall_agentoperator || true
                uninstall_bkn || true
                uninstall_vega || true
                uninstall_studio || true

                # Then uninstall infrastructure
                uninstall_opensearch || true
                uninstall_ingress_nginx || true
                # uninstall_mongodb || true  # MongoDB disabled
                uninstall_kafka || true
                uninstall_redis || true
                uninstall_mariadb || true
                reset_k8s
                
                log_info "Full reset completed!"
                ;;
            *)
                log_error "Unknown full action: ${action}"
                usage
                exit 1
                ;;
        esac
        return 0
    fi
    
    # Unknown module
    usage
    exit 1
}

main "$@"
