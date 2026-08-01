package rdto

import "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"

type LogCount struct {
	Value    *int64 `json:"value"`
	Accuracy string `json:"accuracy"`
}

type RequestTraceContext struct {
	RequestID       *string  `json:"request_id"`
	CurrentTraceID  *string  `json:"current_trace_id,omitempty"`
	RelatedTraceIDs []string `json:"related_trace_ids"`
}

type LogListResponse struct {
	Data                []observabilityvo.LogRecord    `json:"data"`
	NextCursor          *string                        `json:"next_cursor"`
	Partial             bool                           `json:"partial"`
	Count               LogCount                       `json:"count"`
	SourceStatus        []observabilityvo.SourceStatus `json:"source_status"`
	RequestTraceContext RequestTraceContext            `json:"request_trace_context"`
}
