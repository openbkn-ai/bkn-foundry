// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

// QueryMetricReq selects an immutable published object-metric version. Query
// contains only runtime choices allowed by that version's query capabilities.
type QueryMetricReq struct {
	KnID     string            `json:"kn_id" form:"kn_id"`
	Branch   string            `json:"branch,omitempty" form:"branch"`
	MetricID string            `json:"metric_id" form:"metric_id"`
	Version  int               `json:"version" form:"version"`
	Query    ObjectMetricQuery `json:"query"`
}

type ObjectMetricQuery struct {
	Filter          *ObjectMetricCondition       `json:"filter,omitempty"`
	GroupBy         []ObjectMetricPropertyRef    `json:"group_by,omitempty"`
	Having          *ObjectMetricHaving          `json:"having,omitempty"`
	OrderBy         []ObjectMetricOrderBy        `json:"order_by,omitempty"`
	PageSize        int                          `json:"page_size,omitempty"`
	Cursor          string                       `json:"cursor,omitempty"`
	Time            *ObjectMetricTimeRange       `json:"time,omitempty"`
	BucketSelection *ObjectMetricBucketSelection `json:"bucket_selection,omitempty"`
}

// ObjectMetricOperand is one side of a runtime filter predicate. Property
// operands must reference a dimension declared by the published metric;
// literal operands carry their physical value type explicitly.
type ObjectMetricOperand struct {
	Source      string                   `json:"source"`
	PropertyRef *ObjectMetricPropertyRef `json:"property_ref,omitempty"`
	ValueType   string                   `json:"value_type,omitempty"`
	Value       any                      `json:"value,omitempty"`
}

// ObjectMetricCondition mirrors ontology-query's recursive Object Metric V1
// condition contract. A condition is either a predicate or a boolean group.
type ObjectMetricCondition struct {
	NodeType           string                  `json:"node_type"`
	BooleanOperator    string                  `json:"boolean_operator,omitempty"`
	Children           []ObjectMetricCondition `json:"children,omitempty"`
	Left               *ObjectMetricOperand    `json:"left,omitempty"`
	ComparisonOperator string                  `json:"comparison_operator,omitempty"`
	Right              []ObjectMetricOperand   `json:"right,omitempty"`
}

type ObjectMetricHaving struct {
	Operator string `json:"operator"`
	Value    any    `json:"value,omitempty"`
	UnitCode string `json:"unit_code,omitempty"`
}

type ObjectMetricOrderBy struct {
	Field     string                   `json:"field"`
	Dimension *ObjectMetricPropertyRef `json:"dimension,omitempty"`
	Direction string                   `json:"direction"`
}

type ObjectMetricBucketSelection struct {
	ParentQuery ObjectMetricQuery `json:"parent_query"`
	BucketKey   string            `json:"bucket_key"`
}

type ObjectMetricPropertyRef struct {
	ObjectTypeID      string `json:"object_type_id"`
	PropertyName      string `json:"property_name"`
	RelationPathAlias string `json:"relation_path_alias,omitempty"`
}

type ObjectMetricTimeRange struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

// QueryMetricResp carries the V1 result status, entries, and model.
type QueryMetricResp struct {
	KnID            string                  `json:"kn_id"`
	MetricID        string                  `json:"metric_id"`
	Version         int                     `json:"version"`
	Model           map[string]any          `json:"model"`
	Entries         []ObjectMetricDataEntry `json:"entries"`
	Status          string                  `json:"status"`
	EmptyReason     string                  `json:"empty_reason,omitempty"`
	TotalCount      int                     `json:"total_count"`
	NextCursor      string                  `json:"next_cursor,omitempty"`
	Warnings        []string                `json:"warnings,omitempty"`
	OverallMs       int64                   `json:"overall_ms"`
	EvaluatedAt     int64                   `json:"evaluated_at"`
	EffectiveWindow *ObjectMetricTimeRange  `json:"effective_window,omitempty"`
}

type ObjectMetricDataEntry struct {
	Time             *int64                      `json:"time,omitempty"`
	Value            any                         `json:"value,omitempty"`
	Status           string                      `json:"status"`
	DimensionCells   []ObjectMetricDimensionCell `json:"dimension_cells,omitempty"`
	TimeBucket       *ObjectMetricTimeRange      `json:"time_bucket,omitempty"`
	BucketKey        string                      `json:"bucket_key,omitempty"`
	AvailableActions []string                    `json:"available_actions,omitempty"`
}

type ObjectMetricDimensionCell struct {
	Ref   ObjectMetricPropertyRef `json:"ref"`
	Value any                     `json:"value"`
}

// MetricQueryDownstreamResp mirrors ontology-query's ObjectMetricData V1.
type MetricQueryDownstreamResp struct {
	Model           map[string]any          `json:"model"`
	Entries         []ObjectMetricDataEntry `json:"entries"`
	Status          string                  `json:"status"`
	EmptyReason     string                  `json:"empty_reason,omitempty"`
	TotalCount      int                     `json:"total_count"`
	NextCursor      string                  `json:"next_cursor,omitempty"`
	Warnings        []string                `json:"warnings,omitempty"`
	OverallMs       int64                   `json:"overall_ms"`
	EvaluatedAt     int64                   `json:"evaluated_at"`
	EffectiveWindow *ObjectMetricTimeRange  `json:"effective_window,omitempty"`
}

type MetricDrillThroughReq struct {
	KnID      string            `json:"kn_id"`
	Branch    string            `json:"branch,omitempty"`
	MetricID  string            `json:"metric_id"`
	Version   int               `json:"version"`
	Query     ObjectMetricQuery `json:"query"`
	BucketKey string            `json:"bucket_key"`
	PageSize  int               `json:"page_size,omitempty"`
	Cursor    string            `json:"cursor,omitempty"`
}

type ObjectMetricParticipant struct {
	InstanceIdentity map[string]any `json:"instance_identity"`
	Role             string         `json:"role"`
}

type MetricDrillThroughResp struct {
	KnID         string                    `json:"kn_id"`
	MetricID     string                    `json:"metric_id"`
	Version      int                       `json:"version"`
	Participants []ObjectMetricParticipant `json:"participants"`
	NextCursor   string                    `json:"next_cursor,omitempty"`
	BucketValue  any                       `json:"bucket_value,omitempty"`
	EvaluatedAt  int64                     `json:"evaluated_at"`
	Status       string                    `json:"status"`
	Warnings     []string                  `json:"warnings,omitempty"`
}
