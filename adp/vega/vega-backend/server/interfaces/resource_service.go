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

// ResourceService defines resource business logic interface.
//
//go:generate mockgen -source ../interfaces/resource_service.go -destination ../interfaces/mock/mock_resource_service.go
type ResourceService interface {
	// Create creates a new Resource.
	Create(ctx context.Context, req *ResourceRequest) (*Resource, error)
	// Get retrieves a Resource by ID.
	GetByID(ctx context.Context, id string) (*Resource, error)
	// GetByIDs retrieves Resources by IDs.
	GetByIDs(ctx context.Context, ids []string) ([]*Resource, error)
	// GetByCatalogID retrieves all Resources under a Catalog.
	GetByCatalogID(ctx context.Context, catalogID string) ([]*Resource, error)
	// GetByName retrieves a Resource by catalog and name.
	GetByName(ctx context.Context, catalogID string, name string) (*Resource, error)
	// List lists Resources with filters.
	List(ctx context.Context, params ResourcesQueryParams) ([]*Resource, int64, error)
	// Update updates a Resource.
	Update(ctx context.Context, resource *Resource, req *ResourceRequest) error
	// UpdateStatus updates a Resource's status.
	UpdateStatus(ctx context.Context, id string, status string, statusMessage string) error
	// UpdateDiscoverStatus updates a Resource's last discover status.
	UpdateDiscoverStatus(ctx context.Context, id string, status string) error
	// DeleteByIDs deletes Resources by IDs.
	DeleteByIDs(ctx context.Context, ids []string) error
	// CheckExistByID checks if a Resource exists by ID.
	CheckExistByID(ctx context.Context, id string) (bool, error)
	// CheckExistByName checks if a Resource exists by name.
	CheckExistByName(ctx context.Context, catalogID string, name string) (bool, error)

	// ListAuthResources lists resource auth resources with filters.
	ListAuthResources(ctx context.Context, params AuthResourceQueryParams) ([]*AuthResourceEntry, int64, error)

	// CheckExistByCategories checks if Resources exists by catalog ID and categories.
	CheckExistByCategories(ctx context.Context, catalogID string, categories []string) (bool, error)

	// CheckResourcePermission reports whether the caller may perform op on the
	// resource, falling back to the owning catalog exactly as the resource's own
	// endpoints do (#817). Task services use it so a build, discover or semantic
	// task is authorized by the table it belongs to rather than by nothing at all.
	CheckResourcePermission(ctx context.Context, resourceID string, op string) error

	// AuthorizedResources resolves the whole visible set up front, for listings
	// that can push it into their SQL. Small sets are worth pushing down: the
	// count and the page then agree, which is what lets a narrowly granted
	// account page to its own rows at all.
	AuthorizedResources(ctx context.Context, op string) (AuthorizedScope, error)

	// FilterAuthorizedResources keeps the ids the caller may perform op on, for
	// listings of things that hang off a resource — build tasks above all. The
	// caller passes the ids on the page it has already fetched, so the question
	// is bounded by the page size rather than by how much the account was granted.
	//
	// The answer runs through the same two steps as every other check: ask the
	// resource, then the catalog it lives in. Internal-catalog resources are asked
	// under their own type, so a business role holding resource:* does not reach
	// them, while one granted a single internal catalog does.
	FilterAuthorizedResources(ctx context.Context, ids []string, op string) (map[string]bool, error)

	// InternalGetByID retrieves a Resource by ID for internal workers.
	InternalGetByID(ctx context.Context, id string) (*Resource, error)
	// InternalGetByIDs retrieves Resources for internal callers without permission filtering.
	InternalGetByIDs(ctx context.Context, ids []string) ([]*Resource, error)
	// InternalGetByCatalogID retrieves all Resources under a Catalog for internal callers.
	InternalGetByCatalogID(ctx context.Context, catalogID string) ([]*Resource, error)
	// InternalUpdateLocalIndexName updates only a Resource's local index name for internal workers.
	InternalUpdateLocalIndexName(ctx context.Context, tx *sql.Tx, id, localIndexName string) error
	// InternalUpdateSemanticMetadata updates only Resource metadata owned by semantic understanding.
	InternalUpdateSemanticMetadata(ctx context.Context, tx *sql.Tx, resource *Resource, expectedUpdateTime int64) error
	// InternalUpdateDiscoveryMetadata updates only Resource metadata owned by discovery.
	InternalUpdateDiscoveryMetadata(ctx context.Context, tx *sql.Tx, resource *Resource, expectedUpdateTime int64) error
	// InternalCreate creates a Resource for internal workers within a transaction.
	InternalCreate(ctx context.Context, tx *sql.Tx, req *ResourceRequest) (*Resource, error)
	// InternalUpdateStatus updates a Resource status for internal workers within a transaction.
	InternalUpdateStatus(ctx context.Context, tx *sql.Tx, id string, status string, statusMessage string) error
}
