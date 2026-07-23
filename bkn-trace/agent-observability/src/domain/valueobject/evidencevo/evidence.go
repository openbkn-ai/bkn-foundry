package evidencevo

import "encoding/json"

const ContractVersion = "2.0.0"

type IngestRequest struct {
	SchemaVersion string          `json:"bkn.trace.schema.version"`
	Trace         TraceContext    `json:"trace"`
	Events        []EvidenceEvent `json:"events"`
}

type TraceContext struct {
	TraceID        string `json:"trace_id"`
	Traceparent    string `json:"traceparent"`
	RequestID      string `json:"bkn.request.id"`
	TenantID       string `json:"bkn.tenant.id,omitempty"`
	BusinessDomain string `json:"business_domain,omitempty"`
	AccountID      string `json:"bkn.account.id"`
	AccountType    string `json:"bkn.account.type"`
}

type EvidenceEvent struct {
	EventID       string          `json:"event_id"`
	EventType     string          `json:"event_type"`
	SchemaVersion string          `json:"bkn.trace.schema.version"`
	ObservedAt    string          `json:"observed_at"`
	EmittedAt     string          `json:"emitted_at"`
	Producer      string          `json:"producer_module"`
	TraceID       string          `json:"trace_id"`
	SpanID        string          `json:"span_id"`
	RequestID     string          `json:"bkn.request.id"`
	OperationName string          `json:"bkn.operation.name"`
	Payload       map[string]any  `json:"payload"`
	RawPayload    json.RawMessage `json:"-"`
}

type ValidationError struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

type ValidationErrors []ValidationError

func (e ValidationErrors) Error() string {
	if len(e) == 0 {
		return "validation failed"
	}
	return e[0].Code + ": " + e[0].Path
}

type NormalizedTrace struct {
	TraceID          string
	RequestID        string
	SchemaVersion    string
	Events           []EvidenceEvent
	ClaimIDs         []string
	AcceptedEvents   int
	ClaimCount       int
	EvidenceRefCount int
	BusinessRefCount int
}

type EvidenceChainResponse struct {
	TraceID           string            `json:"trace_id"`
	RequestID         string            `json:"bkn.request.id"`
	Partial           bool              `json:"partial"`
	PartialReasons    []string          `json:"partial_reason"`
	VisibilitySummary VisibilitySummary `json:"visibility_summary"`
	Page              EvidencePage      `json:"page"`
	Data              EvidenceChainData `json:"data"`
}

type VisibilitySummary struct {
	AuthorizedRefCount int `json:"authorized_ref_count"`
	RedactedRefCount   int `json:"redacted_ref_count"`
	HiddenRefCount     int `json:"hidden_ref_count"`
	OmittedRefCount    int `json:"omitted_ref_count"`
	UnresolvedRefCount int `json:"unresolved_ref_count"`
}

type EvidencePage struct {
	NextCursor *string `json:"next_cursor"`
	NodeCount  int     `json:"node_count"`
	EdgeCount  int     `json:"edge_count"`
	Truncated  bool    `json:"truncated"`
}

type EvidenceChainData struct {
	Claims       []map[string]any `json:"claims"`
	EvidenceRefs []map[string]any `json:"evidence_refs"`
	BusinessRefs []map[string]any `json:"business_refs"`
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
