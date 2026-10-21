// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"encoding/json"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common/maskrule"
)

// RelationType mapping types.
const (
	RELATION_MAPPING_TYPE_DIRECT              = "direct"
	RELATION_MAPPING_TYPE_INDIRECT            = "indirect"
	RELATION_MAPPING_TYPE_FILTERED_CROSS_JOIN = "filtered_cross_join"
)

// ObjectType data source types.
const (
	DATA_SOURCE_TYPE_RESOURCE = "resource"
)

// BknNetworkFrontmatter is YAML frontmatter metadata for a .bkn file.
type BknNetworkFrontmatter struct {
	Type string   `yaml:"type"`
	ID   string   `yaml:"id"`
	Name string   `yaml:"name"`
	Tags []string `yaml:"tags"`

	Version string `yaml:"version,omitempty"`
	Branch  string `yaml:"branch,omitempty"`

	// BknSpec is the BKN specification version the package is written in, normalized to
	// major.minor ("3.0"). Empty means a package written before the field existed; such packages
	// are read by the 2.x rules, exactly as before. See spec.go.
	BknSpec string `yaml:"bkn_spec,omitempty"`

	// Capabilities declares which Skills and ToolBox tools this network depends on. It is a
	// dependency declaration, not a payload: the capabilities themselves travel through their own
	// channels (a skill's package, a tool box's import/export), the way package.json names
	// dependencies without carrying them.
	Capabilities *BknCapabilities `yaml:"capabilities,omitempty"`
}

// BknCapabilities is the capability dependency section of a network.
type BknCapabilities struct {
	Skills    []*BknCapabilitySkill    `yaml:"skills,omitempty"`
	Functions []*BknCapabilityFunction `yaml:"functions,omitempty"`
	MCPTools  []*BknCapabilityMCPTool  `yaml:"mcp_tools,omitempty"`
}

// BknCapabilitySkill names one skill dependency by both id and name.
//
// The id is generated per environment and does not survive a move between them; the name is what
// makes a cross-environment import resolvable at all. Writing only the id would make this section
// decoration everywhere except the environment that produced it.
type BknCapabilitySkill struct {
	ID   string `yaml:"id,omitempty"`
	Name string `yaml:"name,omitempty"`
}

// BknCapabilityMCPTool names one MCP tool dependency.
//
// The tool is identified by name on both sides — that is the MCP protocol's own addressing, so
// unlike a toolbox tool there is no separate id to fall back from. Only the server needs both:
// its id is environment-local, its name is what an import elsewhere can resolve against.
type BknCapabilityMCPTool struct {
	MCPID    string `yaml:"mcp_id,omitempty"`
	MCPName  string `yaml:"mcp_name,omitempty"`
	ToolName string `yaml:"tool_name,omitempty"`
}

// BknCapabilityFunction names one tool dependency. Both halves of the identity are written twice,
// as ids for an exact match at home and as names for a fallback elsewhere.
type BknCapabilityFunction struct {
	BoxID    string `yaml:"box_id,omitempty"`
	ToolID   string `yaml:"tool_id,omitempty"`
	BoxName  string `yaml:"box_name,omitempty"`
	ToolName string `yaml:"tool_name,omitempty"`
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
	ObjectMetrics []json.RawMessage

	// ValueTypes are the 3.0 value types of value_types/ (SPECIFICATION.md §16). A 2.x package never
	// sets them.
	ValueTypes []*BknValueType
}

// BknObjectTypeFrontmatter is YAML frontmatter metadata for a .bkn file.
type BknObjectTypeFrontmatter struct {
	Type string   `yaml:"type"`
	ID   string   `yaml:"id"`
	Name string   `yaml:"name"`
	Tags []string `yaml:"tags"`

	// Aliases are other names of the object type, for concept search and understanding (3.0,
	// SPECIFICATION.md §6.8). A 2.x package never sets them.
	Aliases []string `yaml:"aliases,omitempty"`
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

// ResourceInfo represents a data source reference.
//
// A logic property of type tool is addressed by BoxID + ToolID instead of ID, and may pick one
// field out of the tool result with ResultPath. The other source types only use Type/ID/Name.
type ResourceInfo struct {
	Type string
	ID   string
	Name string

	BoxID      string
	ToolID     string
	ResultPath string
}

// DataProperty is a ### Data Properties table row.
//
// Semantic, Required, Aliases and Constraints are 3.0 declarations (SPECIFICATION.md §6.8). They
// describe what the data should be, for understanding and display; nothing checks the source data
// against them. A 2.x package never sets them.
type DataProperty struct {
	Name        string
	DisplayName string
	Type        string
	Description string
	MappedField string
	MaskRule    *maskrule.Rule

	Semantic    string
	Required    bool
	Aliases     []string
	Constraints *PropertyConstraints
}

// PropertyConstraints is one property's entry in ### Property Constraints. A nil field is not
// declared. A property that references a value type takes its value rules from it, so ValueType
// goes with none of Range, Length, Pattern and Unit.
type PropertyConstraints struct {
	ValueType string
	Range     *ValueRange
	Length    *LengthRange
	Pattern   string
	Unit      string
	Unique    bool
	CRS       string
	Timezone  string
}

// ValueRange is a closed interval. Min and Max hold a number for a numeric property and the ISO
// 8601 text for a date or time property, as written; either may be absent.
type ValueRange struct {
	Min any
	Max any
}

// LengthRange is a closed interval on the number of characters.
type LengthRange struct {
	Min *int
	Max *int
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
	Type   string // direct | indirect | filtered_cross_join
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

	// Aliases are other names of the action type (3.0, SPECIFICATION.md §8.2). A 2.x package never
	// sets them.
	Aliases []string `yaml:"aliases,omitempty"`
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
	// type is tool.
	BoxID  string
	ToolID string
	// type is mcp.
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

// BknValueTypeFrontmatter is YAML frontmatter metadata for a value type file.
type BknValueTypeFrontmatter struct {
	Type string   `yaml:"type"`
	ID   string   `yaml:"id"`
	Name string   `yaml:"name"`
	Tags []string `yaml:"tags"`
}

// BknValueType is a 3.0 value type (SPECIFICATION.md §16): a base type with a set of value rules,
// named so that several properties can share it. A nil or empty field is not declared; Enum goes
// with none of Range, Length and Pattern.
type BknValueType struct {
	BknValueTypeFrontmatter

	Summary     string
	Description string

	RawContent string

	BaseType string
	Enum     []*ValueTypeEnumItem
	Range    *ValueRange
	Length   *LengthRange
	Pattern  string
	Unit     string
}

// ValueTypeEnumItem is one enum item of a value type. Value is the value as written; the value
// type's BaseType says whether it is a number, a date or text.
type ValueTypeEnumItem struct {
	Value       string
	Display     string
	Deprecated  bool
	Description string
}
