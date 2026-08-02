package httphandler

import (
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/rdto"
)

// GetAccessProfile godoc
// @Summary Get the current BKN Trace access profile
// @Description Returns server-derived page capabilities and a scope fingerprint. Roles and managed resource identifiers are never accepted from or disclosed to the client.
// @Tags access
// @Produce json
// @Success 200 {object} rdto.AccessProfileResponse
// @Failure 401 {object} rdto.ErrorResponse
// @Failure 405 {object} rdto.ErrorResponse
// @Failure 503 {object} rdto.ErrorResponse
// @Router /access-profile [get]
func (h *EvidenceHandler) GetAccessProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, rdto.ErrorResponse{Code: "METHOD_NOT_ALLOWED", Message: "only GET is supported"})
		return
	}
	if !h.authorizeQueryGateway(w, r) {
		return
	}
	if h.authorizationScopeResolver == nil {
		writeJSON(w, http.StatusServiceUnavailable, rdto.ErrorResponse{
			Code: "ACCESS_PROFILE_NOT_CONFIGURED", Message: "current access profile resolution is not configured",
		})
		return
	}
	scope, ok := h.queryScopeFromRequest(w, r, false)
	if !ok || scope.AccessProfile == nil {
		return
	}
	profile := *scope.AccessProfile
	writeJSON(w, http.StatusOK, accessProfileResponse(profile))
}

func accessProfileResponse(profile evidencevo.AccessProfile) rdto.AccessProfileResponse {
	capabilities := observabilityvo.CapabilitiesFor(profile)
	return rdto.AccessProfileResponse{
		BusinessProvenanceOwn:             capabilities.BusinessProvenanceOwn,
		BusinessProvenanceManagedNetworks: capabilities.BusinessProvenanceManagedNetworks,
		TechnicalTrace:                    capabilities.TechnicalTrace,
		SecurityAudit:                     capabilities.SecurityAudit,
		ManagementAudit:                   capabilities.ManagementAudit,
		GlobalLogSearch:                   capabilities.GlobalLogSearch,
		AllowedLogCategories:              capabilities.AllowedLogCategories,
		LogSensitiveFields:                capabilities.LogSensitiveFields,
		LogExport:                         capabilities.LogExport,
		LogPolicyRead:                     capabilities.LogPolicyRead,
		AccessScopeFingerprint:            profile.Fingerprint,
	}
}
