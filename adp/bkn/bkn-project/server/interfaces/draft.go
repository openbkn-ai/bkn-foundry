// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "errors"

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
	// DRAFT_KEY_CHOSEN is a key a person chose; its counts, when computed, are kept as found.
	DRAFT_KEY_CHOSEN = "chosen"
)

// Relation cardinalities.
const (
	DRAFT_CARDINALITY_ONE_TO_ONE   = "one_to_one"
	DRAFT_CARDINALITY_MANY_TO_ONE  = "many_to_one"
	DRAFT_CARDINALITY_MANY_TO_MANY = "many_to_many"
)

// Kinds of draft items people edit, as they appear in item paths.
const (
	DRAFT_KIND_OBJECT_TYPES   = "object_types"
	DRAFT_KIND_RELATION_TYPES = "relation_types"
	DRAFT_KIND_VALUE_TYPES    = "value_types"
	// DRAFT_KIND_EXCLUDED names decisions on excluded fields; their item is the object type.
	DRAFT_KIND_EXCLUDED = "excluded"
)

// What a person decided about a draft item.
const (
	DRAFT_DECISION_ADOPT    = "adopt"
	DRAFT_DECISION_UNADOPT  = "unadopt"
	DRAFT_DECISION_RENAME   = "rename"
	DRAFT_DECISION_DESCRIBE = "describe"
	DRAFT_DECISION_IDENTITY = "identity"
	DRAFT_DECISION_RESTORE  = "restore"
)

// Limits of what people write into a draft, those of BKN definitions.
const (
	DRAFT_NAME_MAX_LENGTH        = 128
	DRAFT_DESCRIPTION_MAX_LENGTH = 1000
)

// ErrDraftHasDecisions is returned by StartDraft when drafting again would discard the
// decisions people made on the draft and the caller did not agree to discard them.
var ErrDraftHasDecisions = errors.New("draft has decisions")

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
	// Decisions people made on the items, oldest first. They are kept with the items so a
	// draft and its decisions are read and written together.
	Decisions []*DraftDecision `json:"decisions"`
	// Confirmation records the last time the draft was confirmed. The draft is confirmed while
	// its update time is the draft's: any change since moves the draft's on. Drafting again keeps
	// it, unconfirmed, so what it wrote on the branch stays the draft's.
	Confirmation *DraftConfirmation `json:"confirmation,omitempty"`
}

// DraftConfirmation records a draft committed to its project branch as BKN definitions.
type DraftConfirmation struct {
	// UpdateTime is the draft's update time as confirmed; 0 while a confirmation is under way,
	// after one failed, and once the draft is drafted again.
	UpdateTime int64 `json:"update_time"`
	// Commit is the branch commit of the last confirmation that committed.
	Commit string      `json:"commit"`
	By     AccountInfo `json:"by"`
	Time   int64       `json:"time"`
	// Files are the definition paths the last confirmation wrote on the branch; the next one may
	// replace or remove them.
	Files []string `json:"files"`
	// Pending are the paths a confirmation under way is about to write. A confirmation that
	// fails drops them; they stay only when one stopped between committing and recording it, and
	// are then the draft's too.
	Pending []string `json:"pending,omitempty"`
	// SkippedValueTypes are adopted value types BKN 3.0 would refuse; their properties keep
	// their base type.
	SkippedValueTypes []string `json:"skipped_value_types"`
}

// DraftConfirm asks to commit the draft made at UpdateTime to the project branch.
type DraftConfirm struct {
	UpdateTime int64 `json:"update_time"`
}

// DraftDecision records one change a person made to a draft item. Before and After are the
// values the change replaced and wrote: a bool for adopt and unadopt, the property names for
// identity, the excluded field and the property it became for restore, a string otherwise.
type DraftDecision struct {
	Kind   string      `json:"kind"`
	ItemID string      `json:"item_id"`
	Action string      `json:"action"`
	Before any         `json:"before"`
	After  any         `json:"after"`
	By     AccountInfo `json:"by"`
	Time   int64       `json:"time"`
}

// DraftIdentity is a person's choice of the properties that identify an object type's records.
type DraftIdentity struct {
	UpdateTime int64    `json:"update_time"`
	Properties []string `json:"properties"`
}

// DraftRestore puts a field the drafting excluded back as a property of its object type.
type DraftRestore struct {
	UpdateTime int64  `json:"update_time"`
	ObjectType string `json:"object_type"`
	Field      string `json:"field"`
}

// DraftItemEdit is a person's edit of one draft item; nil fields stay as they are.
// UpdateTime is the update time of the draft the edit was made on.
type DraftItemEdit struct {
	UpdateTime  int64   `json:"update_time"`
	Adopted     *bool   `json:"adopted"`
	Name        *string `json:"name"`
	Description *string `json:"description"`
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
	Description string              `json:"description"`
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
	// ProposedBecause says why drafting proposed the relation rather than adopting it: not counted,
	// a hit rate below the suggest or adopt rate, a field of too few values to tell a match from
	// chance, or an object type at an end that is not adopted. Empty for a relation drafting adopted.
	ProposedBecause string `json:"proposed_because,omitempty"`
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
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	BaseType    string             `json:"base_type"`
	Values      []DraftEnumValue   `json:"values"`
	CodeTable   DraftTable         `json:"code_table"`
	UsedBy      []DraftPropertyRef `json:"used_by"`
	Adopted     bool               `json:"adopted"`
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
