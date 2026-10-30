// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

// Source changes are how the tables a project binds differ now, as vega-backend's discovery last
// recorded them, from what the draft and the published definitions map: columns added, removed or
// retyped, per object type. Drafting them changes the draft only; the confirmation goes out of date
// and the project is confirmed, checked and published as usual. Codes are for machines; clients
// word them.

// States of a project's source changes.
const (
	SOURCE_CHANGES_IN_STEP = "in_step"
	SOURCE_CHANGES_CHANGED = "changed"
	// SOURCE_CHANGES_UNREACHABLE: vega-backend did not tell; nothing is known to be in step.
	SOURCE_CHANGES_UNREACHABLE = "unreachable"
)

// How a column changed.
const (
	SOURCE_CHANGE_ADDED   = "added"
	SOURCE_CHANGE_REMOVED = "removed"
	SOURCE_CHANGE_RETYPED = "retyped"
)

// DRAFT_DECISION_SYNC records the source changes drafted into an object type or relation type; After
// lists them.
const DRAFT_DECISION_SYNC = "sync"

// DRAFT_PROPOSED_FIELD_REMOVED is why a relation drafting the source changes left out: a field it
// joins by is gone. It waits for a person, as other relations drafting did not adopt.
const DRAFT_PROPOSED_FIELD_REMOVED = "field_removed"

// SourceChanges are a project's source changes. Signature stands for the draft and the tables they
// were read from; drafting them refuses another.
type SourceChanges struct {
	ProjectID     string                     `json:"project_id"`
	State         string                     `json:"state"`
	ObjectTypes   []SourceObjectTypeChanges  `json:"object_types"`
	RelationTypes []SourceRelationTypeChange `json:"relation_types"`
	// MissingTables are the bound tables that are gone, as status data_sources reads them; they are
	// not compared.
	MissingTables []ProjectStatusAffected `json:"missing_tables"`
	Signature     string                  `json:"signature"`
}

// SourceObjectTypeChanges are the changes of one object type's table. InDraft tells that the
// project's draft has the object type, so drafting the changes applies them; Published, that the
// published definitions have it. One only published is compared as published, and drafting leaves it.
type SourceObjectTypeChanges struct {
	ID        string               `json:"id"`
	Name      string               `json:"name"`
	Table     string               `json:"table"`
	InDraft   bool                 `json:"in_draft"`
	Published bool                 `json:"published"`
	Changes   []SourceColumnChange `json:"changes"`
}

// SourceColumnChange is one column that changed. Field is the column and FieldName its business
// name; Property and PropertyName are the property mapping it, for a removed or retyped column.
// Excluded is the rule that would leave an added column out: privacy or unsupported_type. Key marks
// a removed or retyped column the object type is identified or displayed by; Unsupported, a column
// retyped to a type BKN cannot map. Relations are the relation types joining by the property.
type SourceColumnChange struct {
	Change       string        `json:"change"`
	Field        string        `json:"field"`
	FieldName    string        `json:"field_name"`
	Property     string        `json:"property,omitempty"`
	PropertyName string        `json:"property_name,omitempty"`
	FromType     string        `json:"from_type,omitempty"`
	ToType       string        `json:"to_type,omitempty"`
	Excluded     string        `json:"excluded,omitempty"`
	Key          bool          `json:"key,omitempty"`
	Unsupported  bool          `json:"unsupported,omitempty"`
	Relations    []ReleaseItem `json:"relations,omitempty"`
}

// SourceRelationTypeChange is a relation type of the draft whose link table lost a field it joins
// by.
type SourceRelationTypeChange struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Table   string   `json:"table"`
	Removed []string `json:"removed"`
}

// SourceChangesDraft is the body of drafting the source changes: the signature of the changes read.
type SourceChangesDraft struct {
	Signature string `json:"signature"`
}
