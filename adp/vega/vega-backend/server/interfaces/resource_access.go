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

// ResourceAccess defines resource data access interface.
//
//go:generate mockgen -source ../interfaces/resource_access.go -destination ../interfaces/mock/mock_resource_access.go
type ResourceAccess interface {
	// Create creates a new Resource.
	Create(ctx context.Context, tx *sql.Tx, resource *Resource) error
	// GetByID retrieves a Resource by ID, using tx when provided.
	GetByID(ctx context.Context, tx *sql.Tx, id string) (*Resource, error)
	// GetByIDs retrieves Resources by IDs.
	GetByIDs(ctx context.Context, ids []string) ([]*Resource, error)
	// GetSummariesByIDs retrieves resource list summaries by IDs.
	GetSummariesByIDs(ctx context.Context, ids []string) ([]*ResourceSummary, error)
	// GetPermissionRefsByIDs retrieves the resource-to-catalog relations by IDs.
	GetPermissionRefsByIDs(ctx context.Context, ids []string) ([]ResourcePermissionRef, error)
	// GetByName retrieves a Resource by catalog and name.
	GetByName(ctx context.Context, catalogID string, name string) (*Resource, error)
	// GetByCatalogID retrieves all Resources under a Catalog.
	GetByCatalogID(ctx context.Context, catalogID string) ([]*Resource, error)
	// List lists resource summaries with filters.
	List(ctx context.Context, params ResourcesQueryParams) ([]*ResourceSummary, int64, error)
	// ListPermissionRefs lists the minimal relations needed before list authorization.
	ListPermissionRefs(ctx context.Context, params ResourcesQueryParams) ([]ResourcePermissionRef, error)
	// Update updates a Resource.
	Update(ctx context.Context, tx *sql.Tx, resource *Resource, expectedUpdateTime int64) (int64, error)
	// UpdateEnabled updates only a Resource's enabled state and audit fields.
	UpdateEnabled(ctx context.Context, id string, enabled bool, updateTime int64, updater AccountInfo) error
	// UpdateLocalIndexName updates only a Resource's local index name.
	UpdateLocalIndexName(ctx context.Context, tx *sql.Tx, id, localIndexName string) error
	// UpdateLocalIndexState atomically updates Resource-owned index state.
	UpdateLocalIndexState(ctx context.Context, tx *sql.Tx, id string,
		localIndexStatus, localIndexName, syncMark string) (bool, error)
	// UpdateSemanticMetadata updates only Resource metadata owned by semantic understanding.
	UpdateSemanticMetadata(ctx context.Context, tx *sql.Tx, resource *Resource, expectedUpdateTime int64) (int64, error)
	// UpdateDiscoveryMetadata updates only Resource metadata owned by discovery.
	UpdateDiscoveryMetadata(ctx context.Context, tx *sql.Tx, resource *Resource, expectedUpdateTime int64) (int64, error)
	// UpdateStatus updates a Resource's status, using tx when provided.
	UpdateStatus(ctx context.Context, tx *sql.Tx, id string, status string, statusMessage string) error
	// UpdateDiscoverStatus updates a Resource's last discover status.
	UpdateDiscoverStatus(ctx context.Context, id string, status string) error
	// DeleteByIDs deletes Resources by IDs.
	DeleteByIDs(ctx context.Context, ids []string) error

	// ListAuthResources lists resource auth resources with filters.
	ListAuthResources(ctx context.Context, params AuthResourceQueryParams) ([]*AuthResourceEntry, error)

	// CheckExistByCategories checks if Resources exists by catalog ID and categories.
	CheckExistByCategories(ctx context.Context, catalogID string, categories []string) (bool, error)

	// DeleteByCatalogID deletes Resources by catalog ID.
	DeleteByCatalogID(ctx context.Context, tx *sql.Tx, catalogID string) error
}
