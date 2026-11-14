// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

// DatasetField and DatasetLayout describe physical storage, never instance data.
// Mapping preserves model names when Vega reserves a physical field name.
type DatasetField struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	DisplayName  string `json:"display_name"`
	OriginalName string `json:"original_name"`
	Description  string `json:"description"`
}

type DatasetLayout struct {
	Fields      []DatasetField    `json:"fields"`
	Mapping     map[string]string `json:"mapping"`
	PrimaryKeys []string          `json:"primary_keys"`
	Fingerprint string            `json:"fingerprint"`
}

// DraftDatasetPlan is a durable preparation journal for one unpublished target
// generation. Replacing a model creates another generation; no published Dataset
// is changed while preparing the replacement.
type DraftDatasetPlan struct {
	Owner             AccountInfo        `json:"owner"`
	ID                string             `json:"id"`
	ModelFingerprint  string             `json:"model_fingerprint"`
	SelectionID       string             `json:"selection_id"`
	SelectionRevision int64              `json:"selection_revision"`
	State             string             `json:"state"`
	Datasets          []DraftDatasetSpec `json:"datasets"`
}

type DraftDatasetSpec struct {
	Owner          AccountInfo   `json:"owner"`
	ProjectID      string        `json:"project_id"`
	GenerationID   string        `json:"generation_id"`
	ObjectTypeID   string        `json:"object_type_id"`
	RelationTypeID string        `json:"relation_type_id,omitempty"`
	ResourceID     string        `json:"resource_id"`
	CatalogID      string        `json:"catalog_id"`
	Name           string        `json:"name"`
	Layout         DatasetLayout `json:"layout"`
	Ready          bool          `json:"ready"`
}

// The states a DatasetPreparation reads: stale is a preparation for another model or file
// selection than the current one.
const (
	DATASET_PREPARATION_PENDING   = "pending"
	DATASET_PREPARATION_PREPARING = "preparing"
	DATASET_PREPARATION_READY     = "ready"
	DATASET_PREPARATION_FAILED    = "failed"
	DATASET_PREPARATION_STALE     = "stale"
)

// DatasetPreparation is a read-time projection of the journal for the current
// model and source selection. It does not expose physical resource identities.
type DatasetPreparation struct {
	State string `json:"state"`
	Ready int    `json:"ready"`
	Total int    `json:"total"`
}
