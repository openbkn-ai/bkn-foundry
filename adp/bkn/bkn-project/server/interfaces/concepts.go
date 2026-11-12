// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

// Concepts are what a project's sources hold, read from the draft and the requirement
// specification each time they are listed: none is stored. A person's judgment of a concept, and
// merging one concept into another, are stored as decisions of the draft (kind DRAFT_KIND_CONCEPTS,
// item id the concept's key), so drafting again over them asks first and discarding drops them.
const (
	// DRAFT_KIND_CONCEPTS names decisions on concepts; their item id is the concept's key.
	DRAFT_KIND_CONCEPTS = "concepts"

	// DRAFT_DECISION_JUDGE sets what a concept is: After is the verdict, Reason why.
	DRAFT_DECISION_JUDGE = "judge"
	// DRAFT_DECISION_MERGE merges the concept into another: After is the other's key.
	DRAFT_DECISION_MERGE = "merge"
	// DRAFT_DECISION_UNMERGE takes a merged concept out again.
	DRAFT_DECISION_UNMERGE = "unmerge"
	// DRAFT_DECISION_DEFINE names and defines a concept no draft item is: After is a
	// DraftConceptDefinition. A concept that is an item is named on the item.
	DRAFT_DECISION_DEFINE = "define"
	// DRAFT_DECISION_ADD adds a concept a person adopted from Ask AI, no source extracted: After is
	// a DraftConceptDefinition, its item id the concept's key.
	DRAFT_DECISION_ADD = "add"
)

// DraftConceptDefinition is a concept's name, definition and suggested verdict as a decision
// records it. From, of an added concept, is the concept it was split from, whose sources it shares.
type DraftConceptDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Verdict     string `json:"verdict,omitempty"`
	From        string `json:"from,omitempty"`
}

// What a concept is judged to be.
const (
	CONCEPT_VERDICT_OBJECT   = "object"
	CONCEPT_VERDICT_EVENT    = "event"
	CONCEPT_VERDICT_PROPERTY = "property"
	CONCEPT_VERDICT_VALUES   = "values"
	CONCEPT_VERDICT_RELATION = "relation"
	CONCEPT_VERDICT_KINETIC  = "kinetic"
	CONCEPT_VERDICT_NONE     = "none"
)

// CONCEPT_VERDICTS are the verdicts a person may give.
var CONCEPT_VERDICTS = []string{
	CONCEPT_VERDICT_OBJECT, CONCEPT_VERDICT_EVENT, CONCEPT_VERDICT_PROPERTY, CONCEPT_VERDICT_VALUES,
	CONCEPT_VERDICT_RELATION, CONCEPT_VERDICT_KINETIC, CONCEPT_VERDICT_NONE,
}

// DOCUMENT_CONCEPT_VERDICTS are what a concept of the document samples that drafting modeled no
// item for may be suggested as: none of them is an object type or relation type of its own.
var DOCUMENT_CONCEPT_VERDICTS = []string{
	CONCEPT_VERDICT_PROPERTY, CONCEPT_VERDICT_VALUES, CONCEPT_VERDICT_KINETIC, CONCEPT_VERDICT_NONE,
}

// Where a concept stands.
const (
	// CONCEPT_STATE_PENDING: no person judged it yet. It may already be in the model, as drafting
	// adopted it; confirming the model then writes it as it is.
	CONCEPT_STATE_PENDING = "pending"
	// CONCEPT_STATE_IN_MODEL: judged, and the model item it is is adopted.
	CONCEPT_STATE_IN_MODEL = "in_model"
	// CONCEPT_STATE_BLOCKED: judged as a model item, which cannot be adopted yet; Blocked says why.
	CONCEPT_STATE_BLOCKED = "blocked"
	// CONCEPT_STATE_JUDGED: judged, and not a model item of the concept model: a property, a value
	// list without its item, kinetic, or not included.
	CONCEPT_STATE_JUDGED = "judged"
)

// Why a concept judged as a model item cannot be adopted yet.
const (
	CONCEPT_BLOCKED_NEEDS_IDENTITY  = "needs_identity"
	CONCEPT_BLOCKED_END_NOT_ADOPTED = "end_not_adopted"
	CONCEPT_BLOCKED_REPAIR          = "repair"
)

// Where a concept was found.
const (
	CONCEPT_SOURCE_TABLE       = "table"
	CONCEPT_SOURCE_DOCUMENT    = "document"
	CONCEPT_SOURCE_REQUIREMENT = "requirement"
)

// Why drafting suggested what a concept drafted from a table is: from the table alone, or from the
// business name and description the catalog's semantic understanding gave the table.
const (
	CONCEPT_BECAUSE_TABLE      = "table_entity"
	CONCEPT_BECAUSE_UNDERSTOOD = "table_understood"
	// CONCEPT_BECAUSE_AI is a concept a person added from Ask AI's revisions.
	CONCEPT_BECAUSE_AI = "ai"
)

// DraftConcepts lists a project's concepts: those of the draft's items, each with the
// requirement concepts that are it, then the requirement concepts no item is. DraftUpdateTime is
// zero without a ready draft; nothing can be judged then.
type DraftConcepts struct {
	ProjectID       string `json:"project_id"`
	DraftUpdateTime int64  `json:"draft_update_time"`
	// RequirementUpdateTime is the update time of the specification read, zero without one: a
	// requirement concept no item is is judged by editing it there, over this time.
	RequirementUpdateTime int64          `json:"requirement_update_time"`
	Concepts              []DraftConcept `json:"concepts"`
	// Counts are the concepts in each state, and by verdict under "verdict:<verdict>".
	Counts map[string]int `json:"counts"`
	// NotUnderstood counts the tables concepts are drafted from that the catalog's semantic
	// understanding has given no business name yet: their concepts are named by the table.
	NotUnderstood int `json:"not_understood"`
}

// DraftConcept is one concept. Item is the draft item it is, absent for a requirement concept no
// item is. Suggested is what drafting or the specification says it is, Verdict what it is now: a
// person's judgment, else the suggestion.
type DraftConcept struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Aliases are its other names: an item's aliases, and the table name of a concept the
	// catalog's semantic understanding named.
	Aliases []string             `json:"aliases,omitempty"`
	Sources []DraftConceptSource `json:"sources"`
	// AI tells a concept AI named or added: by the catalog's semantic understanding, or adopted
	// from Ask AI.
	AI        bool              `json:"ai,omitempty"`
	Item      *DraftConceptItem `json:"item,omitempty"`
	Suggested string            `json:"suggested"`
	// SuggestedBecause is a code for why drafting suggested it, worded by the client.
	SuggestedBecause string `json:"suggested_because,omitempty"`
	Verdict          string `json:"verdict"`
	// Reason is what the person gave with their judgment.
	Reason  string       `json:"reason,omitempty"`
	Judged  *DraftReview `json:"judged,omitempty"`
	State   string       `json:"state"`
	Blocked string       `json:"blocked,omitempty"`
	// Merged are the concepts merged into this one; their sources are listed here.
	Merged []DraftConceptRef `json:"merged,omitempty"`
}

// DraftConceptRef names a concept by its key and name.
type DraftConceptRef struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// DraftConceptSource is where a concept was found: a table, a document of a knowledge base, or a
// requirement concept of the specification, with its citations' documents.
type DraftConceptSource struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
	// DocumentIDs are the requirement documents a requirement concept cites.
	DocumentIDs []string `json:"document_ids,omitempty"`
}

// DraftConceptItem is the draft item a concept is, and whether it is adopted.
type DraftConceptItem struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Adopted bool   `json:"adopted"`
}

// DraftDocumentConcept is a concept the knowledge-base samples hold that document drafting
// proposed no object type or relation type for: a property, a value list, kinetic, or not
// included, with why and the sample text it was read from. Drafting records it in the draft, so it
// is listed among the concepts and judged like any other; it is no model item.
type DraftDocumentConcept struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Verdict is one of DOCUMENT_CONCEPT_VERDICTS.
	Verdict   string                `json:"verdict"`
	Reason    string                `json:"reason"`
	Citations []DraftSourceCitation `json:"citations"`
}

// DraftConceptJudge is the body of judging a concept, on the draft read.
type DraftConceptJudge struct {
	UpdateTime int64  `json:"update_time"`
	Key        string `json:"key"`
	Verdict    string `json:"verdict"`
	Reason     string `json:"reason"`
}

// DraftConceptMerge is the body of merging a concept into another, or of taking it out again
// (Into empty), on the draft read.
type DraftConceptMerge struct {
	UpdateTime int64  `json:"update_time"`
	Key        string `json:"key"`
	Into       string `json:"into"`
}

// DraftConceptAskRequest is a question to Ask AI about the project's concepts, with the
// conversation so far, kept by the client.
type DraftConceptAskRequest struct {
	Question string         `json:"question"`
	History  []DraftAskTurn `json:"history"`
}

// The revisions Ask AI proposes of the concepts, as DraftAskRevision's field; Value is a
// DraftConceptRevision.
const (
	// CONCEPT_REVISION_DEFINE renames and redefines a concept: Key, Name and Description.
	CONCEPT_REVISION_DEFINE = "define"
	// CONCEPT_REVISION_VERDICT judges a concept: Key and Verdict.
	CONCEPT_REVISION_VERDICT = "verdict"
	// CONCEPT_REVISION_MERGE merges a concept into another: Key and Into.
	CONCEPT_REVISION_MERGE = "merge"
	// CONCEPT_REVISION_SPLIT splits a concept: Key and Parts, the first of which it becomes.
	CONCEPT_REVISION_SPLIT = "split"
	// CONCEPT_REVISION_ADD adds a concept: Name, Description and Verdict.
	CONCEPT_REVISION_ADD = "add"
	// CONCEPT_REVISION_ALIASES replaces a concept's aliases: Key and Aliases, the whole list.
	CONCEPT_REVISION_ALIASES = "aliases"
)

// CONCEPT_REVISIONS are the revisions Ask AI may propose of the concepts.
var CONCEPT_REVISIONS = []string{
	CONCEPT_REVISION_DEFINE, CONCEPT_REVISION_VERDICT, CONCEPT_REVISION_MERGE, CONCEPT_REVISION_SPLIT,
	CONCEPT_REVISION_ADD, CONCEPT_REVISION_ALIASES,
}

// DraftConceptRevision is one revision of the concepts, checked against them as they stand.
type DraftConceptRevision struct {
	Key         string             `json:"key,omitempty"`
	Name        string             `json:"name,omitempty"`
	Description string             `json:"description,omitempty"`
	Verdict     string             `json:"verdict,omitempty"`
	Into        string             `json:"into,omitempty"`
	Aliases     []string           `json:"aliases,omitempty"`
	Parts       []DraftConceptPart `json:"parts,omitempty"`
}

// DraftConceptPart is one of the concepts a split makes.
type DraftConceptPart struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Verdict     string `json:"verdict"`
}

// DraftConceptRevise adopts one revision Ask AI proposed, on the draft read: it is recorded as
// the person's decision, as judging or merging by hand is, and can be changed again.
type DraftConceptRevise struct {
	UpdateTime int64                `json:"update_time"`
	Field      string               `json:"field"`
	Value      DraftConceptRevision `json:"value"`
	Reason     string               `json:"reason"`
}
