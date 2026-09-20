// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

const AuthResourceSortName = "name"

var AuthResourceSort = map[string]struct{}{
	AuthResourceSortName: {},
}

type AuthResourceQueryParams struct {
	PaginationQueryParams
	Name           string
	CatalogID      string
	IncludeBuiltin bool
}

type AuthResourceEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
