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
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iartifactstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ibusinessresolver"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
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
	start := summaryOffset(options, len(filtered))
	if hasCursor {
		start = 0
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
		Page:      normalizeSummaryPage(options.Page),
		PageSize:  limit,
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
	s.enrichRequestBusinessSummaries(ctx, page.Entries, options.Scope)
	return page, nil
}

func (s *Service) ListConversations(ctx context.Context, options evidencevo.SummaryQueryOptions) (evidencevo.ConversationSummaryPage, error) {
	loadOptions := options
	loadOptions.Status = ""
	requests, _, metadata, err := s.loadExecutionSummaries(ctx, loadOptions)
	if err != nil {
		return evidencevo.ConversationSummaryPage{}, err
	}
	grouped := map[string][]evidencevo.RequestSummary{}
	childOptions := options
	childOptions.Status = ""
	childOptions.EvidenceCompleteness = ""
	for _, request := range requests {
		if request.ConversationID != "" && matchesRequestFilters(request, childOptions) {
			grouped[request.ConversationID] = append(grouped[request.ConversationID], request)
		}
	}
	entries := make([]evidencevo.ConversationSummary, 0, len(grouped))
	for conversationID, group := range grouped {
		entries = append(entries, buildConversationSummary(conversationID, group))
	}
	if err := s.applyCanonicalConversationState(ctx, entries, grouped); err != nil {
		return evidencevo.ConversationSummaryPage{}, err
	}
	entries = filterConversationSummaries(entries, options)
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].StartedAt == entries[j].StartedAt {
			return entries[i].ConversationID < entries[j].ConversationID
		}
		return entries[i].StartedAt > entries[j].StartedAt
	})
	cursor, hasCursor, err := decodeSummaryCursor(options.Cursor)
	if err != nil {
		return evidencevo.ConversationSummaryPage{}, err
	}
	start := summaryOffset(options, len(entries))
	if hasCursor {
		start = summaryPageStart(len(entries), true, func(index int) bool {
			return afterSummaryCursor(entries[index].StartedAt, entries[index].ConversationID, cursor)
		})
	}
	end := summaryPageEnd(start, len(entries), options.Limit)
	page := evidencevo.ConversationSummaryPage{
		Entries: append([]evidencevo.ConversationSummary{}, entries[start:end]...),
		Total:   len(entries), Page: normalizeSummaryPage(options.Page), PageSize: normalizeSummaryLimit(options.Limit), Truncated: metadata.Truncated, Partial: metadata.Truncated,
		PartialReasons: append([]string{}, metadata.PartialReasons...),
	}
	if end < len(entries) && len(page.Entries) > 0 {
		last := page.Entries[len(page.Entries)-1]
		next := encodeSummaryCursor(summaryCursor{StartedAt: last.StartedAt, ID: last.ConversationID})
		page.NextCursor = &next
	}
	return page, nil
}

func (s *Service) ListInteractions(ctx context.Context, options evidencevo.SummaryQueryOptions) (evidencevo.InteractionSummaryPage, error) {
	loadOptions := options
	loadOptions.Status = ""
	requests, _, metadata, err := s.loadExecutionSummaries(ctx, loadOptions)
	if err != nil {
		return evidencevo.InteractionSummaryPage{}, err
	}
	grouped := map[string][]evidencevo.RequestSummary{}
	childOptions := options
	childOptions.Status = ""
	childOptions.EvidenceCompleteness = ""
	for _, request := range requests {
		if request.InteractionID != "" && matchesRequestFilters(request, childOptions) {
			grouped[request.InteractionID] = append(grouped[request.InteractionID], request)
		}
	}
	entries := make([]evidencevo.InteractionListSummary, 0, len(grouped))
	for interactionID, group := range grouped {
		entries = append(entries, buildInteractionListSummary(interactionID, group))
	}
	if err := s.applyCanonicalInteractionState(ctx, entries); err != nil {
		return evidencevo.InteractionSummaryPage{}, err
	}
	entries = filterInteractionSummaries(entries, options)
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].StartedAt == entries[j].StartedAt {
			return entries[i].InteractionID < entries[j].InteractionID
		}
		return entries[i].StartedAt > entries[j].StartedAt
	})
	cursor, hasCursor, err := decodeSummaryCursor(options.Cursor)
	if err != nil {
		return evidencevo.InteractionSummaryPage{}, err
	}
	start := summaryOffset(options, len(entries))
	if hasCursor {
		start = summaryPageStart(len(entries), true, func(index int) bool {
			return afterSummaryCursor(entries[index].StartedAt, entries[index].InteractionID, cursor)
		})
	}
	end := summaryPageEnd(start, len(entries), options.Limit)
	page := evidencevo.InteractionSummaryPage{
		Entries: append([]evidencevo.InteractionListSummary{}, entries[start:end]...),
		Total:   len(entries), Page: normalizeSummaryPage(options.Page), PageSize: normalizeSummaryLimit(options.Limit), Truncated: metadata.Truncated, Partial: metadata.Truncated,
		PartialReasons: append([]string{}, metadata.PartialReasons...),
	}
	if end < len(entries) && len(page.Entries) > 0 {
		last := page.Entries[len(page.Entries)-1]
		next := encodeSummaryCursor(summaryCursor{StartedAt: last.StartedAt, ID: last.InteractionID})
		page.NextCursor = &next
	}
	return page, nil
}

func buildConversationSummary(conversationID string, requests []evidencevo.RequestSummary) evidencevo.ConversationSummary {
	base, interactionCount := aggregateRequestGroup(requests)
	return evidencevo.ConversationSummary{
		ConversationID: conversationID,
		StartedAt:      base.StartedAt, CompletedAt: base.CompletedAt,
		Initiator: base.Initiator, AgentOrApp: base.AgentOrApp, BusinessDomain: base.BusinessDomain,
		AgentName: base.AgentName, ApplicationPrincipalID: base.ApplicationPrincipalID,
		EffectiveSubjectID: base.EffectiveSubjectID,
		KnowledgeNetworks:  base.KnowledgeNetworks, QuestionPreview: base.QuestionPreview, ResultPreview: base.ResultPreview,
		Status: base.Status, EvidenceCompleteness: base.EvidenceCompleteness, PartialReasons: base.PartialReasons,
		InteractionCount: interactionCount, RequestCount: len(requests), TraceCount: base.TraceCount,
		DurationMS: conversationDurationMS(requests), ErrorSummary: base.ErrorSummary,
	}
}

func buildInteractionListSummary(interactionID string, requests []evidencevo.RequestSummary) evidencevo.InteractionListSummary {
	base, _ := aggregateRequestGroup(requests)
	conversationID := ""
	conversationConflict := false
	for _, request := range requests {
		if request.ConversationID == "" {
			continue
		}
		if conversationID == "" && !conversationConflict {
			conversationID = request.ConversationID
		} else if conversationID != request.ConversationID {
			conversationID = ""
			conversationConflict = true
		}
	}
	return evidencevo.InteractionListSummary{
		InteractionID: interactionID, ConversationID: conversationID,
		StartedAt: base.StartedAt, CompletedAt: base.CompletedAt,
		Initiator: base.Initiator, AgentOrApp: base.AgentOrApp, BusinessDomain: base.BusinessDomain,
		AgentName: base.AgentName, ApplicationPrincipalID: base.ApplicationPrincipalID,
		EffectiveSubjectID: base.EffectiveSubjectID,
		KnowledgeNetworks:  base.KnowledgeNetworks, QuestionPreview: base.QuestionPreview, ResultPreview: base.ResultPreview,
		Status: base.Status, EvidenceCompleteness: base.EvidenceCompleteness, PartialReasons: base.PartialReasons,
		RequestCount: len(requests), TraceCount: base.TraceCount, DurationMS: base.DurationMS, ErrorSummary: base.ErrorSummary,
	}
}

func filterConversationSummaries(
	entries []evidencevo.ConversationSummary,
	options evidencevo.SummaryQueryOptions,
) []evidencevo.ConversationSummary {
	filtered := make([]evidencevo.ConversationSummary, 0, len(entries))
	for _, entry := range entries {
		if options.Status != "" && entry.Status != options.Status {
			continue
		}
		if options.EvidenceCompleteness != "" && entry.EvidenceCompleteness != options.EvidenceCompleteness {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func filterInteractionSummaries(
	entries []evidencevo.InteractionListSummary,
	options evidencevo.SummaryQueryOptions,
) []evidencevo.InteractionListSummary {
	filtered := make([]evidencevo.InteractionListSummary, 0, len(entries))
	for _, entry := range entries {
		if options.Status != "" && entry.Status != options.Status {
			continue
		}
		if options.EvidenceCompleteness != "" && entry.EvidenceCompleteness != options.EvidenceCompleteness {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func aggregateRequestGroup(requests []evidencevo.RequestSummary) (evidencevo.RequestSummary, int) {
	result := evidencevo.RequestSummary{Status: "unknown"}
	interactions := map[string]struct{}{}
	knowledgeNetworks := map[string]struct{}{}
	partialReasons := map[string]struct{}{}
	var started, completed time.Time
	questionAt := ""
	resultAt := ""
	allCompleted := len(requests) > 0
	hasError := false
	hasRunning := false
	for _, request := range requests {
		mergeSummaryTime(&started, request.StartedAt, true)
		mergeSummaryTime(&completed, request.CompletedAt, false)
		questionPreview := firstPresentSummary(request.InteractionQuestion, request.QuestionPreview)
		if questionPreview != "" && (result.QuestionPreview == "" || summaryTimeBefore(request.StartedAt, questionAt)) {
			result.QuestionPreview = questionPreview
			questionAt = request.StartedAt
		}
		resultPreview := firstPresentSummary(request.InteractionResult, request.ResultPreview)
		if resultPreview != "" && (result.ResultPreview == "" || summaryTimeAfter(request.CompletedAt, resultAt)) {
			result.ResultPreview = resultPreview
			resultAt = request.CompletedAt
		}
		firstNonEmptySummary(&result.Initiator, request.Initiator)
		firstNonEmptySummary(&result.AgentOrApp, request.AgentOrApp)
		firstNonEmptySummary(&result.AgentName, request.AgentName)
		firstNonEmptySummary(&result.ApplicationPrincipalID, request.ApplicationPrincipalID)
		firstNonEmptySummary(&result.EffectiveSubjectID, request.EffectiveSubjectID)
		firstNonEmptySummary(&result.BusinessDomain, request.BusinessDomain)
		firstNonEmptySummary(&result.ErrorSummary, request.ErrorSummary)
		result.TraceCount += request.TraceCount
		if request.InteractionID != "" {
			interactions[request.InteractionID] = struct{}{}
		}
		for _, network := range request.KnowledgeNetworks {
			knowledgeNetworks[network] = struct{}{}
		}
		if !isAuxiliaryDiscoveryGap(request) {
			for _, reason := range request.PartialReasons {
				partialReasons[reason] = struct{}{}
			}
			if evidenceCompletenessRank(request.EvidenceCompleteness) > evidenceCompletenessRank(result.EvidenceCompleteness) {
				result.EvidenceCompleteness = request.EvidenceCompleteness
			}
		}
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
	switch {
	case hasError:
		result.Status = "error"
	case hasRunning:
		result.Status = "running"
	case allCompleted:
		result.Status = "completed"
	}
	if !started.IsZero() {
		result.StartedAt = started.Format(time.RFC3339Nano)
	}
	if !completed.IsZero() {
		result.CompletedAt = completed.Format(time.RFC3339Nano)
	}
	if !started.IsZero() && !completed.IsZero() && !completed.Before(started) {
		result.DurationMS = completed.Sub(started).Milliseconds()
	}
	result.KnowledgeNetworks = sortedSummarySet(knowledgeNetworks)
	result.PartialReasons = sortedSummarySet(partialReasons)
	reconcileAggregatedContentAvailability(&result)
	if result.EvidenceCompleteness == "" {
		result.EvidenceCompleteness = "content_unavailable"
	}
	return result, len(interactions)
}

func conversationDurationMS(requests []evidencevo.RequestSummary) int64 {
	byInteraction := map[string][]evidencevo.RequestSummary{}
	for _, request := range requests {
		if request.InteractionID != "" {
			byInteraction[request.InteractionID] = append(byInteraction[request.InteractionID], request)
		}
	}
	var total int64
	for _, interactionRequests := range byInteraction {
		summary, _ := aggregateRequestGroup(interactionRequests)
		if summary.Status != "running" && summary.Status != "unknown" {
			total += summary.DurationMS
		}
	}
	return total
}

func reconcileAggregatedContentAvailability(summary *evidencevo.RequestSummary) {
	if summary == nil || len(summary.PartialReasons) == 0 {
		return
	}
	reasons := make([]string, 0, len(summary.PartialReasons))
	removed := false
	for _, reason := range summary.PartialReasons {
		if reason == "question_content_unavailable" && summary.QuestionPreview != "" {
			removed = true
			continue
		}
		if reason == "result_content_unavailable" && summary.ResultPreview != "" {
			removed = true
			continue
		}
		reasons = append(reasons, reason)
	}
	summary.PartialReasons = reasons
	if removed && len(reasons) == 0 && summary.EvidenceCompleteness == "partial" {
		summary.EvidenceCompleteness = "complete"
	}
}

func firstPresentSummary(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func isAuxiliaryDiscoveryGap(request evidencevo.RequestSummary) bool {
	if request.EvidenceCompleteness != "partial" || len(request.PartialReasons) != 1 ||
		request.PartialReasons[0] != "supporting_evidence_unavailable" {
		return false
	}
	switch strings.TrimPrefix(request.ToolName, "context.") {
	case "list_knowledge_networks", "list_resources":
		return true
	default:
		return false
	}
}

func (s *Service) applyCanonicalConversationState(
	ctx context.Context,
	entries []evidencevo.ConversationSummary,
	requestsByConversation map[string][]evidencevo.RequestSummary,
) error {
	if s.sessionStore == nil || len(entries) == 0 {
		return nil
	}
	return s.sessionStore.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		for index := range entries {
			conversation, found := tx.PeekConversation(entries[index].ConversationID)
			if !found {
				continue
			}
			applyCanonicalConversationEvidenceAndDuration(
				&entries[index].EvidenceCompleteness, &entries[index].DurationMS,
				tx, requestsByConversation[entries[index].ConversationID],
			)
			entries[index].Status = string(conversation.Status)
			entries[index].AgentName = conversation.AgentName
			entries[index].ApplicationPrincipalID = conversation.Owner.ApplicationPrincipalID
			entries[index].EffectiveSubjectID = conversation.Owner.EffectiveSubjectID
			if conversation.Status == sessionvo.ConversationActive {
				entries[index].CompletedAt = ""
				continue
			}
			completedAt := conversation.UpdatedAt
			if conversation.ClosedAt != nil {
				completedAt = *conversation.ClosedAt
			}
			entries[index].CompletedAt = completedAt.UTC().Format(time.RFC3339Nano)
		}
		return nil
	})
}

func applyCanonicalConversationEvidenceAndDuration(
	evidenceCompleteness *string,
	durationMS *int64,
	tx isessionstore.Transaction,
	requests []evidencevo.RequestSummary,
) {
	byInteraction := map[string][]evidencevo.RequestSummary{}
	for _, request := range requests {
		if request.InteractionID != "" {
			byInteraction[request.InteractionID] = append(byInteraction[request.InteractionID], request)
		}
	}
	var total int64
	for interactionID, interactionRequests := range byInteraction {
		interaction, found := tx.PeekInteraction(interactionID)
		if found {
			total += canonicalInteractionDurationMS(interaction)
			candidate := string(interaction.EvidenceStatus)
			if canonicalEvidenceRank(candidate) > canonicalEvidenceRank(*evidenceCompleteness) {
				*evidenceCompleteness = candidate
			}
			continue
		}
		summary, _ := aggregateRequestGroup(interactionRequests)
		if summary.Status != "running" && summary.Status != "unknown" {
			total += summary.DurationMS
		}
	}
	*durationMS = total
}

func canonicalInteractionDurationMS(interaction sessionvo.Interaction) int64 {
	if interaction.ExecutionStatus == sessionvo.InteractionActive || interaction.CreatedAt.IsZero() {
		return 0
	}
	terminalAt := interaction.UpdatedAt
	if interaction.TerminalAt != nil {
		terminalAt = *interaction.TerminalAt
	}
	if terminalAt.IsZero() || terminalAt.Before(interaction.CreatedAt) {
		return 0
	}
	return terminalAt.Sub(interaction.CreatedAt).Milliseconds()
}

func canonicalEvidenceRank(value string) int {
	switch value {
	case string(sessionvo.EvidenceFailed):
		return 5
	case string(sessionvo.EvidenceAssembling):
		return 4
	case string(sessionvo.EvidencePartial):
		return 3
	case "content_unavailable":
		return 2
	case string(sessionvo.EvidenceComplete):
		return 1
	default:
		return 0
	}
}

func (s *Service) applyCanonicalInteractionState(ctx context.Context, entries []evidencevo.InteractionListSummary) error {
	if s.sessionStore == nil || len(entries) == 0 {
		return nil
	}
	return s.sessionStore.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		for index := range entries {
			interaction, found := tx.PeekInteraction(entries[index].InteractionID)
			if !found {
				continue
			}
			applyInteractionState(
				&entries[index].Status,
				&entries[index].EvidenceCompleteness,
				&entries[index].StartedAt,
				&entries[index].CompletedAt,
				&entries[index].DurationMS,
				interaction,
			)
		}
		return nil
	})
}

func applyInteractionState(
	status, evidenceCompleteness, startedAt, completedAt *string,
	durationMS *int64,
	interaction sessionvo.Interaction,
) {
	*status = string(interaction.ExecutionStatus)
	*evidenceCompleteness = string(interaction.EvidenceStatus)
	if !interaction.CreatedAt.IsZero() {
		*startedAt = interaction.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	if interaction.ExecutionStatus == sessionvo.InteractionActive {
		*completedAt = ""
		*durationMS = 0
		return
	}
	terminalAt := interaction.UpdatedAt
	if interaction.TerminalAt != nil {
		terminalAt = *interaction.TerminalAt
	}
	if !terminalAt.IsZero() {
		*completedAt = terminalAt.UTC().Format(time.RFC3339Nano)
	}
	if !interaction.CreatedAt.IsZero() && !terminalAt.IsZero() && !terminalAt.Before(interaction.CreatedAt) {
		*durationMS = terminalAt.Sub(interaction.CreatedAt).Milliseconds()
	}
}

func (s *Service) applyCanonicalRequestIdentity(ctx context.Context, requests []evidencevo.RequestSummary) error {
	if s.sessionStore == nil || len(requests) == 0 {
		return nil
	}
	return s.sessionStore.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		byConversation := map[string]sessionvo.Conversation{}
		for index := range requests {
			conversationID := requests[index].ConversationID
			if conversationID == "" {
				continue
			}
			conversation, cached := byConversation[conversationID]
			if !cached {
				var found bool
				conversation, found = tx.PeekConversation(conversationID)
				if !found {
					continue
				}
				byConversation[conversationID] = conversation
			}
			requests[index].AgentName = conversation.AgentName
			requests[index].ApplicationPrincipalID = conversation.Owner.ApplicationPrincipalID
			requests[index].EffectiveSubjectID = conversation.Owner.EffectiveSubjectID
		}
		return nil
	})
}

func evidenceCompletenessRank(value string) int {
	switch value {
	case "partial":
		return 3
	case "content_unavailable":
		return 2
	case "complete":
		return 1
	default:
		return 0
	}
}

func sortedSummarySet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func firstNonEmptySummary(target *string, value string) {
	if *target == "" && value != "" {
		*target = value
	}
}

func summaryTimeBefore(value, other string) bool {
	if other == "" {
		return true
	}
	return value != "" && value < other
}

func summaryTimeAfter(value, other string) bool {
	if other == "" {
		return true
	}
	return value != "" && value > other
}

func summaryPageStart(length int, hasCursor bool, after func(int) bool) int {
	start := 0
	if hasCursor {
		for start < length && !after(start) {
			start++
		}
	}
	return start
}

func summaryPageEnd(start, length, limit int) int {
	end := start + normalizeSummaryLimit(limit)
	if end > length {
		return length
	}
	return end
}

func (s *Service) GetRequestSummary(ctx context.Context, requestID string, scope evidencevo.QueryScope) (evidencevo.RequestSummary, bool, error) {
	requests, _, metadata, err := s.loadRequestExecutionSummaries(ctx, strings.TrimSpace(requestID), scope)
	if err != nil {
		return evidencevo.RequestSummary{}, false, err
	}
	requestID = strings.TrimSpace(requestID)
	for _, request := range requests {
		if request.RequestID == requestID {
			if request.InteractionID != "" &&
				(request.InteractionQuestion == "" || request.InteractionResult == "") &&
				s.projectionSource != nil {
				result, projectionErr := s.projectionSource.LoadExecutionProjection(ctx, iprojectionsource.Query{
					Scope: scope, InteractionID: request.InteractionID, Limit: MaxSummaryScanEntries,
				})
				if projectionErr != nil {
					return evidencevo.RequestSummary{}, false, projectionErr
				}
				enrichedRequests, _ := evidencevo.BuildExecutionSummaries(result.Traces, result.Artifacts)
				for _, enriched := range enrichedRequests {
					if enriched.RequestID == requestID {
						request = enrichExactRequestSummary(request, enriched)
						break
					}
				}
				if result.Truncated {
					metadata.addReason("projection_scan_cap_reached")
				}
			}
			if metadata.Truncated {
				request.EvidenceCompleteness = "partial"
				for _, reason := range metadata.PartialReasons {
					request.PartialReasons = appendUniqueSummaryReason(request.PartialReasons, reason)
				}
			}
			requestsForDisplay := []evidencevo.RequestSummary{request}
			s.enrichRequestBusinessSummaries(ctx, requestsForDisplay, scope)
			request = requestsForDisplay[0]
			return request, true, nil
		}
	}
	return evidencevo.RequestSummary{}, false, nil
}

func enrichExactRequestSummary(target, source evidencevo.RequestSummary) evidencevo.RequestSummary {
	firstNonEmptySummary(&target.InteractionQuestion, source.InteractionQuestion)
	firstNonEmptySummary(&target.InteractionResult, source.InteractionResult)
	firstNonEmptySummary(&target.Initiator, source.Initiator)
	target.BusinessRefs = sortedSummarySet(summaryStringSet(target.BusinessRefs, source.BusinessRefs))
	target.KnowledgeNetworks = sortedSummarySet(summaryStringSet(target.KnowledgeNetworks, source.KnowledgeNetworks))
	if source.EvidenceCompleteness != "" && source.EvidenceCompleteness != "content_unavailable" {
		target.EvidenceCompleteness = source.EvidenceCompleteness
		target.PartialReasons = append([]string{}, source.PartialReasons...)
	}
	return target
}

func summaryStringSet(groups ...[]string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, group := range groups {
		for _, value := range group {
			if value != "" {
				result[value] = struct{}{}
			}
		}
	}
	return result
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
	s.enrichRequestBusinessSummaries(ctx, summary.Requests, scope)
	if len(summary.Requests) == 0 && len(summary.Traces) == 0 {
		return evidencevo.InteractionSummary{}, false, nil
	}
	base, _ := aggregateRequestGroup(summary.Requests)
	summary.AgentName = base.AgentName
	summary.ApplicationPrincipalID = base.ApplicationPrincipalID
	summary.EffectiveSubjectID = base.EffectiveSubjectID
	summary.QuestionPreview = base.QuestionPreview
	summary.ResultPreview = base.ResultPreview
	summary.EvidenceCompleteness = base.EvidenceCompleteness
	summary.PartialReasons = append([]string{}, base.PartialReasons...)
	summary.ErrorSummary = base.ErrorSummary
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
	if s.sessionStore != nil {
		err := s.sessionStore.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
			interaction, found := tx.PeekInteraction(interactionID)
			if !found {
				return nil
			}
			applyInteractionState(
				&summary.Status,
				&summary.EvidenceCompleteness,
				&summary.StartedAt,
				&summary.CompletedAt,
				&summary.DurationMS,
				interaction,
			)
			return nil
		})
		if err != nil {
			return evidencevo.InteractionSummary{}, false, err
		}
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

func (s *Service) enrichRequestBusinessSummaries(
	ctx context.Context,
	requests []evidencevo.RequestSummary,
	scope evidencevo.QueryScope,
) {
	if s.businessResolver == nil || len(requests) == 0 || !trustedQueryScope(scope) {
		return
	}
	refs := make([]ibusinessresolver.BusinessRef, 0)
	seen := map[string]struct{}{}
	for _, request := range requests {
		for _, refID := range request.BusinessRefs {
			refID = strings.TrimSpace(refID)
			if refID == "" {
				continue
			}
			if _, exists := seen[refID]; exists {
				continue
			}
			seen[refID] = struct{}{}
			refs = append(refs, ibusinessresolver.BusinessRef{RefID: refID})
		}
	}
	if len(refs) == 0 {
		return
	}
	resolved, err := s.businessResolver.ResolveBusinessRefs(ctx, ibusinessresolver.ResolveRequest{
		Scope: scope,
		Refs:  refs,
	})
	if err != nil {
		return
	}
	namesByRef := make(map[string]string, len(resolved))
	for _, resolution := range resolved {
		if resolution.Visibility != "visible" || resolution.Display == nil {
			continue
		}
		name := strings.TrimSpace(resolution.Display.Name)
		if name != "" {
			namesByRef[resolution.RefID] = name
		}
	}
	for index := range requests {
		businessRefs := requests[index].BusinessRefs
		if strings.TrimPrefix(requests[index].ToolName, "context.") == "search_schema" {
			networkRefs := make([]string, 0, 1)
			for _, refID := range businessRefs {
				if strings.HasPrefix(refID, "kn:") {
					networkRefs = append(networkRefs, refID)
				}
			}
			if len(networkRefs) > 0 {
				businessRefs = networkRefs
			}
		}
		names := make([]string, 0, 3)
		seenNames := map[string]struct{}{}
		for _, refID := range businessRefs {
			name := namesByRef[refID]
			if name == "" {
				continue
			}
			if _, exists := seenNames[name]; exists {
				continue
			}
			seenNames[name] = struct{}{}
			names = append(names, name)
			if len(names) == 3 {
				break
			}
		}
		requests[index].ControlledSummary = strings.Join(names, "、")
	}
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
		return s.loadProjectedExecutionSummaries(ctx, iprojectionsource.Query{
			Scope: scope, TraceID: traceID, Limit: MaxSummaryScanEntries,
		}, metadata)
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
	requestSummaries, traceSummaries := evidencevo.BuildExecutionSummaries(traces, artifacts)
	if err := s.applyCanonicalRequestIdentity(ctx, requestSummaries); err != nil {
		return nil, nil, summaryLoadMetadata{}, err
	}
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
	requests, traceSummaries := evidencevo.BuildExecutionSummaries(result.Traces, result.Artifacts)
	if err := s.applyCanonicalRequestIdentity(ctx, requests); err != nil {
		return nil, nil, summaryLoadMetadata{}, err
	}
	s.enrichTraceStats(ctx, traceSummaries)
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
		return s.loadProjectedExecutionSummaries(ctx, iprojectionsource.Query{
			Scope: scope, RequestID: requestID, Limit: MaxSummaryScanEntries,
		}, metadata)
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
	requests, traceSummaries := evidencevo.BuildExecutionSummaries(traces, artifacts)
	if err := s.applyCanonicalRequestIdentity(ctx, requests); err != nil {
		return nil, nil, summaryLoadMetadata{}, err
	}
	s.enrichTraceStats(ctx, traceSummaries)
	return requests, traceSummaries, metadata, nil
}

func (s *Service) loadProjectedExecutionSummaries(
	ctx context.Context,
	query iprojectionsource.Query,
	metadata summaryLoadMetadata,
) ([]evidencevo.RequestSummary, []evidencevo.TraceSummary, summaryLoadMetadata, error) {
	if s.projectionSource == nil {
		return []evidencevo.RequestSummary{}, []evidencevo.TraceSummary{}, metadata, nil
	}
	result, err := s.projectionSource.LoadExecutionProjection(ctx, query)
	if err != nil {
		return nil, nil, summaryLoadMetadata{}, err
	}
	if result.Truncated {
		metadata.addReason("projection_scan_cap_reached")
	}
	requests, traces := evidencevo.BuildExecutionSummaries(result.Traces, result.Artifacts)
	if err := s.applyCanonicalRequestIdentity(ctx, requests); err != nil {
		return nil, nil, summaryLoadMetadata{}, err
	}
	s.enrichTraceStats(ctx, traces)
	return requests, traces, metadata, nil
}

func (s *Service) enrichTraceStats(ctx context.Context, traces []evidencevo.TraceSummary) {
	if s.traceStatsSource == nil || len(traces) == 0 {
		return
	}
	traceIDs := make([]string, 0, len(traces))
	for _, trace := range traces {
		traceIDs = append(traceIDs, trace.TraceID)
	}
	counts, err := s.traceStatsSource.CountSpansByTraceIDs(ctx, traceIDs)
	if err != nil {
		return
	}
	for index := range traces {
		count, found := counts[traces[index].TraceID]
		if !found {
			continue
		}
		traces[index].SpanCount = count
		traces[index].SpanCountStatus = "available"
	}
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
	if options.AgentOrApp != "" &&
		summary.AgentOrApp != options.AgentOrApp &&
		summary.AgentName != options.AgentOrApp &&
		summary.ApplicationPrincipalID != options.AgentOrApp &&
		summary.EffectiveSubjectID != options.AgentOrApp {
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
			summary.QuestionPreview, summary.ResultPreview, summary.AgentOrApp, summary.AgentName,
			summary.ApplicationPrincipalID, summary.EffectiveSubjectID,
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

func normalizeSummaryPage(page int) int {
	if page <= 0 {
		return 1
	}
	return page
}

func summaryOffset(options evidencevo.SummaryQueryOptions, length int) int {
	page := normalizeSummaryPage(options.Page)
	limit := normalizeSummaryLimit(options.Limit)
	if page > 1 && page-1 > length/limit {
		return length
	}
	return (page - 1) * limit
}
