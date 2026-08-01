package logsvc

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

var (
	ErrAccessDenied       = errors.New("observability access denied")
	ErrSourcesUnavailable = errors.New("observability sources unavailable")
	ErrNotDisclosed       = errors.New("observability log not disclosed")
	ErrCursorInvalid      = errors.New("observability cursor invalid")
	ErrCursorStale        = errors.New("observability cursor stale")
	ErrInvalidQuery       = errors.New("observability query invalid")
)

type Source interface {
	ID() string
	Search(context.Context, observabilityvo.LogQuery) (observabilityvo.SourcePage, error)
}

type detailSource interface {
	Get(context.Context, string) (observabilityvo.LogRecord, bool, error)
}

type metadataSource interface {
	Metadata() observabilityvo.SourceStatus
}

type Service struct {
	sources   []Source
	cursorKey []byte
}

func New(sources []Source) *Service {
	return NewWithCursorKey(sources, randomCursorKey())
}

func NewWithCursorKey(sources []Source, cursorKey []byte) *Service {
	if len(cursorKey) == 0 {
		cursorKey = randomCursorKey()
	}
	return &Service{
		sources: append([]Source(nil), sources...), cursorKey: append([]byte(nil), cursorKey...),
	}
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
	effectiveCategories := authorizedCategories(capabilities, query)
	if len(effectiveCategories) == 0 {
		return observabilityvo.ListResult{}, ErrAccessDenied
	}
	visibleSources := service.visibleSources(effectiveCategories)
	if len(visibleSources) == 0 {
		return observabilityvo.ListResult{}, ErrSourcesUnavailable
	}
	visibleSourceIDs := sourceIDs(visibleSources)
	positions := make(map[string]observabilityvo.SourcePosition)
	queryWatermark := time.Now().UTC()
	if query.Cursor != "" {
		payload, ok := decodeCursor(service.cursorKey, query.Cursor)
		if !ok {
			return observabilityvo.ListResult{}, ErrCursorInvalid
		}
		if !cursorMatches(payload, profile, query, visibleSourceIDs, time.Now()) {
			return observabilityvo.ListResult{}, ErrCursorStale
		}
		for sourceID, position := range payload.Positions {
			positions[sourceID] = position
		}
		queryWatermark = payload.QueryWatermark
	}

	result := observabilityvo.ListResult{Records: []observabilityvo.LogRecord{}, SourceStatus: []observabilityvo.SourceStatus{}}
	sourceQuery := query
	sourceQuery.Cursor = ""
	sourceQuery.Limit = 200
	if err := applyLogTimeWindow(&sourceQuery, queryWatermark); err != nil {
		return observabilityvo.ListResult{}, ErrInvalidQuery
	}
	sourceQuery.AuthorizedTenantID = profile.TenantID
	sourceQuery.AuthorizedBusinessDomain = profile.BusinessDomain
	sourceQuery.AuthorizedSubjectID = profile.EffectiveSubjectID
	sourceQuery.AuthorizedApplicationID = profile.ApplicationPrincipalID
	sourceQuery.AuthorizedCategories = append([]string(nil), effectiveCategories...)
	sourceQuery.RequireRecordScope = !capabilities.GlobalLogSearch ||
		(hasRole(profile, "network_builder") && !hasRole(profile, "admin", "super_admin"))
	sourceQuery.AuthorizedKnowledgeNetworkIDs = append(
		[]string(nil), profile.ManagedKnowledgeNetworkIDs...,
	)
	succeeded := 0
	failed := 0
	sourcePageSizes := make(map[string]int)
	recordSources := make(map[string]string)
	sourceHasMore := false
	for _, source := range visibleSources {
		metadata := sourceStatus(source)
		if metadata.Status == "not_integrated" {
			failed++
			result.SourceStatus = append(result.SourceStatus, metadata)
			continue
		}
		position, hasPosition := positions[source.ID()]
		if hasPosition {
			sourceQuery.PageBefore = &position
		} else {
			sourceQuery.PageBefore = nil
		}
		page, err := source.Search(ctx, sourceQuery)
		if err != nil {
			failed++
			status := sourceStatus(source)
			status.Status, status.Reason, status.CountAccuracy = "unavailable", "source_query_failed", "unavailable"
			result.SourceStatus = append(result.SourceStatus, status)
			continue
		}
		succeeded++
		status := sourceStatus(source)
		status.Status = "healthy"
		status.CountAccuracy = normalizedAccuracy(page.CountAccuracy)
		result.SourceStatus = append(result.SourceStatus, status)
		sourcePageSizes[source.ID()] = len(page.Records)
		if page.NextCursor != "" || page.Count > int64(len(page.Records)) {
			sourceHasMore = true
		}
		for _, record := range page.Records {
			if contains(effectiveCategories, record.Category) && afterSourcePosition(record, sourceQuery.PageBefore) &&
				matchesQuery(record, sourceQuery) && canReadLog(profile, capabilities, record, query.IsAssociatedDrilldown()) {
				result.Records = append(result.Records, record)
				recordSources[recordKey(record)] = source.ID()
			}
		}
	}
	if succeeded == 0 {
		return observabilityvo.ListResult{}, ErrSourcesUnavailable
	}

	sort.SliceStable(result.Records, func(i, j int) bool {
		if result.Records[i].EventTimestamp.Equal(result.Records[j].EventTimestamp) {
			leftSource := recordSources[recordKey(result.Records[i])]
			rightSource := recordSources[recordKey(result.Records[j])]
			if leftSource == rightSource {
				return result.Records[i].LogID > result.Records[j].LogID
			}
			return leftSource < rightSource
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
	hasMore := sourceHasMore || len(result.Records) > limit
	if len(result.Records) > limit {
		result.Records = result.Records[:limit]
	}
	for _, size := range sourcePageSizes {
		if size >= sourceQuery.Limit {
			hasMore = true
		}
	}
	if hasMore && len(result.Records) > 0 {
		for _, record := range result.Records {
			positions[recordSources[recordKey(record)]] = observabilityvo.SourcePosition{
				EventTimestamp: record.EventTimestamp, LogID: record.LogID,
			}
		}
		result.NextCursor = encodeCursor(service.cursorKey, cursorPayload{
			Version: cursorVersion, SortVersion: cursorSortVersion, FilterHash: logFilterHash(query),
			TenantID: profile.TenantID, EffectiveSubject: profile.EffectiveSubjectID,
			ApplicationID: profile.ApplicationPrincipalID, ScopeFingerprint: profile.Fingerprint,
			VisibleSources: visibleSourceIDs, Positions: positions, QueryWatermark: queryWatermark,
			ExpiresAt: time.Now().Add(cursorTTL),
		})
	}
	result.Partial = failed > 0
	result.Count = int64(len(result.Records))
	result.CountExact = !result.Partial && !hasMore
	return result, nil
}

func (service *Service) Get(
	ctx context.Context,
	profile evidencevo.AccessProfile,
	logID string,
) (observabilityvo.LogRecord, error) {
	capabilities := observabilityvo.CapabilitiesFor(profile)
	detailCategories := append([]string(nil), capabilities.AllowedLogCategories...)
	if !capabilities.GlobalLogSearch {
		detailCategories = []string{observabilityvo.CategoryRuntimeBusiness, observabilityvo.CategoryRuntimeModel}
	}
	ctx = observabilityvo.WithSourceAccessScope(ctx, observabilityvo.SourceAccessScope{
		TenantID: profile.TenantID, BusinessDomain: profile.BusinessDomain,
	})
	for _, source := range service.visibleSources(detailCategories) {
		detail, ok := source.(detailSource)
		if !ok {
			continue
		}
		record, found, err := detail.Get(ctx, logID)
		if err != nil || !found {
			continue
		}
		associated := record.ConversationID != "" || record.InteractionID != "" || record.OperationID != "" ||
			record.RequestID != "" || record.TraceID != "" || record.SpanID != ""
		if !canReadLog(profile, capabilities, record, associated) {
			return observabilityvo.LogRecord{}, ErrNotDisclosed
		}
		return record, nil
	}
	return observabilityvo.LogRecord{}, ErrNotDisclosed
}

func (service *Service) Facets(
	ctx context.Context,
	profile evidencevo.AccessProfile,
	query observabilityvo.LogQuery,
	facet string,
) (observabilityvo.FacetResult, error) {
	query.Limit = 200
	list, err := service.List(ctx, profile, query)
	if err != nil {
		return observabilityvo.FacetResult{}, err
	}
	counts := make(map[string]int64)
	for _, record := range list.Records {
		value := facetValue(record, facet)
		if value != "" {
			counts[value]++
		}
	}
	values := make([]observabilityvo.FacetValue, 0, len(counts))
	for value, count := range counts {
		values = append(values, observabilityvo.FacetValue{Value: value, Count: count})
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].Count == values[j].Count {
			return values[i].Value < values[j].Value
		}
		return values[i].Count > values[j].Count
	})
	return observabilityvo.FacetResult{
		Values: values, Partial: list.Partial || !list.CountExact, SourceStatus: list.SourceStatus,
	}, nil
}

func (service *Service) Sources(ctx context.Context, profile evidencevo.AccessProfile) ([]observabilityvo.SourceStatus, error) {
	capabilities := observabilityvo.CapabilitiesFor(profile)
	if !capabilities.GlobalLogSearch {
		return nil, ErrAccessDenied
	}
	visibleSources := service.visibleSources(capabilities.AllowedLogCategories)
	statuses := make([]observabilityvo.SourceStatus, 0, len(visibleSources))
	now := time.Now().UTC()
	from := now.Add(-time.Hour)
	query := observabilityvo.LogQuery{
		Limit: 1, AuthorizedTenantID: profile.TenantID, AuthorizedBusinessDomain: profile.BusinessDomain,
		TimeFrom: &from, TimeTo: &now,
		AuthorizedSubjectID: profile.EffectiveSubjectID, AuthorizedApplicationID: profile.ApplicationPrincipalID,
		AuthorizedCategories:          append([]string(nil), capabilities.AllowedLogCategories...),
		AuthorizedKnowledgeNetworkIDs: append([]string(nil), profile.ManagedKnowledgeNetworkIDs...),
		RequireRecordScope:            hasRole(profile, "network_builder") && !hasRole(profile, "admin", "super_admin"),
	}
	for _, source := range visibleSources {
		status := sourceStatus(source)
		if status.Status == "not_integrated" {
			statuses = append(statuses, status)
			continue
		}
		page, err := source.Search(ctx, query)
		if err != nil {
			status.Status, status.Reason, status.CountAccuracy = "unavailable", "source_health_check_failed", "unavailable"
		} else {
			status.Status = "healthy"
			status.CountAccuracy = normalizedAccuracy(page.CountAccuracy)
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func (service *Service) Policies(profile evidencevo.AccessProfile) ([]observabilityvo.LogPolicy, error) {
	capabilities := observabilityvo.CapabilitiesFor(profile)
	if !capabilities.LogPolicyRead {
		return nil, ErrAccessDenied
	}
	policies := make([]observabilityvo.LogPolicy, 0, len(capabilities.AllowedLogCategories))
	for _, category := range capabilities.AllowedLogCategories {
		kind := "runtime"
		retentionDays := 7
		if strings.HasPrefix(category, "audit.") || category == observabilityvo.CategoryAccessUser {
			kind = "audit"
			retentionDays = 365
		}
		policies = append(policies, observabilityvo.LogPolicy{
			Scope: map[string]string{"tenant_id": profile.TenantID}, PolicyRevision: "r6.2-default",
			Category: category, RetentionDays: retentionDays, PolicyKind: kind,
		})
	}
	return policies, nil
}

func associatedCategoriesOnly(categories []string) bool {
	for _, category := range categories {
		if category != observabilityvo.CategoryRuntimeBusiness && category != observabilityvo.CategoryRuntimeModel {
			return false
		}
	}
	return true
}

func authorizedCategories(
	capabilities observabilityvo.AccessCapabilities,
	query observabilityvo.LogQuery,
) []string {
	allowed := append([]string(nil), capabilities.AllowedLogCategories...)
	if !capabilities.GlobalLogSearch && query.IsAssociatedDrilldown() {
		allowed = []string{observabilityvo.CategoryRuntimeBusiness, observabilityvo.CategoryRuntimeModel}
	}
	if len(query.Categories) == 0 {
		return normalizedStrings(allowed)
	}
	result := make([]string, 0, len(query.Categories))
	for _, category := range query.Categories {
		if contains(allowed, category) {
			result = append(result, category)
		}
	}
	return normalizedStrings(result)
}

func (service *Service) visibleSources(categories []string) []Source {
	result := make([]Source, 0, len(service.sources))
	for _, source := range service.sources {
		metadata, ok := source.(metadataSource)
		if ok && len(metadata.Metadata().Categories) > 0 && !intersects(metadata.Metadata().Categories, categories) {
			continue
		}
		result = append(result, source)
	}
	return result
}

func sourceIDs(sources []Source) []string {
	result := make([]string, 0, len(sources))
	for _, source := range sources {
		result = append(result, source.ID())
	}
	return normalizedStrings(result)
}

func sourceStatus(source Source) observabilityvo.SourceStatus {
	if metadata, ok := source.(metadataSource); ok {
		return metadata.Metadata()
	}
	return observabilityvo.SourceStatus{
		SourceID: source.ID(), Reliability: "best_effort", CountAccuracy: "exact",
	}
}

func afterSourcePosition(record observabilityvo.LogRecord, position *observabilityvo.SourcePosition) bool {
	if position == nil || position.EventTimestamp.IsZero() {
		return true
	}
	if record.EventTimestamp.Before(position.EventTimestamp) {
		return true
	}
	return record.EventTimestamp.Equal(position.EventTimestamp) && record.LogID < position.LogID
}

func recordKey(record observabilityvo.LogRecord) string {
	return record.SourceID + "\x00" + record.LogID
}

func applyLogTimeWindow(query *observabilityvo.LogQuery, watermark time.Time) error {
	if query.TimeFrom == nil && query.TimeTo == nil {
		to := watermark
		from := to.Add(-time.Hour)
		query.TimeFrom, query.TimeTo = &from, &to
	} else if query.TimeFrom == nil {
		from := query.TimeTo.Add(-time.Hour)
		query.TimeFrom = &from
	} else if query.TimeTo == nil {
		to := watermark
		query.TimeTo = &to
	}
	if query.TimeTo.Before(*query.TimeFrom) || query.TimeTo.Sub(*query.TimeFrom) > 7*24*time.Hour {
		return ErrInvalidQuery
	}
	return nil
}

func canReadLog(
	profile evidencevo.AccessProfile,
	capabilities observabilityvo.AccessCapabilities,
	record observabilityvo.LogRecord,
	associated bool,
) bool {
	if !validLogProjection(record) || record.TrustLevel != "trusted" || record.IngressPrincipal == "" ||
		!observabilityvo.IsRegisteredLogEvent(record.Category, record.EventName) {
		return false
	}
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
	if !associated && !capabilities.GlobalLogSearch {
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

func validLogProjection(record observabilityvo.LogRecord) bool {
	return record.SchemaVersion != "" && record.LogID != "" && record.SourceID != "" &&
		record.SourceLogID != "" && !record.EventTimestamp.IsZero() && !record.ObservedTimestamp.IsZero() &&
		record.SeverityNumber >= 1 && record.SeverityNumber <= 24 && record.SeverityText != "" &&
		contains([]string{"success", "failure", "denied", "canceled", "unknown"}, record.Outcome) &&
		len(record.SafeSummary) <= 2048 && record.ServiceName != "" && record.Environment != ""
}

func matchesQuery(record observabilityvo.LogRecord, query observabilityvo.LogQuery) bool {
	return (query.TimeFrom == nil || !record.EventTimestamp.Before(*query.TimeFrom)) &&
		(query.TimeTo == nil || !record.EventTimestamp.After(*query.TimeTo)) &&
		matchesOptional(record.TraceID, query.TraceID) && matchesOptional(record.SpanID, query.SpanID) &&
		matchesOptional(record.RequestID, query.RequestID) && matchesOptional(record.ConversationID, query.ConversationID) &&
		matchesOptional(record.InteractionID, query.InteractionID) && matchesOptional(record.OperationID, query.OperationID) &&
		matchesOptional(record.BusinessDomain, query.BusinessDomain) && matchesOptional(record.ActorID, query.ActorID) &&
		matchesOptional(record.ApplicationID, query.ApplicationID) && matchesSet(record.Category, query.Categories) &&
		matchesSet(record.ServiceName, query.Services) && matchesSet(record.Environment, query.Environments) &&
		matchesSet(record.EventName, query.EventNames) && (query.SeverityMinimum <= 0 || record.SeverityNumber >= query.SeverityMinimum) &&
		(!query.FailedOnly || record.Outcome == "failure" || record.Outcome == "denied") &&
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

func facetValue(record observabilityvo.LogRecord, facet string) string {
	switch facet {
	case "log_category":
		return record.Category
	case "severity_text":
		return record.SeverityText
	case "service_name":
		return record.ServiceName
	case "deployment_environment":
		return record.Environment
	case "event_name":
		return record.EventName
	default:
		return ""
	}
}

func intersects(left, right []string) bool {
	for _, value := range left {
		if contains(right, value) {
			return true
		}
	}
	return false
}
