// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

// A project package is a zip file a project is exported to and imported from. It holds the
// project's metadata, configuration and generated material — never data, indexes, regression
// results or the original files of a knowledge base:
//
//	manifest.json                    PackageManifest
//	requirements/documents.json      requirement documents, the text extracted from them as stored
//	requirements/specification.json  the requirement specification
//	model/draft.json                 the ontology draft with its decisions
//	model/confirmed/bkn/...          BKN files the last confirmation wrote on the project branch
//	editions/<name>/...              parts an edition adds, such as terms and knowledge-base selections

// PROJECT_PACKAGE_FORMAT names the format in a manifest; PROJECT_PACKAGE_VERSION is the version
// written and the only one read.
const (
	PROJECT_PACKAGE_FORMAT    = "bkn-project-package"
	PROJECT_PACKAGE_VERSION   = 1
	PROJECT_PACKAGE_MAX_BYTES = 50 << 20
	PROJECT_PACKAGE_EXTENSION = ".bknproj"
)

// PackageManifest describes a project package.
type PackageManifest struct {
	Format         string            `json:"format"`
	FormatVersion  int               `json:"format_version"`
	ExportTime     int64             `json:"export_time"`
	Name           string            `json:"name"`
	NamingLanguage string            `json:"naming_language"`
	KN             PackageNetwork    `json:"kn"`
	Catalogs       []PackageCatalog  `json:"catalogs"`
	Tables         []PackageTable    `json:"tables"`
	Contents       PackageContents   `json:"contents"`
	Files          map[string]string `json:"files"`
}

// PackageNetwork is the network the project was made on, by id and name.
type PackageNetwork struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// PackageCatalog is a data catalog the project read, by id and name.
type PackageCatalog struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// PackageTable is a table the draft reads, so an import can find it by name in the catalog it is
// mapped to.
type PackageTable struct {
	ID        string `json:"id"`
	CatalogID string `json:"catalog_id"`
	Name      string `json:"name"`
}

// PackageContents counts what a package holds.
type PackageContents struct {
	Documents      int  `json:"documents"`
	Requirement    bool `json:"requirement"`
	Draft          bool `json:"draft"`
	ConfirmedFiles int  `json:"confirmed_files"`
	// Editions are the parts an edition added, by name.
	Editions []string `json:"editions"`
}

// ProjectPackagePreview is what a package holds, as an import shows it.
type ProjectPackagePreview struct {
	FormatVersion  int              `json:"format_version"`
	Name           string           `json:"name"`
	NamingLanguage string           `json:"naming_language"`
	KN             PackageNetwork   `json:"kn"`
	Catalogs       []PackageCatalog `json:"catalogs"`
	Contents       PackageContents  `json:"contents"`
}

// ImportProjectOptions is where a package is imported to: the project's name, its network, and
// the catalog of this environment each catalog of the package is read from.
type ImportProjectOptions struct {
	Name             string            `json:"name"`
	KNID             string            `json:"kn_id"`
	NewKN            *NewKNRequest     `json:"new_kn"`
	EnableVersioning bool              `json:"enable_versioning"`
	CatalogMapping   map[string]string `json:"catalog_mapping"`
}

// AdminAccess tells whether the caller is a platform administrator.
//
//go:generate mockgen -source ../interfaces/project_package.go -destination ../interfaces/mock/mock_project_package.go
type AdminAccess interface {
	// IsAdmin is false when it cannot be told.
	IsAdmin(ctx context.Context, token string) bool
}
