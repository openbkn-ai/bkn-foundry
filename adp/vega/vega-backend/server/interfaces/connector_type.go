// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

const (
	ConnectorModeLocal  string = "local"  // It runs built-in within the vega-backend process
	ConnectorModeRemote string = "remote" // It runs as an independent service and is invoked via HTTP
)

const (
	ConnectorCategoryTable   string = "table"   // Relational database
	ConnectorCategoryIndex   string = "index"   // Search engine
	ConnectorCategoryTopic   string = "topic"   // Message queue
	ConnectorCategoryFile    string = "file"    // "File
	ConnectorCategoryFileset string = "fileset" // File set
	ConnectorCategoryMetric  string = "metric"  // Time series database
	ConnectorCategoryAPI     string = "api"     // API service
)

var (
	CONNECTOR_TYPE_SORT = map[string]string{
		"name": "f_name",
	}
)

// Connector type constant definition
const (
	ConnectorTypeMySQL      string = "mysql"
	ConnectorTypeMariaDB    string = "mariadb"
	ConnectorTypePostgreSQL string = "postgresql"
	ConnectorTypeSQLServer  string = "sqlserver"
	ConnectorTypeOpenSearch string = "opensearch"
	ConnectorTypeOracle     string = "oracle"
	ConnectorTypeAnyShare   string = "anyshare"
)

// The list of connector types supported by the current unified query interface
// Note: The system supports more connector types, but the current unified query interface only supports the following types
var SupportedConnectorTypesForQuery = map[string]bool{
	ConnectorTypeMySQL:      true,
	ConnectorTypeMariaDB:    true,
	ConnectorTypePostgreSQL: true,
	ConnectorTypeSQLServer:  true,
	ConnectorTypeOpenSearch: true,
}

// Support GetSupportedConnectorTypesForQuery returns the current unified query interface connector type list
// Note: The system supports more connector types, but the current unified query interface only supports the following types
func GetSupportedConnectorTypesForQuery() []string {
	return []string{
		ConnectorTypeMySQL,
		ConnectorTypeMariaDB,
		ConnectorTypePostgreSQL,
		ConnectorTypeSQLServer,
		ConnectorTypeOpenSearch,
	}
}

// IsConnectorTypeSupportedForQuery check whether a given connector type supported by the current unified query interface
// Note: The system supports more connector types, but the current unified query interface only supports some types
func IsConnectorTypeSupportedForQuery(connectorType string) bool {
	return SupportedConnectorTypesForQuery[connectorType]
}

// ConnectorFieldConfig defines the metadata of the connector configuration field (compatible with JSON Schema properties)
type ConnectorFieldConfig struct {
	Name        string `json:"name"`        // Field display name
	Type        string `json:"type"`        // Field types: string, integer, number, boolean, object, array
	Description string `json:"description"` // Field description
	Required    bool   `json:"required"`    // Whether required
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

	Available   bool                            `json:"available"`              // Whether the current binary contains the implementation of this connector
	FieldConfig map[string]ConnectorFieldConfig `json:"field_config,omitempty"` // Runtime field configuration (compatible with JSON Schema properties)

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
