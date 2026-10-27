// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

// ObjectTypeStatsQuery asks for the instance counts of a knowledge network's object types.
type ObjectTypeStatsQuery struct {
	KNID   string
	Branch string
	// OTIDs limits the answer to these object types. Empty means every object type the caller can
	// see in the network.
	OTIDs []string
}

// ObjectTypeStatsError explains why one row carries no count.
type ObjectTypeStatsError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ObjectTypeStatsEntry is the count of one object type, or why there is none.
type ObjectTypeStatsEntry struct {
	ID string `json:"id"`
	// Count is null when no Vega measurement exists or Error is set.
	Count *int64 `json:"count"`
	// Exact is true when Count is available.
	Exact bool `json:"exact"`
	// Source is catalog, index (live dataset), or query (row-filtered).
	Source string `json:"source,omitempty"`
	// AsOf is Vega's collection time or the Cypher query time, in Unix milliseconds.
	AsOf  int64                 `json:"as_of,omitempty"`
	Error *ObjectTypeStatsError `json:"error,omitempty"`
}

// ObjectTypeStatsResult is the response of the object type statistics endpoint.
type ObjectTypeStatsResult struct {
	Entries []*ObjectTypeStatsEntry `json:"entries"`
	Total   int                     `json:"total"`
}

// ObjectTypeStatsService counts the instances behind a knowledge network's object types, taking
// unfiltered counts from Vega and filtered counts through Cypher.
//
//go:generate mockgen -source ../interfaces/object_type_stats_service.go -destination ../interfaces/mock/mock_object_type_stats_service.go
type ObjectTypeStatsService interface {
	ObjectTypeStats(ctx context.Context, query ObjectTypeStatsQuery) (*ObjectTypeStatsResult, error)
}
