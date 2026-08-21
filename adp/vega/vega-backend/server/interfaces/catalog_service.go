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
	// Get retrieves a Catalog by IDs.
	GetByIDs(ctx context.Context, ids []string) ([]*Catalog, error)
	// List lists Catalogs with filters.
	List(ctx context.Context, params CatalogsQueryParams) ([]*Catalog, int64, error)
	// Update updates a Catalog.
	Update(ctx context.Context, catalog *Catalog, req *CatalogRequest, allowUnhealthy bool) error
	// SetEnabled updates Catalog enabled state.
	SetEnabled(ctx context.Context, catalog *Catalog, enabled bool) error
	// DeleteByID deletes a Catalog by ID.
	DeleteByID(ctx context.Context, id string) error
	// GetDeletionImpact returns the current impact and guards for deleting a catalog.
	GetDeletionImpact(ctx context.Context, id string) (*CatalogDeletionImpact, error)
	// CheckExistByID checks if a Catalog exists by ID.
	CheckExistByID(ctx context.Context, id string) (bool, error)
	// ListInternalIDs lists the ids of all internal system directories (grouped by internal_resource type for resource permission verification).
	ListInternalIDs(ctx context.Context) ([]string, error)
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

	// FilterAuthorizedCatalogs keeps the ids the caller may perform op on,
	// bounded by the page the caller already fetched rather than by the size of
	// the grant. Every listing that hangs off a catalog filters through this.
	FilterAuthorizedCatalogs(ctx context.Context, ids []string, op string) (map[string]bool, error)

	// CheckTaskPermission authorizes an operation on something that hangs off a
	// catalog. Unlike CheckCatalogPermission it survives the catalog's deletion:
	// tasks outlive their catalog, and judging them on an object that is gone
	// would strand them beyond anyone's reach.
	CheckTaskPermission(ctx context.Context, catalogID string, op string) error

	// InternalGetByID retrieves a Catalog by ID for internal workers.
	InternalGetByID(ctx context.Context, id string, withSensitiveFields bool) (*Catalog, error)
	// InternalGetByIDs retrieves Catalogs for internal callers without permission filtering.
	InternalGetByIDs(ctx context.Context, ids []string) ([]*Catalog, error)
	// InternalTestConnection tests catalog connection without user permission checks.
	InternalTestConnection(ctx context.Context, catalogID string) (*CatalogHealthCheckStatus, error)
}
