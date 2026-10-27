// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

//go:generate mockgen -source ../interfaces/project_access.go -destination ../interfaces/mock/mock_project_access.go
type ProjectAccess interface {
	// CreateProject inserts a project.
	CreateProject(ctx context.Context, project *Project) error
	// GetProject returns the project, and false when there is none.
	GetProject(ctx context.Context, id string) (*Project, bool, error)
	// ListProjects returns at most query.Limit projects after query.Cursor, newest first.
	ListProjects(ctx context.Context, query ProjectsQuery) ([]*Project, error)
	// CountProjects returns how many projects query selects, ignoring its cursor and limit.
	CountProjects(ctx context.Context, query ProjectsQuery) (int64, error)
	// CountUnarchivedProjects returns how many unarchived projects each of knIDs has;
	// a network with none is left out.
	CountUnarchivedProjects(ctx context.Context, knIDs []string) (map[string]int64, error)
	// SetProjectArchived archives or unarchives the project.
	SetProjectArchived(ctx context.Context, id string, archived bool, updateTime int64) error
}
