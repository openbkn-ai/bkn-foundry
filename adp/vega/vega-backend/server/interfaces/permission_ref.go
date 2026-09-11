// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.

package interfaces

// CatalogPermissionRef is the minimal Catalog relation required before list authorization.
type CatalogPermissionRef struct {
	CatalogID string
}

// CatalogConnectorTypePermissionRef is the minimal Catalog relation required to aggregate connector types after authorization.
type CatalogConnectorTypePermissionRef struct {
	CatalogID     string
	CatalogType   string
	ConnectorType string
}

// ResourcePermissionRef is the minimal Resource relation required before list authorization.
type ResourcePermissionRef struct {
	ResourceID string
	CatalogID  string
}
