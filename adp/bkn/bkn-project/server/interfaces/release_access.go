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
	// GetRelease returns the lineage's release of version, and false when there is none.
	GetRelease(ctx context.Context, lineageID string, version string) (*Release, bool, error)
	// PrepareRelease inserts a prepared release, or replaces the one of the same version while it is
	// still prepared. It reports false when that version is already published.
	PrepareRelease(ctx context.Context, release *Release) (bool, error)
	// SavePublishRequest keeps the publish sent for a prepared release, or forgets it when nil.
	SavePublishRequest(ctx context.Context, releaseID string, req *KNVersionPublish) error
	// MarkPublished records the network version a prepared release was published as, with its notes.
	MarkPublished(ctx context.Context, releaseID string, knVersion int, notes string, updateTime int64) error
}
