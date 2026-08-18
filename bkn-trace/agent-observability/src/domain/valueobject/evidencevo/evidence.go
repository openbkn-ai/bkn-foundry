package evidencevo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

const (
	ContractVersion       = "2.1.0"
	LegacyContractVersion = "2.0.0"
)

type IngestRequest struct {
	SchemaVersion string          `json:"bkn.trace.schema.version"`
	Trace         TraceContext    `json:"trace"`
	Events        []EvidenceEvent `json:"events"`
}

type TraceContext struct {
	TraceID        string `json:"trace_id"`
	Traceparent    string `json:"traceparent"`
	RequestID      string `json:"bkn.request.id"`
	ConversationID string `json:"bkn.conversation.id,omitempty"`
	TenantID       string `json:"bkn.tenant.id,omitempty"`
	BusinessDomain string `json:"business_domain,omitempty"`
	AccountID      string `json:"bkn.account.id"`
	AccountType    string `json:"bkn.account.type"`
}

type EvidenceEvent struct {
	EventID       string         `json:"event_id"`
	EventType     string         `json:"event_type"`
	SchemaVersion string         `json:"bkn.trace.schema.version"`
	ObservedAt    string         `json:"observed_at"`
	EmittedAt     string         `json:"emitted_at"`
	Producer      string         `json:"producer_module"`
	TraceID       string         `json:"trace_id"`
	SpanID        string         `json:"span_id"`
	RequestID     string         `json:"bkn.request.id"`
	OperationName string         `json:"bkn.operation.name"`
	InteractionID string         `json:"interaction_id,omitempty"`
	OperationID   string         `json:"operation_id,omitempty"`
	CausationID   string         `json:"causation_event_id,omitempty"`
	ClaimID       string         `json:"claim_id,omitempty"`
	Attempt       int            `json:"attempt,omitempty"`
	Payload       map[string]any `json:"payload"`
}

const (
	AppendViolationEventIDConflict = "event_id_conflict"
	AppendViolationCausation       = "causation_invalid"
	AppendViolationAction          = "action_transition_invalid"
	AppendViolationOwnership       = "ownership_conflict"
	AppendViolationCapacity        = "trace_capacity_exceeded"
	MaxTraceEvents                 = 10000
	MaxTraceSerializedBytes        = 8 << 20
)

type AppendViolation struct {
	Kind    string
	EventID string
}

func (e EvidenceEvent) ContentHash() (string, error) {
	body, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func SupportedContractVersion(version string) bool {
	return version == LegacyContractVersion || version == ContractVersion || version == ArtifactContractVersion
}

func NovelEvents(existing []NormalizedTrace, incoming []EvidenceEvent) ([]EvidenceEvent, string, error) {
	hashes := map[string]string{}
	for _, trace := range existing {
		for _, event := range trace.Events {
			hash, err := event.ContentHash()
			if err != nil {
				return nil, "", err
			}
			hashes[event.EventID] = hash
		}
	}
	novel := make([]EvidenceEvent, 0, len(incoming))
	for _, event := range incoming {
		hash, err := event.ContentHash()
		if err != nil {
			return nil, "", err
		}
		if existingHash, ok := hashes[event.EventID]; ok {
			if existingHash != hash {
				return nil, event.EventID, nil
			}
			continue
		}
		hashes[event.EventID] = hash
		novel = append(novel, event)
	}
	return novel, "", nil
}

// ValidateAppend protects invariants that must be checked at the atomic storage boundary.
func ValidateAppend(existing []NormalizedTrace, incoming NormalizedTrace) *AppendViolation {
	priorEventIDs := map[string]struct{}{}
	type actionState struct {
		state       string
		claimID     string
		operationID string
		lastEventID string
	}
	actions := map[string]actionState{}
	for _, trace := range existing {
		if !SameOwnership(trace, incoming) {
			return &AppendViolation{Kind: AppendViolationOwnership}
		}
		for _, event := range trace.Events {
			priorEventIDs[event.EventID] = struct{}{}
			if actionID := payloadString(event.Payload, "action_instance_id"); actionID != "" && isActionEvent(event.EventType) {
				actions[actionID] = actionState{
					state:       actionEventState(event.EventType),
					claimID:     event.ClaimID,
					operationID: event.OperationID,
					lastEventID: event.EventID,
				}
			}
		}
	}

	novel, conflictID, err := NovelEvents(existing, incoming.Events)
	if err != nil || conflictID != "" {
		return &AppendViolation{Kind: AppendViolationEventIDConflict, EventID: conflictID}
	}
	causes := map[string]string{}
	for _, trace := range existing {
		for _, event := range trace.Events {
			causes[event.EventID] = event.CausationID
		}
	}
	for _, event := range novel {
		causes[event.EventID] = event.CausationID
	}
	if eventID := causationCycleEventID(causes); eventID != "" {
		return &AppendViolation{Kind: AppendViolationCausation, EventID: eventID}
	}
	for _, event := range novel {
		if isActionEvent(event.EventType) {
			actionID := payloadString(event.Payload, "action_instance_id")
			previous, exists := actions[actionID]
			expectedPrevious := map[string]string{
				"action.approval_requested": "recommended",
				"action.approved":           "approval_requested",
				"action.rejected":           "approval_requested",
				"action.executed":           "approved",
				"action.result_recorded":    "executed",
			}[event.EventType]
			valid := event.EventType == "action.recommended" && !exists
			if event.EventType != "action.recommended" {
				valid = exists && previous.state == expectedPrevious && event.CausationID == previous.lastEventID
			}
			if exists && (previous.claimID != event.ClaimID || previous.operationID != event.OperationID) {
				valid = false
			}
			if !valid {
				return &AppendViolation{Kind: AppendViolationAction, EventID: event.EventID}
			}
			actions[actionID] = actionState{
				state:       actionEventState(event.EventType),
				claimID:     event.ClaimID,
				operationID: event.OperationID,
				lastEventID: event.EventID,
			}
		}
		priorEventIDs[event.EventID] = struct{}{}
	}
	return nil
}

func causationCycleEventID(causes map[string]string) string {
	for eventID := range causes {
		seen := map[string]struct{}{}
		for current := eventID; current != ""; current = causes[current] {
			if _, ok := seen[current]; ok {
				return eventID
			}
			seen[current] = struct{}{}
			if _, known := causes[current]; !known {
				break
			}
		}
	}
	return ""
}

func payloadString(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return value
}

func isActionEvent(eventType string) bool {
	return len(eventType) > len("action.") && eventType[:len("action.")] == "action."
}

func actionEventState(eventType string) string {
	return eventType[len("action."):]
}

func WithEvents(trace NormalizedTrace, events []EvidenceEvent) NormalizedTrace {
	trace.Events = events
	trace.KnowledgeNetworkIDs = knowledgeNetworkIDsFromEvents(events)
	trace.AcceptedEvents = len(events)
	trace.ClaimIDs = nil
	trace.ClaimCount = 0
	trace.EvidenceRefCount = 0
	trace.BusinessRefCount = 0
	for _, event := range events {
		switch event.EventType {
		case "claim.created":
			trace.ClaimCount++
			if claimID, ok := event.Payload["claim_id"].(string); ok && claimID != "" {
				trace.ClaimIDs = append(trace.ClaimIDs, claimID)
			}
		case "evidence.refs.created":
			if refs, ok := event.Payload["evidence_refs"].([]any); ok {
				trace.EvidenceRefCount += len(refs)
			}
		case "business.refs.resolved":
			if refs, ok := event.Payload["business_refs"].([]any); ok {
				trace.BusinessRefCount += len(refs)
			}
		case "knowledge.read.observed":
			if refs, ok := event.Payload["business_refs"].([]any); ok {
				trace.BusinessRefCount += len(refs)
			}
		}
	}
	return trace
}

func knowledgeNetworkIDsFromEvents(events []EvidenceEvent) []string {
	networks := map[string]struct{}{}
	for _, event := range events {
		collectKnowledgeNetworkIDs(event.Payload, networks, false)
	}
	return sortedKnowledgeNetworkIDs(networks)
}

// KnowledgeNetworkIDsFromRefs derives the knowledge-network boundary from trusted canonical refs.
func KnowledgeNetworkIDsFromRefs(refs []string) []string {
	networks := map[string]struct{}{}
	for _, ref := range refs {
		if networkID := knowledgeNetworkIDFromCanonicalRef(ref); networkID != "" {
			networks[networkID] = struct{}{}
		}
	}
	return sortedKnowledgeNetworkIDs(networks)
}

func collectKnowledgeNetworkIDs(value any, networks map[string]struct{}, allowBareRef bool) {
	switch item := value.(type) {
	case string:
		if !allowBareRef {
			return
		}
		if networkID := knowledgeNetworkIDFromCanonicalRef(item); networkID != "" {
			networks[networkID] = struct{}{}
		}
	case map[string]any:
		if networkID, ok := item["kn_id"].(string); ok {
			addKnowledgeNetworkID(networkID, networks)
		}
		if refID, ok := item["ref_id"].(string); ok {
			collectKnowledgeNetworkIDs(refID, networks, true)
		}
		for key, nested := range item {
			collectKnowledgeNetworkIDs(nested, networks, isCanonicalRefContainer(key))
		}
	case []any:
		for _, nested := range item {
			collectKnowledgeNetworkIDs(nested, networks, allowBareRef)
		}
	}
}

func isCanonicalRefContainer(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return strings.HasSuffix(key, "_ref") || strings.HasSuffix(key, "_refs")
}

func knowledgeNetworkIDFromCanonicalRef(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimPrefix(ref, "business:")
	for _, refType := range []sessionvo.BusinessRefType{
		sessionvo.BusinessRefKnowledgeNetwork,
		sessionvo.BusinessRefObjectType,
		sessionvo.BusinessRefObjectInstance,
		sessionvo.BusinessRefProperty,
		sessionvo.BusinessRefRelationType,
		sessionvo.BusinessRefMetric,
		sessionvo.BusinessRefLogic,
		sessionvo.BusinessRefFunction,
		sessionvo.BusinessRefActionType,
		sessionvo.BusinessRefActionInstance,
	} {
		if !refType.MatchesCanonicalRefID(ref) {
			continue
		}
		parts := strings.SplitN(ref, ":", 3)
		if len(parts) >= 2 {
			return strings.TrimSpace(parts[1])
		}
	}
	return ""
}

func addKnowledgeNetworkID(networkID string, networks map[string]struct{}) {
	if networkID = strings.TrimSpace(networkID); networkID != "" {
		networks[networkID] = struct{}{}
	}
}

func sortedKnowledgeNetworkIDs(networks map[string]struct{}) []string {
	values := make([]string, 0, len(networks))
	for networkID := range networks {
		values = append(values, networkID)
	}
	sort.Strings(values)
	return values
}

type ValidationError struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`

	messageID   string
	messageData map[string]any
}

type ValidationErrors []ValidationError

func (e ValidationErrors) Error() string {
	if len(e) == 0 {
		return "validation failed"
	}
	return e[0].Code + ": " + e[0].Path
}

type NormalizedTrace struct {
	TraceID                string
	RequestID              string
	ConversationID         string
	TenantID               string
	BusinessDomain         string
	AccountID              string
	AccountType            string
	EffectiveSubjectID     string
	ApplicationPrincipalID string
	KnowledgeNetworkIDs    []string
	SchemaVersion          string
	Events                 []EvidenceEvent
	ClaimIDs               []string
	AcceptedEvents         int
	ClaimCount             int
	EvidenceRefCount       int
	BusinessRefCount       int
}

type EvidenceQueryOptions struct {
	Limit int
	Scope QueryScope
}

type QueryScope struct {
	TenantID       string
	BusinessDomain string
	AccountID      string
	AccountType    string
	Authorization  string         `json:"-"`
	AccessProfile  *AccessProfile `json:"-"`
	View           AccessView     `json:"-"`
}

func SameOwnership(existing NormalizedTrace, incoming NormalizedTrace) bool {
	return existing.TraceID == incoming.TraceID &&
		existing.RequestID == incoming.RequestID &&
		compatibleOptionalIdentity(existing.ConversationID, incoming.ConversationID) &&
		existing.TenantID == incoming.TenantID &&
		existing.BusinessDomain == incoming.BusinessDomain &&
		existing.AccountID == incoming.AccountID &&
		existing.AccountType == incoming.AccountType
}

func compatibleOptionalIdentity(existing, incoming string) bool {
	return existing == "" || incoming == "" || existing == incoming
}

func MatchesScope(trace NormalizedTrace, scope QueryScope) bool {
	if scope.AccessProfile != nil {
		return CanReadRecord(*scope.AccessProfile, trace.RecordScope(), defaultAccessView(scope.View))
	}
	if trace.AccountID == "" || trace.AccountType == "" || trace.TenantID == "" && trace.BusinessDomain == "" {
		return false
	}
	if trace.AccountID != scope.AccountID || trace.AccountType != scope.AccountType {
		return false
	}
	if trace.TenantID != "" && trace.TenantID != scope.TenantID {
		return false
	}
	if trace.BusinessDomain != "" && trace.BusinessDomain != scope.BusinessDomain {
		return false
	}
	return true
}

func (trace NormalizedTrace) RecordScope() RecordScope {
	effectiveSubjectID := trace.EffectiveSubjectID
	applicationPrincipalID := trace.ApplicationPrincipalID
	if effectiveSubjectID == "" && trace.AccountType != "app" && trace.AccountType != "service" {
		effectiveSubjectID = trace.AccountID
	}
	if applicationPrincipalID == "" && (trace.AccountType == "app" || trace.AccountType == "service") {
		applicationPrincipalID = trace.AccountID
	}
	return RecordScope{
		TenantID: trace.TenantID, BusinessDomain: trace.BusinessDomain,
		EffectiveSubjectID: effectiveSubjectID, ApplicationPrincipalID: applicationPrincipalID,
		KnowledgeNetworkIDs: trace.KnowledgeNetworkIDs,
	}
}

type EvidenceQueryResult struct {
	Traces    []NormalizedTrace
	Truncated bool
}

type EvidenceChainResponse struct {
	TraceID           string            `json:"trace_id"`
	RequestID         string            `json:"bkn.request.id"`
	ConclusionScope   string            `json:"conclusion_scope"`
	Partial           bool              `json:"partial"`
	PartialReasons    []string          `json:"partial_reason"`
	VisibilitySummary VisibilitySummary `json:"visibility_summary"`
	Page              EvidencePage      `json:"page"`
	Data              EvidenceChainData `json:"data"`
}

type VisibilitySummary struct {
	AuthorizedRefCount   int `json:"authorized_ref_count"`
	RedactedRefCount     int `json:"redacted_ref_count"`
	HiddenRefCount       int `json:"hidden_ref_count"`
	OmittedRefCount      int `json:"omitted_ref_count"`
	UnresolvedRefCount   int `json:"unresolved_ref_count"`
	UnauthorizedRefCount int `json:"unauthorized_ref_count"`
}

type EvidencePage struct {
	NextCursor *string `json:"next_cursor"`
	NodeCount  int     `json:"node_count"`
	EdgeCount  int     `json:"edge_count"`
	Truncated  bool    `json:"truncated"`
}

type EvidenceChainData struct {
	Claims        []map[string]any `json:"claims"`
	EvidenceRefs  []map[string]any `json:"evidence_refs"`
	BusinessRefs  []map[string]any `json:"business_refs"`
	ArtifactLinks []ArtifactLink   `json:"artifact_links"`
}

type ArtifactLink struct {
	ArtifactRef  string       `json:"artifact_ref"`
	ArtifactType ArtifactType `json:"artifact_type"`
	Role         string       `json:"role"`
	EventID      string       `json:"event_id"`
	EventType    string       `json:"event_type"`
	OperationID  string       `json:"operation_id,omitempty"`
	ClaimID      string       `json:"claim_id,omitempty"`
}

type BusinessGraphResponse struct {
	TraceID           string            `json:"trace_id"`
	RequestID         string            `json:"bkn.request.id"`
	ConclusionScope   string            `json:"conclusion_scope"`
	Partial           bool              `json:"partial"`
	PartialReasons    []string          `json:"partial_reason"`
	VisibilitySummary VisibilitySummary `json:"visibility_summary"`
	Page              EvidencePage      `json:"page"`
	Data              BusinessGraphData `json:"data"`
}

type EvidenceNodeResponse struct {
	TraceID       string         `json:"trace_id"`
	RequestID     string         `json:"bkn.request.id"`
	NodeID        string         `json:"node_id"`
	NodeType      string         `json:"node_type"`
	ClaimID       string         `json:"claim_id,omitempty"`
	Visibility    string         `json:"visibility"`
	VersionStatus string         `json:"version_status,omitempty"`
	Data          map[string]any `json:"data"`
}

type SnapshotPreviewResponse struct {
	TraceID           string            `json:"trace_id"`
	RequestID         string            `json:"bkn.request.id"`
	Partial           bool              `json:"partial"`
	PartialReasons    []string          `json:"partial_reason"`
	VisibilitySummary VisibilitySummary `json:"visibility_summary"`
	SnapshotRef       SnapshotRef       `json:"snapshot_ref"`
	Manifest          SnapshotManifest  `json:"manifest"`
}

type SnapshotRef struct {
	SnapshotID string `json:"snapshot_id"`
	Mode       string `json:"mode"`
	URI        string `json:"uri,omitempty"`
}

type SnapshotManifest struct {
	SchemaVersion     string            `json:"schema_version"`
	Producer          string            `json:"producer"`
	TraceID           string            `json:"trace_id"`
	RequestID         string            `json:"bkn.request.id"`
	ArtifactCount     int               `json:"artifact_count"`
	ClaimCount        int               `json:"claim_count"`
	EvidenceRefCount  int               `json:"evidence_ref_count"`
	BusinessRefCount  int               `json:"business_ref_count"`
	VisibilitySummary VisibilitySummary `json:"visibility_summary"`
	ComplianceStatus  string            `json:"compliance_status"`
	DLPClassification string            `json:"dlp_classification"`
	RetentionPolicy   string            `json:"retention_policy"`
	LegalHold         string            `json:"legal_hold"`
	SignatureStatus   string            `json:"signature_status"`
	ArtifactHash      string            `json:"artifact_hash"`
	ManifestHash      string            `json:"manifest_hash"`
}

type BusinessGraphData struct {
	Nodes []BusinessGraphNode `json:"nodes"`
	Edges []BusinessGraphEdge `json:"edges"`
}

type BusinessGraphNode struct {
	ID            string           `json:"id"`
	NodeType      string           `json:"node_type"`
	Stage         string           `json:"stage,omitempty"`
	Label         string           `json:"label,omitempty"`
	EventID       string           `json:"event_id,omitempty"`
	InteractionID string           `json:"interaction_id,omitempty"`
	OperationID   string           `json:"operation_id,omitempty"`
	ClaimID       string           `json:"claim_id,omitempty"`
	ActionID      string           `json:"action_instance_id,omitempty"`
	VersionStatus string           `json:"version_status,omitempty"`
	Visibility    string           `json:"visibility,omitempty"`
	Display       *BusinessDisplay `json:"display,omitempty"`
	Properties    map[string]any   `json:"properties,omitempty"`
}

type BusinessDisplay struct {
	Name              string   `json:"name"`
	BusinessPath      []string `json:"business_path,omitempty"`
	ControlledSummary string   `json:"controlled_summary,omitempty"`
	ResolutionStatus  string   `json:"resolution_status"`
	SourceVersion     string   `json:"source_version,omitempty"`
}

type BusinessGraphEdge struct {
	ID         string `json:"id"`
	SourceID   string `json:"source_id"`
	TargetID   string `json:"target_id"`
	EdgeType   string `json:"edge_type"`
	Visibility string `json:"visibility,omitempty"`
}

type IngestResponse struct {
	TraceID          string `json:"trace_id"`
	RequestID        string `json:"bkn.request.id"`
	SchemaVersion    string `json:"bkn.trace.schema.version"`
	AcceptedEvents   int    `json:"accepted_event_count"`
	ClaimCount       int    `json:"claim_count"`
	EvidenceRefCount int    `json:"evidence_ref_count"`
	BusinessRefCount int    `json:"business_ref_count"`
}

func NewValidationError(code, path, message string) ValidationError {
	return ValidationError{Code: code, Path: path, Message: message}
}

// NewTemplatedValidationError preserves the rendered English message while
// retaining a stable localization key and runtime template data internally.
func NewTemplatedValidationError(
	code, path, messageID, message string,
	messageData map[string]any,
) ValidationError {
	return ValidationError{
		Code: code, Path: path, Message: message,
		messageID: messageID, messageData: messageData,
	}
}

// LocalizableMessage returns the stable localization input for this error.
func (e ValidationError) LocalizableMessage() (string, map[string]any) {
	if e.messageID == "" {
		return e.Message, nil
	}
	return e.messageID, e.messageData
}
