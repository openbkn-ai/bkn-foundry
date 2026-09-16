// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "context"

// CatalogService defines catalog business logic interface.
//
//go:generate mockgen -source ../interfaces/catalog_service.go -destination ../interfaces/mock/mock_catalog_service.go
type CatalogService interface {
	// Create creates a new Catalog.
	Create(ctx context.Context, req *CatalogRequest, allowUnhealthy bool) (string, error)
	// Get retrieves a Catalog by ID.
	GetByID(ctx context.Context, id string, withSensitiveFields bool) (*Catalog, error)
	// GetByIDs retrieves Catalogs by IDs. Callers must provide unique IDs.
	GetByIDs(ctx context.Context, ids []string) ([]*Catalog, error)
	// List lists Catalogs with filters.
	List(ctx context.Context, params CatalogsQueryParams) ([]*CatalogSummary, int64, error)
	// ListConnectorTypeStats groups visible catalogs matching params by connector type.
	ListConnectorTypeStats(ctx context.Context, params CatalogsQueryParams) ([]*CatalogConnectorTypeStat, error)
	// Update updates a Catalog.
	Update(ctx context.Context, req *CatalogRequest, allowUnhealthy bool) error
	// SetEnabled updates Catalog enabled state.
	SetEnabled(ctx context.Context, id string, enabled bool) (*Catalog, error)
	// DeleteByID deletes a Catalog by ID.
	DeleteByID(ctx context.Context, id string) error
	// GetDeletionImpact returns the current impact and guards for deleting a catalog.
	GetDeletionImpact(ctx context.Context, id string) (*CatalogDeletionImpact, error)
	// CheckExistByID checks if a Catalog exists by ID.
	CheckExistByID(ctx context.Context, id string) (bool, error)
	// CheckExistByName checks if a Catalog exists by name.
	CheckExistByName(ctx context.Context, name string) (bool, error)
	// TestConnection tests catalog connection.
	TestConnection(ctx context.Context, catalogID string) (*CatalogHealthCheckStatus, error)
	// TestConnectionConfig tests an unpersisted physical catalog connection configuration.
	TestConnectionConfig(ctx context.Context, req *CatalogConnectionTestRequest) (*CatalogHealthCheckStatus, error)

	// UpdateMetadata updates a Catalog metadata.
	UpdateMetadata(ctx context.Context, id string, metadata map[string]any) error

	// ListAuthResources lists catalog auth resources with filters.
	ListAuthResources(ctx context.Context, params AuthResourceQueryParams) ([]*AuthResourceEntry, int64, error)

	// ListPermittedCatalogIDs returns the catalog IDs permitted for every
	// requested operation, preserving the requested catalog sort order. The
	// resource operations map contains the bkn-safe result for each returned ID.
	ListPermittedCatalogIDs(ctx context.Context, ops []string, allowOperation bool,
		params CatalogsQueryParams) ([]string, map[string]PermissionResourceOps, error)

	// CheckCatalogPermission checks bkn-safe permission for a catalog ID. When
	// getCatalog is true, it also returns the existing, non-sensitive catalog.
	CheckCatalogPermission(ctx context.Context, catalogID string, ops []string, getCatalog bool) (bool, *Catalog, error)

	// InternalGetByID retrieves a Catalog by ID for internal workers.
	InternalGetByID(ctx context.Context, id string, withSensitiveFields bool) (*Catalog, error)
	// InternalGetByIDs retrieves Catalogs keyed by ID for internal callers without permission filtering.
	InternalGetByIDs(ctx context.Context, ids []string) (map[string]*Catalog, error)
	// InternalTestConnection tests catalog connection without user permission checks.
	InternalTestConnection(ctx context.Context, catalogID string) (*CatalogHealthCheckStatus, error)
}
