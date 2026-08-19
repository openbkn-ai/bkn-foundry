// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"encoding/json"
	"math/big"
)

var maxSafeTOONInteger = big.NewInt(9007199254740991)

// VegaRawQueryReq vega raw SQL query request (read-only).
// Query is MySQL dialect SQL. The table name is referenced by {{.resource_id}} placeholder, which is parsed into the real table name by vega.
type VegaRawQueryReq struct {
	Query           string            `json:"query"`                       // MySQL Dialect SQL.
	QueryFormat     string            `json:"query_format"`                // fixed to sql.
	InputDialect    string            `json:"input_dialect"`               // fixed to mysql.
	QueryTimeoutSec int               `json:"query_timeout_sec,omitempty"` // Query timeout (seconds), 1-3600.
	Paging          VegaPagingRequest `json:"paging"`
}

type VegaPagingRequest struct {
	Mode   string `json:"mode"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor,omitempty"`
}

type VegaPagingResponse struct {
	NextCursor   *string `json:"next_cursor" toon:"next_cursor"`
	ExpiresAtSec *int64  `json:"expires_at_sec" toon:"expires_at_sec"`
}

// VegaColumn The column information returned by the vega query.
type VegaColumn struct {
	Name string `json:"name" toon:"name"`
	Type string `json:"type,omitempty" toon:"type,omitempty"`
}

// VegaRawQueryResp vega raw query response.
type VegaRawQueryResp struct {
	Columns    []VegaColumn        `json:"columns" toon:"columns"`
	Entries    []map[string]any    `json:"entries" toon:"entries"`
	TotalCount *int64              `json:"total_count,omitempty" toon:"total_count,omitempty"`
	Warnings   []string            `json:"warnings,omitempty" toon:"warnings,omitempty"`
	Paging     *VegaPagingResponse `json:"paging,omitempty" toon:"paging,omitempty"`
}

// TOONValue returns a shallow response copy whose dynamic entries are safe for
// toon-go to encode. The original response remains unchanged for JSON output
// and MCP structuredContent.
func (r *VegaRawQueryResp) TOONValue() any {
	if r == nil {
		return r
	}

	var entries []map[string]any
	for i, entry := range r.Entries {
		value, changed := toonSafeValue(entry)
		if !changed {
			continue
		}
		if entries == nil {
			entries = make([]map[string]any, len(r.Entries))
			copy(entries, r.Entries)
		}
		entries[i] = value.(map[string]any)
	}
	if entries == nil {
		return r
	}

	copy := *r
	copy.Entries = entries
	return &copy
}

// toonSafeValue copies only branches that contain integer json.Number values
// outside the IEEE 754 safe range. toon-go currently normalizes json.Number
// through float64, so those values must be rendered as strings.
func toonSafeValue(value any) (any, bool) {
	switch v := value.(type) {
	case json.Number:
		integer, ok := new(big.Int).SetString(v.String(), 10)
		if ok && new(big.Int).Abs(integer).Cmp(maxSafeTOONInteger) > 0 {
			return v.String(), true
		}
		return v, false
	case []any:
		var clone []any
		for i, item := range v {
			converted, changed := toonSafeValue(item)
			if !changed {
				continue
			}
			if clone == nil {
				clone = make([]any, len(v))
				copy(clone, v)
			}
			clone[i] = converted
		}
		if clone != nil {
			return clone, true
		}
	case map[string]any:
		var clone map[string]any
		for key, item := range v {
			converted, changed := toonSafeValue(item)
			if !changed {
				continue
			}
			if clone == nil {
				clone = make(map[string]any, len(v))
				for originalKey, originalValue := range v {
					clone[originalKey] = originalValue
				}
			}
			clone[key] = converted
		}
		if clone != nil {
			return clone, true
		}
	}
	return value, false
}

// VegaListResourcesReq Vega resource list query input parameters (direct query at the data layer, separated from the ontology).
// Empty fields do not participate in filtering; when Offset/Limit is 0, vega takes the default value (offset=0, limit=20).
type VegaListResourcesReq struct {
	CatalogID string // Limit a catalog.
	Category  string // Resource category: table/file/fileset/api/metric/topic/index/logicview/dataset.
	Offset    int
	Limit     int
}

// VegaResourceColumn The physical column of the vega resource (taken from schema_definition).
type VegaResourceColumn struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}

// VegaResource vega data resource (shared by list/get, SchemaDefinition is usually empty when using list).
type VegaResource struct {
	ID               string               `json:"id"`
	CatalogID        string               `json:"catalog_id"`
	Name             string               `json:"name"`
	Category         string               `json:"category"`
	Status           string               `json:"status"`
	SchemaDefinition []VegaResourceColumn `json:"schema_definition"`
}

// VegaListResourcesResp vega resource list response (entries envelope + total).
type VegaListResourcesResp struct {
	Entries    []VegaResource `json:"entries"`
	TotalCount int64          `json:"total_count"`
}

// DrivenVega vega data directory backend access interface (read-only query).
type DrivenVega interface {
	// RawQuery executes read-only SQL. The caller (MCP tool layer) must ensure SELECT-only by itself, and this interface does not perform statement verification.
	RawQuery(ctx context.Context, req *VegaRawQueryReq) (*VegaRawQueryResp, error)
	// GetResourceConnectorType parses the connector type of the catalog to which it belongs based on resource_id.
	// For resource discovery and display.
	GetResourceConnectorType(ctx context.Context, resourceID string) (string, error)
	// ListResources lists queryable data resources (filtered by account view_detail authorization, forced by vega).
	ListResources(ctx context.Context, req *VegaListResourcesReq) (*VegaListResourcesResp, error)
	// GetResource gets a single resource (including physical column schema_definition); an error is returned when the resource does not exist or has no rights.
	GetResource(ctx context.Context, resourceID string) (*VegaResource, error)
}
