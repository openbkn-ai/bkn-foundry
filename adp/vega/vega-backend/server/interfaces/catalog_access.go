// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
	"database/sql"
)

// CatalogAccess defines catalog data access interface.
//
//go:generate mockgen -source ../interfaces/catalog_access.go -destination ../interfaces/mock/mock_catalog_access.go
type CatalogAccess interface {
	// Create creates a new Catalog.
	Create(ctx context.Context, tx *sql.Tx, catalog *Catalog) error
	// GetByID retrieves a Catalog by ID.
	GetByID(ctx context.Context, id string) (*Catalog, error)
	// GetByIDs retrieves a Catalog by IDs.
	GetByIDs(ctx context.Context, ids []string) ([]*Catalog, error)
	// AttachListExtensions loads or clears root-level extensions by List query parameters (for list to call after GetByIDs).
	AttachListExtensions(ctx context.Context, params CatalogsQueryParams, catalogs []*Catalog) error
	// GetByName retrieves a Catalog by name.
	GetByName(ctx context.Context, name string) (*Catalog, error)
	// List lists Catalogs with filters.
	List(ctx context.Context, params CatalogsQueryParams) ([]*Catalog, int64, error)
	// ListIDs lists Catalog IDs with filters.
	ListIDs(ctx context.Context, params CatalogsQueryParams) ([]string, error)
	// ListInternalIDs lists the ids of all internal system directories (grouped by internal_catalog type when used for permission verification).
	ListInternalIDs(ctx context.Context) ([]string, error)
	// Update updates a Catalog.
	Update(ctx context.Context, tx *sql.Tx, catalog *Catalog) error
	// DeleteByID deletes a Catalog by ID.
	DeleteByID(ctx context.Context, tx *sql.Tx, id string) error
	// UpdateHealthCheckStatus updates Catalog health check status.
	UpdateHealthCheckStatus(ctx context.Context, id string, status CatalogHealthCheckStatus) error
	// UpdateEnabled updates Catalog enabled status and health check status.
	UpdateEnabled(ctx context.Context, id string, enabled bool, status CatalogHealthCheckStatus, updateTime int64, updater AccountInfo) error

	// UpdateMetadata updates a Catalog metadata.
	UpdateMetadata(ctx context.Context, id string, metadata map[string]any) error

	// ListAuthResources lists catalog auth resources with filters.
	ListAuthResources(ctx context.Context, params AuthResourceQueryParams) ([]*AuthResourceEntry, error)
}
