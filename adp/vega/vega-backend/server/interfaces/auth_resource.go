// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

const AuthResourceTypeConnectorType = "connector-type"

var AuthResourceSort = map[string]string{
	"name": "f_name",
}

func AuthResourceTypes() []string {
	return []string{
		AUTH_RESOURCE_TYPE_CATALOG,
		AUTH_RESOURCE_TYPE_RESOURCE,
		AuthResourceTypeConnectorType,
	}
}

type AuthResourceQueryParams struct {
	PaginationQueryParams
	ID      string
	Keyword string
}

type AuthResourceEntry struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Name string `json:"name"`
}
