package httphandler

import (
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
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
	scope, ok := h.queryScopeFromRequest(w, r)
	if !ok || scope.AccessProfile == nil {
		return
	}
	profile := *scope.AccessProfile
	writeJSON(w, http.StatusOK, accessProfileResponse(profile))
}

func accessProfileResponse(profile evidencevo.AccessProfile) rdto.AccessProfileResponse {
	roles := make(map[string]struct{}, len(profile.Roles))
	for _, role := range profile.Roles {
		roles[role] = struct{}{}
	}
	hasRole := func(values ...string) bool {
		for _, value := range values {
			if _, ok := roles[value]; ok {
				return true
			}
		}
		return false
	}
	active := profile.AccountActive && profile.TenantActive
	return rdto.AccessProfileResponse{
		BusinessProvenanceOwn: active,
		BusinessProvenanceManagedNetworks: active && hasRole("network_builder") &&
			len(profile.ManagedKnowledgeNetworkIDs) > 0,
		TechnicalTrace:         false,
		SecurityAudit:          false,
		ManagementAudit:        false,
		GlobalLogSearch:        false,
		AccessScopeFingerprint: profile.Fingerprint,
	}
}
