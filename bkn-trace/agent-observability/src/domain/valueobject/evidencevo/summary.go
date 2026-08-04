package evidencevo

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type ActionSummary struct {
	Recommended int    `json:"recommended"`
	Approved    int    `json:"approved"`
	Executed    int    `json:"executed"`
	Completed   int    `json:"completed"`
	LastStatus  string `json:"last_status,omitempty"`
}

type RequestSummary struct {
	RequestID              string        `json:"request_id"`
	OperationID            string        `json:"operation_id,omitempty"`
	OperationKey           string        `json:"operation_key,omitempty"`
	ToolName               string        `json:"tool_name,omitempty"`
	ControlledSummary      string        `json:"controlled_summary,omitempty"`
	ConversationID         string        `json:"conversation_id,omitempty"`
	InteractionID          string        `json:"interaction_id,omitempty"`
	StartedAt              string        `json:"started_at,omitempty"`
	CompletedAt            string        `json:"completed_at,omitempty"`
	Initiator              string        `json:"initiator,omitempty"`
	AgentOrApp             string        `json:"agent_or_app,omitempty"`
	AgentName              string        `json:"agent_name,omitempty"`
	ApplicationPrincipalID string        `json:"application_principal_id,omitempty"`
	EffectiveSubjectID     string        `json:"effective_subject_id,omitempty"`
	BusinessDomain         string        `json:"business_domain,omitempty"`
	KnowledgeNetworks      []string      `json:"knowledge_networks,omitempty"`
	QuestionPreview        string        `json:"question_preview,omitempty"`
	ResultPreview          string        `json:"result_preview,omitempty"`
	ResultCount            *int          `json:"result_count,omitempty"`
	Status                 string        `json:"status"`
	EvidenceCompleteness   string        `json:"evidence_completeness"`
	PartialReasons         []string      `json:"partial_reasons,omitempty"`
	BusinessRefs           []string      `json:"business_refs,omitempty"`
	ActionSummary          ActionSummary `json:"action_summary"`
	TraceCount             int           `json:"trace_count"`
	DurationMS             int64         `json:"duration_ms,omitempty"`
	ErrorSummary           string        `json:"error_summary,omitempty"`
	InteractionQuestion    string        `json:"-"`
	InteractionResult      string        `json:"-"`
}

type TraceSummary struct {
	TraceID                string `json:"trace_id"`
	RequestID              string `json:"request_id"`
	ConversationID         string `json:"conversation_id,omitempty"`
	InteractionID          string `json:"interaction_id,omitempty"`
	StartedAt              string `json:"started_at,omitempty"`
	CompletedAt            string `json:"completed_at,omitempty"`
	AgentOrApp             string `json:"agent_or_app,omitempty"`
	AgentName              string `json:"agent_name,omitempty"`
	ApplicationPrincipalID string `json:"application_principal_id,omitempty"`
	EffectiveSubjectID     string `json:"effective_subject_id,omitempty"`
	BusinessDomain         string `json:"business_domain,omitempty"`
	RootOperation          string `json:"root_operation,omitempty"`
	Status                 string `json:"status"`
	SpanCount              int    `json:"span_count"`
	SpanCountStatus        string `json:"span_count_status"`
	DurationMS             int64  `json:"duration_ms,omitempty"`
	ErrorSummary           string `json:"error_summary,omitempty"`
}

type SummaryQueryOptions struct {
	Limit                int
	Page                 int
	Cursor               string
	TraceID              string
	ConversationID       string
	InteractionID        string
	Scope                QueryScope
	From                 time.Time
	To                   time.Time
	Status               string
	AgentOrApp           string
	BusinessDomain       string
	KnowledgeNetwork     string
	EvidenceCompleteness string
	Keyword              string
}

type RequestSummaryPage struct {
	Entries        []RequestSummary `json:"entries"`
	Total          int              `json:"total"`
	Page           int              `json:"page"`
	PageSize       int              `json:"page_size"`
	NextCursor     *string          `json:"next_cursor"`
	Truncated      bool             `json:"truncated"`
	Partial        bool             `json:"partial"`
	PartialReasons []string         `json:"partial_reasons,omitempty"`
}

type TraceSummaryPage struct {
	Entries        []TraceSummary `json:"entries"`
	Total          int            `json:"total"`
	Page           int            `json:"page"`
	PageSize       int            `json:"page_size"`
	NextCursor     *string        `json:"next_cursor"`
	Truncated      bool           `json:"truncated"`
	Partial        bool           `json:"partial"`
	PartialReasons []string       `json:"partial_reasons,omitempty"`
}

type ConversationSummary struct {
	ConversationID         string   `json:"conversation_id"`
	StartedAt              string   `json:"started_at,omitempty"`
	CompletedAt            string   `json:"completed_at,omitempty"`
	Initiator              string   `json:"initiator,omitempty"`
	AgentOrApp             string   `json:"agent_or_app,omitempty"`
	AgentName              string   `json:"agent_name,omitempty"`
	ApplicationPrincipalID string   `json:"application_principal_id,omitempty"`
	EffectiveSubjectID     string   `json:"effective_subject_id,omitempty"`
	BusinessDomain         string   `json:"business_domain,omitempty"`
	KnowledgeNetworks      []string `json:"knowledge_networks,omitempty"`
	QuestionPreview        string   `json:"question_preview,omitempty"`
	ResultPreview          string   `json:"result_preview,omitempty"`
	Status                 string   `json:"status"`
	EvidenceCompleteness   string   `json:"evidence_completeness"`
	PartialReasons         []string `json:"partial_reasons,omitempty"`
	InteractionCount       int      `json:"interaction_count"`
	RequestCount           int      `json:"request_count"`
	TraceCount             int      `json:"trace_count"`
	DurationMS             int64    `json:"duration_ms,omitempty"`
	ErrorSummary           string   `json:"error_summary,omitempty"`
}

type InteractionListSummary struct {
	InteractionID          string   `json:"interaction_id"`
	ConversationID         string   `json:"conversation_id,omitempty"`
	StartedAt              string   `json:"started_at,omitempty"`
	CompletedAt            string   `json:"completed_at,omitempty"`
	Initiator              string   `json:"initiator,omitempty"`
	AgentOrApp             string   `json:"agent_or_app,omitempty"`
	AgentName              string   `json:"agent_name,omitempty"`
	ApplicationPrincipalID string   `json:"application_principal_id,omitempty"`
	EffectiveSubjectID     string   `json:"effective_subject_id,omitempty"`
	BusinessDomain         string   `json:"business_domain,omitempty"`
	KnowledgeNetworks      []string `json:"knowledge_networks,omitempty"`
	QuestionPreview        string   `json:"question_preview,omitempty"`
	ResultPreview          string   `json:"result_preview,omitempty"`
	Status                 string   `json:"status"`
	EvidenceCompleteness   string   `json:"evidence_completeness"`
	PartialReasons         []string `json:"partial_reasons,omitempty"`
	RequestCount           int      `json:"request_count"`
	TraceCount             int      `json:"trace_count"`
	DurationMS             int64    `json:"duration_ms,omitempty"`
	ErrorSummary           string   `json:"error_summary,omitempty"`
}

type ConversationSummaryPage struct {
	Entries        []ConversationSummary `json:"entries"`
	Total          int                   `json:"total"`
	Page           int                   `json:"page"`
	PageSize       int                   `json:"page_size"`
	NextCursor     *string               `json:"next_cursor"`
	Truncated      bool                  `json:"truncated"`
	Partial        bool                  `json:"partial"`
	PartialReasons []string              `json:"partial_reasons,omitempty"`
}

type InteractionSummaryPage struct {
	Entries        []InteractionListSummary `json:"entries"`
	Total          int                      `json:"total"`
	Page           int                      `json:"page"`
	PageSize       int                      `json:"page_size"`
	NextCursor     *string                  `json:"next_cursor"`
	Truncated      bool                     `json:"truncated"`
	Partial        bool                     `json:"partial"`
	PartialReasons []string                 `json:"partial_reasons,omitempty"`
}

type InteractionSummary struct {
	InteractionID          string           `json:"interaction_id"`
	ConversationID         string           `json:"conversation_id,omitempty"`
	AgentName              string           `json:"agent_name,omitempty"`
	ApplicationPrincipalID string           `json:"application_principal_id,omitempty"`
	EffectiveSubjectID     string           `json:"effective_subject_id,omitempty"`
	StartedAt              string           `json:"started_at,omitempty"`
	CompletedAt            string           `json:"completed_at,omitempty"`
	QuestionPreview        string           `json:"question_preview,omitempty"`
	ResultPreview          string           `json:"result_preview,omitempty"`
	Status                 string           `json:"status"`
	EvidenceCompleteness   string           `json:"evidence_completeness"`
	PartialReasons         []string         `json:"partial_reasons,omitempty"`
	ErrorSummary           string           `json:"error_summary,omitempty"`
	DurationMS             int64            `json:"duration_ms,omitempty"`
	Requests               []RequestSummary `json:"requests"`
	Traces                 []TraceSummary   `json:"traces"`
}

func BuildExecutionSummaries(traces []NormalizedTrace, artifacts []EvidenceArtifact) ([]RequestSummary, []TraceSummary) {
	type requestSource struct {
		traces               []NormalizedTrace
		artifacts            []EvidenceArtifact
		artifactRoles        map[string][]ArtifactLinkRole
		artifactTypeMismatch bool
	}
	requests := map[string]*requestSource{}
	for _, trace := range traces {
		if trace.RequestID == "" {
			continue
		}
		source := requests[trace.RequestID]
		if source == nil {
			source = &requestSource{}
			requests[trace.RequestID] = source
		}
		source.traces = append(source.traces, trace)
	}
	for _, source := range requests {
		source.artifactRoles = referencedArtifactRoles(source.traces)
	}
	for _, artifact := range artifacts {
		if artifact.RequestID == "" || requests[artifact.RequestID] == nil {
			continue
		}
		roles := requests[artifact.RequestID].artifactRoles[artifact.ArtifactID]
		if len(roles) == 0 {
			continue
		}
		matches := true
		for _, role := range roles {
			if !ArtifactTypeMatchesRole(artifact.ArtifactType, role) {
				matches = false
				requests[artifact.RequestID].artifactTypeMismatch = true
			}
		}
		if !matches {
			continue
		}
		requests[artifact.RequestID].artifacts = append(requests[artifact.RequestID].artifacts, artifact)
	}

	requestSummaries := make([]RequestSummary, 0, len(requests))
	traceSummaries := make([]TraceSummary, 0, len(traces))
	for requestID, source := range requests {
		requestSummary, requestTraces := buildRequestSummary(
			requestID,
			source.traces,
			source.artifacts,
			source.artifactTypeMismatch,
		)
		requestSummaries = append(requestSummaries, requestSummary)
		traceSummaries = append(traceSummaries, requestTraces...)
	}
	sort.Slice(requestSummaries, func(i, j int) bool {
		if requestSummaries[i].StartedAt == requestSummaries[j].StartedAt {
			return requestSummaries[i].RequestID < requestSummaries[j].RequestID
		}
		return requestSummaries[i].StartedAt > requestSummaries[j].StartedAt
	})
	sort.Slice(traceSummaries, func(i, j int) bool {
		if traceSummaries[i].StartedAt == traceSummaries[j].StartedAt {
			return traceSummaries[i].TraceID < traceSummaries[j].TraceID
		}
		return traceSummaries[i].StartedAt > traceSummaries[j].StartedAt
	})
	return requestSummaries, traceSummaries
}

func buildRequestSummary(
	requestID string,
	traces []NormalizedTrace,
	artifacts []EvidenceArtifact,
	artifactTypeMismatch bool,
) (RequestSummary, []TraceSummary) {
	summary := RequestSummary{
		RequestID: requestID, Status: "unknown",
		EvidenceCompleteness: "content_unavailable",
	}
	businessRefs := map[string]struct{}{}
	knowledgeNetworks := map[string]struct{}{}
	var started, completed time.Time
	allTracesTerminal := len(traces) > 0
	hasTerminalFact := false
	hasError := false
	hasRunning := false
	hasDurableReceipt := false
	receiptPartialReasons := map[string]struct{}{}
	rootOperationID := ""
	rootOperationKey := ""
	rootToolName := ""
	var rootResultCount *int
	traceSummaries := make([]TraceSummary, 0, len(traces))
	for _, trace := range traces {
		traceSummary := buildTraceSummary(trace, artifactsForTrace(trace.TraceID, artifacts))
		traceSummaries = append(traceSummaries, traceSummary)
		summary.TraceCount++
		mergeStableIdentity(&summary.ConversationID, traceSummary.ConversationID)
		mergeStableIdentity(&summary.InteractionID, traceSummary.InteractionID)
		firstNonEmpty(&summary.BusinessDomain, trace.BusinessDomain)
		firstNonEmpty(&summary.AgentOrApp, traceSummary.AgentOrApp)
		firstNonEmpty(&summary.ApplicationPrincipalID, traceSummary.ApplicationPrincipalID)
		firstNonEmpty(&summary.EffectiveSubjectID, traceSummary.EffectiveSubjectID)
		mergeStarted(&started, traceSummary.StartedAt)
		switch traceSummary.Status {
		case "error":
			hasError = true
			hasTerminalFact = true
			mergeCompleted(&completed, traceSummary.CompletedAt)
			firstNonEmpty(&summary.ErrorSummary, traceSummary.ErrorSummary)
		case "completed":
			hasTerminalFact = true
			mergeCompleted(&completed, traceSummary.CompletedAt)
		case "running":
			allTracesTerminal = false
			hasRunning = true
		default:
			allTracesTerminal = false
		}
		for _, event := range trace.Events {
			if strings.HasPrefix(event.EventID, "receipt:") {
				durability, _ := summaryStringField(event.Payload, "evidence_durability")
				if durability == "durable" {
					hasDurableReceipt = true
				} else if durability == "failed" {
					receiptPartialReasons["evidence_durability_failed"] = struct{}{}
				}
				for _, reason := range summaryStringValues(event.Payload["partial_reasons"]) {
					receiptPartialReasons[reason] = struct{}{}
				}
			}
			mergeStableIdentity(&summary.OperationID, event.OperationID)
			if event.OperationID != "" || event.EventType == "retrieval.completed" {
				mergeStableIdentity(&summary.ToolName, event.OperationName)
				if operationKey, _ := summaryStringField(event.Payload, "operation_key"); operationKey != "" {
					mergeStableIdentity(&summary.OperationKey, operationKey)
				}
			}
			if event.EventType == "retrieval.completed" {
				mergeStableIdentity(&rootOperationID, event.OperationID)
				mergeStableIdentity(&rootToolName, event.OperationName)
				if operationKey, _ := summaryStringField(event.Payload, "operation_key"); operationKey != "" {
					mergeStableIdentity(&rootOperationKey, operationKey)
				}
				if resultCount := summaryIntPointer(event.Payload, "candidate_count"); resultCount != nil {
					rootResultCount = resultCount
				}
			}
			collectBusinessRefs(event.Payload, businessRefs)
			advanceActionSummary(&summary.ActionSummary, event.EventType)
		}
	}
	if rootOperationID != "" && rootOperationID != "-" {
		summary.OperationID = rootOperationID
	}
	if rootOperationKey != "" && rootOperationKey != "-" {
		summary.OperationKey = rootOperationKey
	}
	if rootToolName != "" && rootToolName != "-" {
		summary.ToolName = rootToolName
	}
	summary.ResultCount = rootResultCount

	hasQuestion := false
	hasResult := false
	hasSupportingEvidence := false
	sort.Slice(artifacts, func(i, j int) bool {
		if artifacts[i].ObservedAt == artifacts[j].ObservedAt {
			return artifacts[i].ArtifactID < artifacts[j].ArtifactID
		}
		return artifacts[i].ObservedAt < artifacts[j].ObservedAt
	})
	for _, artifact := range artifacts {
		firstNonEmpty(&summary.BusinessDomain, artifact.BusinessDomain)
		firstNonEmpty(&summary.AgentOrApp, artifact.AgentOrApp)
		firstNonEmpty(&summary.Initiator, artifact.Initiator)
		if started.IsZero() {
			mergeStarted(&started, artifact.ObservedAt)
		}
		for _, ref := range artifact.BusinessRefs {
			if ref != "" {
				businessRefs[ref] = struct{}{}
			}
		}
		switch artifact.ArtifactType {
		case ArtifactTypeQuestion:
			if preview := artifactPreview(artifact); preview != "" {
				if interactionScopedArtifact(artifact) {
					firstNonEmpty(&summary.InteractionQuestion, preview)
				} else if !hasQuestion {
					summary.QuestionPreview = preview
					hasQuestion = true
				}
			}
		case ArtifactTypeResult:
			if preview := artifactPreview(artifact); preview != "" {
				if interactionScopedArtifact(artifact) {
					summary.InteractionResult = preview
				} else {
					summary.ResultPreview = preview
					hasResult = true
				}
			}
			if !interactionScopedArtifact(artifact) && completed.IsZero() {
				mergeCompleted(&completed, artifact.ObservedAt)
			}
		default:
			hasSupportingEvidence = true
		}
	}

	switch {
	case hasError:
		summary.Status = "error"
	case hasResult && allTracesTerminal:
		summary.Status = "completed"
	case allTracesTerminal && hasTerminalFact:
		summary.Status = "completed"
	case hasRunning:
		summary.Status = "running"
	default:
		summary.Status = "unknown"
	}
	hasSupportingEvidence = hasSupportingEvidence || len(businessRefs) > 0
	switch {
	case hasDurableReceipt && len(receiptPartialReasons) == 0:
		summary.EvidenceCompleteness = "complete"
	case hasDurableReceipt:
		summary.EvidenceCompleteness = "partial"
		for reason := range receiptPartialReasons {
			summary.PartialReasons = append(summary.PartialReasons, reason)
		}
		sort.Strings(summary.PartialReasons)
	case len(receiptPartialReasons) > 0:
		summary.EvidenceCompleteness = "partial"
		for reason := range receiptPartialReasons {
			summary.PartialReasons = append(summary.PartialReasons, reason)
		}
		sort.Strings(summary.PartialReasons)
	case hasQuestion && hasResult && hasSupportingEvidence:
		summary.EvidenceCompleteness = "complete"
	case len(artifacts) == 0:
		summary.EvidenceCompleteness = "content_unavailable"
		summary.PartialReasons = []string{"content_unavailable"}
	default:
		summary.EvidenceCompleteness = "partial"
		if !hasQuestion {
			summary.PartialReasons = append(summary.PartialReasons, "question_content_unavailable")
		}
		if !hasResult {
			summary.PartialReasons = append(summary.PartialReasons, "result_content_unavailable")
		}
		if hasQuestion && hasResult && !hasSupportingEvidence {
			summary.PartialReasons = append(summary.PartialReasons, "supporting_evidence_unavailable")
		}
	}
	if artifactTypeMismatch {
		summary.EvidenceCompleteness = "partial"
		summary.PartialReasons = appendUniqueSummaryValue(summary.PartialReasons, "artifact_type_mismatch")
	}

	for ref := range businessRefs {
		summary.BusinessRefs = append(summary.BusinessRefs, ref)
		if network := knowledgeNetworkFromRef(ref); network != "" {
			knowledgeNetworks[network] = struct{}{}
		}
	}
	sort.Strings(summary.BusinessRefs)
	for network := range knowledgeNetworks {
		summary.KnowledgeNetworks = append(summary.KnowledgeNetworks, network)
	}
	sort.Strings(summary.KnowledgeNetworks)
	if !started.IsZero() {
		summary.StartedAt = started.Format(time.RFC3339Nano)
	}
	if allTracesTerminal && hasTerminalFact && !completed.IsZero() {
		summary.CompletedAt = completed.Format(time.RFC3339Nano)
	}
	if allTracesTerminal && hasTerminalFact && !started.IsZero() && !completed.IsZero() && !completed.Before(started) {
		summary.DurationMS = completed.Sub(started).Milliseconds()
	}
	if summary.ConversationID == "-" {
		summary.ConversationID = ""
	}
	if summary.InteractionID == "-" {
		summary.InteractionID = ""
	}
	if summary.OperationID == "-" {
		summary.OperationID = ""
	}
	if summary.OperationKey == "-" {
		summary.OperationKey = ""
	}
	if summary.ToolName == "-" {
		summary.ToolName = ""
	}
	return summary, traceSummaries
}

func summaryStringValues(value any) []string {
	result := []string{}
	switch values := value.(type) {
	case []string:
		for _, item := range values {
			if item != "" {
				result = append(result, item)
			}
		}
	case []any:
		for _, item := range values {
			if text, ok := item.(string); ok && text != "" {
				result = append(result, text)
			}
		}
	}
	return result
}

func interactionScopedArtifact(artifact EvidenceArtifact) bool {
	return artifact.InteractionID != "" && artifact.OperationID == ""
}

func summaryIntPointer(payload map[string]any, key string) *int {
	value, exists := payload[key]
	if !exists {
		return nil
	}
	var result int
	switch typed := value.(type) {
	case int:
		result = typed
	case int32:
		result = int(typed)
	case int64:
		result = int(typed)
	case float64:
		result = int(typed)
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return nil
		}
		result = int(parsed)
	default:
		return nil
	}
	if result < 0 {
		return nil
	}
	return &result
}

func buildTraceSummary(trace NormalizedTrace, artifacts []EvidenceArtifact) TraceSummary {
	summary := TraceSummary{
		TraceID: trace.TraceID, RequestID: trace.RequestID,
		ConversationID:         trace.ConversationID,
		AgentOrApp:             trace.ApplicationPrincipalID,
		ApplicationPrincipalID: trace.ApplicationPrincipalID,
		EffectiveSubjectID:     trace.EffectiveSubjectID,
		BusinessDomain:         trace.BusinessDomain, Status: "unknown", SpanCountStatus: "unavailable",
	}
	var started, completed time.Time
	for _, event := range trace.Events {
		mergeStableIdentity(&summary.InteractionID, event.InteractionID)
		if event.EventType == "agent.interaction.started" {
			if agentID, _ := summaryStringField(event.Payload, "agent_id"); agentID != "" {
				firstNonEmpty(&summary.AgentOrApp, agentID)
			} else if appRef, _ := summaryStringField(event.Payload, "app_ref"); appRef != "" {
				firstNonEmpty(&summary.AgentOrApp, appRef)
			}
			firstNonEmpty(&summary.RootOperation, event.OperationName)
		}
		if event.EventType == "retrieval.completed" {
			firstNonEmpty(&summary.RootOperation, event.OperationName)
		}
		if strings.HasPrefix(event.EventID, "artifact-link:") {
			continue
		}
		mergeStarted(&started, event.ObservedAt)
		if eventHasError(event) {
			summary.Status = "error"
			firstNonEmpty(&summary.ErrorSummary, eventErrorSummary(event))
			mergeCompleted(&completed, eventTerminalTimestamp(event))
		} else if summary.Status != "error" && eventIsTerminal(event, artifacts) {
			summary.Status = "completed"
			mergeCompleted(&completed, eventTerminalTimestamp(event))
		} else if summary.Status == "unknown" {
			summary.Status = "running"
		}
	}
	for _, artifact := range artifacts {
		firstNonEmpty(&summary.AgentOrApp, artifact.AgentOrApp)
		if artifact.ArtifactType == ArtifactTypeResult || artifact.ArtifactType == ArtifactTypeActionResult {
			if summary.Status != "error" {
				summary.Status = "completed"
			}
			if completed.IsZero() {
				mergeCompleted(&completed, artifact.ObservedAt)
			}
		}
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
	if summary.InteractionID == "-" {
		summary.InteractionID = ""
	}
	return summary
}

func mergeStableIdentity(target *string, value string) {
	if value == "" || *target == "-" {
		return
	}
	if *target == "" {
		*target = value
		return
	}
	if *target != value {
		*target = "-"
	}
}

func mergeStarted(started *time.Time, value string) {
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		if started.IsZero() || parsed.Before(*started) {
			*started = parsed
		}
	}
}

func mergeCompleted(completed *time.Time, value string) {
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		if completed.IsZero() || parsed.After(*completed) {
			*completed = parsed
		}
	}
}

func eventIsTerminal(event EvidenceEvent, artifacts []EvidenceArtifact) bool {
	switch event.EventType {
	case "action.result_recorded", "retrieval.completed":
		return true
	case "data.query.observed":
		return eventReferencesArtifactType(event, artifacts, "result_artifact_ref", ArtifactTypeDataResult)
	case "logic.execution.observed":
		return eventReferencesArtifactType(event, artifacts, "result_artifact_ref", ArtifactTypeLogicExecution)
	}
	if event.EventType == "claim.created" {
		return eventReferencesArtifactType(event, artifacts, "result_artifact_ref", ArtifactTypeResult)
	}
	return false
}

func eventReferencesArtifactType(
	event EvidenceEvent,
	artifacts []EvidenceArtifact,
	field string,
	artifactType ArtifactType,
) bool {
	ref, _ := summaryStringField(event.Payload, field)
	artifactID, ok := ArtifactIDFromReference(ref)
	if !ok {
		return false
	}
	for _, artifact := range artifacts {
		if artifact.ArtifactID == artifactID && artifact.ArtifactType == artifactType {
			return true
		}
	}
	return false
}

func eventTerminalTimestamp(event EvidenceEvent) string {
	if event.EmittedAt != "" {
		return event.EmittedAt
	}
	return event.ObservedAt
}

func referencedArtifactRoles(traces []NormalizedTrace) map[string][]ArtifactLinkRole {
	rolesByArtifact := map[string][]ArtifactLinkRole{}
	for _, trace := range traces {
		for _, event := range trace.Events {
			for _, role := range ArtifactLinkRoles(event.EventType) {
				ref, _ := event.Payload[role.Field].(string)
				artifactID, ok := ArtifactIDFromReference(ref)
				if !ok {
					continue
				}
				rolesByArtifact[artifactID] = append(rolesByArtifact[artifactID], role)
			}
		}
	}
	return rolesByArtifact
}

func artifactsForTrace(traceID string, artifacts []EvidenceArtifact) []EvidenceArtifact {
	result := make([]EvidenceArtifact, 0)
	for _, artifact := range artifacts {
		if artifact.TraceID == traceID {
			result = append(result, artifact)
		}
	}
	return result
}

func summaryStringField(value map[string]any, key string) (string, bool) {
	text, ok := value[key].(string)
	return text, ok
}

func artifactPreview(artifact EvidenceArtifact) string {
	if artifact.Content == nil {
		return ""
	}
	if text, ok := artifact.Content.(string); ok {
		return truncatePreview(strings.TrimSpace(text), 240)
	}
	if object, ok := artifact.Content.(map[string]any); ok {
		for _, key := range []string{"text", "question", "result", "answer", "summary"} {
			if text, ok := object[key].(string); ok && strings.TrimSpace(text) != "" {
				return truncatePreview(strings.TrimSpace(text), 240)
			}
		}
	}
	body, err := json.Marshal(artifact.Content)
	if err != nil {
		return ""
	}
	return truncatePreview(string(body), 240)
}

func truncatePreview(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}

func collectBusinessRefs(value any, refs map[string]struct{}) {
	switch item := value.(type) {
	case map[string]any:
		if refID, ok := item["ref_id"].(string); ok && refID != "" {
			visibility, _ := item["visibility"].(string)
			if visibility != "" && visibility != "visible" {
				return
			}
			refs[refID] = struct{}{}
		}
		for _, nested := range item {
			collectBusinessRefs(nested, refs)
		}
	case []any:
		for _, nested := range item {
			collectBusinessRefs(nested, refs)
		}
	}
}

func knowledgeNetworkFromRef(ref string) string {
	return knowledgeNetworkIDFromCanonicalRef(ref)
}

func advanceActionSummary(summary *ActionSummary, eventType string) {
	switch eventType {
	case "action.recommended":
		summary.Recommended++
		summary.LastStatus = "recommended"
	case "action.approved":
		summary.Approved++
		summary.LastStatus = "approved"
	case "action.executed":
		summary.Executed++
		summary.LastStatus = "executed"
	case "action.result_recorded":
		summary.Completed++
		summary.LastStatus = "completed"
	case "action.rejected":
		summary.LastStatus = "rejected"
	}
}

func eventHasError(event EvidenceEvent) bool {
	for _, key := range []string{"status", "outcome"} {
		if value, ok := event.Payload[key].(string); ok {
			switch strings.ToLower(value) {
			case "error", "failed", "failure":
				return true
			}
		}
	}
	return event.EventType == "model.call.failed" || event.EventType == "tool.call.failed"
}

func eventErrorSummary(event EvidenceEvent) string {
	for _, key := range []string{"safe_error_summary", "error_type", "error_code"} {
		if value, ok := event.Payload[key].(string); ok && value != "" {
			return value
		}
	}
	if event.EventType == "model.call.failed" || event.EventType == "tool.call.failed" {
		return event.EventType
	}
	return ""
}

func firstNonEmpty(target *string, value string) {
	if *target == "" && value != "" {
		*target = value
	}
}

func appendUniqueSummaryValue(values []string, value string) []string {
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}
