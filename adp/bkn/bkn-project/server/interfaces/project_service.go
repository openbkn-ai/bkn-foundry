// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

// CreateProjectRequest is the body of POST /projects. Exactly one of KNID, an
// existing network, and NewKN, a network to create, is set.
type CreateProjectRequest struct {
	Name       string        `json:"name"`
	KNID       string        `json:"kn_id"`
	NewKN      *NewKNRequest `json:"new_kn"`
	CatalogIDs []string      `json:"catalog_ids"`
}

// NewKNRequest names the network a project creates; bkn-backend makes up an id when ID is empty.
// SetProjectCatalogsRequest replaces the data catalogs a project drafts from.
type SetProjectCatalogsRequest struct {
	CatalogIDs []string `json:"catalog_ids"`
}

type NewKNRequest struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ProjectEntry is a project as the API returns it.
type ProjectEntry struct {
	*Project
	// OtherProjects counts the other unarchived projects of the same network.
	OtherProjects int64 `json:"other_projects"`
}

type ProjectList struct {
	Entries    []*ProjectEntry `json:"entries"`
	Total      int64           `json:"total"`
	NextCursor *string         `json:"next_cursor"`
}

// ProjectService serves projects. token is the caller's Authorization header: a
// project is visible only to callers who can see its network in bkn-backend.
//
//go:generate mockgen -source ../interfaces/project_service.go -destination ../interfaces/mock/mock_project_service.go
type ProjectService interface {
	// CreateProject creates the network when asked to, starts its lineage unless it has one,
	// and gives the project a branch of it.
	CreateProject(ctx context.Context, token string, req CreateProjectRequest, owner AccountInfo,
		author GitAuthor) (*ProjectEntry, error)
	// ListProjects lists the visible projects newest first. archived is "false" (the default
	// when empty), "true" or "all"; name matches names containing it.
	ListProjects(ctx context.Context, token string, archived string, name string, limit int,
		cursor string) (*ProjectList, error)
	// GetProject returns a visible project, and 404 for one the caller cannot see.
	GetProject(ctx context.Context, token string, id string) (*ProjectEntry, error)
	// SetProjectArchived archives or unarchives a visible project; only its owner may.
	SetProjectArchived(ctx context.Context, token string, id string, archived bool,
		caller AccountInfo) (*ProjectEntry, error)
	// SetProjectCatalogs replaces the project's data catalogs; only its owner may.
	SetProjectCatalogs(ctx context.Context, token string, id string, catalogIDs []string,
		caller AccountInfo) (*ProjectEntry, error)
}
