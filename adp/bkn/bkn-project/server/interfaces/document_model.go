// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

// DraftDocumentModel is proposal/process metadata, not extracted instance data.
// Layer, selection identity and adoption are checked/set by core, never trusted
// from an Agent. An absent Document retains the existing table workflow.
type DraftDocumentModel struct {
	Role                 string                `json:"role"`
	Layer                string                `json:"layer"`
	Reason               string                `json:"reason"`
	Citations            []DraftSourceCitation `json:"citations"`
	TimeProperty         string                `json:"time_property,omitempty"`
	ParticipantRelations []string              `json:"participant_relations,omitempty"`
	SelectionID          string                `json:"selection_id"`
	SelectionRevision    int64                 `json:"selection_revision"`
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
	Mode               string `json:"mode"`
	MentionProperty    string `json:"mention_property,omitempty"`
	IdentityProperty   string `json:"identity_property,omitempty"`
	SourceNameProperty string `json:"source_name_property,omitempty"`
	TargetNameProperty string `json:"target_name_property,omitempty"`
}
