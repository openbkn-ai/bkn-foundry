// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

// KBPublication is private process metadata, independent of the mutable project.
// Runtime models and source evidence live only in its private Vega resource.
// Holds are anonymous release credentials; this record is never a business DTO.
type KBPublication struct {
	NetworkID        string           `json:"network_id"`
	ProjectID        string           `json:"project_id"`
	LineageID        string           `json:"lineage_id"`
	DefinitionCommit string           `json:"definition_commit"`
	ModelFingerprint string           `json:"model_fingerprint"`
	GenerationID     string           `json:"generation_id"`
	FullBuildID      string           `json:"full_build_id"`
	Publisher        AccountInfo      `json:"publisher"`
	Snapshot         DraftDatasetSpec `json:"snapshot"`
	RuntimeRowID     string           `json:"runtime_row_id"`
	RuntimeDigest    string           `json:"runtime_digest"`
	DefinitionDigest string           `json:"definition_digest"`
	Ranges           []KBSourceRange  `json:"ranges"`
	Holds            []KBHoldGroup    `json:"holds"`
	State            string           `json:"state"`
	Revision         int64            `json:"revision"`
	DraftUpdateTime  int64            `json:"draft_update_time"`
	CreateTime       int64            `json:"create_time"`
	UpdateTime       int64            `json:"update_time"`
}

// KBPublicationAccess prepares release intent before remote writes and seals a
// generation under the same project lock used by extraction/linking claims.
// Sealing refuses active or uncertain writers; build claims refuse any sealed
// generation. These guards must be deployed together before enabling publishing.
type KBPublicationAccess interface {
	Get(context.Context, string) (*KBPublication, error)
	Prepare(context.Context, *KBPublication) (bool, error)
	Seal(context.Context, string, string, int64) (bool, error)
	MarkMounted(context.Context, string, string, int64) (bool, error)
	Activate(context.Context, string, string, int64) (bool, error)
}

// KBPublicationDiscovery pages private publication keys for the licensed worker.
// It does not make a ready release the current consumer target.
type KBPublicationDiscovery interface {
	ActiveNetworks(context.Context, string, int) ([]string, error)
}
