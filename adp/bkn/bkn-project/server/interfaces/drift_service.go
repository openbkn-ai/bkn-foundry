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
	// Live is the exported definition set the report was made from.
	Live map[string][]byte `json:"-"`
}

//go:generate mockgen -source ../interfaces/drift_service.go -destination ../interfaces/mock/mock_drift_service.go
type DriftService interface {
	// DriftCheck exports the live definitions of knID and compares them with main of the lineage.
	DriftCheck(ctx context.Context, token string, lineageID string, knID string) (*DriftReport, error)
	// CommitLiveToMain commits the live definitions of the report to main and returns the new head.
	CommitLiveToMain(ctx context.Context, report *DriftReport, author GitAuthor) (string, error)
}
