// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

// SourceFieldRule records a human's placement and authority decision, not an
// extracted value. All participating definitions must already exist in draft.
type SourceFieldRule struct {
	Name                    string   `json:"name"`
	Category                string   `json:"category"`
	SourceOfRecord          string   `json:"source_of_record"`
	DestinationObjectTypeID string   `json:"destination_object_type_id"`
	EvidenceOrder           []string `json:"evidence_order,omitempty"`
}

type DualSourceChoice struct {
	SystemObjectTypeID string            `json:"system_object_type_id"`
	FileObjectTypeID   string            `json:"file_object_type_id"`
	RelationTypeID     string            `json:"relation_type_id"`
	Fields             []SourceFieldRule `json:"fields"`
}

type DraftDualSourcePlan struct {
	State string `json:"state,omitempty"`
	DualSourceChoice
	ConfirmedBy AccountInfo `json:"confirmed_by"`
	ConfirmedAt int64       `json:"confirmed_at"`
	Signature   string      `json:"signature"`
}

// DualSourceDecision is an explicit model-edit request. A caller can submit
// choices for review without confirming them; only the server sets audit proof.
type DualSourceDecision struct {
	Clear bool `json:"clear,omitempty"`
	DualSourceChoice
	Confirm bool `json:"confirm"`
}
