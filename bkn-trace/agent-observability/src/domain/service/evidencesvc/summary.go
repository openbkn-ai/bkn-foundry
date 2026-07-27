package evidencesvc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iartifactstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ibusinessresolver"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
)

const (
	DefaultSummaryQueryLimit = 50
	MaxSummaryQueryLimit     = 200
	MaxSummaryScanEntries    = 2000
)

var ErrSummaryCursorInvalid = errors.New("BKN_TRACE_SUMMARY_CURSOR_INVALID")

type summaryCursor struct {
	StartedAt string `json:"started_at"`
	ID        string `json:"id"`
}

type summaryLoadMetadata struct {
	Truncated      bool
	PartialReasons []string
}

func (metadata *summaryLoadMetadata) addReason(reason string) {
	metadata.Truncated = true
	metadata.PartialReasons = appendUniqueSummaryReason(metadata.PartialReasons, reason)
}

func (s *Service) ListRequests(ctx context.Context, options evidencevo.SummaryQueryOptions) (evidencevo.RequestSummaryPage, error) {
	requests, _, metadata, err := s.loadExecutionSummaries(ctx, options)
	if err != nil {
		return evidencevo.RequestSummaryPage{}, err
	}
	filtered := make([]evidencevo.RequestSummary, 0, len(requests))
	for _, request := range requests {
		if matchesRequestFilters(request, options) {
			filtered = append(filtered, request)
		}
	}
	cursor, hasCursor, err := decodeSummaryCursor(options.Cursor)
	if err != nil {
		return evidencevo.RequestSummaryPage{}, err
	}
	start := 0
	if hasCursor {
		for start < len(filtered) && !afterSummaryCursor(filtered[start].StartedAt, filtered[start].RequestID, cursor) {
			start++
		}
	}
	limit := normalizeSummaryLimit(options.Limit)
	end := start + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	page := evidencevo.RequestSummaryPage{
		Entries:   append([]evidencevo.RequestSummary{}, filtered[start:end]...),
		Total:     len(filtered),
		Truncated: metadata.Truncated,
		Partial:   metadata.Truncated,
	}
	if metadata.Truncated {
		page.PartialReasons = append([]string{}, metadata.PartialReasons...)
	}
	if end < len(filtered) && len(page.Entries) > 0 {
		last := page.Entries[len(page.Entries)-1]
		next := encodeSummaryCursor(summaryCursor{StartedAt: last.StartedAt, ID: last.RequestID})
		page.NextCursor = &next
	}
	return page, nil
}

func (s *Service) GetRequestSummary(ctx context.Context, requestID string, scope evidencevo.QueryScope) (evidencevo.RequestSummary, bool, error) {
	requests, _, metadata, err := s.loadRequestExecutionSummaries(ctx, strings.TrimSpace(requestID), scope)
	if err != nil {
		return evidencevo.RequestSummary{}, false, err
	}
	requestID = strings.TrimSpace(requestID)
	for _, request := range requests {
		if request.RequestID == requestID {
			if metadata.Truncated {
				request.EvidenceCompleteness = "partial"
				for _, reason := range metadata.PartialReasons {
					request.PartialReasons = appendUniqueSummaryReason(request.PartialReasons, reason)
				}
			}
			return request, true, nil
		}
	}
	return evidencevo.RequestSummary{}, false, nil
}

func (s *Service) GetInteractionSummary(
	ctx context.Context,
	interactionID string,
	scope evidencevo.QueryScope,
) (evidencevo.InteractionSummary, bool, error) {
	interactionID = strings.TrimSpace(interactionID)
	if interactionID == "" || !trustedQueryScope(scope) {
		return evidencevo.InteractionSummary{}, false, nil
	}
	requests, traces, metadata, err := s.loadExecutionSummaries(ctx, evidencevo.SummaryQueryOptions{
		Scope: scope, InteractionID: interactionID,
	})
	if err != nil {
		return evidencevo.InteractionSummary{}, false, err
	}
	summary := evidencevo.InteractionSummary{
		InteractionID: interactionID,
		Status:        "unknown",
		Requests:      []evidencevo.RequestSummary{},
		Traces:        []evidencevo.TraceSummary{},
	}
	var started, completed time.Time
	conversationConflict := false
	allCompleted := true
	hasError := false
	hasRunning := false
	for _, request := range requests {
		if request.InteractionID != interactionID {
			continue
		}
		summary.Requests = append(summary.Requests, request)
		if summary.ConversationID == "" && !conversationConflict {
			summary.ConversationID = request.ConversationID
		} else if request.ConversationID != "" && request.ConversationID != summary.ConversationID {
			summary.ConversationID = ""
			conversationConflict = true
		}
		mergeSummaryTime(&started, request.StartedAt, true)
		mergeSummaryTime(&completed, request.CompletedAt, false)
		switch request.Status {
		case "error":
			hasError = true
			allCompleted = false
		case "running":
			hasRunning = true
			allCompleted = false
		case "completed":
		default:
			allCompleted = false
		}
	}
	for _, trace := range traces {
		if trace.InteractionID == interactionID {
			summary.Traces = append(summary.Traces, trace)
		}
	}
	if len(summary.Requests) == 0 && len(summary.Traces) == 0 {
		return evidencevo.InteractionSummary{}, false, nil
	}
	switch {
	case hasError:
		summary.Status = "error"
	case hasRunning:
		summary.Status = "running"
	case allCompleted && len(summary.Requests) > 0:
		summary.Status = "completed"
	}
	if !started.IsZero() {
		summary.StartedAt = started.Format(time.RFC3339Nano)
	}
	if !completed.IsZero() {
		summary.CompletedAt = completed.Format(time.RFC3339Nano)
	}
	if !started.IsZero() && !completed.IsZero() && !completed.Before(started) {
		summary.DurationMS = completed.Sub(started).Milliseconds()
	}
	if metadata.Truncated {
		for index := range summary.Requests {
			summary.Requests[index].EvidenceCompleteness = "partial"
			for _, reason := range metadata.PartialReasons {
				summary.Requests[index].PartialReasons = appendUniqueSummaryReason(
					summary.Requests[index].PartialReasons,
					reason,
				)
			}
		}
	}
	return summary, true, nil
}

func mergeSummaryTime(target *time.Time, value string, earliest bool) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return
	}
	if target.IsZero() || earliest && parsed.Before(*target) || !earliest && parsed.After(*target) {
		*target = parsed
	}
}

func (s *Service) ListRequestTraces(ctx context.Context, requestID string, options evidencevo.SummaryQueryOptions) (evidencevo.TraceSummaryPage, error) {
	_, traces, metadata, err := s.loadRequestExecutionSummaries(ctx, strings.TrimSpace(requestID), options.Scope)
	if err != nil {
		return evidencevo.TraceSummaryPage{}, err
	}
	filtered := make([]evidencevo.TraceSummary, 0)
	for _, trace := range traces {
		if trace.RequestID == strings.TrimSpace(requestID) && matchesTraceFilters(trace, options) {
			filtered = append(filtered, trace)
		}
	}
	page, err := paginateTraceSummaries(filtered, options)
	if metadata.Truncated {
		page.Truncated = true
		page.Partial = true
		page.PartialReasons = append([]string{}, metadata.PartialReasons...)
	}
	return page, err
}

func (s *Service) ListTraceExecutions(ctx context.Context, options evidencevo.SummaryQueryOptions) (evidencevo.TraceSummaryPage, error) {
	var traces []evidencevo.TraceSummary
	var metadata summaryLoadMetadata
	var err error
	if strings.TrimSpace(options.TraceID) != "" {
		_, traces, metadata, err = s.loadTraceExecutionSummaries(ctx, strings.TrimSpace(options.TraceID), options.Scope)
	} else {
		_, traces, metadata, err = s.loadExecutionSummaries(ctx, options)
	}
	if err != nil {
		return evidencevo.TraceSummaryPage{}, err
	}
	filtered := make([]evidencevo.TraceSummary, 0, len(traces))
	for _, trace := range traces {
		if matchesTraceFilters(trace, options) {
			filtered = append(filtered, trace)
		}
	}
	page, err := paginateTraceSummaries(filtered, options)
	if metadata.Truncated {
		page.Truncated = true
		page.Partial = true
		page.PartialReasons = append([]string{}, metadata.PartialReasons...)
	}
	return page, err
}

func (s *Service) loadTraceExecutionSummaries(ctx context.Context, traceID string, scope evidencevo.QueryScope) ([]evidencevo.RequestSummary, []evidencevo.TraceSummary, summaryLoadMetadata, error) {
	if traceID == "" || !trustedQueryScope(scope) {
		return []evidencevo.RequestSummary{}, []evidencevo.TraceSummary{}, summaryLoadMetadata{}, nil
	}
	result, err := s.store.GetEvidenceByTraceID(ctx, traceID, evidencevo.EvidenceQueryOptions{
		Limit: MaxEvidenceQueryLimit, Scope: scope,
	})
	if err != nil {
		return nil, nil, summaryLoadMetadata{}, err
	}
	metadata := summaryLoadMetadata{}
	if result.Truncated {
		metadata.addReason("evidence_query_truncated")
	}
	if len(result.Traces) == 0 {
		return []evidencevo.RequestSummary{}, []evidencevo.TraceSummary{}, metadata, nil
	}
	traces := mergeTraceBatches(result.Traces)
	artifacts := []evidencevo.EvidenceArtifact{}
	if s.artifactStore != nil {
		requests := map[string]struct{}{}
		for _, trace := range traces {
			requests[trace.RequestID] = struct{}{}
		}
		for requestID := range requests {
			requestArtifacts, listErr := s.artifactStore.ListArtifactsByRequestID(ctx, requestID, iartifactstore.QueryOptions{
				Scope: scope,
				Limit: MaxEvidenceQueryLimit,
			})
			if listErr != nil {
				return nil, nil, summaryLoadMetadata{}, listErr
			}
			if requestArtifacts.Truncated {
				metadata.addReason("artifact_query_truncated")
			}
			artifacts = append(artifacts, requestArtifacts.Entries...)
		}
	}
	traces, artifacts, resolverUnavailable := s.authorizeSummaryInputs(ctx, traces, artifacts, scope)
	if resolverUnavailable {
		metadata.addReason("business_resolver_unavailable")
	}
	requestSummaries, traceSummaries := evidencevo.BuildExecutionSummaries(traces, artifacts)
	return requestSummaries, traceSummaries, metadata, nil
}

func mergeTraceBatches(batches []evidencevo.NormalizedTrace) []evidencevo.NormalizedTrace {
	byTrace := map[string]evidencevo.NormalizedTrace{}
	for _, batch := range batches {
		current, exists := byTrace[batch.TraceID]
		if !exists {
			byTrace[batch.TraceID] = evidencevo.WithEvents(batch, append([]evidencevo.EvidenceEvent(nil), batch.Events...))
			continue
		}
		events := append(append([]evidencevo.EvidenceEvent(nil), current.Events...), batch.Events...)
		byTrace[batch.TraceID] = evidencevo.WithEvents(batch, events)
	}
	result := make([]evidencevo.NormalizedTrace, 0, len(byTrace))
	for _, trace := range byTrace {
		result = append(result, trace)
	}
	return result
}

func (s *Service) loadExecutionSummaries(ctx context.Context, options evidencevo.SummaryQueryOptions) ([]evidencevo.RequestSummary, []evidencevo.TraceSummary, summaryLoadMetadata, error) {
	if !trustedQueryScope(options.Scope) {
		return []evidencevo.RequestSummary{}, []evidencevo.TraceSummary{}, summaryLoadMetadata{}, nil
	}
	if s.projectionSource == nil {
		return nil, nil, summaryLoadMetadata{}, errors.New("execution summary projection source is not configured")
	}
	result, err := s.projectionSource.LoadExecutionProjection(ctx, iprojectionsource.Query{
		Scope: options.Scope, From: options.From, To: options.To,
		BusinessDomain: options.BusinessDomain, Status: options.Status, Limit: MaxSummaryScanEntries,
	})
	if err != nil {
		return nil, nil, summaryLoadMetadata{}, err
	}
	metadata := summaryLoadMetadata{}
	if result.Truncated {
		metadata.addReason("projection_scan_cap_reached")
	}
	traces, artifacts, resolverUnavailable := s.authorizeSummaryInputs(ctx, result.Traces, result.Artifacts, options.Scope)
	if resolverUnavailable {
		metadata.addReason("business_resolver_unavailable")
	}
	requests, traceSummaries := evidencevo.BuildExecutionSummaries(traces, artifacts)
	if resolverUnavailable {
		markRequestSummariesPartial(requests, "business_resolver_unavailable")
	}
	return requests, traceSummaries, metadata, nil
}

func (s *Service) loadRequestExecutionSummaries(ctx context.Context, requestID string, scope evidencevo.QueryScope) ([]evidencevo.RequestSummary, []evidencevo.TraceSummary, summaryLoadMetadata, error) {
	if requestID == "" || !trustedQueryScope(scope) {
		return []evidencevo.RequestSummary{}, []evidencevo.TraceSummary{}, summaryLoadMetadata{}, nil
	}
	result, err := s.store.GetEvidenceByRequestID(ctx, requestID, evidencevo.EvidenceQueryOptions{
		Limit: MaxEvidenceQueryLimit, Scope: scope,
	})
	if err != nil {
		return nil, nil, summaryLoadMetadata{}, err
	}
	metadata := summaryLoadMetadata{}
	if result.Truncated {
		metadata.addReason("evidence_query_truncated")
	}
	if len(result.Traces) == 0 {
		return []evidencevo.RequestSummary{}, []evidencevo.TraceSummary{}, metadata, nil
	}
	traces := mergeTraceBatches(result.Traces)
	artifacts := []evidencevo.EvidenceArtifact{}
	if s.artifactStore != nil {
		artifactResult, listErr := s.artifactStore.ListArtifactsByRequestID(ctx, requestID, iartifactstore.QueryOptions{
			Scope: scope,
			Limit: MaxEvidenceQueryLimit,
		})
		if listErr != nil {
			return nil, nil, summaryLoadMetadata{}, listErr
		}
		if artifactResult.Truncated {
			metadata.addReason("artifact_query_truncated")
		}
		artifacts = artifactResult.Entries
	}
	traces, artifacts, resolverUnavailable := s.authorizeSummaryInputs(ctx, traces, artifacts, scope)
	if resolverUnavailable {
		metadata.addReason("business_resolver_unavailable")
	}
	requests, traceSummaries := evidencevo.BuildExecutionSummaries(traces, artifacts)
	return requests, traceSummaries, metadata, nil
}

func (s *Service) authorizeSummaryInputs(
	ctx context.Context,
	traces []evidencevo.NormalizedTrace,
	artifacts []evidencevo.EvidenceArtifact,
	scope evidencevo.QueryScope,
) ([]evidencevo.NormalizedTrace, []evidencevo.EvidenceArtifact, bool) {
	candidates := map[string]ibusinessresolver.BusinessRef{}
	for _, trace := range traces {
		for _, event := range trace.Events {
			if !visible(event.Payload) {
				continue
			}
			collectVisibleSummaryRefCandidates(event.Payload, candidates)
		}
	}
	for _, artifact := range artifacts {
		if resolverSupportsArtifactRef(artifact.SourceRef) {
			addSummaryResolverRef(candidates, artifact.SourceRef, "", "", "")
		}
		for _, refID := range artifact.BusinessRefs {
			addSummaryResolverRef(candidates, refID, "", "", "")
		}
	}

	authorized := map[string]struct{}{}
	resolverUnavailable := false
	if len(candidates) > 0 && s.businessResolver != nil {
		refs := make([]ibusinessresolver.BusinessRef, 0, len(candidates))
		for _, ref := range candidates {
			refs = append(refs, ref)
		}
		sort.Slice(refs, func(i, j int) bool { return refs[i].RefID < refs[j].RefID })
		resolutions, err := s.businessResolver.ResolveBusinessRefs(ctx, ibusinessresolver.ResolveRequest{
			Scope: scope,
			Refs:  refs,
		})
		if err != nil {
			resolverUnavailable = true
		} else {
			for _, resolution := range resolutions {
				if visibleResolution(resolution) {
					authorized[resolution.RefID] = struct{}{}
				}
			}
		}
	}

	filtered := make([]evidencevo.NormalizedTrace, 0, len(traces))
	for _, trace := range traces {
		events := make([]evidencevo.EvidenceEvent, 0, len(trace.Events))
		for _, event := range trace.Events {
			payload := cloneSummaryPayloadWithoutUnauthorizedRefs(event.Payload, authorized, visible(event.Payload))
			event.Payload = payload
			events = append(events, event)
		}
		filtered = append(filtered, evidencevo.WithEvents(trace, events))
	}
	filteredArtifacts := make([]evidencevo.EvidenceArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		if summaryArtifactRefsAuthorized(artifact, authorized) {
			filteredArtifacts = append(filteredArtifacts, artifact)
		}
	}
	return filtered, filteredArtifacts, resolverUnavailable
}

func markRequestSummariesPartial(requests []evidencevo.RequestSummary, reason string) {
	for index := range requests {
		requests[index].EvidenceCompleteness = "partial"
		requests[index].PartialReasons = appendUniqueSummaryReason(requests[index].PartialReasons, reason)
	}
}

func collectVisibleSummaryRefCandidates(value any, candidates map[string]ibusinessresolver.BusinessRef) {
	switch item := value.(type) {
	case map[string]any:
		if refID, ok := item["ref_id"].(string); ok && strings.TrimSpace(refID) != "" {
			if visible(item) {
				addSummaryResolverRef(
					candidates,
					refID,
					stringSummaryField(item, "ref_type"),
					stringSummaryField(item, "source_system"),
					stringSummaryField(item, "version_status"),
				)
			}
			return
		}
		for _, nested := range item {
			collectVisibleSummaryRefCandidates(nested, candidates)
		}
	case []any:
		for _, nested := range item {
			collectVisibleSummaryRefCandidates(nested, candidates)
		}
	}
}

func addSummaryResolverRef(
	candidates map[string]ibusinessresolver.BusinessRef,
	refID string,
	refType string,
	sourceSystem string,
	versionStatus string,
) {
	refID = strings.TrimSpace(refID)
	if refID == "" {
		return
	}
	if _, exists := candidates[refID]; exists {
		return
	}
	if refType == "" {
		refType, _, _ = strings.Cut(refID, ":")
	}
	candidates[refID] = ibusinessresolver.BusinessRef{
		RefID:         refID,
		RefType:       refType,
		SourceSystem:  sourceSystem,
		VersionStatus: versionStatus,
	}
}

func summaryArtifactRefsAuthorized(artifact evidencevo.EvidenceArtifact, authorized map[string]struct{}) bool {
	refs := make([]string, 0, len(artifact.BusinessRefs)+1)
	if resolverSupportsArtifactRef(artifact.SourceRef) {
		refs = append(refs, strings.TrimSpace(artifact.SourceRef))
	}
	refs = append(refs, artifact.BusinessRefs...)
	for _, refID := range refs {
		if _, ok := authorized[strings.TrimSpace(refID)]; !ok {
			return false
		}
	}
	return true
}

func cloneSummaryPayloadWithoutUnauthorizedRefs(
	payload map[string]any,
	authorized map[string]struct{},
	allowRefs bool,
) map[string]any {
	cloned, _ := cloneSummaryValueWithoutUnauthorizedRefs(payload, authorized, allowRefs)
	result, _ := cloned.(map[string]any)
	if result == nil {
		return map[string]any{}
	}
	return result
}

func cloneSummaryValueWithoutUnauthorizedRefs(value any, authorized map[string]struct{}, allowRefs bool) (any, bool) {
	switch item := value.(type) {
	case map[string]any:
		if refID, ok := item["ref_id"].(string); ok && strings.TrimSpace(refID) != "" {
			if !allowRefs || !visible(item) {
				return nil, false
			}
			if _, ok := authorized[strings.TrimSpace(refID)]; !ok {
				return nil, false
			}
		}
		cloned := make(map[string]any, len(item))
		for key, nested := range item {
			value, keep := cloneSummaryValueWithoutUnauthorizedRefs(nested, authorized, allowRefs)
			if keep {
				cloned[key] = value
			}
		}
		return cloned, true
	case []any:
		cloned := make([]any, 0, len(item))
		for _, nested := range item {
			value, keep := cloneSummaryValueWithoutUnauthorizedRefs(nested, authorized, allowRefs)
			if keep {
				cloned = append(cloned, value)
			}
		}
		return cloned, true
	default:
		return value, true
	}
}

func stringSummaryField(value map[string]any, key string) string {
	text, _ := value[key].(string)
	return text
}

func appendUniqueSummaryReason(reasons []string, reason string) []string {
	for _, current := range reasons {
		if current == reason {
			return reasons
		}
	}
	return append(reasons, reason)
}

func matchesRequestFilters(summary evidencevo.RequestSummary, options evidencevo.SummaryQueryOptions) bool {
	if options.ConversationID != "" && summary.ConversationID != options.ConversationID {
		return false
	}
	if options.InteractionID != "" && summary.InteractionID != options.InteractionID {
		return false
	}
	if options.Status != "" && summary.Status != options.Status {
		return false
	}
	if options.AgentOrApp != "" && summary.AgentOrApp != options.AgentOrApp {
		return false
	}
	if options.BusinessDomain != "" && summary.BusinessDomain != options.BusinessDomain {
		return false
	}
	if options.KnowledgeNetwork != "" && !containsSummaryValue(summary.KnowledgeNetworks, options.KnowledgeNetwork) {
		return false
	}
	if options.EvidenceCompleteness != "" && summary.EvidenceCompleteness != options.EvidenceCompleteness {
		return false
	}
	if !matchesTimeRange(summary.StartedAt, options.From, options.To) {
		return false
	}
	if keyword := strings.ToLower(strings.TrimSpace(options.Keyword)); keyword != "" {
		haystack := strings.ToLower(strings.Join(append([]string{
			summary.RequestID, summary.ConversationID, summary.InteractionID,
			summary.QuestionPreview, summary.ResultPreview, summary.AgentOrApp,
			summary.BusinessDomain, summary.ErrorSummary,
		}, summary.BusinessRefs...), "\n"))
		if !strings.Contains(haystack, keyword) {
			return false
		}
	}
	return true
}

func containsSummaryValue(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func matchesTraceFilters(summary evidencevo.TraceSummary, options evidencevo.SummaryQueryOptions) bool {
	if options.ConversationID != "" && summary.ConversationID != options.ConversationID {
		return false
	}
	if options.InteractionID != "" && summary.InteractionID != options.InteractionID {
		return false
	}
	if options.Status != "" && summary.Status != options.Status {
		return false
	}
	if options.AgentOrApp != "" && summary.AgentOrApp != options.AgentOrApp {
		return false
	}
	if options.BusinessDomain != "" && summary.BusinessDomain != options.BusinessDomain {
		return false
	}
	if !matchesTimeRange(summary.StartedAt, options.From, options.To) {
		return false
	}
	if keyword := strings.ToLower(strings.TrimSpace(options.Keyword)); keyword != "" {
		haystack := strings.ToLower(strings.Join([]string{
			summary.TraceID, summary.RequestID, summary.ConversationID, summary.InteractionID,
			summary.AgentOrApp, summary.BusinessDomain,
			summary.RootOperation, summary.ErrorSummary,
		}, "\n"))
		return strings.Contains(haystack, keyword)
	}
	return true
}

func matchesTimeRange(value string, from, to time.Time) bool {
	if from.IsZero() && to.IsZero() {
		return true
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return false
	}
	return (from.IsZero() || !parsed.Before(from)) && (to.IsZero() || !parsed.After(to))
}

func paginateTraceSummaries(filtered []evidencevo.TraceSummary, options evidencevo.SummaryQueryOptions) (evidencevo.TraceSummaryPage, error) {
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].StartedAt == filtered[j].StartedAt {
			return filtered[i].TraceID < filtered[j].TraceID
		}
		return filtered[i].StartedAt > filtered[j].StartedAt
	})
	cursor, hasCursor, err := decodeSummaryCursor(options.Cursor)
	if err != nil {
		return evidencevo.TraceSummaryPage{}, err
	}
	start := 0
	if hasCursor {
		for start < len(filtered) && !afterSummaryCursor(filtered[start].StartedAt, filtered[start].TraceID, cursor) {
			start++
		}
	}
	end := start + normalizeSummaryLimit(options.Limit)
	if end > len(filtered) {
		end = len(filtered)
	}
	page := evidencevo.TraceSummaryPage{
		Entries: append([]evidencevo.TraceSummary{}, filtered[start:end]...),
		Total:   len(filtered),
	}
	if end < len(filtered) && len(page.Entries) > 0 {
		last := page.Entries[len(page.Entries)-1]
		next := encodeSummaryCursor(summaryCursor{StartedAt: last.StartedAt, ID: last.TraceID})
		page.NextCursor = &next
	}
	return page, nil
}

func afterSummaryCursor(startedAt, id string, cursor summaryCursor) bool {
	return startedAt < cursor.StartedAt || startedAt == cursor.StartedAt && id > cursor.ID
}

func encodeSummaryCursor(cursor summaryCursor) string {
	body, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeSummaryCursor(value string) (summaryCursor, bool, error) {
	if strings.TrimSpace(value) == "" {
		return summaryCursor{}, false, nil
	}
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return summaryCursor{}, false, ErrSummaryCursorInvalid
	}
	var cursor summaryCursor
	if err := json.Unmarshal(body, &cursor); err != nil || cursor.ID == "" {
		return summaryCursor{}, false, ErrSummaryCursorInvalid
	}
	return cursor, true, nil
}

func normalizeSummaryLimit(limit int) int {
	if limit <= 0 {
		return DefaultSummaryQueryLimit
	}
	if limit > MaxSummaryQueryLimit {
		return MaxSummaryQueryLimit
	}
	return limit
}
