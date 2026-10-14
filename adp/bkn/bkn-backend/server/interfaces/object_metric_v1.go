// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "context"

const (
	ObjectMetricTypeAtomic    = "atomic"
	ObjectMetricTypeDerived   = "derived"
	ObjectMetricTypeComposite = "composite"

	ObjectMetricScopeInstance = "object_instance"
	ObjectMetricScopeSet      = "object_set"

	ObjectMetricOutputObjectValue = "object_value"
	ObjectMetricOutputSummary     = "summary"
	ObjectMetricOutputGrouped     = "grouped"
	ObjectMetricOutputTimeSeries  = "time_series"

	ObjectMetricAggregationIdentity      = "identity"
	ObjectMetricAggregationCountRows     = "count_rows"
	ObjectMetricAggregationCount         = "count"
	ObjectMetricAggregationCountDistinct = "count_distinct"
	ObjectMetricAggregationSum           = "sum"
	ObjectMetricAggregationAvg           = "avg"
	ObjectMetricAggregationMin           = "min"
	ObjectMetricAggregationMax           = "max"
)

// ObjectMetricDefinitionV1 is the versioned aggregate root of the object metric domain model.
// OutputShape is deliberately absent because it is derived from CalculationScope and StatisticalGrain.
type ObjectMetricDefinitionV1 struct {
	ID                string                       `json:"id"`
	Version           int                          `json:"version"`
	Code              string                       `json:"code"`
	Name              string                       `json:"name"`
	Description       string                       `json:"description"`
	OwnerObjectTypeID string                       `json:"owner_object_type_id"`
	MetricType        string                       `json:"metric_type"`
	CalculationScope  string                       `json:"calculation_scope"`
	ResultSemantics   ObjectMetricResultSemantics  `json:"result_semantics"`
	Specification     ObjectMetricSpecificationV1  `json:"specification"`
	QueryCapabilities ObjectMetricQueryCapability  `json:"query_capabilities"`
	LogicProperty     *ObjectMetricLogicPropertyV1 `json:"logic_property,omitempty"`
	Lifecycle         ObjectMetricLifecycleV1      `json:"lifecycle"`
}

// ObjectMetricLogicPropertyV1 stores the authoring intent for exposing an
// object-value metric as a logical property after the metric is published.
type ObjectMetricLogicPropertyV1 struct {
	PropertyName string `json:"property_name"`
	DisplayName  string `json:"display_name"`
	Comment      string `json:"comment,omitempty"`
}

// ObjectMetricRecordV1 adds persistence metadata around an immutable metric definition version.
type ObjectMetricRecordV1 struct {
	Definition ObjectMetricDefinitionV1 `json:"definition"`
	KNID       string                   `json:"kn_id"`
	Branch     string                   `json:"branch"`
	Creator    AccountInfo              `json:"creator"`
	Updater    AccountInfo              `json:"updater"`
	CreateTime int64                    `json:"create_time"`
	UpdateTime int64                    `json:"update_time"`
}

type ObjectMetricListQueryV1 struct {
	KNID              string
	Branch            string
	OwnerObjectTypeID string
	MetricType        string
	Status            string
	Keyword           string
	Offset            int
	Limit             int
	MetricIDs         []string
}

type ObjectMetricListV1 struct {
	Entries    []*ObjectMetricRecordV1 `json:"entries"`
	TotalCount int                     `json:"total_count"`
}

// ObjectMetricValidationIssueV1 is stable validation output for UI, save, and publish flows.
type ObjectMetricValidationIssueV1 struct {
	Code    string `json:"code"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

type ObjectMetricValidationResultV1 struct {
	Valid       bool                            `json:"valid"`
	Issues      []ObjectMetricValidationIssueV1 `json:"issues"`
	OutputShape string                          `json:"output_shape"`
}

type ObjectMetricPreviewV1 struct {
	Validation   ObjectMetricValidationResultV1 `json:"validation"`
	OutputSchema ObjectMetricOutputSchemaV1     `json:"output_schema"`
}

// ObjectMetricExecutionContextV1 is the immutable definition closure consumed by
// ontology-query. Dependencies are pinned records and are ordered from direct to
// transitive dependencies. The root is never repeated in Dependencies.
type ObjectMetricExecutionContextV1 struct {
	Root         *ObjectMetricRecordV1   `json:"root"`
	Dependencies []*ObjectMetricRecordV1 `json:"dependencies"`
}

type ObjectMetricOutputSchemaV1 struct {
	Shape      string                       `json:"shape"`
	Dimensions []ObjectMetricPropertyRef    `json:"dimensions"`
	Value      ObjectMetricValueSemanticsV1 `json:"value"`
	TimeKey    *ObjectMetricPropertyRef     `json:"time_key,omitempty"`
}

type ObjectMetricCapabilitiesV1 struct {
	MetricTypes         []string          `json:"metric_types"`
	CalculationScopes   []string          `json:"calculation_scopes"`
	Aggregations        []string          `json:"aggregations"`
	ExpressionOperators []string          `json:"expression_operators"`
	Functions           []string          `json:"functions"`
	ComparisonOperators []string          `json:"comparison_operators"`
	BooleanOperators    []string          `json:"boolean_operators"`
	TimeWindowModes     []string          `json:"time_window_modes"`
	TimeGranularities   []string          `json:"time_granularities"`
	PhysicalTypes       []string          `json:"physical_types"`
	SemanticTypes       []string          `json:"semantic_types"`
	UnitSystems         []string          `json:"unit_systems"`
	QuantityKinds       []string          `json:"quantity_kinds"`
	LifecycleStatuses   []string          `json:"lifecycle_statuses"`
	RegistryVersions    map[string]string `json:"registry_versions"`
}

// ObjectMetricAccessV1 persists versioned object metric definitions.
type ObjectMetricAccessV1 interface {
	CreateObjectMetric(ctx context.Context, record *ObjectMetricRecordV1) error
	UpdateObjectMetricDraft(ctx context.Context, record *ObjectMetricRecordV1) error
	GetObjectMetric(ctx context.Context, knID, branch, metricID string, version int) (*ObjectMetricRecordV1, error)
	ListObjectMetrics(ctx context.Context, query ObjectMetricListQueryV1) ([]*ObjectMetricRecordV1, int, error)
	ListObjectMetricIDs(ctx context.Context, query ObjectMetricListQueryV1) ([]string, error)
	ListObjectMetricVersions(ctx context.Context, knID, branch, metricID string) ([]*ObjectMetricRecordV1, error)
	DeleteObjectMetricDraft(ctx context.Context, knID, branch, metricID string, version int) error
	PublishObjectMetric(ctx context.Context, record *ObjectMetricRecordV1) error
	DeprecateObjectMetric(ctx context.Context, record *ObjectMetricRecordV1) error
	NextObjectMetricVersion(ctx context.Context, knID, branch, metricID string) (int, error)
	ObjectMetricCodeExists(ctx context.Context, knID, branch, code, exceptID string) (bool, error)
}

// ObjectMetricServiceV1 owns draft, validation, version, and publish semantics.
type ObjectMetricServiceV1 interface {
	CreateDraft(ctx context.Context, knID, branch string, definition *ObjectMetricDefinitionV1) (*ObjectMetricRecordV1, error)
	UpdateDraft(ctx context.Context, knID, branch, metricID string, definition *ObjectMetricDefinitionV1) (*ObjectMetricRecordV1, error)
	Get(ctx context.Context, knID, branch, metricID string, version int) (*ObjectMetricRecordV1, error)
	List(ctx context.Context, query ObjectMetricListQueryV1) (*ObjectMetricListV1, error)
	ListVersions(ctx context.Context, knID, branch, metricID string) ([]*ObjectMetricRecordV1, error)
	DeleteDraft(ctx context.Context, knID, branch, metricID string, version int) error
	CreateNextDraft(ctx context.Context, knID, branch, metricID string) (*ObjectMetricRecordV1, error)
	Validate(ctx context.Context, knID, branch string, definition *ObjectMetricDefinitionV1) (ObjectMetricValidationResultV1, error)
	ValidateVersion(ctx context.Context, knID, branch, metricID string, version int) (*ObjectMetricRecordV1, []ObjectMetricValidationIssueV1, error)
	Preview(ctx context.Context, knID, branch string, definition *ObjectMetricDefinitionV1) (ObjectMetricPreviewV1, error)
	GetExecutionContext(ctx context.Context, knID, branch, metricID string, version int) (*ObjectMetricExecutionContextV1, error)
	Publish(ctx context.Context, knID, branch, metricID string, version int) (*ObjectMetricRecordV1, []ObjectMetricValidationIssueV1, error)
	Deprecate(ctx context.Context, knID, branch, metricID string, version int) (*ObjectMetricRecordV1, error)
	BindLogicProperty(ctx context.Context, knID, branch, metricID string, version int, binding *MetricPropertyBindingV1) (*LogicProperty, error)
	Capabilities() ObjectMetricCapabilitiesV1
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

type ObjectMetricPropertyRef struct {
	ObjectTypeID      string `json:"object_type_id"`
	PropertyName      string `json:"property_name"`
	RelationPathAlias string `json:"relation_path_alias,omitempty"`
}

type ObjectMetricRefV1 struct {
	MetricID string `json:"metric_id"`
	Version  int    `json:"version"`
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

type ObjectMetricUnitSpecV1 struct {
	System        string                  `json:"system"`
	QuantityKind  string                  `json:"quantity_kind"`
	Code          string                  `json:"code"`
	DisplaySymbol string                  `json:"display_symbol,omitempty"`
	ScaleFactor   float64                 `json:"scale_factor"`
	Numerator     *ObjectMetricUnitSpecV1 `json:"numerator,omitempty"`
	Denominator   *ObjectMetricUnitSpecV1 `json:"denominator,omitempty"`
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

type ObjectMetricStatisticalGrainV1 struct {
	GroupByDimensions []ObjectMetricPropertyRef `json:"group_by_dimensions"`
	TimeGranularity   string                    `json:"time_granularity,omitempty"`
	Timezone          string                    `json:"timezone,omitempty"`
	WeekStart         string                    `json:"week_start,omitempty"`
}

type ObjectMetricResultSemantics struct {
	StatisticalGrain  ObjectMetricStatisticalGrainV1 `json:"statistical_grain"`
	Value             ObjectMetricValueSemanticsV1   `json:"value"`
	NoDataPolicy      string                         `json:"no_data_policy"`
	PartialDataPolicy string                         `json:"partial_data_policy"`
}

type ObjectMetricQueryCapability struct {
	AnalysisDimensions []ObjectMetricPropertyRef `json:"analysis_dimensions"`
	TimeDimensions     []ObjectMetricPropertyRef `json:"time_dimensions"`
}

type ObjectMetricLifecycleV1 struct {
	Status                     string `json:"status"`
	OntologyVersion            string `json:"ontology_version"`
	ExpressionLanguageVersion  string `json:"expression_language_version"`
	FunctionRegistryVersion    string `json:"function_registry_version"`
	AggregationRegistryVersion string `json:"aggregation_registry_version"`
	UnitRegistryVersion        string `json:"unit_registry_version"`
	PublishedAt                string `json:"published_at,omitempty"`
}

type ObjectMetricExpressionV1 struct {
	NodeType        string                     `json:"node_type"`
	PropertyRef     *ObjectMetricPropertyRef   `json:"property_ref,omitempty"`
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
	Source      string                   `json:"source"`
	PropertyRef *ObjectMetricPropertyRef `json:"property_ref,omitempty"`
	ValueType   string                   `json:"value_type,omitempty"`
	Value       any                      `json:"value,omitempty"`
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
	EventTime    *ObjectMetricPropertyRef    `json:"event_time,omitempty"`
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
	Mode      string                   `json:"mode"`
	Dimension *ObjectMetricPropertyRef `json:"dimension,omitempty"`
}

type ObjectMetricDependencyV1 struct {
	Alias             string                          `json:"alias"`
	MetricRef         ObjectMetricRefV1               `json:"metric_ref"`
	ObjectAlignment   ObjectMetricObjectAlignmentV1   `json:"object_alignment"`
	EvaluationContext ObjectMetricEvaluationContextV1 `json:"evaluation_context"`
	RollupAggregation string                          `json:"rollup_aggregation,omitempty"`
}

type ObjectMetricAlignmentV1 struct {
	ResultOwnerObjectTypeID string                    `json:"result_owner_object_type_id"`
	GroupByDimensions       []ObjectMetricPropertyRef `json:"group_by_dimensions"`
	GrainPolicy             string                    `json:"grain_policy"`
	ResultSetPolicy         string                    `json:"result_set_policy"`
	PrimaryDependencyAlias  string                    `json:"primary_dependency_alias,omitempty"`
	TimeAlignment           string                    `json:"time_alignment"`
	TimeKey                 *ObjectMetricPropertyRef  `json:"time_key,omitempty"`
}

type MetricPropertyBindingV1 struct {
	ObjectTypeID string                           `json:"object_type_id"`
	PropertyName string                           `json:"property_name"`
	DisplayName  string                           `json:"display_name"`
	Comment      string                           `json:"comment,omitempty"`
	MetricRef    ObjectMetricRefV1                `json:"metric_ref"`
	Execution    MetricPropertyBindingExecutionV1 `json:"execution"`
	NoDataPolicy string                           `json:"no_data_policy"`
	OnError      string                           `json:"on_error"`
	Status       string                           `json:"status"`
}

type MetricPropertyBindingExecutionV1 struct {
	Mode          string `json:"mode"`
	RefreshPolicy string `json:"refresh_policy,omitempty"`
	FreshnessSLO  string `json:"freshness_slo,omitempty"`
}

// InferObjectMetricOutputShapeV1 derives response shape instead of accepting it as authored input.
func InferObjectMetricOutputShapeV1(def *ObjectMetricDefinitionV1) string {
	if def == nil {
		return ""
	}
	if def.CalculationScope == ObjectMetricScopeInstance {
		return ObjectMetricOutputObjectValue
	}
	if def.ResultSemantics.StatisticalGrain.TimeGranularity != "" {
		return ObjectMetricOutputTimeSeries
	}
	if len(def.ResultSemantics.StatisticalGrain.GroupByDimensions) > 0 {
		return ObjectMetricOutputGrouped
	}
	return ObjectMetricOutputSummary
}
