// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

// DraftDocumentModel is proposal/process metadata, not extracted instance data.
// Layer, selection identity and adoption are checked/set by core, never trusted
// from an Agent. An absent Document retains the existing table workflow.
type DraftDocumentModel struct {
	FactResolution       *FactResolutionRule       `json:"fact_resolution,omitempty"`
	FilePeriod           *FileEffectivePeriod      `json:"file_period,omitempty"`
	SentenceIdentity     *SentenceBusinessIdentity `json:"sentence_identity,omitempty"`
	FileIdentity         *FileBusinessIdentity     `json:"file_identity,omitempty"`
	DualSource           *DraftDualSourcePlan      `json:"dual_source,omitempty"`
	Role                 string                    `json:"role"`
	Layer                string                    `json:"layer"`
	Reason               string                    `json:"reason"`
	Citations            []DraftSourceCitation     `json:"citations"`
	TimeProperty         string                    `json:"time_property,omitempty"`
	ParticipantRelations []string                  `json:"participant_relations,omitempty"`
	SelectionID          string                    `json:"selection_id"`
	SelectionRevision    int64                     `json:"selection_revision"`
}

type DraftSourceCitation struct {
	KnowledgeBaseID  string `json:"knowledge_base_id"`
	FileID           string `json:"file_id"`
	ContentVersionID string `json:"content_version_id"`
	BlockID          string `json:"block_id"`
	Start            int    `json:"start"`
	End              int    `json:"end"`
	Quote            string `json:"quote,omitempty"`
}

type DraftExtractionField struct {
	Instruction string                `json:"instruction"`
	Examples    []DraftSourceCitation `json:"examples"`
}

// DraftDocumentMapping has no table projection. Linking resolves a mention to a
// target identity; same_document joins document_id plus the explicit name pair.
type DraftDocumentMapping struct {
	BusinessIdentity   *DraftBusinessIdentityEvidence `json:"business_identity,omitempty"`
	Mode               string                         `json:"mode"`
	MentionProperty    string                         `json:"mention_property,omitempty"`
	IdentityProperty   string                         `json:"identity_property,omitempty"`
	SourceNameProperty string                         `json:"source_name_property,omitempty"`
	TargetNameProperty string                         `json:"target_name_property,omitempty"`
}

// DraftDocumentSettings contains explicit human modeling choices; role, layer
// and source evidence remain server-owned metadata.
type FieldSourceDecision struct {
	Property       string `json:"property"`
	SourceOfRecord string `json:"source_of_record"`
	Confirm        bool   `json:"confirm"`
}

type DraftDocumentSettings struct {
	FactResolution       *FactResolutionDecision   `json:"fact_resolution,omitempty"`
	FieldSources         []FieldSourceDecision     `json:"field_sources,omitempty"`
	FilePeriod           *FilePeriodDecision       `json:"file_period,omitempty"`
	SentenceIdentity     *SentenceIdentityDecision `json:"sentence_identity,omitempty"`
	FileIdentity         *FileIdentityDecision     `json:"file_identity,omitempty"`
	Derive               *DraftDocumentDerivation  `json:"derive,omitempty"`
	DualSource           *DualSourceDecision       `json:"dual_source,omitempty"`
	TimeProperty         string                    `json:"time_property"`
	ParticipantRelations []string                  `json:"participant_relations"`
}

// FileEffectivePeriod maps existing extracted fields only. The service records
// a human confirmation; an Agent cannot choose or forge this authority.
type FileEffectivePeriod struct {
	From        string      `json:"from,omitempty"`
	Until       string      `json:"until,omitempty"`
	ConfirmedBy AccountInfo `json:"confirmed_by"`
	ConfirmedAt int64       `json:"confirmed_at"`
}
type FilePeriodDecision struct {
	From    string `json:"from,omitempty"`
	Until   string `json:"until,omitempty"`
	Confirm bool   `json:"confirm"`
	Clear   bool   `json:"clear,omitempty"`
}

type FileBusinessIdentity struct {
	Property    string      `json:"property"`
	ConfirmedBy AccountInfo `json:"confirmed_by"`
	ConfirmedAt int64       `json:"confirmed_at"`
}
type FileIdentityDecision struct {
	Property string `json:"property"`
	Confirm  bool   `json:"confirm"`
	Clear    bool   `json:"clear,omitempty"`
}

// DraftBusinessIdentityEvidence is created only by a human-requested full-table
// verification. It never changes the system object's original primary key.
// Confirmation/builds must recheck current counts; this snapshot is not a lease
// guaranteeing that an independently changing source table remains unique.
type DraftBusinessIdentityEvidence struct {
	TargetObjectTypeID string      `json:"target_object_type_id"`
	ResourceID         string      `json:"resource_id"`
	Property           string      `json:"property"`
	MappedField        string      `json:"mapped_field"`
	Type               string      `json:"type"`
	Rows               int64       `json:"rows"`
	Distinct           int64       `json:"distinct"`
	Nulls              int64       `json:"nulls"`
	Blanks             int64       `json:"blanks"`
	ConfirmedBy        AccountInfo `json:"confirmed_by"`
	CheckedAt          int64       `json:"checked_at"`
}

// DraftDocumentDerivation explicitly copies a reviewed subset of existing fields.
// Evidence and extraction instructions are always copied from the source model.
type DraftDocumentDerivation struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Role     string   `json:"role"`
	Fields   []string `json:"fields"`
	Identity string   `json:"identity"`
	Confirm  bool     `json:"confirm"`
}

// Sentence keys distinguish multiple records of one type in the same verified
// source sentence. They are not a global-uniqueness assertion.
type SentenceBusinessIdentity struct {
	Properties  []string    `json:"properties"`
	ConfirmedBy AccountInfo `json:"confirmed_by"`
	ConfirmedAt int64       `json:"confirmed_at"`
}
type SentenceIdentityDecision struct {
	Properties []string `json:"properties"`
	Confirm    bool     `json:"confirm"`
	Clear      bool     `json:"clear,omitempty"`
}

// FactResolutionRule confirms the sole business value and an adopted entity
// relationship. Validity dates remain independently extracted native claims.
type FactResolutionRule struct {
	ValueProperty     string      `json:"value_property"`
	SubjectRelationID string      `json:"subject_relation_id"`
	RuleSignature     string      `json:"rule_signature"`
	State             string      `json:"state,omitempty"`
	ConfirmedBy       AccountInfo `json:"confirmed_by"`
	ConfirmedAt       int64       `json:"confirmed_at"`
}
type FactResolutionDecision struct {
	ValueProperty     string `json:"value_property,omitempty"`
	SubjectRelationID string `json:"subject_relation_id,omitempty"`
	Confirm           bool   `json:"confirm"`
	Clear             bool   `json:"clear,omitempty"`
}

// documentTechnicalColumns are written on every business row of a document object for building
// and refreshing; a published network does not declare them.
var documentTechnicalColumns = map[string]bool{
	"entity_id": true, "document_id": true, "content_version_id": true, "block_id": true,
	"source_start": true, "source_end": true, "source_quote": true,
	"extraction_version": true, "model_fingerprint": true,
}

// DocumentHiddenColumn tells whether a published document object of role leaves property out of
// its definition. Its Dataset column stays, for the build and refresh.
func DocumentHiddenColumn(role, property string) bool {
	switch role {
	case "entity", "file", "in_document", "fact", "event":
		return documentTechnicalColumns[property]
	case "evidence_document", "evidence_block", "evidence_image", "evidence_media":
		return property == "extraction_version" || property == "model_fingerprint"
	}
	return false
}
