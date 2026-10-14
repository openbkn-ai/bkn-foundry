// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

// ObjectMetricQueryRequestV1 contains only runtime choices. Fixed business
// qualifiers and fixed statistical grain always come from the published metric
// version and cannot be overridden by a caller.
type ObjectMetricQueryRequestV1 struct {
	InstanceIdentities []map[string]any              `json:"instance_identities,omitempty"`
	Filter             *ObjectMetricConditionV1      `json:"filter,omitempty"`
	AnalysisDimensions []ObjectMetricPropertyRefV1   `json:"analysis_dimensions,omitempty"`
	Time               *ObjectMetricQueryTimeRangeV1 `json:"time,omitempty"`
	// RuntimeAnchor and WindowOverride are execution-plan fields. They are never
	// accepted from an HTTP request and are only populated while evaluating a
	// version-pinned composite dependency.
	RuntimeAnchor  *int64                    `json:"-"`
	WindowOverride *ObjectMetricTimeWindowV1 `json:"-"`
}

// ObjectMetricTrialRequestV1 evaluates a caller-supplied definition against
// real data without persisting or publishing it. Derived and composite trials
// resolve their immutable, version-pinned dependencies from published metrics.
type ObjectMetricTrialRequestV1 struct {
	Definition ObjectMetricDefinitionV1   `json:"definition"`
	Query      ObjectMetricQueryRequestV1 `json:"query"`
}

type ObjectMetricQueryTimeRangeV1 struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

type ObjectMetricDataV1 struct {
	Model     ObjectMetricResultModelV1 `json:"model"`
	Entries   []ObjectMetricDataEntryV1 `json:"entries"`
	Status    string                    `json:"status"`
	Warnings  []string                  `json:"warnings,omitempty"`
	OverallMs int64                     `json:"overall_ms"`
}

type ObjectMetricResultModelV1 struct {
	MetricID        string                       `json:"metric_id"`
	Version         int                          `json:"version"`
	Name            string                       `json:"name"`
	OwnerObjectType string                       `json:"owner_object_type_id"`
	OutputShape     string                       `json:"output_shape"`
	Value           ObjectMetricValueSemanticsV1 `json:"value"`
}

type ObjectMetricDataEntryV1 struct {
	InstanceIdentity map[string]any `json:"instance_identity,omitempty"`
	Dimensions       map[string]any `json:"dimensions,omitempty"`
	Time             *int64         `json:"time,omitempty"`
	Value            any            `json:"value,omitempty"`
	Status           string         `json:"status"`
}

// ObjectMetricExecutionContextV1 is the immutable, version-pinned metric graph
// returned by bkn-backend. ontology-query never resolves an unversioned metric
// at execution time.
type ObjectMetricExecutionContextV1 struct {
	Root         *ObjectMetricRecordV1   `json:"root"`
	Dependencies []*ObjectMetricRecordV1 `json:"dependencies"`
}

type ObjectMetricRecordV1 struct {
	Definition ObjectMetricDefinitionV1 `json:"definition"`
	KNID       string                   `json:"kn_id"`
	Branch     string                   `json:"branch"`
}

type ObjectMetricDefinitionV1 struct {
	ID                string                      `json:"id"`
	Version           int                         `json:"version"`
	Code              string                      `json:"code"`
	Name              string                      `json:"name"`
	OwnerObjectTypeID string                      `json:"owner_object_type_id"`
	MetricType        string                      `json:"metric_type"`
	CalculationScope  string                      `json:"calculation_scope"`
	ResultSemantics   ObjectMetricResultSemantics `json:"result_semantics"`
	Specification     ObjectMetricSpecificationV1 `json:"specification"`
	QueryCapabilities ObjectMetricQueryCapability `json:"query_capabilities"`
	Lifecycle         ObjectMetricLifecycleV1     `json:"lifecycle"`
}

type ObjectMetricPropertyRefV1 struct {
	ObjectTypeID      string `json:"object_type_id"`
	PropertyName      string `json:"property_name"`
	RelationPathAlias string `json:"relation_path_alias,omitempty"`
}

type ObjectMetricRefV1 struct {
	MetricID string `json:"metric_id"`
	Version  int    `json:"version"`
}

type ObjectMetricExpressionV1 struct {
	NodeType        string                     `json:"node_type"`
	PropertyRef     *ObjectMetricPropertyRefV1 `json:"property_ref,omitempty"`
	DependencyAlias string                     `json:"dependency_alias,omitempty"`
	Value           any                        `json:"value,omitempty"`
	ValueType       string                     `json:"value_type,omitempty"`
	Function        string                     `json:"function,omitempty"`
	Operator        string                     `json:"operator,omitempty"`
	Arguments       []ObjectMetricExpressionV1 `json:"arguments,omitempty"`
	Left            *ObjectMetricExpressionV1  `json:"left,omitempty"`
	Right           *ObjectMetricExpressionV1  `json:"right,omitempty"`
}

type ObjectMetricOperandV1 struct {
	Source      string                     `json:"source"`
	PropertyRef *ObjectMetricPropertyRefV1 `json:"property_ref,omitempty"`
	ValueType   string                     `json:"value_type,omitempty"`
	Value       any                        `json:"value,omitempty"`
}

type ObjectMetricConditionV1 struct {
	NodeType           string                    `json:"node_type"`
	BooleanOperator    string                    `json:"boolean_operator,omitempty"`
	Children           []ObjectMetricConditionV1 `json:"children,omitempty"`
	Left               *ObjectMetricOperandV1    `json:"left,omitempty"`
	ComparisonOperator string                    `json:"comparison_operator,omitempty"`
	Right              []ObjectMetricOperandV1   `json:"right,omitempty"`
}

type ObjectMetricMeasureV1 struct {
	Expression  *ObjectMetricExpressionV1 `json:"expression,omitempty"`
	Aggregation string                    `json:"aggregation"`
}

type ObjectMetricBusinessQualifiersV1 struct {
	Filters      *ObjectMetricConditionV1    `json:"filters,omitempty"`
	RelationPath *ObjectMetricRelationPathV1 `json:"relation_path,omitempty"`
	EventTime    *ObjectMetricPropertyRefV1  `json:"event_time,omitempty"`
	TimeWindow   *ObjectMetricTimeWindowV1   `json:"time_window,omitempty"`
}

type ObjectMetricRelationPathV1 struct {
	Alias             string                       `json:"alias"`
	Steps             []ObjectMetricRelationStepV1 `json:"steps"`
	Purpose           string                       `json:"purpose"`
	Quantifier        string                       `json:"quantifier,omitempty"`
	RelatedFilters    *ObjectMetricConditionV1     `json:"related_filters,omitempty"`
	CardinalityPolicy string                       `json:"cardinality_policy"`
	CountComparison   string                       `json:"count_comparison,omitempty"`
	CountThreshold    *int                         `json:"count_threshold,omitempty"`
}

type ObjectMetricRelationStepV1 struct {
	RelationTypeID     string `json:"relation_type_id"`
	Direction          string `json:"direction"`
	TargetObjectTypeID string `json:"target_object_type_id"`
}

type ObjectMetricTimeWindowV1 struct {
	Mode      string `json:"mode"`
	Length    int    `json:"length,omitempty"`
	Unit      string `json:"unit,omitempty"`
	Period    string `json:"period,omitempty"`
	StartAt   int64  `json:"start_at,omitempty"`
	EndAt     int64  `json:"end_at,omitempty"`
	Timezone  string `json:"timezone,omitempty"`
	WeekStart string `json:"week_start,omitempty"`
}

type ObjectMetricDependencyV1 struct {
	Alias             string                          `json:"alias"`
	MetricRef         ObjectMetricRefV1               `json:"metric_ref"`
	ObjectAlignment   ObjectMetricObjectAlignmentV1   `json:"object_alignment"`
	EvaluationContext ObjectMetricEvaluationContextV1 `json:"evaluation_context"`
	RollupAggregation string                          `json:"rollup_aggregation,omitempty"`
}

type ObjectMetricObjectAlignmentV1 struct {
	RelationPath            []ObjectMetricRelationStepV1 `json:"relation_path,omitempty"`
	Mode                    string                       `json:"mode"`
	RelationshipCardinality string                       `json:"relationship_cardinality,omitempty"`
	Deduplication           string                       `json:"deduplication"`
}

type ObjectMetricEvaluationContextV1 struct {
	TimeShift     *ObjectMetricTimeShiftV1    `json:"time_shift,omitempty"`
	Window        string                      `json:"window"`
	WindowSpec    *ObjectMetricTimeWindowV1   `json:"window_spec,omitempty"`
	FilterContext ObjectMetricFilterContextV1 `json:"filter_context"`
}

type ObjectMetricTimeShiftV1 struct {
	Amount int    `json:"amount"`
	Unit   string `json:"unit"`
}

type ObjectMetricFilterContextV1 struct {
	Mode      string                     `json:"mode"`
	Dimension *ObjectMetricPropertyRefV1 `json:"dimension,omitempty"`
}

type ObjectMetricAlignmentV1 struct {
	ResultOwnerObjectTypeID string                      `json:"result_owner_object_type_id"`
	GroupByDimensions       []ObjectMetricPropertyRefV1 `json:"group_by_dimensions"`
	GrainPolicy             string                      `json:"grain_policy"`
	ResultSetPolicy         string                      `json:"result_set_policy"`
	PrimaryDependencyAlias  string                      `json:"primary_dependency_alias,omitempty"`
	TimeAlignment           string                      `json:"time_alignment"`
	TimeKey                 *ObjectMetricPropertyRefV1  `json:"time_key,omitempty"`
}

type ObjectMetricSpecificationV1 struct {
	Kind               string                            `json:"kind"`
	Measure            *ObjectMetricMeasureV1            `json:"measure,omitempty"`
	BaseMetric         *ObjectMetricRefV1                `json:"base_metric,omitempty"`
	BusinessQualifiers *ObjectMetricBusinessQualifiersV1 `json:"business_qualifiers,omitempty"`
	Dependencies       []ObjectMetricDependencyV1        `json:"dependencies,omitempty"`
	Expression         *ObjectMetricExpressionV1         `json:"expression,omitempty"`
	Alignment          *ObjectMetricAlignmentV1          `json:"alignment,omitempty"`
	ZeroDivisionPolicy string                            `json:"zero_division_policy,omitempty"`
	NullPolicy         string                            `json:"null_policy,omitempty"`
}

type ObjectMetricStatisticalGrainV1 struct {
	GroupByDimensions []ObjectMetricPropertyRefV1 `json:"group_by_dimensions"`
	TimeGranularity   string                      `json:"time_granularity,omitempty"`
	Timezone          string                      `json:"timezone,omitempty"`
	WeekStart         string                      `json:"week_start,omitempty"`
}

type ObjectMetricResultSemantics struct {
	StatisticalGrain  ObjectMetricStatisticalGrainV1 `json:"statistical_grain"`
	Value             ObjectMetricValueSemanticsV1   `json:"value"`
	NoDataPolicy      string                         `json:"no_data_policy"`
	PartialDataPolicy string                         `json:"partial_data_policy"`
}

type ObjectMetricValueSemanticsV1 struct {
	PhysicalType string                  `json:"physical_type"`
	SemanticType string                  `json:"semantic_type"`
	Precision    *int                    `json:"precision,omitempty"`
	Scale        *int                    `json:"scale,omitempty"`
	RoundingMode string                  `json:"rounding_mode,omitempty"`
	Unit         *ObjectMetricUnitSpecV1 `json:"unit,omitempty"`
	Minimum      *float64                `json:"minimum,omitempty"`
	Maximum      *float64                `json:"maximum,omitempty"`
}

type ObjectMetricUnitSpecV1 struct {
	System        string                  `json:"system"`
	QuantityKind  string                  `json:"quantity_kind"`
	Code          string                  `json:"code"`
	DisplaySymbol string                  `json:"display_symbol,omitempty"`
	ScaleFactor   float64                 `json:"scale_factor"`
	Numerator     *ObjectMetricUnitSpecV1 `json:"numerator,omitempty"`
	Denominator   *ObjectMetricUnitSpecV1 `json:"denominator,omitempty"`
}

type ObjectMetricQueryCapability struct {
	AnalysisDimensions []ObjectMetricPropertyRefV1 `json:"analysis_dimensions"`
	TimeDimensions     []ObjectMetricPropertyRefV1 `json:"time_dimensions"`
}

type ObjectMetricLifecycleV1 struct {
	Status      string `json:"status"`
	PublishedAt string `json:"published_at,omitempty"`
}
