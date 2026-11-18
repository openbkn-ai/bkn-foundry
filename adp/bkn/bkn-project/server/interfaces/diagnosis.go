// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"encoding/json"
)

// A network diagnosis looks at the draft's knowledge network as a whole: whether its adopted object
// types form one connected network, whether the references the data holds have relations, whether
// the scenario questions can be answered and which requirement concepts wait for a verdict. The
// server computes the facts from full-table counts; Ask AI judges them by the ontology rules; the
// server checks and ranks what it suggests. Nothing changes until a person applies a suggestion.

// Diagnosis states.
const (
	DIAGNOSIS_STATE_RUNNING = "running"
	DIAGNOSIS_STATE_READY   = "ready"
	DIAGNOSIS_STATE_FAILED  = "failed"
)

// Why a diagnosis failed, as a code for the client to word.
const (
	// DIAGNOSIS_ERROR_INTERRUPTED is a run the service stopped before it recorded its outcome.
	DIAGNOSIS_ERROR_INTERRUPTED = "interrupted"
	// DIAGNOSIS_ERROR_DRAFT_UNAVAILABLE is a run whose draft could no longer be read.
	DIAGNOSIS_ERROR_DRAFT_UNAVAILABLE = "draft_unavailable"
)

// Kinds of suggestion.
const (
	DIAGNOSIS_KIND_CHOOSE_IDENTITY     = "choose_identity"
	DIAGNOSIS_KIND_ADOPT_OBJECT        = "adopt_object"
	DIAGNOSIS_KIND_ADOPT_RELATION      = "adopt_relation"
	DIAGNOSIS_KIND_CREATE_RELATION     = "create_relation"
	DIAGNOSIS_KIND_EXCLUDE_OBJECT      = "exclude_object"
	DIAGNOSIS_KIND_WRITEBACK_OBJECT    = "writeback_object"
	DIAGNOSIS_KIND_CONCEPT_DISPOSITION = "concept_disposition"
	DIAGNOSIS_KIND_DATA_QUALITY        = "data_quality"
)

// Groups the suggestions are listed in.
const (
	DIAGNOSIS_GROUP_CONNECT = "connect"
	DIAGNOSIS_GROUP_OBJECT  = "object"
	DIAGNOSIS_GROUP_DATA    = "data"
)

// Where a suggestion stands.
const (
	DIAGNOSIS_STATUS_OPEN      = "open"
	DIAGNOSIS_STATUS_APPLIED   = "applied"
	DIAGNOSIS_STATUS_DISMISSED = "dismissed"
)

// What a requirement concept is, as a concept_disposition suggestion records it.
const (
	DIAGNOSIS_DISPOSITION_PROPERTY     = "property"
	DIAGNOSIS_DISPOSITION_KINETIC      = "kinetic"
	DIAGNOSIS_DISPOSITION_NOT_ONTOLOGY = "not_ontology"
)

// Roles of a write-back object's properties: the decision-tracking pattern. id is its own identity,
// tracked the identity of the object it tracks, time a lifecycle time, actor who acted, decision
// what was decided, snapshot the few values the decision rested on, outcome what came of it.
const (
	WRITEBACK_ROLE_ID       = "id"
	WRITEBACK_ROLE_TRACKED  = "tracked"
	WRITEBACK_ROLE_TIME     = "time"
	WRITEBACK_ROLE_ACTOR    = "actor"
	WRITEBACK_ROLE_DECISION = "decision"
	WRITEBACK_ROLE_SNAPSHOT = "snapshot"
	WRITEBACK_ROLE_OUTCOME  = "outcome"
)

// Kinds of a write-back object's object-operation action types.
const (
	WRITEBACK_ACTION_ADD    = "add"
	WRITEBACK_ACTION_MODIFY = "modify"
)

// DRAFT_KIND_DIAGNOSIS names the decisions on diagnosis suggestions; their item is the suggestion.
const DRAFT_KIND_DIAGNOSIS = "diagnosis"

// DRAFT_DECISION_DISMISS records a suggestion a person dismissed, so later runs do not make it again.
const DRAFT_DECISION_DISMISS = "dismiss"

// DraftNetworkFacts is what the draft's network is, computed from the draft and full-table counts
// as the caller: the deterministic part of a diagnosis.
type DraftNetworkFacts struct {
	// Draft is the ready draft the facts were computed on.
	Draft *Draft `json:"-"`
	// Components are the adopted object types joined by adopted relation types, by id, the largest
	// first; an adopted object type no relation reaches is a component of its own.
	Components [][]string `json:"components"`
	// Pending are the object types not adopted, with the relations of the draft their adoption
	// brings in.
	Pending []DraftPendingObject `json:"pending"`
	// References are the fields whose values were counted against another object type's identity,
	// each with its counts; Counted is false for the pairs the scan budget left out.
	References []DraftReference `json:"references"`
	// Subtypes are the object types whose every identity value is another's.
	Subtypes []DraftSubtype `json:"subtypes"`
	// Pairs is how many field pairs the scan found, NotCounted how many of them it did not count.
	Pairs      int `json:"pairs"`
	NotCounted int `json:"not_counted"`
}

// DraftPendingObject is an object type no one adopted. Relations are the relation types of the draft
// with it at one end and an adopted object type, or itself, at the other: adopting it brings them in.
type DraftPendingObject struct {
	ObjectTypeID  string     `json:"object_type_id"`
	NeedsIdentity bool       `json:"needs_identity"`
	Candidates    [][]string `json:"candidates,omitempty"`
	Relations     []string   `json:"relations"`
}

// DraftReferenceCondition restricts a reference to the records whose type field holds Value: one
// branch of a polymorphic reference, a type field and a code field read together.
type DraftReferenceCondition struct {
	Property string `json:"property"`
	Value    string `json:"value"`
}

// DraftReference is a field of Source whose values were counted against Target's single identity
// property. Self is a reference to the source's own identity, such as a parent. RelationTypeID names
// the relation type of the draft with the same mapping, adopted or not; Name is the name a relation
// drafted from the field takes.
type DraftReference struct {
	Source         string                   `json:"source"`
	SourceProperty string                   `json:"source_property"`
	Target         string                   `json:"target"`
	TargetProperty string                   `json:"target_property"`
	Condition      *DraftReferenceCondition `json:"condition,omitempty"`
	Self           bool                     `json:"self,omitempty"`
	Counted        bool                     `json:"counted"`
	Rows           int64                    `json:"rows"`
	Nulls          int64                    `json:"nulls"`
	Hits           int64                    `json:"hits"`
	Rate           float64                  `json:"rate"`
	RelationTypeID string                   `json:"relation_type_id,omitempty"`
	Name           string                   `json:"name"`
	// FieldRows and FieldNulls are the source's records and those whose field is empty, over all its
	// records whatever their type: how far the data fills the reference in.
	FieldRows  int64 `json:"field_rows,omitempty"`
	FieldNulls int64 `json:"field_nulls,omitempty"`
}

// DraftSubtype is an object type whose every identity value is an identity value of Super: a kind of
// it.
type DraftSubtype struct {
	Sub           string `json:"sub"`
	Super         string `json:"super"`
	SubProperty   string `json:"sub_property"`
	SuperProperty string `json:"super_property"`
	Rows          int64  `json:"rows"`
	Hits          int64  `json:"hits"`
}

// Diagnosis is the latest diagnosis of a project's network.
type Diagnosis struct {
	ProjectID string `json:"-"`
	State     string `json:"state"`
	// Error is why the run failed, as a code for the client to word.
	Error           string `json:"error,omitempty"`
	RunID           string `json:"run_id"`
	ComputedAt      int64  `json:"computed_at"`
	DraftUpdateTime int64  `json:"draft_update_time"`
	// Stale is set when the draft's model changed since the diagnosis was computed.
	Stale       bool                   `json:"stale"`
	Metrics     DiagnosisMetrics       `json:"metrics"`
	Components  [][]string             `json:"components"`
	Suggestions []*DiagnosisSuggestion `json:"suggestions"`
	// AI tells whether Ask AI judged the facts; false when the suggestions are the server's own.
	AI         bool        `json:"ai"`
	Runner     AccountInfo `json:"-"`
	StartTime  int64       `json:"-"`
	FinishTime int64       `json:"-"`
}

type DiagnosisMetrics struct {
	Connectivity    DiagnosisConnectivity `json:"connectivity"`
	References      DiagnosisReferences   `json:"references"`
	Reachability    DiagnosisReachability `json:"reachability"`
	PendingConcepts int                   `json:"pending_concepts"`
}

// DiagnosisConnectivity is the largest component's size among Objects adopted object types in
// Components components.
type DiagnosisConnectivity struct {
	Largest    int `json:"largest"`
	Objects    int `json:"objects"`
	Components int `json:"components"`
}

// DiagnosisReferences counts the references whose counts reach the threshold and, of them, those a
// relation covers or a person dismissed; NotCounted are the field pairs the budget left uncounted.
type DiagnosisReferences struct {
	Covered    int `json:"covered"`
	Total      int `json:"total"`
	NotCounted int `json:"not_counted"`
}

// DiagnosisReachability counts the scenario questions an adopted path answers.
type DiagnosisReachability struct {
	Reachable int `json:"reachable"`
	Total     int `json:"total"`
}

// DiagnosisSuggestion is one ranked suggestion. ID is a stable hash of its kind and what it changes,
// so the same suggestion keeps its id across runs. Apply is the payload of its operation, null for
// one that has none.
type DiagnosisSuggestion struct {
	ID       string            `json:"id"`
	Kind     string            `json:"kind"`
	Group    string            `json:"group"`
	Rank     int               `json:"rank"`
	Title    string            `json:"title"`
	Reason   string            `json:"reason"`
	Evidence DiagnosisEvidence `json:"evidence"`
	Impact   DiagnosisImpact   `json:"impact"`
	Apply    json.RawMessage   `json:"apply"`
	Status   string            `json:"status"`
}

// DiagnosisEvidence is what a suggestion rests on: the full-table counts of a field pair, the
// condition of a polymorphic branch, and requirement-document quotes. NotCounted marks a pair the
// scan budget left out.
type DiagnosisEvidence struct {
	Rows        int64               `json:"rows,omitempty"`
	Hits        int64               `json:"hits,omitempty"`
	Rate        float64             `json:"rate,omitempty"`
	Nulls       int64               `json:"nulls,omitempty"`
	SourceField string              `json:"source_field,omitempty"`
	TargetField string              `json:"target_field,omitempty"`
	Condition   *DiagnosisCondition `json:"condition,omitempty"`
	Quotes      []DiagnosisQuote    `json:"quotes,omitempty"`
	NotCounted  bool                `json:"not_counted,omitempty"`
}

// DiagnosisCondition is a polymorphic branch: the type field and the value it holds.
type DiagnosisCondition struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

// DiagnosisQuote is a requirement-document passage a suggestion rests on.
type DiagnosisQuote struct {
	DocumentID string `json:"document_id"`
	Text       string `json:"text"`
}

// DiagnosisImpact is what applying a suggestion does: the adopted object types it connects to the
// network and the scenario questions it makes answerable.
type DiagnosisImpact struct {
	Connects  []string `json:"connects"`
	Questions []string `json:"questions"`
}

// The apply payloads, one per kind.

type DiagnosisAdoptRelation struct {
	RelationTypeID string `json:"relation_type_id"`
}

type DiagnosisCreateRelation struct {
	Source      string              `json:"source"`
	Target      string              `json:"target"`
	Mapping     []DraftFieldMapping `json:"mapping"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Cardinality string              `json:"cardinality"`
}

type DiagnosisObject struct {
	ObjectTypeID string `json:"object_type_id"`
}

type DiagnosisChooseIdentity struct {
	ObjectTypeID string     `json:"object_type_id"`
	Candidates   [][]string `json:"candidates"`
}

type DiagnosisConceptDisposition struct {
	ConceptID         string `json:"concept_id"`
	Disposition       string `json:"disposition"`
	OwnerObjectTypeID string `json:"owner_object_type_id,omitempty"`
}

// DiagnosisWriteback is a write-back object type: one the application writes, tracking decisions on
// TrackedObjectTypeID, with the properties of the decision-tracking pattern and the object-operation
// action types that write them.
type DiagnosisWriteback struct {
	Name                string                       `json:"name"`
	Description         string                       `json:"description"`
	TrackedObjectTypeID string                       `json:"tracked_object_type_id"`
	Properties          []DiagnosisWritebackProperty `json:"properties"`
	Actions             []DiagnosisWritebackAction   `json:"actions"`
}

type DiagnosisWritebackProperty struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"`
	Role        string `json:"role"`
}

type DiagnosisWritebackAction struct {
	Kind       string   `json:"kind"`
	Name       string   `json:"name"`
	Parameters []string `json:"parameters"`
}

// DiagnosisApply is the body of applying a suggestion: the update time of the draft it was read on.
type DiagnosisApply struct {
	DraftUpdateTime int64 `json:"draft_update_time"`
}

// DiagnosisApplied is what applying a suggestion did.
type DiagnosisApplied struct {
	DraftUpdateTime int64                `json:"draft_update_time"`
	Suggestion      *DiagnosisSuggestion `json:"suggestion"`
}

// DiagnosisDismiss is the body of dismissing a suggestion.
type DiagnosisDismiss struct {
	Reason string `json:"reason"`
}

// DiagnosisRun is the reply of starting a diagnosis.
type DiagnosisRun struct {
	State string `json:"state"`
	RunID string `json:"run_id"`
}

//go:generate mockgen -source ../interfaces/diagnosis.go -destination ../interfaces/mock/mock_diagnosis.go
type DiagnosisService interface {
	// Run starts a diagnosis of the project's draft, for the project owner: the counts, then Ask AI,
	// in the background.
	Run(ctx context.Context, token string, projectID string, caller AccountInfo) (*DiagnosisRun, error)
	// Get returns the project's latest diagnosis to whoever can see the project.
	Get(ctx context.Context, token string, projectID string) (*Diagnosis, error)
	// Apply carries out a suggestion with the draft's own operations, for the project owner.
	Apply(ctx context.Context, token string, projectID string, suggestionID string, req DiagnosisApply,
		caller AccountInfo) (*DiagnosisApplied, error)
	// Dismiss records that a person dismissed a suggestion, as a decision of the draft.
	Dismiss(ctx context.Context, token string, projectID string, suggestionID string, req DiagnosisDismiss,
		caller AccountInfo) (*DiagnosisSuggestion, error)
}

type DiagnosisAccess interface {
	// GetDiagnosis returns the project's diagnosis, and false when it has none.
	GetDiagnosis(ctx context.Context, projectID string) (*Diagnosis, bool, error)
	// StartDiagnosis writes diagnosis, running, as the project's unless one is running since
	// staleBefore or later; it reports whether it did. The previous outcome is replaced.
	StartDiagnosis(ctx context.Context, diagnosis *Diagnosis, staleBefore int64) (bool, error)
	// SaveDiagnosis writes diagnosis while its run is still the project's; false means a later run
	// replaced it.
	SaveDiagnosis(ctx context.Context, diagnosis *Diagnosis) (bool, error)
}
