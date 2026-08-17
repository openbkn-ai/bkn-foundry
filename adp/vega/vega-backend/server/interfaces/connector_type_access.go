// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "context"

// ConnectorTypeAccess defines the connector type data access interface
//
//go:generate mockgen -source ../interfaces/connector_type_access.go -destination ../interfaces/mock/mock_connector_type_access.go
type ConnectorTypeAccess interface {
	// Create creates the connector type
	Create(ctx context.Context, ct *ConnectorType) error
	// Update updates the connector type
	Update(ctx context.Context, ct *ConnectorType) error
	// Delete deletes the connector type
	DeleteByType(ctx context.Context, tp string) error
	// GetByType obtains the connector type based on the type
	GetByType(ctx context.Context, tp string) (*ConnectorType, error)
	// GetByName retrieves the connector type based on the name
	GetByName(ctx context.Context, name string) (*ConnectorType, error)
	// List the connector types
	List(ctx context.Context, params ConnectorTypesQueryParams) ([]*ConnectorType, int64, error)
	// ListAuthResources lists connector type auth resources with filters.
	ListAuthResources(ctx context.Context, params AuthResourceQueryParams) ([]*AuthResourceEntry, error)
	// SetEnabled enables/disables the connector type
	SetEnabled(ctx context.Context, tp string, enabled bool) error
}
