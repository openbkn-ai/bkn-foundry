#!/usr/bin/env bash
# Completion report for deploy/onboard.sh. Source after onboard libs; call onboard_print_completion_report on success.
# Opt out: ONBOARD_NO_COMPLETION_REPORT=1
# shellcheck source=/dev/null

# Optional state (set by probe steps):
#   ONBOARD_REPORT_MAIN_MODE   interactive | bkn-only | config-yaml
#   ONBOARD_REPORT_ISF_TEST_USER  human-readable status
#   ONBOARD_REPORT_MODELS         human-readable status (e.g. "skipped — N LLM, M small/embedding already registered")
#   ONBOARD_REPORT_BKN_CM         human-readable status (e.g. "skipped — already patched (defaultSmallModelName=…)")

onboard_print_completion_report() {
    if [[ "${ONBOARD_NO_COMPLETION_REPORT:-}" == "1" || "${ONBOARD_NO_COMPLETION_REPORT:-}" == "true" ]]; then
        return 0
    fi

    local _isfu _line _kwh _kctx _bd _acurl _isf _isf_styled
    _isfu="${ONBOARD_REPORT_ISF_TEST_USER:-}"
    _line="--------------------------------------------"

    if type onboard_isf_full_install &>/dev/null && onboard_isf_full_install 2>/dev/null; then
        _isf="ISF (full install detected)"
        _isf_styled="${GREEN}${_isf}${NC}"
    else
        _isf="Minimum install / no ISF components detected"
        _isf_styled="${YELLOW}${_isf}${NC}"
    fi

    if command -v kweaver &>/dev/null; then
        _kwh="$(kweaver --version 2>/dev/null | head -1 || true)"
    else
        _kwh="(kweaver not on PATH)"
    fi

    if command -v kubectl &>/dev/null; then
        _kctx="$(kubectl config current-context 2>/dev/null || echo "(kubectl context not set)")"
    else
        _kctx="(kubectl not on PATH or not configured)"
    fi

    _bd="${DEPLOY_BUSINESS_DOMAIN:-bd_public}"
    if type onboard_default_access_base_url &>/dev/null; then
        _acurl="$(onboard_default_access_base_url 2>/dev/null || true)"
    else
        _acurl="${ONBOARD_DEFAULT_ACCESS_BASE:-(set ONBOARD_DEFAULT_ACCESS_BASE or use default host IP)}"
    fi

    {
        echo ""
        echo "============================================"
        echo "  BKN Foundry Onboard — completion report"
        echo "  Time (UTC)  $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
        echo "  Mode        ${ONBOARD_REPORT_MAIN_MODE:-interactive}"
        echo "${_line}"
        echo "  Environment host=$(hostname 2>/dev/null || echo '?')"
        echo "  Node          $(command -v node &>/dev/null && node -v || echo '—')"
        echo "  kweaver       ${_kwh}"
        echo "  kubectl       ctx=${_kctx}  namespace=${NAMESPACE:-kweaver}"
        echo "  Business -bd  ${_bd}  (DEPLOY_BUSINESS_DOMAIN)"
        echo "  Default base  ${_acurl}"
        echo "${_line}"
        echo -e "  Install type   ${_isf_styled}"
        echo "  User [test]    ${_isfu:-(not run or not recorded)}"
        echo "  Models         ${ONBOARD_REPORT_MODELS:-(not run or not recorded)}"
        echo "  BKN ConfigMap  ${ONBOARD_REPORT_BKN_CM:-(not run or not recorded)}"
        echo "${_line}"
        case "${_isfu}" in
            created*)
                echo -e "  ${GREEN}✓ User [test] was created for the first time on this platform.${NC}"
                echo "${_line}"
                ;;
            ready*)
                echo -e "  ${GREEN}✓ User [test] is ready on this platform (already existed; roles re-synced).${NC}"
                echo "${_line}"
                ;;
        esac
        echo "  Next steps"
        case "${_isfu}" in
            ready*|created*)
                echo "   • User test:  default sign-in:  kweaver auth login ${_acurl} -u test -p '<password>' --http-signin -k"
                ;;
        esac
        echo "   • Verify:    kweaver bkn list -bd ${_bd} --pretty"
        echo "   • Toolbox:   the Context Loader toolset is auto-imported by agent-retrieval at startup (no manual step)."
        echo "   • Docs:      https://github.com/openbkn-ai/bkn-foundry/blob/main/help/README.md"
        echo "                https://github.com/openbkn-ai/bkn-foundry/blob/main/help/en/README.md  (EN)"
        echo "                https://github.com/openbkn-ai/bkn-foundry/blob/main/help/zh/README.md  (中文)"
        echo "============================================"
        echo ""
    } 2>/dev/null || {
        echo ""
        echo "============================================"
        echo "  BKN Foundry Onboard — done"
        echo "============================================"
        echo ""
    }
}
