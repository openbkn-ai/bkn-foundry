// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
)

// BKNService defines the BKN import and export service interface.
//
//go:generate mockgen -source ../interfaces/bkn_service.go -destination ../interfaces/mock/mock_bkn_service.go
type BKNService interface {
	// ExportToTar exports a knowledge network as a tar archive.
	ExportToTar(ctx context.Context, knID string, branch string) ([]byte, error)
}
