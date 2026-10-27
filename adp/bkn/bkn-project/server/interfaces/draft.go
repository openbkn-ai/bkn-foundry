// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

// An ontology draft holds what drafting from the table structure of a project's data
// catalogs proposes: object types, relation types, enum value types, excluded fields
// and tables that could not be read. Every item carries its evidence and whether it is
// adopted by default; people decide later. A draft stays out of git until confirmed.

// Draft states.
const (
	DRAFT_STATUS_DRAFTING = "drafting"
	DRAFT_STATUS_READY    = "ready"
	DRAFT_STATUS_FAILED   = "failed"
)

// Where an item of a draft came from.
const (
	DRAFT_ORIGIN_FOREIGN_KEY = "foreign_key"
	DRAFT_ORIGIN_NAME_MATCH  = "name_match"
	DRAFT_ORIGIN_LINK_TABLE  = "link_table"
)

// How an object type's key was found.
const (
	DRAFT_KEY_PRIMARY_KEY  = "primary_key"
	DRAFT_KEY_UNIQUE_INDEX = "unique_index"
	// DRAFT_KEY_INFERRED is a key no constraint declares, taken from an identity-like field
	// whose full-table count shows it unique and non-null.
	DRAFT_KEY_INFERRED = "inferred"
	DRAFT_KEY_NONE     = "none"
)

// Relation cardinalities.
const (
	DRAFT_CARDINALITY_ONE_TO_ONE   = "one_to_one"
	DRAFT_CARDINALITY_MANY_TO_ONE  = "many_to_one"
	DRAFT_CARDINALITY_MANY_TO_MANY = "many_to_many"
)

// SOURCE_OF_RECORD_TABLE marks a property whose value comes from its mapped table field.
const SOURCE_OF_RECORD_TABLE = "table"

type Draft struct {
	ProjectID  string      `json:"project_id"`
	Status     string      `json:"status"`
	Items      DraftItems  `json:"items"`
	Error      string      `json:"error"`
	Drafter    AccountInfo `json:"drafter"`
	CreateTime int64       `json:"create_time"`
	UpdateTime int64       `json:"update_time"`
}

type DraftItems struct {
	ObjectTypes   []*DraftObjectType    `json:"object_types"`
	RelationTypes []*DraftRelationType  `json:"relation_types"`
	ValueTypes    []*DraftValueType     `json:"value_types"`
	Excluded      []*DraftExcludedField `json:"excluded"`
	Unreadable    []*DraftTable         `json:"unreadable"`
}

// DraftTable names a table of a data catalog.
type DraftTable struct {
	ResourceID string `json:"resource_id"`
	Name       string `json:"name"`
	CatalogID  string `json:"catalog_id"`
	// Reason says why the table could not be read, for unreadable tables.
	Reason string `json:"reason,omitempty"`
}

type DraftObjectType struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Adopted     bool             `json:"adopted"`
	Table       DraftTable       `json:"table"`
	PrimaryKeys []string         `json:"primary_keys"`
	DisplayKey  string           `json:"display_key"`
	Properties  []*DraftProperty `json:"properties"`
	Key         DraftKeyEvidence `json:"key"`
	// NeedsIdentity is set when no unique, non-null key was found: a person decides the identity.
	NeedsIdentity bool `json:"needs_identity"`
}

type DraftProperty struct {
	Name           string `json:"name"`
	DisplayName    string `json:"display_name"`
	Type           string `json:"type"`
	Description    string `json:"description"`
	MappedField    string `json:"mapped_field"`
	ValueTypeID    string `json:"value_type_id,omitempty"`
	SourceOfRecord string `json:"source_of_record"`
	// MissingDescription is a quality reminder; it does not block.
	MissingDescription bool `json:"missing_description"`
}

// DraftKeyEvidence records how the key was found and the full-table counts that check it.
type DraftKeyEvidence struct {
	Source string `json:"source"`
	// Field is the field an inferred identity counted; it stays when none qualified.
	Field    string `json:"field,omitempty"`
	Computed bool   `json:"computed"`
	Rows     int64  `json:"rows"`
	Distinct int64  `json:"distinct"`
	Nulls    int64  `json:"nulls"`
	// Reason says why the counts were not computed.
	Reason string `json:"reason,omitempty"`
}

type DraftRelationType struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Source      string              `json:"source"`
	Target      string              `json:"target"`
	Origin      string              `json:"origin"`
	Cardinality string              `json:"cardinality"`
	Mapping     []DraftFieldMapping `json:"mapping"`
	// LinkTable backs a many-to-many relation; its mappings join it to each end.
	LinkTable     *DraftTable         `json:"link_table,omitempty"`
	SourceMapping []DraftFieldMapping `json:"source_mapping,omitempty"`
	TargetMapping []DraftFieldMapping `json:"target_mapping,omitempty"`
	Adopted       bool                `json:"adopted"`
	// Verification is "confirmed" for a declared foreign key, "unconfirmed" for an inferred one.
	Verification string            `json:"verification"`
	Evidence     DraftJoinEvidence `json:"evidence"`
}

type DraftFieldMapping struct {
	SourceProperty string `json:"source_property"`
	TargetProperty string `json:"target_property"`
}

// DraftJoinEvidence is the full-table count behind a relation: of the source rows whose
// field is not null, how many find a target row (hits) and how many do not (orphans).
type DraftJoinEvidence struct {
	Computed bool    `json:"computed"`
	Rows     int64   `json:"rows"`
	Nulls    int64   `json:"nulls"`
	Hits     int64   `json:"hits"`
	Orphans  int64   `json:"orphans"`
	HitRate  float64 `json:"hit_rate"`
	// SourceDistinct counts the distinct values of the referring field: a field of a few
	// values hits any key by chance.
	SourceDistinct int64  `json:"source_distinct"`
	Reason         string `json:"reason,omitempty"`
}

// DraftValueType is an enum drafted from a code table; the properties that reference the
// code table take it as their value type.
type DraftValueType struct {
	ID        string             `json:"id"`
	Name      string             `json:"name"`
	BaseType  string             `json:"base_type"`
	Values    []DraftEnumValue   `json:"values"`
	CodeTable DraftTable         `json:"code_table"`
	UsedBy    []DraftPropertyRef `json:"used_by"`
	Adopted   bool               `json:"adopted"`
}

type DraftEnumValue struct {
	Value   string `json:"value"`
	Display string `json:"display"`
}

type DraftPropertyRef struct {
	ObjectType string `json:"object_type"`
	Property   string `json:"property"`
}

// DraftExcludedField is a field left unmapped by the privacy rules; it can be restored.
type DraftExcludedField struct {
	ObjectType string `json:"object_type"`
	Field      string `json:"field"`
	Reason     string `json:"reason"`
	Keyword    string `json:"keyword"`
}
