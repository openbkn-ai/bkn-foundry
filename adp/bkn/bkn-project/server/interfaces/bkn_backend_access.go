// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
)

// CapabilitySkip is one declared capability bkn-backend could not bind on
// import (bkn-backend interfaces.CapabilitySkip).
type CapabilitySkip struct {
	CapabilityType string `json:"capability_type"`
	Name           string `json:"name,omitempty"`
	BoxName        string `json:"box_name,omitempty"`
	DeclaredID     string `json:"declared_id,omitempty"`
	DeclaredBoxID  string `json:"declared_box_id,omitempty"`
	Reason         string `json:"reason"`
	Detail         string `json:"detail,omitempty"`
}

// CapabilityImportReport is what an import did with the declared
// capabilities (bkn-backend interfaces.CapabilityImportReport).
type CapabilityImportReport struct {
	Bound   int               `json:"bound"`
	Skipped []*CapabilitySkip `json:"skipped"`
}

// BKNImportResult is the body of a successful bkn-backend import. A non-empty
// Capabilities.Skipped means the network was created without some of its
// Skills or functions bound, although the import returned 200.
type BKNImportResult struct {
	KNID         string                  `json:"kn_id"`
	Capabilities *CapabilityImportReport `json:"capabilities"`
}

// BknBackendError is a non-2xx reply from bkn-backend, kept whole so the
// caller can tell a refused permission from an unreachable service.
type BknBackendError struct {
	HTTPCode int
	httperrors.BaseError
}

func (e *BknBackendError) Error() string {
	return fmt.Sprintf("bkn-backend returned HTTP %d: %s %s", e.HTTPCode, e.ErrorCode, e.Description)
}

// BknBackendAccess exports and imports knowledge network definitions through
// the bkn-backend public BKN API. token is the caller's Authorization header
// (hydra.Visitor.TokenID): bkn-backend checks the caller's own permissions.
//
//go:generate mockgen -source ../interfaces/bkn_backend_access.go -destination ../interfaces/mock/mock_bkn_backend_access.go
type BknBackendAccess interface {
	// ExportBKN returns the files of the network's main branch, keyed by their path in the export tar.
	ExportBKN(ctx context.Context, token string, knID string) (map[string][]byte, error)
	// ImportBKN imports files into the main branch with import_mode=overwrite.
	ImportBKN(ctx context.Context, token string, files map[string][]byte) (*BKNImportResult, error)
}
