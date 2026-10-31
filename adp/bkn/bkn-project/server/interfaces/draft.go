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
	// DRAFT_ORIGIN_STEM_MATCH is a field whose name only resembles the other table's:
	// supplier_number for erp_supplier's supplier_code.
	DRAFT_ORIGIN_STEM_MATCH = "stem_match"
	DRAFT_ORIGIN_LINK_TABLE = "link_table"
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
	// DRAFT_KIND_DRAFT names decisions on the draft as a whole; they have no item.
	DRAFT_KIND_DRAFT = "draft"
)

// What a person decided about a draft item.
const (
	DRAFT_DECISION_ADOPT    = "adopt"
	DRAFT_DECISION_UNADOPT  = "unadopt"
	DRAFT_DECISION_RENAME   = "rename"
	DRAFT_DECISION_DESCRIBE = "describe"
	DRAFT_DECISION_IDENTITY = "identity"
	// DRAFT_DECISION_INCREMENTAL sets or clears an object type's update field.
	DRAFT_DECISION_INCREMENTAL = "incremental"
	DRAFT_DECISION_RESTORE     = "restore"
	// DRAFT_DECISION_ACCEPT_PARTIAL confirms a draft knowing the tables it could not read, the
	// resource IDs in After.
	DRAFT_DECISION_ACCEPT_PARTIAL = "accept_partial"
	// DRAFT_DECISION_ACCEPT_UNDECIDED confirms a draft leaving out the items drafting did not adopt
	// and no one decided on, listed in After as kind/item_id.
	DRAFT_DECISION_ACCEPT_UNDECIDED = "accept_undecided"
	// DRAFT_DECISION_ACCEPT_UNRESOLVED_CONFLICTS records confirming while conflicts between the
	// requirement documents were unresolved; After lists them.
	DRAFT_DECISION_ACCEPT_UNRESOLVED_CONFLICTS = "accept_unresolved_conflicts"
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
	// Inputs records what the draft was made from, so a draft can be explained and remade later.
	Inputs *DraftInputs `json:"inputs,omitempty"`
	// Confirmation records the last time the draft was confirmed. The draft is confirmed while
	// its update time is the draft's: any change since moves the draft's on. Drafting again keeps
	// it, unconfirmed, so what it wrote on the branch stays the draft's.
	Confirmation *DraftConfirmation `json:"confirmation,omitempty"`
	// Adding is the catalogs being added and drafted in the background; the draft stays ready and
	// editable meanwhile, and their items join it when done. Absent when none are.
	Adding *DraftAdding `json:"adding,omitempty"`
}

// DraftInputs is what drafting read and how it decided: the version of its rules and their
// settings, the catalogs, and each table read with a fingerprint of its structure. It holds no data
// values.
type DraftInputs struct {
	RulesVersion int               `json:"rules_version"`
	Settings     DraftRuleSettings `json:"settings"`
	CatalogIDs   []string          `json:"catalog_ids"`
	Tables       []DraftInputTable `json:"tables"`
	// Partial is set when some tables of the catalogs could not be read: they are in unreadable.
	Partial bool `json:"partial"`
	// NamingLanguage is the language the draft named its items in.
	NamingLanguage string `json:"naming_language"`
	// IDs sums up the project's registered ids as drafting left them; absent when there are none.
	IDs *DraftInputIDs `json:"ids,omitempty"`
}

// DraftInputIDs sums up the ids of items drafted from names that are not ASCII. Placeholders are
// the ids of items in the draft that stand until semantic understanding gives a short name, not
// counting those a confirmation locked; Fingerprint is a SHA-256 of every registered id, its origin
// and whether it is locked, and changes when any of them does.
type DraftInputIDs struct {
	Count        int    `json:"count"`
	Placeholders int    `json:"placeholders"`
	Fingerprint  string `json:"fingerprint"`
}

// DraftRuleSettings are the settings drafting's rules read.
type DraftRuleSettings struct {
	CodeTableMaxRows    int64    `json:"code_table_max_rows"`
	CodeTableMaxColumns int      `json:"code_table_max_columns"`
	AdoptHitRate        float64  `json:"adopt_hit_rate"`
	SuggestHitRate      float64  `json:"suggest_hit_rate"`
	MinInferredDistinct int64    `json:"min_inferred_distinct"`
	PrivacyKeywords     []string `json:"privacy_keywords"`
	NameFieldKeywords   []string `json:"name_field_keywords"`
}

// DraftInputTable is a table drafting read. Fingerprint is a SHA-256 of its structure — names,
// columns and their types, the catalog's names and descriptions, keys, indices and foreign keys —
// and changes when any of them does; fingerprints compare between drafts of one rules version.
// Rows is the count discovery gave, as of drafting.
type DraftInputTable struct {
	ResourceID  string `json:"resource_id"`
	Fingerprint string `json:"fingerprint"`
	Rows        int64  `json:"rows"`
	RowsCounted bool   `json:"rows_counted"`
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
	// RequirementVersion is the n of the requirement specification rq-n the draft was confirmed
	// against; 0 when the project had no ready specification.
	RequirementVersion int `json:"requirement_version,omitempty"`
}

// DraftConfirm asks to commit the draft made at UpdateTime to the project branch.
type DraftConfirm struct {
	UpdateTime int64 `json:"update_time"`
	// AcceptPartial confirms a partial draft knowing the tables drafting could not read.
	AcceptPartial bool `json:"accept_partial"`
	// AcceptUndecided confirms a draft leaving out the items no one decided on.
	AcceptUndecided bool `json:"accept_undecided"`
	// AcceptUnresolvedConflicts confirms a draft knowing conflicts between the requirement
	// documents are unresolved: they are a risk to see, not a gate.
	AcceptUnresolvedConflicts bool `json:"accept_unresolved_conflicts"`
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
	// IncrementalKey sets an object type's update field; empty clears it.
	IncrementalKey *string `json:"incremental_key"`
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
	ID   string `json:"id"`
	Name string `json:"name"`
	// Aliases are its names in the other naming language, from the catalog; absent when none.
	Aliases     []string         `json:"aliases,omitempty"`
	Description string           `json:"description"`
	Adopted     bool             `json:"adopted"`
	Table       DraftTable       `json:"table"`
	PrimaryKeys []string         `json:"primary_keys"`
	DisplayKey  string           `json:"display_key"`
	Properties  []*DraftProperty `json:"properties"`
	Key         DraftKeyEvidence `json:"key"`
	// NeedsIdentity is set when no unique, non-null key was found: a person decides the identity.
	NeedsIdentity bool `json:"needs_identity"`
	// IncrementalKey is the property whose value grows whenever a record changes, which the
	// table's local index is refreshed by; empty when none is known.
	IncrementalKey string `json:"incremental_key,omitempty"`
	// AddedByCatalog is the catalog whose adding drafted the object type, absent for one the draft was made with.
	AddedByCatalog string `json:"added_by_catalog,omitempty"`
}

type DraftProperty struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	// Aliases are its names in the other naming language, from the catalog; absent when none.
	Aliases        []string `json:"aliases,omitempty"`
	Type           string   `json:"type"`
	Description    string   `json:"description"`
	MappedField    string   `json:"mapped_field"`
	ValueTypeID    string   `json:"value_type_id,omitempty"`
	SourceOfRecord string   `json:"source_of_record"`
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
	// Candidate is a pair of properties drafting counted unique and non-null together, for an
	// object type that waits for its identity: offered to choose, never taken by drafting.
	Candidate []string `json:"candidate,omitempty"`
	// Candidates are every pair drafting counted so, in column order, Candidate first; absent in
	// drafts made before it listed more than one.
	Candidates [][]string `json:"candidates,omitempty"`
}

type DraftRelationType struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Aliases are its names in the other naming language, from the catalog; absent when none.
	Aliases     []string            `json:"aliases,omitempty"`
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
	// AddedByCatalog is the catalog whose adding drafted the relation type, absent for one the draft was made with.
	AddedByCatalog string `json:"added_by_catalog,omitempty"`
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
	// AddedByCatalog is the catalog whose adding drafted the value type, absent for one the draft was made with.
	AddedByCatalog string `json:"added_by_catalog,omitempty"`
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

// Fields an answer of Ask AI may suggest changing, each by an edit the draft already takes:
// the name and description by EditDraftItem, the identity by ChooseIdentity.
const (
	DRAFT_ASK_FIELD_NAME        = "name"
	DRAFT_ASK_FIELD_DESCRIPTION = "description"
	DRAFT_ASK_FIELD_IDENTITY    = "identity"
)

// DRAFT_ASK_QUESTION_MAX_LENGTH bounds a question to Ask AI, in characters.
const DRAFT_ASK_QUESTION_MAX_LENGTH = 500

// DraftAskRequest is a question about one item of the draft.
type DraftAskRequest struct {
	Question string `json:"question"`
}

// DraftAskAnswer is Ask AI's answer about one item, and at most one change it suggests. Nothing is
// changed until a person adopts the suggestion through the edit it names.
type DraftAskAnswer struct {
	Answer     string              `json:"answer"`
	Suggestion *DraftAskSuggestion `json:"suggestion"`
}

// DraftAskSuggestion is a change to one field: Value for the name or the description, Properties
// for the identity.
type DraftAskSuggestion struct {
	Field      string   `json:"field"`
	Value      string   `json:"value,omitempty"`
	Properties []string `json:"properties,omitempty"`
	Reason     string   `json:"reason"`
}

// DRAFT_DECISION_SAVE records an object type saved as a whole; Before and After are its
// properties and keys as they were and became.
const DRAFT_DECISION_SAVE = "save"

// DraftObjectTypeSave is an object type of a ready draft saved as a whole, shaped as bkn-backend's
// object type: comment is the description, and each data property maps a field of the object
// type's table. UpdateTime is the update time of the draft the change was made on.
type DraftObjectTypeSave struct {
	UpdateTime     int64                   `json:"update_time"`
	Name           string                  `json:"name"`
	Comment        string                  `json:"comment"`
	DataProperties []DraftDataPropertySave `json:"data_properties"`
	PrimaryKeys    []string                `json:"primary_keys"`
	DisplayKey     string                  `json:"display_key"`
	IncrementalKey string                  `json:"incremental_key"`
}

// DraftDataPropertySave is a data property as bkn-backend's: a property kept by its name, or a new
// one, its name made as drafting makes it when left empty. Type, when given, must be the type the
// field maps to.
type DraftDataPropertySave struct {
	Name        string            `json:"name"`
	DisplayName string            `json:"display_name"`
	Comment     string            `json:"comment"`
	Type        string            `json:"type"`
	MappedField *DraftMappedField `json:"mapped_field"`
}

// DraftMappedField is the field of the table a property maps, by its name.
type DraftMappedField struct {
	Name string `json:"name"`
}

// DraftObjectTypeSnapshot is what saving an object type records of it, before and after.
type DraftObjectTypeSnapshot struct {
	Properties  []DraftPropertySnapshot `json:"properties"`
	PrimaryKeys []string                `json:"primary_keys"`
	DisplayKey  string                  `json:"display_key"`
}

type DraftPropertySnapshot struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	MappedField string `json:"mapped_field"`
}

// DraftPropertyUse is a relation type a property to be removed joins by, named for people to read.
type DraftPropertyUse struct {
	Property string      `json:"property"`
	Relation ReleaseItem `json:"relation"`
	Source   ReleaseItem `json:"source"`
	Target   ReleaseItem `json:"target"`
}

// DRAFT_DECISION_ADD_CATALOGS records catalogs added to a draft: After lists the catalogs and the
// items their tables drafted, or why drafting them failed.
const DRAFT_DECISION_ADD_CATALOGS = "add_catalogs"

// DraftAdding is catalogs being added to a draft, by whom and since when. One past the drafting
// timeout has ended without being recorded, such as on a restart, and holds nothing.
type DraftAdding struct {
	CatalogIDs []string    `json:"catalog_ids"`
	By         AccountInfo `json:"by"`
	Since      int64       `json:"since"`
}

// DraftAddCatalogs is the body of adding catalogs to a project's draft.
type DraftAddCatalogs struct {
	CatalogIDs []string `json:"catalog_ids"`
}

// DraftCatalogsAdded is what adding catalogs drafted, as its decision records it.
type DraftCatalogsAdded struct {
	CatalogIDs    []string `json:"catalog_ids"`
	ObjectTypes   []string `json:"object_types"`
	RelationTypes []string `json:"relation_types"`
	ValueTypes    []string `json:"value_types"`
	// Error is why drafting the catalogs failed; the draft stayed as it was.
	Error string `json:"error,omitempty"`
}
