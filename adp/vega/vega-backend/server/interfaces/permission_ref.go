// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.

package interfaces

// CatalogPermissionRef is the minimal Catalog relation required before list authorization.
type CatalogPermissionRef struct {
	CatalogID string
}

// ResourcePermissionRef is the minimal Resource relation required before list authorization.
type ResourcePermissionRef struct {
	ResourceID string
	CatalogID  string
}
