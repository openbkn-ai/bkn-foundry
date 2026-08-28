// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

type contextKey string // Private context key type.

const (
	CONTENT_TYPE_NAME = "Content-Type"
	CONTENT_TYPE_JSON = "application/json"

	HTTP_HEADER_METHOD_OVERRIDE = "x-http-method-override"
	HTTP_HEADER_ACCOUNT_ID      = "x-account-id"
	HTTP_HEADER_ACCOUNT_TYPE    = "x-account-type"

	ACCOUNT_INFO_KEY contextKey = "x-account-info" // Avoid using an untyped string as a context key.

	SERVICE_NAME = "ontology-query"

	// Default branch.
	MAIN_BRANCH = "main"

	// Whether metric data queries include model information.
	DEFAULT_INCLUDE_TYPE_INFO    = "false"
	DEFAULT_INCLUDE_LOGIC_PARAMS = "false"

	// Parameter sources.
	VALUE_FROM_INPUT    = "input"
	VALUE_FROM_PROPERTY = "property"

	// Property types.
	PROPERTY_TYPE_METRIC = "metric"

	// Sort directions.
	DESC_DIRECTION = "desc"
	ASC_DIRECTION  = "asc"

	// Score sort field.
	SORT_FIELD_SCORE = "_score"

	// Maximum number of paths explored by a subgraph query.
	MAX_PATHS = 2000

	// Default result count for path-based queries.
	DEFAULT_PATHS = 2000

	// Path-based subgraph query type.
	QUERY_TYPE_RELATION_TYPE_PATH = "relation_path"

	// Maximum limit value.
	MAX_LIMIT = 10000

	// Default result count for object-type instance searches.
	DEFAULT_OBJECT_LIMIT = 10

	// Default instance count per node object type in subgraph queries.
	DEFAULT_LIMIT = 1000

	// Operator execution_mode values.
	OPERATOR_EXECUTION_MODE_SYNC = "sync"

	// Edge directions.
	DIRECTION_FORWARD       = "forward"
	DIRECTION_BACKWARD      = "backward"
	DIRECTION_BIDIRECTIONAL = "bidirectional"

	// Default limit for search_after queries.
	SearchAfter_Limit = 10000

	// Default use of cached persistent data for object-type queries.
	DEFAULT_IGNORING_STORE_CACHE = "false"
)

const (
	OBJECT_TYPE_KN            = "knowledge_network"
	OBJECT_TYPE_OBJECT_TYPE   = "object_type"
	OBJECT_TYPE_RELATION_TYPE = "relation_type"
	OBJECT_TYPE_ACTION_TYPE   = "action_type"
)

type ResourceInfo struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	BoxID      string `json:"box_id,omitempty"`
	ToolID     string `json:"tool_id,omitempty"`
	ResultPath string `json:"result_path,omitempty"`
}

type CommonInfo struct {
	Tags    []string `json:"tags" mapstructure:"tags"`
	Comment string   `json:"comment" mapstructure:"comment"`
	Icon    string   `json:"icon" mapstructure:"icon"`
	Color   string   `json:"color" mapstructure:"color"`
	Detail  string   `json:"detail" mapstructure:"detail"`
}

type AccountInfo struct {
	ID   string `json:"id" mapstructure:"id"`
	Type string `json:"type" mapstructure:"type"`
	Name string `json:"name" mapstructure:"name"`
}

type PageQuery struct {
	// Pagination information.
	NeedTotal bool `json:"need_total"`
	Limit     int  `json:"limit"`
	// Offset pagination applies only to resource-backed Vega tables and is ignored when search_after is provided.
	Offset int `json:"offset"`
	// UseSearchAfter bool          `json:"use_search_after"` // Business knowledge networks only expose search_after, so this option is unnecessary.
	Sort []*SortParams `json:"sort"`
	SearchAfterParams
}
