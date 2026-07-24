package evidencevo

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
	Payload       map[string]any `json:"payload"`
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

type EvidenceQueryOptions struct {
	Limit int
}

type EvidenceQueryResult struct {
	Traces    []NormalizedTrace
	Truncated bool
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
	Claims       []map[string]any `json:"claims"`
	EvidenceRefs []map[string]any `json:"evidence_refs"`
	BusinessRefs []map[string]any `json:"business_refs"`
}

type BusinessGraphResponse struct {
	TraceID           string            `json:"trace_id"`
	RequestID         string            `json:"bkn.request.id"`
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
	ID            string         `json:"id"`
	NodeType      string         `json:"node_type"`
	Label         string         `json:"label,omitempty"`
	ClaimID       string         `json:"claim_id,omitempty"`
	VersionStatus string         `json:"version_status,omitempty"`
	Visibility    string         `json:"visibility,omitempty"`
	Properties    map[string]any `json:"properties,omitempty"`
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
