package oteltracevo

type TraceData struct {
	ResourceSpans []ResourceSpan `json:"resourceSpans"`
}

type TraceGraphResponse struct {
	TraceID        string         `json:"trace_id"`
	Status         string         `json:"status"`
	DurationNano   int64          `json:"duration_nano"`
	Partial        bool           `json:"partial"`
	PartialReasons []string       `json:"partial_reason"`
	Page           TraceGraphPage `json:"page"`
	Data           TraceGraphData `json:"data"`
}

type TraceGraphPage struct {
	NodeCount int  `json:"node_count"`
	EdgeCount int  `json:"edge_count"`
	Truncated bool `json:"truncated"`
}

type TraceGraphData struct {
	Nodes []TraceGraphNode `json:"nodes"`
	Edges []TraceGraphEdge `json:"edges"`
}

type TraceGraphNode struct {
	SpanID       string `json:"span_id"`
	ParentSpanID string `json:"parent_span_id,omitempty"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	ServiceName  string `json:"service_name,omitempty"`
	Status       string `json:"status"`
	ErrorMessage string `json:"error_message,omitempty"`
	StartNano    int64  `json:"start_nano"`
	EndNano      int64  `json:"end_nano"`
	DurationNano int64  `json:"duration_nano"`
}

type TraceGraphEdge struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_span_id"`
	ChildID  string `json:"child_span_id"`
	EdgeType string `json:"edge_type"`
}

type ResourceSpan struct {
	Resource   Resource    `json:"resource"`
	ScopeSpans []ScopeSpan `json:"scopeSpans"`
}

type Resource struct {
	Attributes []Attribute `json:"attributes"`
}

type ScopeSpan struct {
	Scope Scope  `json:"scope"`
	Spans []Span `json:"spans"`
}

type Scope struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type Span struct {
	TraceID           string      `json:"traceId"`
	SpanID            string      `json:"spanId"`
	ParentSpanID      string      `json:"parentSpanId,omitempty"`
	Name              string      `json:"name"`
	Kind              string      `json:"kind"`
	StartTimeUnixNano string      `json:"startTimeUnixNano"`
	EndTimeUnixNano   string      `json:"endTimeUnixNano"`
	Attributes        []Attribute `json:"attributes,omitempty"`
	Events            []Event     `json:"events,omitempty"`
	Status            Status      `json:"status"`
}

type Attribute struct {
	Key   string         `json:"key"`
	Value AttributeValue `json:"value"`
}

type AttributeValue struct {
	StringValue string  `json:"stringValue,omitempty"`
	IntValue    string  `json:"intValue,omitempty"`
	BoolValue   bool    `json:"boolValue,omitempty"`
	DoubleValue float64 `json:"doubleValue,omitempty"`
}

type Event struct {
	Name         string      `json:"name"`
	TimeUnixNano string      `json:"timeUnixNano"`
	Attributes   []Attribute `json:"attributes,omitempty"`
}

type Status struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}
