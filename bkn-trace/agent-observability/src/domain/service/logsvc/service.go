package logsvc

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

var (
	ErrAccessDenied       = errors.New("observability access denied")
	ErrSourcesUnavailable = errors.New("observability sources unavailable")
)

type Source interface {
	ID() string
	Search(context.Context, observabilityvo.LogQuery) (observabilityvo.SourcePage, error)
}

type Service struct {
	sources []Source
}

func New(sources []Source) *Service {
	return &Service{sources: append([]Source(nil), sources...)}
}

func (service *Service) List(
	ctx context.Context,
	profile evidencevo.AccessProfile,
	query observabilityvo.LogQuery,
) (observabilityvo.ListResult, error) {
	capabilities := observabilityvo.CapabilitiesFor(profile)
	if !capabilities.GlobalLogSearch && !query.IsAssociatedDrilldown() {
		return observabilityvo.ListResult{}, ErrAccessDenied
	}
	if !capabilities.GlobalLogSearch && !associatedCategoriesOnly(query.Categories) {
		return observabilityvo.ListResult{}, ErrAccessDenied
	}

	result := observabilityvo.ListResult{Records: []observabilityvo.LogRecord{}, SourceStatus: []observabilityvo.SourceStatus{}}
	succeeded := 0
	failed := 0
	for _, source := range service.sources {
		page, err := source.Search(ctx, query)
		if err != nil {
			failed++
			result.SourceStatus = append(result.SourceStatus, observabilityvo.SourceStatus{
				SourceID: source.ID(), Status: "unavailable", Reason: "source_query_failed",
				Reliability: "best_effort", CountAccuracy: "unavailable",
			})
			continue
		}
		succeeded++
		result.SourceStatus = append(result.SourceStatus, observabilityvo.SourceStatus{
			SourceID: source.ID(), Status: "available", Reliability: "best_effort",
			CountAccuracy: normalizedAccuracy(page.CountAccuracy),
		})
		for _, record := range page.Records {
			if matchesQuery(record, query) && canReadLog(profile, capabilities, record, query.IsAssociatedDrilldown()) {
				result.Records = append(result.Records, record)
			}
		}
	}
	if len(service.sources) > 0 && succeeded == 0 {
		return observabilityvo.ListResult{}, ErrSourcesUnavailable
	}

	sort.SliceStable(result.Records, func(i, j int) bool {
		if result.Records[i].EventTimestamp.Equal(result.Records[j].EventTimestamp) {
			if result.Records[i].SourceID == result.Records[j].SourceID {
				return result.Records[i].LogID > result.Records[j].LogID
			}
			return result.Records[i].SourceID < result.Records[j].SourceID
		}
		return result.Records[i].EventTimestamp.After(result.Records[j].EventTimestamp)
	})
	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if len(result.Records) > limit {
		result.Records = result.Records[:limit]
	}
	result.Partial = failed > 0
	result.Count = int64(len(result.Records))
	result.CountExact = !result.Partial
	return result, nil
}

func associatedCategoriesOnly(categories []string) bool {
	for _, category := range categories {
		if category != observabilityvo.CategoryRuntimeBusiness && category != observabilityvo.CategoryRuntimeModel {
			return false
		}
	}
	return true
}

func canReadLog(
	profile evidencevo.AccessProfile,
	capabilities observabilityvo.AccessCapabilities,
	record observabilityvo.LogRecord,
	associated bool,
) bool {
	if profile.TenantID == "" || record.TenantID == "" || profile.TenantID != record.TenantID {
		return false
	}
	if record.BusinessDomain != "" && profile.BusinessDomain != record.BusinessDomain {
		return false
	}
	if associated && !capabilities.GlobalLogSearch &&
		record.Category != observabilityvo.CategoryRuntimeBusiness && record.Category != observabilityvo.CategoryRuntimeModel {
		return false
	}
	if capabilities.GlobalLogSearch && !contains(capabilities.AllowedLogCategories, record.Category) {
		return false
	}

	recordScope := evidencevo.RecordScope{
		TenantID: record.TenantID, BusinessDomain: record.BusinessDomain,
		EffectiveSubjectID: record.EffectiveSubjectID, ApplicationPrincipalID: record.ApplicationID,
		KnowledgeNetworkIDs: record.KnowledgeNetworkIDs,
	}
	switch record.Category {
	case observabilityvo.CategoryRuntimeBusiness, observabilityvo.CategoryRuntimeModel:
		return evidencevo.CanReadRecord(profile, recordScope, evidencevo.AccessViewBusiness) ||
			hasRole(profile, "admin", "super_admin")
	case observabilityvo.CategoryRuntimeSystem:
		return evidencevo.CanReadRecord(profile, recordScope, evidencevo.AccessViewTechnical)
	default:
		return true
	}
}

func matchesQuery(record observabilityvo.LogRecord, query observabilityvo.LogQuery) bool {
	return matchesOptional(record.TraceID, query.TraceID) && matchesOptional(record.SpanID, query.SpanID) &&
		matchesOptional(record.RequestID, query.RequestID) && matchesOptional(record.ConversationID, query.ConversationID) &&
		matchesOptional(record.InteractionID, query.InteractionID) && matchesOptional(record.OperationID, query.OperationID) &&
		matchesOptional(record.BusinessDomain, query.BusinessDomain) && matchesOptional(record.ActorID, query.ActorID) &&
		matchesOptional(record.ApplicationID, query.ApplicationID) && matchesSet(record.Category, query.Categories) &&
		matchesSet(record.ServiceName, query.Services) && matchesSet(record.Environment, query.Environments) &&
		matchesSet(record.EventName, query.EventNames) && (query.SeverityMinimum <= 0 || record.SeverityNumber >= query.SeverityMinimum) &&
		(!query.FailedOnly || record.Outcome == "failure") &&
		(query.Query == "" || strings.Contains(strings.ToLower(record.SafeSummary), strings.ToLower(query.Query)))
}

func matchesOptional(actual, expected string) bool { return expected == "" || actual == expected }

func matchesSet(actual string, expected []string) bool {
	return len(expected) == 0 || contains(expected, actual)
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func hasRole(profile evidencevo.AccessProfile, roles ...string) bool {
	for _, actual := range profile.Roles {
		if contains(roles, actual) {
			return true
		}
	}
	return false
}

func normalizedAccuracy(value string) string {
	if value == "" {
		return "exact"
	}
	return value
}
