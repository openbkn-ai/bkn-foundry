package observabilityvo

import "time"

type ResourceRef struct {
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Version      string `json:"version,omitempty"`
}

type LogRecord struct {
	SchemaVersion       string         `json:"schema_version"`
	LogID               string         `json:"log_id"`
	SourceID            string         `json:"source_id"`
	SourceLogID         string         `json:"source_log_id"`
	Category            string         `json:"log_category"`
	EventName           string         `json:"event_name"`
	EventTimestamp      time.Time      `json:"event_timestamp"`
	ObservedTimestamp   time.Time      `json:"observed_timestamp"`
	SeverityNumber      int            `json:"severity_number"`
	SeverityText        string         `json:"severity_text"`
	Outcome             string         `json:"outcome"`
	SafeSummary         string         `json:"safe_summary"`
	ServiceName         string         `json:"service_name"`
	Environment         string         `json:"deployment_environment"`
	TenantID            string         `json:"tenant_id"`
	BusinessDomain      string         `json:"business_domain_id,omitempty"`
	ActorID             string         `json:"actor_id,omitempty"`
	EffectiveSubjectID  string         `json:"effective_subject_id,omitempty"`
	ApplicationID       string         `json:"application_id,omitempty"`
	IngressPrincipal    string         `json:"ingress_principal"`
	TrustLevel          string         `json:"trust_level"`
	RequestID           string         `json:"request_id,omitempty"`
	TraceID             string         `json:"trace_id,omitempty"`
	SpanID              string         `json:"span_id,omitempty"`
	ConversationID      string         `json:"conversation_id,omitempty"`
	InteractionID       string         `json:"interaction_id,omitempty"`
	OperationID         string         `json:"operation_id,omitempty"`
	ResourceRef         *ResourceRef   `json:"resource_ref,omitempty"`
	ArtifactRef         string         `json:"artifact_ref,omitempty"`
	Attributes          map[string]any `json:"attributes,omitempty"`
	KnowledgeNetworkIDs []string       `json:"-"`
}

type LogQuery struct {
	Query            string
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
	Cursor           string
	ScopeFingerprint string
}

func (query LogQuery) IsAssociatedDrilldown() bool {
	return query.ConversationID != "" || query.InteractionID != "" || query.OperationID != "" ||
		query.RequestID != "" || query.TraceID != "" || query.SpanID != ""
}

type SourcePage struct {
	Records       []LogRecord
	NextCursor    string
	Count         int64
	CountAccuracy string
	Watermark     string
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
}

type ListResult struct {
	Records      []LogRecord
	NextCursor   string
	Partial      bool
	Count        int64
	CountExact   bool
	SourceStatus []SourceStatus
}
