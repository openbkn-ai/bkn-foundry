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

	BinaryModeMetadata = "metadata"
	BinaryModeContent  = "content"

	ResourceQuerySourceLocalIndex = "local_index"
	ResourceQuerySourceSource     = "source"

	ResourceValueReasonNotRequested = "not_requested"

	ResourceValueModeUnavailable = "unavailable"
	ResourceValueModeMetadata    = "metadata"
	ResourceValueModeContent     = "content"
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
	// AllowApproximate defaults to true for count_distinct. False requires exact execution.
	AllowApproximate *bool `json:"allow_approximate,omitempty"`
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
	Paging PagingRequest `json:"paging,omitempty"`
	Sort   []*SortField  `json:"sort,omitempty"`

	FilterCondition any `json:"filter_condition,omitempty"`

	OutputFields     []string `json:"output_fields"` // Specify the list of fields for output
	BinaryMode       *string  `json:"binary_mode,omitempty"`
	IgnoreLocalIndex *bool    `json:"ignore_local_index,omitempty"`

	NeedTotal   bool          `json:"need_total,omitempty"`
	Format      string        `json:"-"`
	Timeout     time.Duration `json:"-"` // Timeout period, query parameters
	SearchAfter []any         `json:"-"` // OpenSearch internal continuation state

	QueryType string `json:"-"`

	FilterCondCfg *FilterCondCfg `json:"-"`
	// FixedFilterCondCfg identifies the stored view condition within FilterCondCfg.
	// It is internal query provenance and must never be accepted from the API.
	FixedFilterCondCfg    *FilterCondCfg  `json:"-"`
	FixedActualFilterCond FilterCondition `json:"-"`
	ActualFilterCond      FilterCondition `json:"-"`

	// CursorEncoded keyset cursor values are injected by the query session; When not empty, use WHERE (sort_cols) > cursor instead of OFFSET
	CursorEncoded string `json:"-"`

	// Aggregate query related fields
	Aggregation *Aggregation   `json:"aggregation,omitempty"` // Aggregation metric
	GroupBy     []*GroupByItem `json:"group_by,omitempty"`    // Grouping dimension
	Having      *HavingClause  `json:"having,omitempty"`      // Filter (HAVING) the aggregation results
}

// ResourceDataQueryResult 统一连接器、中间层和逻辑视图的资源数据查询结果。
// 连接器返回执行结果，上层补充分页和查询来源；不通过请求对象回传结果元数据。
type ResourceDataQueryResult struct {
	Columns       []string         `json:"columns,omitempty"`
	Entries       []map[string]any `json:"entries"`
	TotalCount    int64            `json:"total_count"`
	Paging        *PagingResponse  `json:"paging,omitempty"`
	NeedTotal     bool             `json:"-"`
	QuerySource   string           `json:"query_source,omitempty"`
	IsApproximate bool             `json:"is_approximate"`
	SearchAfter   []any            `json:"-"` // OpenSearch 下一页的查询位置。
}

// ResourceValue is the JSON representation of a resource field that needs
// special handling. Binary content uses Base64 data; byte_length applies only
// to Binary fields.
type ResourceValue struct {
	Reason     string `json:"reason,omitempty"`
	Mode       string `json:"mode"`
	ByteLength *int64 `json:"byte_length,omitempty"`
	Data       any    `json:"data,omitempty"`
}

// AggregateOutputAlias 解析实际输出的聚合列，包含 HAVING count(*) 生成的隐式计数列。
func (params *ResourceDataQueryParams) AggregateOutputAlias() string {
	if params.Aggregation != nil {
		if params.Aggregation.Alias != "" {
			return params.Aggregation.Alias
		}
		return "__value"
	}
	if params.Having != nil && params.Having.Field == "count(*)" {
		return "__value"
	}
	return ""
}
