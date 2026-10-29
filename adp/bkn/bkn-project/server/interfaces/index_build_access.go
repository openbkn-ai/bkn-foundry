// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

// Outcomes of starting a table's local index build.
const (
	INDEX_BUILD_CREATED = "created"
	// INDEX_BUILD_EXISTING is a build task already under way, which the confirmation took as its own.
	INDEX_BUILD_EXISTING = "existing"
	// INDEX_BUILD_NO_PERMISSION is a runner without the catalog's task_manage permission.
	INDEX_BUILD_NO_PERMISSION = "no_permission"
	INDEX_BUILD_FAILED        = "failed"
)

// IndexBuild is a local index build a confirmation started for one table.
type IndexBuild struct {
	ProjectID    string
	Commit       string
	ResourceID   string
	ObjectTypeID string
	// TaskID is the vega-backend build task created or found under way; empty when none started.
	TaskID  string
	Outcome string
	// Reason says why the build could not start.
	Reason     string
	Runner     AccountInfo
	CreateTime int64
}

//go:generate mockgen -source ../interfaces/index_build_access.go -destination ../interfaces/mock/mock_index_build_access.go
type IndexBuildAccess interface {
	// RecordBuilds records builds; a build of the same commit and table replaces the one before.
	RecordBuilds(ctx context.Context, builds []*IndexBuild) error
	// ListBuilds returns the builds the project's confirmation of commit started, by resource id.
	ListBuilds(ctx context.Context, projectID string, commit string) ([]*IndexBuild, error)
}
