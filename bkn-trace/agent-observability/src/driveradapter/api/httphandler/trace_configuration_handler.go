package httphandler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/traceconfig"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

// TraceEvidenceConfigurationHandler exposes only the platform-wide boolean
// desired state. Cluster targets and Helm values remain inside the release
// controller's reviewed manifest.
type TraceEvidenceConfigurationHandler struct {
	service    *traceconfig.ConfigurationService
	authorizer *EvidenceHandler
}

func NewTraceEvidenceConfigurationHandler(service *traceconfig.ConfigurationService, authorizer *EvidenceHandler) *TraceEvidenceConfigurationHandler {
	return &TraceEvidenceConfigurationHandler{service: service, authorizer: authorizer}
}

func (handler *TraceEvidenceConfigurationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if handler.service == nil || handler.authorizer == nil {
		writeJSON(w, r, http.StatusServiceUnavailable, configurationError{Code: "RELEASE_ORCHESTRATOR_UNAVAILABLE", Message: "trace evidence release control is not configured"})
		return
	}
	scope, ok := trustedQueryScopeFromContext(r.Context())
	if !ok || scope.AccessProfile == nil {
		writeJSON(w, r, http.StatusForbidden, configurationError{Code: "OBSERVABILITY_CONFIGURATION_FORBIDDEN", Message: "configuration access is not authorized"})
		return
	}
	capabilities := observabilityvo.CapabilitiesFor(*scope.AccessProfile)
	switch r.Method {
	case http.MethodGet:
		if !capabilities.TraceEvidenceConfigurationRead {
			writeJSON(w, r, http.StatusForbidden, configurationError{Code: "OBSERVABILITY_CONFIGURATION_FORBIDDEN", Message: "configuration read is not authorized"})
			return
		}
		writeJSON(w, r, http.StatusOK, traceConfigurationResponse(handler.service.Current(r.Context())))
	case http.MethodPut:
		if !capabilities.TraceEvidenceConfigurationWrite {
			writeJSON(w, r, http.StatusForbidden, configurationError{Code: "OBSERVABILITY_CONFIGURATION_FORBIDDEN", Message: "configuration change is not authorized"})
			return
		}
		var request struct {
			Enabled          *bool   `json:"enabled"`
			ExpectedRevision *uint64 `json:"expected_revision"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || request.Enabled == nil || request.ExpectedRevision == nil || decoder.Decode(&struct{}{}) != io.EOF {
			writeJSON(w, r, http.StatusBadRequest, configurationError{Code: "INVALID_PARAMETER", Message: "enabled and expected_revision are required"})
			return
		}
		configuration, err := handler.service.Request(r.Context(), traceconfig.ConfigurationRequest{
			Enabled: *request.Enabled, ExpectedRevision: *request.ExpectedRevision, RequestedBy: scope.AccessProfile.EffectiveSubjectID,
		})
		if err != nil {
			switch {
			case errors.Is(err, traceconfig.ErrRevisionConflict):
				writeJSON(w, r, http.StatusConflict, configurationError{Code: "CONFIG_REVISION_CONFLICT", Message: "configuration revision is stale"})
			case errors.Is(err, traceconfig.ErrOperationInProgress):
				writeJSON(w, r, http.StatusConflict, configurationError{Code: "TRACE_EVIDENCE_OPERATION_IN_PROGRESS", Message: "another trace evidence operation is active"})
			default:
				writeJSON(w, r, http.StatusServiceUnavailable, configurationError{Code: "RELEASE_ORCHESTRATOR_UNAVAILABLE", Message: "release control is unavailable"})
			}
			return
		}
		writeJSON(w, r, http.StatusAccepted, traceConfigurationResponse(configuration))
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeJSON(w, r, http.StatusMethodNotAllowed, configurationError{Code: "METHOD_NOT_ALLOWED", Message: "only GET and PUT are supported"})
	}
}

type configurationError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type traceEvidenceConfigurationResponse struct {
	DesiredEnabled     bool                        `json:"desired_enabled"`
	EffectiveEnabled   bool                        `json:"effective_enabled"`
	LastStableRevision uint64                      `json:"last_stable_revision"`
	Operation          *traceconfig.Operation      `json:"operation,omitempty"`
	Revision           uint64                      `json:"revision"`
	Services           []traceconfig.ServiceStatus `json:"services"`
}

func traceConfigurationResponse(configuration traceconfig.Configuration) traceEvidenceConfigurationResponse {
	return traceEvidenceConfigurationResponse{
		DesiredEnabled: configuration.DesiredEnabled, EffectiveEnabled: configuration.EffectiveEnabled,
		LastStableRevision: configuration.LastStableRevision, Operation: configuration.Operation,
		Revision: configuration.Revision, Services: configuration.Services,
	}
}
