// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

// RelationType mapping types.
const (
	RELATION_MAPPING_TYPE_DIRECT              = "direct"
	RELATION_MAPPING_TYPE_DATA_VIEW           = "data_view"
	RELATION_MAPPING_TYPE_FILTERED_CROSS_JOIN = "filtered_cross_join"
)

// ObjectType data source types.
const (
	DATA_SOURCE_TYPE_DATA_VIEW = "data_view"
	DATA_SOURCE_TYPE_RESOURCE  = "resource"
)

// BknNetworkFrontmatter is YAML frontmatter metadata for a .bkn file.
type BknNetworkFrontmatter struct {
	Type string   `yaml:"type"`
	ID   string   `yaml:"id"`
	Name string   `yaml:"name"`
	Tags []string `yaml:"tags"`

	Version        string `yaml:"version,omitempty"`
	Branch         string `yaml:"branch,omitempty"`
	BusinessDomain string `yaml:"business_domain,omitempty"`
}

// BknDocument is a parsed network.bkn file: frontmatter + body definitions.
type BknNetwork struct {
	BknNetworkFrontmatter
	Summary     string
	Description string

	RawContent   string
	SkillContent string

	ObjectTypes   []*BknObjectType
	RelationTypes []*BknRelationType
	ActionTypes   []*BknActionType
	RiskTypes     []*BknRiskType
	ConceptGroups []*BknConceptGroup
	Metrics       []*BknMetric
}

// BknObjectTypeFrontmatter is YAML frontmatter metadata for a .bkn file.
type BknObjectTypeFrontmatter struct {
	Type string   `yaml:"type"`
	ID   string   `yaml:"id"`
	Name string   `yaml:"name"`
	Tags []string `yaml:"tags"`
}

// BknObjectType represents an object type definition.
type BknObjectType struct {
	BknObjectTypeFrontmatter

	Summary     string
	Description string

	RawContent string

	DataSource      *ResourceInfo
	DataProperties  []*DataProperty
	LogicProperties []*LogicProperty

	// Keys section
	PrimaryKeys    []string
	DisplayKey     string
	IncrementalKey string

	// Set during parse; used by ValidateNetwork
	HasDataPropertiesSection bool
	HasKeysSection           bool
}

// MetricAttributes is parsed from the body section ### Metric attributes
// (Markdown table: Metric Type | Unit Type | Unit), analogous to Endpoint on BknRelationType.
// These fields are not part of YAML frontmatter.
type MetricAttributes struct {
	MetricType string // DTO metric_type; should match Formula.Kind when set
	UnitType   string
	Unit       string
}

// BknMetricFrontmatter is YAML frontmatter for a type: metric file.
type BknMetricFrontmatter struct {
	Type string   `yaml:"type"`
	ID   string   `yaml:"id"`
	Name string   `yaml:"name"`
	Tags []string `yaml:"tags"`
}

// BknMetric is a network-level metric definition (metrics/*.bkn).
type BknMetric struct {
	BknMetricFrontmatter

	MetricAttributes MetricAttributes

	Summary     string
	Description string
	RawContent  string

	ScopeType string
	ScopeRef  string

	Formula *MetricFormula

	// Optional tables (parsed from body)
	TimeDimensions     []MetricTimeDimRow
	AnalysisDimensions []MetricAnalysisDimRow

	HasScopeSection              bool
	HasMetricAttributesSection   bool
	HasCalculationFormulaSection bool
	HasTimeDimensionSection      bool
	HasAnalysisDimensionsSection bool
}

// MetricFormula is the in-memory shape of the Calculation Formula YAML.
type MetricFormula struct {
	Kind   string        `yaml:"kind"`
	Atomic *MetricAtomic `yaml:"atomic,omitempty"`
}

// MetricAtomic is the atomic metric calculation subtree.
type MetricAtomic struct {
	Condition   *MetricCondition   `yaml:"condition,omitempty"`
	Aggregation *MetricAggregation `yaml:"aggregation,omitempty"`
	GroupBy     []MetricGroupBy    `yaml:"group_by,omitempty"`
	OrderBy     []MetricOrderBy    `yaml:"order_by,omitempty"`
	Having      *MetricHaving      `yaml:"having,omitempty"`
}

// MetricCondition is a row-level filter (no object_type_id).
type MetricCondition struct {
	Field     string `yaml:"field"`
	Operation string `yaml:"operation"`
	Value     any    `yaml:"value,omitempty"`
}

// MetricAggregation is required for atomic metrics.
type MetricAggregation struct {
	Property string `yaml:"property"`
	Aggr     string `yaml:"aggr"`
}

// MetricGroupBy is a grouping dimension.
type MetricGroupBy struct {
	Property    string `yaml:"property"`
	Description string `yaml:"description,omitempty"`
}

// MetricOrderBy orders grouped or aggregated rows.
type MetricOrderBy struct {
	Property  string `yaml:"property"`
	Direction string `yaml:"direction,omitempty"`
}

// MetricHaving filters on aggregated values.
type MetricHaving struct {
	Field     string `yaml:"field"`
	Operation string `yaml:"operation"`
	Value     any    `yaml:"value,omitempty"`
}

// MetricTimeDimRow is one row under ### Time Dimension.
type MetricTimeDimRow struct {
	Property string
	Policy   string
}

// MetricAnalysisDimRow is one row under ### Analysis Dimensions.
type MetricAnalysisDimRow struct {
	Name        string
	DisplayName string
}

// ResourceInfo represents a data source reference.
type ResourceInfo struct {
	Type string
	ID   string
	Name string
}

// DataProperty is a ### Data Properties table row.
type DataProperty struct {
	Name        string
	DisplayName string
	Type        string
	Description string
	MappedField string
}

// LogicProperty represents a logic property definition.
type LogicProperty struct {
	Name        string
	DisplayName string
	Type        string
	Description string

	DataSource   *ResourceInfo
	Parameters   []Parameter
	AnalysisDims []Field
}

type Field struct {
	Name        string
	Type        string
	DisplayName string
	Description string
}

// Parameter represents a parameter binding.
type Parameter struct {
	Name        string
	Type        string
	Source      string // property, const, etc.
	Operation   string
	ValueFrom   string
	Value       any
	IfSystemGen bool
	Description string
}

// BknRelationTypeFrontmatter is YAML frontmatter metadata for a .bkn file.
type BknRelationTypeFrontmatter struct {
	Type string   `yaml:"type"`
	ID   string   `yaml:"id"`
	Name string   `yaml:"name"`
	Tags []string `yaml:"tags"`
}

// BknRelationType represents a relation type definition.
type BknRelationType struct {
	BknRelationTypeFrontmatter

	Summary     string
	Description string

	RawContent string

	// Endpoint
	Endpoint     Endpoint
	MappingRules any
}

type Endpoint struct {
	Source string
	Target string
	Type   string // direct | data_view | filtered_cross_join
}

// MappingRule represents a property mapping between source and target.
type MappingRule struct {
	SourceProperty string
	TargetProperty string
}

// DirectMappingRule represents a direct mapping rule.
type DirectMappingRule []MappingRule

// InDirectMappingRule represents a non-direct mapping rule.
type InDirectMappingRule struct {
	BackingDataSource  *ResourceInfo
	SourceMappingRules []MappingRule
	TargetMappingRules []MappingRule
}

// FilteredCrossJoinMapping rules for relation type filtered_cross_join (per-side conditions, no key mapping).
type FilteredCrossJoinMapping struct {
	SourceCondition *CondCfg
	TargetCondition *CondCfg
}

type CondCfg struct {
	Field     string     `yaml:"field"`
	Operation string     `yaml:"operation"`
	SubConds  []*CondCfg `yaml:"sub_conds,omitempty"`
	ValueFrom string     `yaml:"value_from,omitempty"`
	Value     any        `yaml:"value,omitempty"`
}

// BknActionTypeFrontmatter is YAML frontmatter metadata for a .bkn file.
type BknActionTypeFrontmatter struct {
	Type string   `yaml:"type"`
	ID   string   `yaml:"id"`
	Name string   `yaml:"name"`
	Tags []string `yaml:"tags"`

	// ActionType is deprecated in favor of ActionIntent; both may appear for compatibility.
	ActionType   string `yaml:"action_type"`
	ActionIntent string `yaml:"action_intent"`
}

// BknActionType represents an action type definition.
type BknActionType struct {
	BknActionTypeFrontmatter

	Summary     string
	Description string

	RawContent string

	// Bound Object
	BoundObject string

	// Affect Object
	AffectObject *ActionAffect

	// ImpactContracts is the preferred structured impact declaration (OpenAPI ImpactContractItem).
	ImpactContracts []*ImpactContractItem

	// Trigger Condition
	TriggerCondition *ActionCondCfg

	// Tool Configuration
	ActionSource *ActionSource

	// Parameter Binding
	Parameters []Parameter

	// Schedule
	Schedule *Schedule
}

// CondCfg represents a condition configuration.
type ActionCondCfg struct {
	ObjectTypeID string           `yaml:"object_type_id"`
	Field        string           `yaml:"field"`
	Operation    string           `yaml:"operation"`
	SubConds     []*ActionCondCfg `yaml:"sub_conds,omitempty"`
	ValueFrom    string           `yaml:"value_from,omitempty"`
	Value        any              `yaml:"value,omitempty"`
}

// PreCondition represents a pre-condition check.
type PreCondition struct {
	Object    string
	Check     string
	Condition string
	Message   string
}

type ActionAffect struct {
	ObjectType  string
	Description string
}

// ImpactContractItem matches bkn-backend OpenAPI ImpactContractItem (impact_contracts array element).
type ImpactContractItem struct {
	ObjectTypeID      string   `yaml:"object_type_id"`
	ExpectedOperation string   `yaml:"expected_operation"`
	Description       string   `yaml:"description"`
	AffectedFields    []string `yaml:"affected_fields,omitempty"`
}

// Schedule represents an action schedule.
type Schedule struct {
	Type       string // FIX_RATE, CRON, etc.
	Expression string
}

// ActionSource represents action source.
type ActionSource struct {
	Type string
	// type 为 tool
	BoxID  string
	ToolID string
	// type 为 mcp
	McpID    string
	ToolName string
}

// BknRiskTypeFrontmatter is YAML frontmatter metadata for a .bkn file.
type BknRiskTypeFrontmatter struct {
	Type string   `yaml:"type"`
	ID   string   `yaml:"id"`
	Name string   `yaml:"name"`
	Tags []string `yaml:"tags"`
}

// BknRiskType represents a risk type definition.
type BknRiskType struct {
	BknRiskTypeFrontmatter

	Summary     string
	Description string

	RawContent string
}

// BknConceptGroupFrontmatter is YAML frontmatter metadata for a .bkn file.
type BknConceptGroupFrontmatter struct {
	Type string   `yaml:"type"`
	ID   string   `yaml:"id"`
	Name string   `yaml:"name"`
	Tags []string `yaml:"tags"`
}

// BknConceptGroup represents a concept group definition.
type BknConceptGroup struct {
	BknConceptGroupFrontmatter

	Summary     string
	Description string

	RawContent string

	ObjectTypes []string
}
