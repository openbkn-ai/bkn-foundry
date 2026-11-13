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
	// NamingLanguage defaults to the creator's language when empty.
	NamingLanguage string `json:"naming_language"`
	// EnableVersioning confirms turning versioning on for KNID when it does not keep versions yet:
	// from then on every edit to it takes effect only when published.
	EnableVersioning bool `json:"enable_versioning"`
}

// RenameProjectRequest is the body of PATCH /projects/{id}. A project's content language is set
// when it is created and never changes: what was generated in it would mix languages.
type RenameProjectRequest struct {
	Name string `json:"name"`
}

// SetProjectCatalogsRequest replaces the data catalogs a project drafts from.
type SetProjectCatalogsRequest struct {
	CatalogIDs []string `json:"catalog_ids"`
}

// NewKNRequest names the network a project creates; bkn-backend makes up an id when ID is empty.
type NewKNRequest struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ProjectEntry is a project as the API returns it.
type ProjectEntry struct {
	*Project
	// OtherProjects counts the other unarchived projects of the same network.
	OtherProjects int64 `json:"other_projects"`
	// OwnerName is the owner's display name, for people who may not list users; empty when the
	// directory does not know it.
	OwnerName string `json:"owner_name,omitempty"`
	// CanManage tells whether the caller may rename and delete the project: its owner or an
	// administrator.
	CanManage bool `json:"can_manage"`
	// NetworkCatalogIDs are the catalogs among CatalogIDs that the network's object types read:
	// the project takes them from the network, which owns them. Set only on a project read for
	// its catalogs, and only on a network the project did not create.
	NetworkCatalogIDs []string `json:"network_catalog_ids,omitempty"`
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
	// when empty), "true" or "all"; name matches names containing it. A page starts after
	// cursor, or after skipping offset projects for a page by number; the two do not combine.
	ListProjects(ctx context.Context, token string, archived string, name string, limit int,
		offset int, cursor string) (*ProjectList, error)
	// GetProject returns a visible project, and 404 for one the caller cannot see.
	GetProject(ctx context.Context, token string, id string) (*ProjectEntry, error)
	// GetProjectForStatus is GetProject, but returns its owner a project whose network bkn-backend
	// no longer has, with networkMissing true; to anyone else that project is not found.
	GetProjectForStatus(ctx context.Context, token string, id string, caller AccountInfo) (entry *ProjectEntry,
		networkMissing bool, err error)
	// SetProjectArchived archives or unarchives a visible project; only its owner may, also when
	// its network was deleted.
	SetProjectArchived(ctx context.Context, token string, id string, archived bool,
		caller AccountInfo) (*ProjectEntry, error)
	// ResolveCatalogs sets the entry's catalogs to those the project reads, as the caller sees
	// them: on a network the project did not create, the catalogs the network's object types read,
	// then those the owner added that still exist.
	ResolveCatalogs(ctx context.Context, token string, entry *ProjectEntry, caller AccountInfo)
	// SetProjectCatalogs replaces the project's data catalogs; only its owner may, and not with a
	// catalog that no longer exists (409). The catalogs the network reads are its own, not stored:
	// they stay the project's whether they are given or not.
	SetProjectCatalogs(ctx context.Context, token string, id string, catalogIDs []string,
		caller AccountInfo) (*ProjectEntry, error)
	// RenameProject renames a visible project; only its owner or an administrator may.
	RenameProject(ctx context.Context, token string, id string, name string, caller AccountInfo) (*ProjectEntry, error)
	// DeleteProject deletes what a project owns: its working material, its edition data and its
	// branch. A prepared release that is not published is withdrawn from the network's draft. The
	// network and its published versions stay. Only the owner or an administrator may.
	DeleteProject(ctx context.Context, token string, id string, caller AccountInfo) error
	// MarkManageable sets CanManage of each entry for the caller.
	MarkManageable(ctx context.Context, token string, caller AccountInfo, entries ...*ProjectEntry)
	// ExportProject packs a visible project into a project package, returned with its file name.
	ExportProject(ctx context.Context, token string, id string, caller AccountInfo) ([]byte, string, error)
	// PreviewPackage reads what a project package holds, for choosing where to import it.
	PreviewPackage(ctx context.Context, pkg []byte) (*ProjectPackagePreview, error)
	// ImportPackage creates a project from a package: its content language, requirement documents,
	// specification and draft, unconfirmed.
	ImportPackage(ctx context.Context, token string, pkg []byte, options ImportProjectOptions, owner AccountInfo,
		author GitAuthor) (*ProjectEntry, error)
}
