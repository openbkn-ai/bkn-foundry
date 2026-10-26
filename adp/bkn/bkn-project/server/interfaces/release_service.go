// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

//go:generate mockgen -source ../interfaces/release_service.go -destination ../interfaces/mock/mock_release_service.go
type ReleaseService interface {
	// ListReleases returns one page of a lineage's releases. cursor is the
	// opaque next_cursor of the previous page, or empty for the first page.
	ListReleases(ctx context.Context, lineageID string, limit int, cursor string) (*ReleaseList, error)
	// GetRelease returns the release that published the network.
	GetRelease(ctx context.Context, networkID string) (*Release, error)
}
