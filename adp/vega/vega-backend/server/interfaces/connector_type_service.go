// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "context"

// The ConnectorTypeService defines the connector type business logic interface
//
//go:generate mockgen -source ../interfaces/connector_type_service.go -destination ../interfaces/mock/mock_connector_type_service.go
type ConnectorTypeService interface {
	// Register registers the connector type
	Register(ctx context.Context, ct *ConnectorTypeReq) error
	// Update updates the connector type
	Update(ctx context.Context, ct *ConnectorType, req *ConnectorTypeReq) error
	// Delete deletes the connector type
	DeleteByType(ctx context.Context, tp string) error
	// GetByType obtains the connector type based on the type
	GetByType(ctx context.Context, tp string) (*ConnectorType, error)
	// List the connector types
	List(ctx context.Context, params ConnectorTypesQueryParams) ([]*ConnectorType, int64, error)
	// ListAuthResources lists connector type auth resources with filters.
	ListAuthResources(ctx context.Context, params AuthResourceQueryParams) ([]*AuthResourceEntry, int64, error)
	// CheckExistByType checks whether the connector type exists
	CheckExistByType(ctx context.Context, tp string) (bool, error)
	// CheckExistByName checks whether the connector type name exists
	CheckExistByName(ctx context.Context, name string) (bool, error)
	// SetEnabled enables/disables the connector type
	SetEnabled(ctx context.Context, tp string, enabled bool) error
}
