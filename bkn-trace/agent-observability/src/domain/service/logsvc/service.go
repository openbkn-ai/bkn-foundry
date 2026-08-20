package logsvc

import (
	"container/heap"
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isourcecoveragestore"
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
	sources              []Source
	cursorKey            []byte
	sourceTimeout        time.Duration
	maxConcurrentSources int
	operationAuditOnly   bool
	coverageStore        isourcecoveragestore.Store
	coverageDeploymentID string
}

type Options struct {
	CursorKey            []byte
	SourceTimeout        time.Duration
	MaxConcurrentSources int
	OperationAuditOnly   bool
	CoverageStore        isourcecoveragestore.Store
	CoverageDeploymentID string
}

const (
	defaultSourceTimeout        = 3 * time.Second
	defaultMaxConcurrentSources = 4
)

func New(sources []Source) *Service {
	return NewWithCursorKey(sources, randomCursorKey())
}

func NewWithCursorKey(sources []Source, cursorKey []byte) *Service {
	return NewWithOptions(sources, Options{CursorKey: cursorKey})
}

func NewWithOptions(sources []Source, options Options) *Service {
	if len(options.CursorKey) == 0 {
		options.CursorKey = randomCursorKey()
	}
	if options.SourceTimeout <= 0 {
		options.SourceTimeout = defaultSourceTimeout
	}
	if options.MaxConcurrentSources <= 0 {
		options.MaxConcurrentSources = defaultMaxConcurrentSources
	}
	return &Service{
		sources: append([]Source(nil), sources...), cursorKey: append([]byte(nil), options.CursorKey...),
		sourceTimeout: options.SourceTimeout, maxConcurrentSources: options.MaxConcurrentSources,
		operationAuditOnly: options.OperationAuditOnly,
		coverageStore:      options.CoverageStore, coverageDeploymentID: options.CoverageDeploymentID,
	}
}

type sourceSearchResult struct {
	source Source
	page   observabilityvo.SourcePage
	status observabilityvo.SourceStatus
	err    error
}

type logCandidate struct {
	record          observabilityvo.LogRecord
	adapterSourceID string
}

type candidateCursor struct {
	batchIndex  int
	recordIndex int
}

type candidateHeap struct {
	items   []candidateCursor
	batches [][]logCandidate
}

func (items candidateHeap) Len() int { return len(items.items) }
func (items candidateHeap) Less(left, right int) bool {
	leftCursor, rightCursor := items.items[left], items.items[right]
	return candidateBefore(
		items.batches[leftCursor.batchIndex][leftCursor.recordIndex],
		items.batches[rightCursor.batchIndex][rightCursor.recordIndex],
	)
}
func (items candidateHeap) Swap(left, right int) {
	items.items[left], items.items[right] = items.items[right], items.items[left]
}
func (items *candidateHeap) Push(value any) {
	items.items = append(items.items, value.(candidateCursor))
}
func (items *candidateHeap) Pop() any {
	last := len(items.items) - 1
	value := items.items[last]
	items.items = items.items[:last]
	return value
}

func (service *Service) List(
	ctx context.Context,
	profile evidencevo.AccessProfile,
	query observabilityvo.LogQuery,
) (observabilityvo.ListResult, error) {
	targetPage := normalizeLogPage(query.Page)
	if query.Cursor != "" || targetPage == 1 {
		return service.listPage(ctx, profile, query)
	}

	query.Page = 1
	var result observabilityvo.ListResult
	for currentPage := 1; currentPage <= targetPage; currentPage++ {
		pageResult, err := service.listPage(ctx, profile, query)
		if err != nil {
			return observabilityvo.ListResult{}, err
		}
		result = pageResult
		if currentPage == targetPage || pageResult.NextCursor == "" {
			break
		}
		query.Cursor = pageResult.NextCursor
	}
	result.Page = targetPage
	return result, nil
}

func (service *Service) listPage(
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
	if service.operationAuditOnly {
		effectiveCategories = authorizedOperationAuditCategories(profile, capabilities, query)
		capabilities.AllowedLogCategories = normalizedStrings(append(
			capabilities.AllowedLogCategories, effectiveCategories...,
		))
	}
	if len(effectiveCategories) == 0 {
		return observabilityvo.ListResult{}, ErrAccessDenied
	}
	visibleSources := service.visibleSources(effectiveCategories)
	if service.operationAuditOnly {
		visibleSources = operationAuditSources(visibleSources)
	}
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

	result := observabilityvo.ListResult{
		Records: []observabilityvo.LogRecord{}, SourceStatus: []observabilityvo.SourceStatus{},
		Page: normalizeLogPage(query.Page), PageSize: normalizeLogLimit(query.Limit),
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	sourceQuery := query
	sourceQuery.ActorQuery = ""
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
	sourceQuery.ObservedBefore = &queryWatermark
	filterQuery := sourceQuery
	filterQuery.ActorQuery = query.ActorQuery
	succeeded := 0
	failed := 0
	coveragePartial := false
	sourcePageSizes := make(map[string]int)
	sourceCandidates := make(map[string]int)
	sourceLastRawPosition := make(map[string]observabilityvo.SourcePosition)
	candidateBatches := make([][]logCandidate, 0, len(visibleSources))
	sourceHasMore := false
	var totalCount int64
	countExact := true
	for _, sourceResult := range service.searchSources(ctx, visibleSources, sourceQuery, positions) {
		source := sourceResult.source
		if sourceResult.status.Status == "not_integrated" {
			// Coverage is shown in source status, but a source that was never
			// queried cannot make results from reachable sources incomplete.
			result.SourceStatus = append(result.SourceStatus, sourceResult.status)
			continue
		}
		position, hasPosition := positions[source.ID()]
		if sourceResult.err != nil {
			failed++
			result.SourceStatus = append(result.SourceStatus, sourceResult.status)
			continue
		}
		succeeded++
		page := sourceResult.page
		result.SourceStatus = append(result.SourceStatus, sourceResult.status)
		coveragePartial = coveragePartial || sourceResult.status.Status == "degraded"
		sourcePageSizes[source.ID()] = len(page.Records)
		countExact = countExact && normalizedAccuracy(page.CountAccuracy) == "exact"
		if page.LastPosition != nil {
			sourceLastRawPosition[source.ID()] = *page.LastPosition
		} else if len(page.Records) > 0 {
			sourceLastRawPosition[source.ID()] = positionForRecord(page.Records[len(page.Records)-1])
		}
		if page.NextCursor != "" || page.Count > int64(len(page.Records)) {
			sourceHasMore = true
		}
		candidates := make([]logCandidate, 0, len(page.Records))
		rejectedProjections := 0
		for _, record := range page.Records {
			var pageBefore *observabilityvo.SourcePosition
			if hasPosition {
				pageBefore = &position
			}
			validProjection := !service.operationAuditOnly ||
				(isOperationAuditRecord(record) && validOperationAuditProjection(record))
			if !validProjection {
				rejectedProjections++
			}
			if validProjection &&
				contains(effectiveCategories, record.Category) && afterSourcePosition(record, pageBefore) &&
				matchesQuery(record, filterQuery) && canReadLog(profile, capabilities, record, query.IsAssociatedDrilldown()) {
				candidates = append(candidates, logCandidate{record: record, adapterSourceID: source.ID()})
			}
		}
		if rejectedProjections > 0 || filterQuery.ActorQuery != "" || normalizedAccuracy(page.CountAccuracy) != "exact" {
			// Source totals describe its raw result set. Once the public contract
			// rejects records or an adapter has already filtered its source result,
			// retaining that raw total produces an impossible UI (for example “5
			// facts” with an empty table). Report the visible lower bound and mark
			// the count inexact; the source itself remains reachable.
			totalCount += int64(len(candidates))
			countExact = false
		} else {
			totalCount += page.Count
		}
		sort.SliceStable(candidates, func(left, right int) bool {
			return candidateBefore(candidates[left], candidates[right])
		})
		sourceCandidates[source.ID()] = len(candidates)
		candidateBatches = append(candidateBatches, candidates)
	}
	if succeeded == 0 {
		return observabilityvo.ListResult{}, ErrSourcesUnavailable
	}

	merged := mergeCandidates(candidateBatches, limit+1)
	hasMore := sourceHasMore || len(merged) > limit
	if len(merged) > limit {
		merged = merged[:limit]
	}
	for _, candidate := range merged {
		result.Records = append(result.Records, candidate.record)
	}
	for _, size := range sourcePageSizes {
		if size >= sourceQuery.Limit {
			hasMore = true
		}
	}
	if hasMore {
		includedBySource := make(map[string]int)
		for _, candidate := range merged {
			positions[candidate.adapterSourceID] = positionForRecord(candidate.record)
			includedBySource[candidate.adapterSourceID]++
		}
		for sourceID, rawPosition := range sourceLastRawPosition {
			if includedBySource[sourceID] == sourceCandidates[sourceID] {
				positions[sourceID] = rawPosition
			}
		}
		if len(positions) > 0 {
			result.NextCursor = encodeCursor(service.cursorKey, cursorPayload{
				Version: cursorVersion, SortVersion: cursorSortVersion, FilterHash: logFilterHash(query),
				TenantID: profile.TenantID, EffectiveSubject: profile.EffectiveSubjectID,
				ApplicationID: profile.ApplicationPrincipalID, ScopeFingerprint: profile.Fingerprint,
				VisibleSources: visibleSourceIDs, Positions: positions, QueryWatermark: queryWatermark,
				ExpiresAt: time.Now().Add(cursorTTL),
			})
		}
	}
	result.Partial = failed > 0 || coveragePartial
	result.Count = totalCount
	result.CountExact = !result.Partial && countExact
	return result, nil
}

func normalizeLogPage(page int) int {
	if page <= 0 {
		return 1
	}
	return page
}

func normalizeLogLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func mergeCandidates(batches [][]logCandidate, limit int) []logCandidate {
	if limit <= 0 {
		return nil
	}
	queue := &candidateHeap{batches: batches, items: make([]candidateCursor, 0, len(batches))}
	totalCandidates := 0
	for batchIndex, batch := range batches {
		if len(batch) > 0 {
			queue.items = append(queue.items, candidateCursor{batchIndex: batchIndex})
			totalCandidates += len(batch)
		}
	}
	heap.Init(queue)
	result := make([]logCandidate, 0, min(limit, totalCandidates))
	for queue.Len() > 0 && len(result) < limit {
		cursor := heap.Pop(queue).(candidateCursor)
		result = append(result, batches[cursor.batchIndex][cursor.recordIndex])
		cursor.recordIndex++
		if cursor.recordIndex < len(batches[cursor.batchIndex]) {
			heap.Push(queue, cursor)
		}
	}
	return result
}

func candidateBefore(left, right logCandidate) bool {
	if !left.record.EventTimestamp.Equal(right.record.EventTimestamp) {
		return left.record.EventTimestamp.After(right.record.EventTimestamp)
	}
	if left.record.SourceID != right.record.SourceID {
		return left.record.SourceID < right.record.SourceID
	}
	if positionID(left.record) != positionID(right.record) {
		return positionID(left.record) < positionID(right.record)
	}
	return left.adapterSourceID < right.adapterSourceID
}

func (service *Service) searchSources(
	ctx context.Context,
	sources []Source,
	query observabilityvo.LogQuery,
	positions map[string]observabilityvo.SourcePosition,
) []sourceSearchResult {
	results := make([]sourceSearchResult, len(sources))
	semaphore := make(chan struct{}, service.maxConcurrentSources)
	var waitGroup sync.WaitGroup
	for index, source := range sources {
		results[index] = sourceSearchResult{source: source, status: service.sourceStatus(ctx, source)}
		if results[index].status.Status == "not_integrated" {
			continue
		}
		waitGroup.Add(1)
		go func(index int, source Source) {
			defer waitGroup.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results[index].err = ctx.Err()
				results[index].status.Status = "unavailable"
				results[index].status.Reason = "source_query_failed"
				results[index].status.CountAccuracy = "unavailable"
				return
			}
			sourceQuery := query
			if position, ok := positions[source.ID()]; ok {
				sourceQuery.PageBefore = &position
			} else {
				sourceQuery.PageBefore = nil
			}
			startedAt := time.Now()
			sourceContext, cancel := context.WithTimeout(ctx, service.sourceTimeout)
			defer cancel()
			page, err := source.Search(sourceContext, sourceQuery)
			latency := time.Since(startedAt).Milliseconds()
			results[index].status.LatencyMS = &latency
			results[index].page = page
			results[index].err = err
			if err != nil {
				results[index].status.Status = "unavailable"
				results[index].status.Reason = "source_query_failed"
				if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
					results[index].status.Reason = "source_timeout"
				}
				results[index].status.CountAccuracy = "unavailable"
				return
			}
			if results[index].status.Status != observabilityvo.SourceCoverageDegraded || results[index].status.Reason == observabilityvo.SourceReasonPartialManagementAuditCoverage {
				results[index].status.Status = "healthy"
				results[index].status.CountAccuracy = normalizedAccuracy(page.CountAccuracy)
			}
		}(index, source)
	}
	waitGroup.Wait()
	return results
}

func (service *Service) Get(
	ctx context.Context,
	profile evidencevo.AccessProfile,
	logID string,
) (observabilityvo.LogRecord, error) {
	capabilities := observabilityvo.CapabilitiesFor(profile)
	detailCategories := append([]string(nil), capabilities.AllowedLogCategories...)
	if service.operationAuditOnly {
		detailCategories = authorizedOperationAuditCategories(profile, capabilities, observabilityvo.LogQuery{})
		if !capabilities.GlobalLogSearch {
			detailCategories = []string{observabilityvo.CategoryAuditAdmin, observabilityvo.CategoryRuntimeBusiness}
		}
		capabilities.AllowedLogCategories = normalizedStrings(append(
			capabilities.AllowedLogCategories, detailCategories...,
		))
	} else if !capabilities.GlobalLogSearch {
		detailCategories = []string{observabilityvo.CategoryRuntimeBusiness, observabilityvo.CategoryRuntimeModel}
	}
	ctx = observabilityvo.WithSourceAccessScope(ctx, observabilityvo.SourceAccessScope{
		TenantID: profile.TenantID, BusinessDomain: profile.BusinessDomain,
	})
	visibleSources := service.visibleSources(detailCategories)
	if service.operationAuditOnly {
		visibleSources = operationAuditSources(visibleSources)
	}
	for _, source := range visibleSources {
		detail, ok := source.(detailSource)
		if !ok {
			continue
		}
		record, found, err := detail.Get(ctx, logID)
		if err != nil || !found {
			continue
		}
		if service.operationAuditOnly && !isOperationAuditRecord(record) {
			continue
		}
		if service.operationAuditOnly && !validOperationAuditProjection(record) {
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
		Values: values, Partial: list.Partial || !list.CountExact, SampledRecords: len(list.Records), SourceStatus: list.SourceStatus,
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
		TimeFrom: &from, TimeTo: &now, ObservedBefore: &now,
		AuthorizedSubjectID: profile.EffectiveSubjectID, AuthorizedApplicationID: profile.ApplicationPrincipalID,
		AuthorizedCategories:          append([]string(nil), capabilities.AllowedLogCategories...),
		AuthorizedKnowledgeNetworkIDs: append([]string(nil), profile.ManagedKnowledgeNetworkIDs...),
		RequireRecordScope:            hasRole(profile, "network_builder") && !hasRole(profile, "admin", "super_admin"),
	}
	for _, result := range service.searchSources(ctx, visibleSources, query, nil) {
		status := result.status
		if result.err != nil && status.Reason != "source_timeout" {
			status.Reason = "source_health_check_failed"
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

func authorizedOperationAuditCategories(
	profile evidencevo.AccessProfile,
	capabilities observabilityvo.AccessCapabilities,
	query observabilityvo.LogQuery,
) []string {
	allowed := make([]string, 0, 3)
	if hasRole(profile, "network_builder", "admin", "audit", "security", "super_admin") {
		allowed = append(allowed, observabilityvo.CategoryAuditAdmin, observabilityvo.CategoryRuntimeBusiness)
	}
	if hasRole(profile, "audit", "security", "super_admin") {
		allowed = append(allowed, observabilityvo.CategoryAccessUser, observabilityvo.CategoryAuditSecurity)
	}
	if !capabilities.GlobalLogSearch && query.IsAssociatedDrilldown() {
		allowed = []string{observabilityvo.CategoryAuditAdmin, observabilityvo.CategoryRuntimeBusiness}
	}
	allowed = normalizedStrings(allowed)
	if len(query.Categories) == 0 {
		return allowed
	}
	result := make([]string, 0, len(query.Categories))
	for _, category := range query.Categories {
		if contains(allowed, category) {
			result = append(result, category)
		}
	}
	return normalizedStrings(result)
}

func isOperationAuditCategory(category string) bool {
	return category == observabilityvo.CategoryAccessUser ||
		category == observabilityvo.CategoryAuditAdmin ||
		category == observabilityvo.CategoryAuditSecurity
}

func isOperationAuditRecord(record observabilityvo.LogRecord) bool {
	return isOperationAuditCategory(record.Category) ||
		(record.Category == observabilityvo.CategoryRuntimeBusiness && (record.EventName == "conversation.created" || record.EventName == "operation.executed"))
}

func validOperationAuditProjection(record observabilityvo.LogRecord) bool {
	return observabilityvo.IsBusinessModule(record.BusinessModule) && record.Action != "" &&
		record.TargetType != "" && record.TargetID != "" && record.TargetNameSnapshot != "" &&
		record.ActorID != "" && record.ActorNameSnapshot != "" && record.AuthMethod != "" &&
		record.SourceChannel != ""
}

func operationAuditSources(sources []Source) []Source {
	result := make([]Source, 0, len(sources))
	for _, source := range sources {
		metadata, ok := source.(metadataSource)
		if !ok {
			result = append(result, source)
			continue
		}
		categories := metadata.Metadata().Categories
		if intersects(categories, []string{
			observabilityvo.CategoryAccessUser,
			observabilityvo.CategoryAuditAdmin,
			observabilityvo.CategoryAuditSecurity,
		}) || source.ID() == "bkn-trace-core" {
			result = append(result, source)
		}
	}
	return result
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

func (service *Service) sourceStatus(ctx context.Context, source Source) observabilityvo.SourceStatus {
	var status observabilityvo.SourceStatus
	if metadata, ok := source.(metadataSource); ok {
		status = metadata.Metadata()
	} else {
		status = observabilityvo.SourceStatus{
			SourceID: source.ID(), Reliability: "best_effort", CountAccuracy: "exact",
		}
	}
	if service.coverageStore == nil || service.coverageDeploymentID == "" {
		return status
	}
	coverage, found, err := service.coverageStore.Get(ctx, source.ID(), service.coverageDeploymentID)
	if err != nil {
		status.Status = observabilityvo.SourceCoverageDegraded
		status.Reason = "source_status_probe_failed"
		status.CountAccuracy = "partial"
		return status
	}
	if found {
		return coverage.Merge(status)
	}
	return status
}

func afterSourcePosition(record observabilityvo.LogRecord, position *observabilityvo.SourcePosition) bool {
	if position == nil || position.EventTimestamp.IsZero() {
		return true
	}
	// A native search_after tuple is the source's authoritative strict boundary.
	// Rechecking it with _source timestamps can use a different precision and
	// incorrectly discard records from the same indexed millisecond.
	if len(position.SearchAfter) > 0 && record.CursorPosition != nil && len(record.CursorPosition.SearchAfter) > 0 {
		return true
	}
	if record.EventTimestamp.Before(position.EventTimestamp) {
		return true
	}
	return record.EventTimestamp.Equal(position.EventTimestamp) && positionID(record) > position.LogID
}

func positionForRecord(record observabilityvo.LogRecord) observabilityvo.SourcePosition {
	if record.CursorPosition != nil {
		position := *record.CursorPosition
		position.SearchAfter = append([]any(nil), record.CursorPosition.SearchAfter...)
		return position
	}
	return observabilityvo.SourcePosition{
		EventTimestamp: record.EventTimestamp, SourceID: record.SourceID, LogID: positionID(record),
	}
}

func positionID(record observabilityvo.LogRecord) string {
	if record.SourceLogID != "" {
		return record.SourceLogID
	}
	return record.LogID
}

func applyLogTimeWindow(query *observabilityvo.LogQuery, watermark time.Time) error {
	defaultWindow := 30 * 24 * time.Hour
	if query.IsAssociatedDrilldown() {
		defaultWindow = 7 * 24 * time.Hour
	}
	if query.TimeFrom == nil && query.TimeTo == nil {
		to := watermark
		from := to.Add(-defaultWindow)
		query.TimeFrom, query.TimeTo = &from, &to
	} else if query.TimeFrom == nil {
		from := query.TimeTo.Add(-defaultWindow)
		query.TimeFrom = &from
	} else if query.TimeTo == nil {
		to := watermark
		query.TimeTo = &to
	}
	if query.TimeTo.Before(*query.TimeFrom) || query.TimeTo.Sub(*query.TimeFrom) > 30*24*time.Hour {
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
	if associated && !capabilities.GlobalLogSearch {
		if isOperationAuditCategory(record.Category) {
			return record.ActorID != "" && record.ActorID == profile.EffectiveSubjectID
		}
		if record.Category != observabilityvo.CategoryRuntimeBusiness && record.Category != observabilityvo.CategoryRuntimeModel {
			return false
		}
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
	case observabilityvo.CategoryAccessUser, observabilityvo.CategoryAuditAdmin, observabilityvo.CategoryAuditSecurity:
		return hasRole(profile, "admin", "super_admin") ||
			(record.ActorID != "" && record.ActorID == profile.EffectiveSubjectID) ||
			evidencevo.CanReadRecord(profile, recordScope, evidencevo.AccessViewBusiness)
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
		(query.TimeTo == nil || record.EventTimestamp.Before(*query.TimeTo)) &&
		(query.ObservedBefore == nil || !record.ObservedTimestamp.After(*query.ObservedBefore)) &&
		matchesOptional(record.TraceID, query.TraceID) && matchesOptional(record.SpanID, query.SpanID) &&
		matchesOptional(record.RequestID, query.RequestID) && matchesOptional(record.ConversationID, query.ConversationID) &&
		matchesOptional(record.InteractionID, query.InteractionID) && matchesOptional(record.OperationID, query.OperationID) &&
		matchesOptional(record.BusinessDomain, query.BusinessDomain) && matchesOptional(record.ActorID, query.ActorID) && matchesActor(record, query.ActorQuery) &&
		matchesOptional(record.BusinessModule, query.BusinessModule) && matchesOptional(record.Action, query.Action) &&
		matchesOptional(record.TargetType, query.TargetType) && matchesOptional(record.TargetID, query.TargetID) &&
		matchesSet(record.Outcome, query.Outcomes) &&
		matchesOptional(record.ApplicationID, query.ApplicationID) && matchesSet(record.Category, query.Categories) &&
		matchesSet(record.ServiceName, query.Services) && matchesSet(record.Environment, query.Environments) &&
		matchesSet(record.EventName, query.EventNames) && (query.SeverityMinimum <= 0 || record.SeverityNumber >= query.SeverityMinimum) &&
		(!query.FailedOnly || record.Outcome == "failure" || record.Outcome == "denied") &&
		(query.Query == "" || strings.Contains(strings.ToLower(record.SafeSummary), strings.ToLower(query.Query)))
}

func matchesOptional(actual, expected string) bool { return expected == "" || actual == expected }

func matchesActor(record observabilityvo.LogRecord, expected string) bool {
	expected = strings.TrimSpace(expected)
	return expected == "" || record.ActorID == expected || strings.Contains(
		strings.ToLower(record.ActorNameSnapshot), strings.ToLower(expected),
	)
}

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
