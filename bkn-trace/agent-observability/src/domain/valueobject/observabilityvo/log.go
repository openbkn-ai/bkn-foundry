package observabilityvo

import "time"

type ResourceRef struct {
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Version      string `json:"version,omitempty"`
}

type LogRecord struct {
	EventID             string          `json:"event_id,omitempty"`
	EventTime           time.Time       `json:"event_time,omitempty"`
	RecordedAt          time.Time       `json:"recorded_at,omitempty"`
	ActorNameSnapshot   string          `json:"actor_name_snapshot,omitempty"`
	ActorType           string          `json:"actor_type,omitempty"`
	AuthMethod          string          `json:"auth_method,omitempty"`
	CredentialID        string          `json:"credential_id,omitempty"`
	CredentialName      string          `json:"credential_name,omitempty"`
	SourceChannel       string          `json:"source_channel,omitempty"`
	BusinessModule      string          `json:"module,omitempty"`
	Action              string          `json:"action,omitempty"`
	TargetType          string          `json:"target_type,omitempty"`
	TargetID            string          `json:"target_id,omitempty"`
	TargetNameSnapshot  string          `json:"target_name_snapshot,omitempty"`
	FailureCode         string          `json:"failure_code,omitempty"`
	FailureMessage      string          `json:"failure_message,omitempty"`
	TaskID              string          `json:"task_id,omitempty"`
	SchemaVersion       string          `json:"schema_version"`
	LogID               string          `json:"log_id"`
	SourceID            string          `json:"source_id"`
	SourceLogID         string          `json:"source_log_id"`
	Category            string          `json:"log_category"`
	EventName           string          `json:"event_name"`
	EventTimestamp      time.Time       `json:"event_timestamp"`
	ObservedTimestamp   time.Time       `json:"observed_timestamp"`
	SeverityNumber      int             `json:"severity_number"`
	SeverityText        string          `json:"severity_text"`
	Outcome             string          `json:"outcome"`
	SafeSummary         string          `json:"safe_summary"`
	ServiceName         string          `json:"service_name"`
	Environment         string          `json:"deployment_environment"`
	TenantID            string          `json:"tenant_id"`
	BusinessDomain      string          `json:"business_domain_id,omitempty"`
	ActorID             string          `json:"actor_id,omitempty"`
	EffectiveSubjectID  string          `json:"effective_subject_id,omitempty"`
	ApplicationID       string          `json:"application_id,omitempty"`
	IngressPrincipal    string          `json:"ingress_principal"`
	TrustLevel          string          `json:"trust_level"`
	RequestID           string          `json:"request_id,omitempty"`
	TraceID             string          `json:"trace_id,omitempty"`
	SpanID              string          `json:"span_id,omitempty"`
	ConversationID      string          `json:"conversation_id,omitempty"`
	InteractionID       string          `json:"interaction_id,omitempty"`
	OperationID         string          `json:"operation_id,omitempty"`
	ToolName            string          `json:"tool_name,omitempty"`
	ResourceRef         *ResourceRef    `json:"resource_ref,omitempty"`
	ArtifactRef         string          `json:"artifact_ref,omitempty"`
	Attributes          map[string]any  `json:"attributes,omitempty"`
	KnowledgeNetworkIDs []string        `json:"-"`
	CursorPosition      *SourcePosition `json:"-"`
}

type LogQuery struct {
	Query            string
	TimeFrom         *time.Time
	TimeTo           *time.Time
	BusinessModule   string
	Action           string
	TargetType       string
	TargetID         string
	Outcomes         []string
	Categories       []string
	SeverityMinimum  int
	Services         []string
	Environments     []string
	EventNames       []string
	BusinessDomain   string
	ActorID          string
	ApplicationID    string
	ResourceType     string
	ResourceID       string
	ConversationID   string
	InteractionID    string
	OperationID      string
	RequestID        string
	TraceID          string
	SpanID           string
	FailedOnly       bool
	Limit            int
	Page             int
	Cursor           string
	ScopeFingerprint string

	// Trusted authorization scope is derived server-side from the Access Profile.
	// Source adapters use it to push isolation filters into their native query.
	AuthorizedTenantID            string
	AuthorizedBusinessDomain      string
	AuthorizedSubjectID           string
	AuthorizedApplicationID       string
	AuthorizedCategories          []string
	AuthorizedKnowledgeNetworkIDs []string
	RequireRecordScope            bool
	PageBefore                    *SourcePosition
	ObservedBefore                *time.Time
}

func (query LogQuery) IsAssociatedDrilldown() bool {
	return query.ConversationID != "" || query.InteractionID != "" || query.OperationID != "" ||
		query.RequestID != "" || query.TraceID != "" || query.SpanID != ""
}

type SourcePage struct {
	Records []LogRecord
	// LastPosition advances the source cursor even when the adapter filters all
	// raw records on this page. It is cursor metadata, never a public log record.
	LastPosition  *SourcePosition
	NextCursor    string
	Count         int64
	CountAccuracy string
	Watermark     string
}

// SourcePosition is the last record consumed from one source. Adapters use it
// as a strict keyset boundary; it is never accepted directly from callers.
type SourcePosition struct {
	EventTimestamp time.Time `json:"event_timestamp"`
	SourceID       string    `json:"source_id,omitempty"`
	LogID          string    `json:"log_id"`
	SearchAfter    []any     `json:"search_after,omitempty"`
}

type SourceStatus struct {
	SourceID         string     `json:"source_id"`
	Status           string     `json:"status"`
	Reason           string     `json:"reason,omitempty"`
	Reliability      string     `json:"reliability"`
	LastReceivedAt   *time.Time `json:"last_received_at"`
	Watermark        *string    `json:"watermark"`
	LatencyMS        *int64     `json:"latency_ms"`
	CollectionMethod string     `json:"collection_method,omitempty"`
	CoveredModules   []string   `json:"covered_modules,omitempty"`
	DroppedRecords   *int64     `json:"dropped_records,omitempty"`
	SamplingRate     *float64   `json:"sampling_rate,omitempty"`
	SampledRecords   *int64     `json:"sampled_records,omitempty"`
	CountAccuracy    string     `json:"count_accuracy,omitempty"`
	Categories       []string   `json:"-"`
}

type ListResult struct {
	Records      []LogRecord
	NextCursor   string
	Page         int
	PageSize     int
	Partial      bool
	Count        int64
	CountExact   bool
	SourceStatus []SourceStatus
}

type FacetValue struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}

type FacetResult struct {
	Values         []FacetValue
	Partial        bool
	SampledRecords int
	SourceStatus   []SourceStatus
}

type LogPolicy struct {
	Scope            map[string]string `json:"scope"`
	PolicyRevision   string            `json:"policy_revision"`
	Category         string            `json:"category"`
	RetentionDays    int               `json:"retention_days"`
	PolicyKind       string            `json:"policy_kind"`
	LegalHold        bool              `json:"legal_hold"`
	StorageTargetRef string            `json:"storage_target_ref,omitempty"`
}
