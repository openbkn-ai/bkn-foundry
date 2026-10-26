// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

//go:generate mockgen -source ../interfaces/release_access.go -destination ../interfaces/mock/mock_release_access.go
type ReleaseAccess interface {
	// ListReleases returns at most query.Limit releases of the lineage after query.Cursor, newest first.
	ListReleases(ctx context.Context, query ReleasesQuery) ([]*Release, error)
	// CountReleases returns how many releases the lineage has.
	CountReleases(ctx context.Context, lineageID string) (int64, error)
	// GetReleaseByNetworkID returns the release that published the network, and false when there is none.
	GetReleaseByNetworkID(ctx context.Context, networkID string) (*Release, bool, error)
	// CreateRelease inserts a release. A second release of the same network fails on the unique key.
	CreateRelease(ctx context.Context, release *Release) error
}
