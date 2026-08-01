package httphandler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/logsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/driveradapter/api/rdto"
)

type LogHandler struct {
	service    *logsvc.Service
	authorizer *EvidenceHandler
}

func NewLogHandler(service *logsvc.Service, authorizer *EvidenceHandler) *LogHandler {
	return &LogHandler{service: service, authorizer: authorizer}
}

func (handler *LogHandler) ListLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, rdto.ErrorResponse{
			Code: "method_not_allowed", Message: "only GET is supported",
		})
		return
	}
	if handler.service == nil || handler.authorizer == nil {
		writeJSON(w, http.StatusServiceUnavailable, rdto.ErrorResponse{
			Code: "sources_unavailable", Message: "observability log query is not configured",
		})
		return
	}
	if !handler.authorizer.authorizeQueryGateway(w, r) {
		return
	}
	scope, ok := handler.authorizer.queryScopeFromRequest(w, r)
	if !ok || scope.AccessProfile == nil {
		return
	}
	query, err := parseLogQuery(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{
			Code: "invalid_log_filter", Message: err.Error(),
		})
		return
	}
	query.ScopeFingerprint = scope.AccessProfile.Fingerprint
	sourceContext := observabilityvo.WithSourceAuthorization(r.Context(), r.Header.Get("Authorization"))
	result, err := handler.service.List(sourceContext, *scope.AccessProfile, query)
	if err != nil {
		switch {
		case errors.Is(err, logsvc.ErrAccessDenied):
			writeJSON(w, http.StatusForbidden, rdto.ErrorResponse{
				Code: "observability_access_denied", Message: "the current access profile cannot search the requested logs",
			})
		case errors.Is(err, logsvc.ErrSourcesUnavailable):
			writeJSON(w, http.StatusServiceUnavailable, rdto.ErrorResponse{
				Code: "sources_unavailable", Message: "all authorized log sources are unavailable",
			})
		default:
			writeJSON(w, http.StatusInternalServerError, rdto.ErrorResponse{
				Code: "log_query_failed", Message: "observability log query failed",
			})
		}
		return
	}

	var nextCursor *string
	if result.NextCursor != "" {
		nextCursor = &result.NextCursor
	}
	count := result.Count
	accuracy := "exact"
	if result.Partial || !result.CountExact {
		accuracy = "partial"
	}
	requestID := optionalString(query.RequestID)
	currentTraceID := optionalString(query.TraceID)
	relatedTraceIDs := uniqueTraceIDs(result.Records)
	writeJSON(w, http.StatusOK, rdto.LogListResponse{
		Data: result.Records, NextCursor: nextCursor, Partial: result.Partial,
		Count: rdto.LogCount{Value: &count, Accuracy: accuracy}, SourceStatus: result.SourceStatus,
		RequestTraceContext: rdto.RequestTraceContext{
			RequestID: requestID, CurrentTraceID: currentTraceID, RelatedTraceIDs: relatedTraceIDs,
		},
	})
}

func (handler *LogHandler) GetLog(w http.ResponseWriter, r *http.Request) {
	profile, ok := handler.authorizedProfile(w, r)
	if !ok {
		return
	}
	logID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/observability/v1/logs/"))
	if logID == "" || strings.Contains(logID, "/") {
		writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{Code: "invalid_log_id", Message: "log_id is required"})
		return
	}
	sourceContext := observabilityvo.WithSourceAuthorization(r.Context(), r.Header.Get("Authorization"))
	record, err := handler.service.Get(sourceContext, profile, logID)
	if err != nil {
		if errors.Is(err, logsvc.ErrNotDisclosed) {
			writeJSON(w, http.StatusNotFound, rdto.ErrorResponse{Code: "resource_not_disclosed", Message: "log was not found in the authorized scope"})
			return
		}
		writeLogServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rdto.LogDetailResponse{
		Data:            record,
		FieldProjection: rdto.LogFieldProjection{PolicyRevision: "r6.2-default", RedactedFields: []string{}},
		RequestTraceContext: rdto.RequestTraceContext{
			RequestID: optionalString(record.RequestID), CurrentTraceID: optionalString(record.TraceID),
			RelatedTraceIDs: uniqueTraceIDs([]observabilityvo.LogRecord{record}),
		},
	})
}

func (handler *LogHandler) GetLogFacets(w http.ResponseWriter, r *http.Request) {
	profile, ok := handler.authorizedProfile(w, r)
	if !ok {
		return
	}
	query, err := parseLogQuery(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{Code: "invalid_log_filter", Message: err.Error()})
		return
	}
	facet := strings.TrimSpace(r.URL.Query().Get("facet"))
	if !supportedLogFacet(facet) {
		writeJSON(w, http.StatusBadRequest, rdto.ErrorResponse{Code: "invalid_log_filter", Message: "facet is not supported"})
		return
	}
	query.ScopeFingerprint = profile.Fingerprint
	sourceContext := observabilityvo.WithSourceAuthorization(r.Context(), r.Header.Get("Authorization"))
	result, err := handler.service.Facets(sourceContext, profile, query, facet)
	if err != nil {
		writeLogServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rdto.LogFacetResponse{
		Data: result.Values, Partial: result.Partial, SourceStatus: result.SourceStatus, NextCursor: nil,
	})
}

func (handler *LogHandler) ListLogSources(w http.ResponseWriter, r *http.Request) {
	profile, ok := handler.authorizedProfile(w, r)
	if !ok {
		return
	}
	data, err := handler.service.Sources(profile)
	if err != nil {
		writeLogServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rdto.LogSourcesResponse{Data: data})
}

func (handler *LogHandler) ListLogPolicies(w http.ResponseWriter, r *http.Request) {
	profile, ok := handler.authorizedProfile(w, r)
	if !ok {
		return
	}
	data, err := handler.service.Policies(profile)
	if err != nil {
		writeLogServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rdto.LogPoliciesResponse{Data: data})
}

func (handler *LogHandler) authorizedProfile(w http.ResponseWriter, r *http.Request) (evidencevo.AccessProfile, bool) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, rdto.ErrorResponse{Code: "method_not_allowed", Message: "only GET is supported"})
		return evidencevo.AccessProfile{}, false
	}
	if handler.service == nil || handler.authorizer == nil {
		writeJSON(w, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "sources_unavailable", Message: "observability log query is not configured"})
		return evidencevo.AccessProfile{}, false
	}
	if !handler.authorizer.authorizeQueryGateway(w, r) {
		return evidencevo.AccessProfile{}, false
	}
	scope, ok := handler.authorizer.queryScopeFromRequest(w, r)
	if !ok || scope.AccessProfile == nil {
		return evidencevo.AccessProfile{}, false
	}
	return *scope.AccessProfile, true
}

func supportedLogFacet(value string) bool {
	switch value {
	case "log_category", "severity_text", "service_name", "deployment_environment", "event_name":
		return true
	default:
		return false
	}
}

func writeLogServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, logsvc.ErrAccessDenied):
		writeJSON(w, http.StatusForbidden, rdto.ErrorResponse{Code: "observability_access_denied", Message: "the current access profile cannot access the requested logs"})
	case errors.Is(err, logsvc.ErrSourcesUnavailable):
		writeJSON(w, http.StatusServiceUnavailable, rdto.ErrorResponse{Code: "sources_unavailable", Message: "all authorized log sources are unavailable"})
	default:
		writeJSON(w, http.StatusInternalServerError, rdto.ErrorResponse{Code: "log_query_failed", Message: "observability log query failed"})
	}
}

func parseLogQuery(r *http.Request) (observabilityvo.LogQuery, error) {
	values := r.URL.Query()
	timeFrom, err := parseOptionalLogTime(values.Get("from"), "from")
	if err != nil {
		return observabilityvo.LogQuery{}, err
	}
	timeTo, err := parseOptionalLogTime(values.Get("to"), "to")
	if err != nil {
		return observabilityvo.LogQuery{}, err
	}
	if timeFrom != nil && timeTo != nil && timeTo.Before(*timeFrom) {
		return observabilityvo.LogQuery{}, errors.New("to must not be before from")
	}
	limit, err := parseBoundedInteger(values.Get("limit"), 50, 1, 200, "limit")
	if err != nil {
		return observabilityvo.LogQuery{}, err
	}
	severity, err := parseBoundedInteger(values.Get("severity_min"), 0, 1, 24, "severity_min")
	if err != nil {
		return observabilityvo.LogQuery{}, err
	}
	failedOnly := false
	if raw := strings.TrimSpace(values.Get("failed_only")); raw != "" {
		failedOnly, err = strconv.ParseBool(raw)
		if err != nil {
			return observabilityvo.LogQuery{}, errors.New("failed_only must be true or false")
		}
	}
	return observabilityvo.LogQuery{
		Query: values.Get("q"), TimeFrom: timeFrom, TimeTo: timeTo,
		Categories: queryList(values["categories"]), SeverityMinimum: severity,
		Services: queryList(values["services"]), Environments: queryList(values["environments"]),
		EventNames: queryList(values["event_names"]), BusinessDomain: values.Get("business_domain_id"),
		ActorID: values.Get("actor_id"), ApplicationID: values.Get("application_id"),
		ResourceType: values.Get("resource_type"), ResourceID: values.Get("resource_id"),
		ConversationID: values.Get("conversation_id"), InteractionID: values.Get("interaction_id"),
		OperationID: values.Get("operation_id"), RequestID: values.Get("request_id"),
		TraceID: values.Get("trace_id"), SpanID: values.Get("span_id"), FailedOnly: failedOnly,
		Limit: limit, Cursor: values.Get("cursor"),
	}, nil
}

func parseOptionalLogTime(raw, name string) (*time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, errors.New(name + " must be an RFC3339 timestamp")
	}
	return &value, nil
}

func parseBoundedInteger(raw string, defaultValue, minimum, maximum int, name string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return defaultValue, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, errors.New(name + " is outside the supported range")
	}
	return value, nil
}

func queryList(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			if item = strings.TrimSpace(item); item != "" {
				result = append(result, item)
			}
		}
	}
	return result
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func uniqueTraceIDs(records []observabilityvo.LogRecord) []string {
	result := make([]string, 0)
	for _, record := range records {
		if record.TraceID != "" && !containsString(result, record.TraceID) {
			result = append(result, record.TraceID)
		}
	}
	return result
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
