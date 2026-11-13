// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "github.com/openbkn-ai/licverify"

const (
	ConnectorModeLocal  string = "local"  // It runs built-in within the vega-backend process
	ConnectorModeRemote string = "remote" // It runs as an independent service and is invoked via HTTP

	ConnectorTypeSortName string = "name"

	ConnectorCategoryTable   string = "table"   // Relational database
	ConnectorCategoryIndex   string = "index"   // Search engine
	ConnectorCategoryTopic   string = "topic"   // Message queue
	ConnectorCategoryFile    string = "file"    // "File
	ConnectorCategoryFileset string = "fileset" // File set
	ConnectorCategoryMetric  string = "metric"  // Time series database
	ConnectorCategoryAPI     string = "api"     // API service
)

var (
	CONNECTOR_TYPE_SORT = map[string]struct{}{
		ConnectorTypeSortName: {},
	}
)

// ConnectorFieldConfig is custom connector configuration metadata, not a JSON Schema.
// Type and Description use the familiar Schema keyword meanings; Name, the field-level
// Required boolean, and Encrypted are custom metadata. Standard JSON Schema requires
// conversion, including collecting required field identifiers in the parent object's
// required array and handling display names and encryption separately.
type ConnectorFieldConfig struct {
	Name        string `json:"name"`        // Field display name
	Type        string `json:"type"`        // Field types: string, integer, number, boolean, object, array
	Description string `json:"description"` // Field description
	Required    bool   `json:"required"`    // Custom field-level required flag
	Encrypted   bool   `json:"encrypted"`   // Is encrypted storage required (custom extension)
}

// ConnectorType represents a registered connector type.
type ConnectorType struct {
	Type        string   `json:"type"`
	Name        string   `json:"name"`        // mysql, postgresql, kafka...
	Tags        []string `json:"tags"`        // Tag
	Description string   `json:"description"` // Type description
	Mode        string   `json:"mode"`        // local | remote
	Category    string   `json:"category"`    // table | index | topic | file | fileset | metric | api
	Endpoint    string   `json:"endpoint"`    // Only remote mode, remote service address
	Enabled     bool     `json:"enabled"`     // Whether to enable

	Available       bool              `json:"available"`                  // Whether the current binary contains the implementation of this connector
	RequiredEdition licverify.Edition `json:"required_edition,omitempty"` // Minimum edition required by this connector implementation

	FieldConfig map[string]ConnectorFieldConfig `json:"field_config,omitempty"` // Custom runtime metadata keyed by field identifier

	Operations []string `json:"operations"`
}

// ConnectorTypesQueryParams query parameters
type ConnectorTypesQueryParams struct {
	PaginationQueryParams
	Name      string `json:"name"`      // Fuzzy filtering by name
	Tag       string `json:"tag"`       // Filter by label
	Mode      string `json:"mode"`      // Filter by pattern
	Category  string `json:"category"`  // Filter by category
	Enabled   *bool  `json:"enabled"`   // Filter by enabled status
	Available *bool  `json:"available"` // Filter by the current binary available state
}

// ConnectorTypeReq indicates a request to create or update the connector type
type ConnectorTypeReq struct {
	Type        string   `json:"type"`
	Name        string   `json:"name"`        // mysql, postgresql, kafka...
	Tags        []string `json:"tags"`        // Tag
	Description string   `json:"description"` // Type description
	Mode        string   `json:"mode"`        // local | remote
	Category    string   `json:"category"`    // table | index | topic | file | fileset | metric | api
	Endpoint    string   `json:"endpoint"`    // Only remote mode, remote service address
	Enabled     bool     `json:"enabled"`     // Whether to enable
}
