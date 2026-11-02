// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

// KBReference contains decisions and immutable Dataset references only. The
// annotation and prediction bodies stay in Vega, never in this process record.
type KBReference struct {
	ID                string            `json:"id"`
	ProjectID         string            `json:"project_id"`
	Owner             AccountInfo       `json:"owner"`
	ModelFingerprint  string            `json:"model_fingerprint"`
	SelectionID       string            `json:"selection_id"`
	SelectionRevision int64             `json:"selection_revision"`
	DraftUpdateTime   int64             `json:"draft_update_time"`
	GenerationID      string            `json:"generation_id"`
	Revision          int64             `json:"revision"`
	Dataset           DraftDatasetSpec  `json:"dataset"`
	Files             []KBReferenceFile `json:"files"`
	UpdateTime        int64             `json:"update_time"`
}
type KBReferenceFile struct {
	ReviewRevision int64          `json:"review_revision"`
	File           KBResourceFile `json:"file"`
	RowID          string         `json:"row_id"`
	InputDigest    string         `json:"input_digest"`
	ReviewHash     string         `json:"review_hash"`
	Reviewer       AccountInfo    `json:"reviewer"`
	ReviewedAt     int64          `json:"reviewed_at"`
}
type KBReferenceAccess interface {
	Get(context.Context, string, string) (*KBReference, error)
	// Put performs an owner/source/draft CAS; expected zero means create. It
	// never persists partial Dataset writes or a stale model's human decision.
	Put(context.Context, *KBReference, int64) (bool, error)
}

type KBAcceptanceCounts struct {
	Expected       int            `json:"expected"`
	Predicted      int            `json:"predicted"`
	Correct        int            `json:"correct"`
	ExpectedLinks  int            `json:"expected_links"`
	PredictedLinks int            `json:"predicted_links"`
	CorrectLinks   int            `json:"correct_links"`
	Precision      *float64       `json:"precision"`
	Recall         *float64       `json:"recall"`
	Linking        *float64       `json:"linking"`
	Issues         map[string]int `json:"issues"`
	Passed         bool           `json:"passed"`
}
