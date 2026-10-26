// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

// DriftReport compares the live definitions of a network with main in its
// lineage repository: Added, Changed and Removed say how live differs from main.
type DriftReport struct {
	LineageID string `json:"lineage_id"`
	KNID      string `json:"kn_id"`
	PathDiff
	// Capabilities says the same of the functions/ and skills/ files of the
	// toolboxes and Skills main's release.yaml lists: toolboxes as impex exports
	// them, Skills as their published release, not their draft.
	Capabilities PathDiff `json:"capabilities"`
	// Live is the exported definition set the report was made from.
	Live map[string][]byte `json:"-"`
	// LiveCapabilities are the live functions/ and skills/ files, keyed from the repository root.
	LiveCapabilities map[string][]byte `json:"-"`
}

// Empty reports whether neither the definitions nor the functions and Skills drifted.
func (r *DriftReport) Empty() bool {
	return r.PathDiff.Empty() && r.Capabilities.Empty()
}

//go:generate mockgen -source ../interfaces/drift_service.go -destination ../interfaces/mock/mock_drift_service.go
type DriftService interface {
	// DriftCheck exports the live definitions of knID, and the functions and Skills main's
	// release.yaml lists, and compares them with main of the lineage.
	DriftCheck(ctx context.Context, token string, lineageID string, knID string) (*DriftReport, error)
	// CommitLiveToMain commits the live definitions, functions and Skills of the report to main
	// and returns the new head. release.yaml is kept: it records what the last release used.
	CommitLiveToMain(ctx context.Context, report *DriftReport, author GitAuthor) (string, error)
}
