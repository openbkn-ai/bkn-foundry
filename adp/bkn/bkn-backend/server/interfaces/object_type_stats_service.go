// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

const (
	// OBJECT_TYPE_STATS_DEFAULT_CACHE_TTL_SEC is how long a count is served from the cache when the
	// deployment does not configure it.
	OBJECT_TYPE_STATS_DEFAULT_CACHE_TTL_SEC = 600
	// OBJECT_TYPE_STATS_DEFAULT_QUERY_TIMEOUT_SEC bounds one exact COUNT(*) when the deployment does
	// not configure it.
	OBJECT_TYPE_STATS_DEFAULT_QUERY_TIMEOUT_SEC = 30
	// OBJECT_TYPE_STATS_DEFAULT_BACKGROUND_TIMEOUT_SEC bounds a count that carries on in the
	// background after its caller was answered, when the deployment does not configure it.
	OBJECT_TYPE_STATS_DEFAULT_BACKGROUND_TIMEOUT_SEC = 600

	// OBJECT_TYPE_COUNT_SOURCE_INDEX is a count read from an index that already holds the data.
	OBJECT_TYPE_COUNT_SOURCE_INDEX = "index"
	// OBJECT_TYPE_COUNT_SOURCE_QUERY is an exact COUNT(*) run against the data.
	OBJECT_TYPE_COUNT_SOURCE_QUERY = "query"
	// OBJECT_TYPE_COUNT_SOURCE_CATALOG is a timestamped exact resource count from Vega.
	OBJECT_TYPE_COUNT_SOURCE_CATALOG = "catalog"
)

// ObjectTypeStatsQuery asks for the instance counts of a knowledge network's object types.
type ObjectTypeStatsQuery struct {
	KNID   string
	Branch string
	// OTIDs limits the answer to these object types. Empty means every object type the caller can
	// see in the network.
	OTIDs []string
	// Refresh recomputes every count instead of serving it from the cache.
	Refresh bool
}

// ObjectTypeStatsError explains why one row carries no count.
type ObjectTypeStatsError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ObjectTypeStatsEntry is the count of one object type, or why there is none.
type ObjectTypeStatsEntry struct {
	ID string `json:"id"`
	// Count is null when Error is set.
	Count *int64 `json:"count"`
	// Exact is false for an estimate: an index that may lag its source.
	Exact bool `json:"exact"`
	// Source is one of the OBJECT_TYPE_COUNT_SOURCE_* values.
	Source string `json:"source,omitempty"`
	// AsOf is when the count was taken, in Unix milliseconds. A cached count keeps the time it was
	// taken, not the time it was served.
	AsOf  int64                 `json:"as_of,omitempty"`
	Error *ObjectTypeStatsError `json:"error,omitempty"`
}

// ObjectTypeStatsResult is the response of the object type statistics endpoint.
type ObjectTypeStatsResult struct {
	Entries []*ObjectTypeStatsEntry `json:"entries"`
	Total   int                     `json:"total"`
}

// ObjectTypeStatsService counts the instances behind a knowledge network's object types, taking
// each count from the cheapest source that can answer it.
//
//go:generate mockgen -source ../interfaces/object_type_stats_service.go -destination ../interfaces/mock/mock_object_type_stats_service.go
type ObjectTypeStatsService interface {
	ObjectTypeStats(ctx context.Context, query ObjectTypeStatsQuery) (*ObjectTypeStatsResult, error)
}
