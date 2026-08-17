// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "time"

const (
	Format_Original = "original"
	Format_Flat     = "flat"

	// The maximum query length is set to 10,000
	MAX_SEARCH_SIZE = 10000

	DEFAULT_DATA_LIMIT = 10

	// Calendar interval constant - Refer to the calendar_interval enumeration definition of OpenSearch
	CALENDAR_UNIT_MINUTE  = "minute"
	CALENDAR_UNIT_HOUR    = "hour"
	CALENDAR_UNIT_DAY     = "day"
	CALENDAR_UNIT_WEEK    = "week"
	CALENDAR_UNIT_MONTH   = "month"
	CALENDAR_UNIT_QUARTER = "quarter"
	CALENDAR_UNIT_YEAR    = "year"
)

// SortField represents a field to sort by.
type SortField struct {
	Field     string `json:"field"`
	Direction string `json:"direction"`
}

// Aggregation represents an aggregation operation.
type Aggregation struct {
	Property string `json:"property"` // The name of the aggregated resource field
	Aggr     string `json:"aggr"`     // Aggregation functions: count, count_distinct, sum, max, min, avg
	Alias    string `json:"alias,omitempty"`
}

// GroupByItem represents a group by dimension.
type GroupByItem struct {
	Property         string `json:"property"`                    // Grouping dimension
	Description      string `json:"description,omitempty"`       // Documentation/debugging only
	CalendarInterval string `json:"calendar_interval,omitempty"` // The calendar_interval parameter of date_histogram supports: minute, hour, day, week, month, quarter, year
}

// HavingClause represents a HAVING clause for aggregation filtering.
type HavingClause struct {
	Field     string `json:"field"`     // Fixed as "__value"
	Operation string `json:"operation"` // ==, !=, >, >=, <, <=, in, not_in, range, out_range
	Value     any    `json:"value"`
}

// ResourceDataQueryParams represents query parameters for data retrieval.
type ResourceDataQueryParams struct {
	// Offset and Limit remain internal connector inputs. The HTTP contract uses
	// Paging so Raw Query and Resource Data share the same request shape.
	Offset int `json:"-"`
	Limit  int `json:"-"`
	// LegacyLimit/LegacyOffset accept older callers that still send top-level
	// limit/offset. ValidateResourceDataQueryParams maps them into Paging when
	// paging is unset (openbkn-ai/bkn-foundry#475).
	LegacyLimit  int           `json:"limit,omitempty"`
	LegacyOffset int           `json:"offset,omitempty"`
	Paging       PagingRequest `json:"paging,omitempty"`
	Sort         []*SortField  `json:"sort,omitempty"`

	FilterCondition any `json:"filter_condition,omitempty"`

	OutputFields []string `json:"output_fields"` // Specify the list of fields for output

	NeedTotal   bool          `json:"need_total,omitempty"`
	Format      string        `json:"-"`
	Timeout     time.Duration `json:"-"` // Timeout period, query parameters
	SearchAfter []any         `json:"-"` // OpenSearch internal continuation state

	QueryType string `json:"-"`

	FilterCondCfg    *FilterCondCfg  `json:"-"`
	ActualFilterCond FilterCondition `json:"-"`

	// CursorEncoded keyset cursor values are injected by the query session; When not empty, use WHERE (sort_cols) > cursor instead of OFFSET
	CursorEncoded string `json:"-"`

	// Aggregate query related fields
	Aggregation *Aggregation   `json:"aggregation,omitempty"` // Aggregation metric
	GroupBy     []*GroupByItem `json:"group_by,omitempty"`    // Grouping dimension
	Having      *HavingClause  `json:"having,omitempty"`      // Filter (HAVING) the aggregation results
}

// ResourceDataQueryResult is the common response model for resource-data and
// logic-view queries. Paging follows the same single/cursor contract as raw
// queries.
type ResourceDataQueryResult struct {
	Entries    []map[string]any
	TotalCount int64
	Paging     *PagingResponse
	NeedTotal  bool
}
