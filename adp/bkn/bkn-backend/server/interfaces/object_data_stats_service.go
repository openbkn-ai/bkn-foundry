// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "context"

const (
	// OBJECT_DATA_STATS_TIMEOUT_SEC bounds one aggregate at the connector. Two counts over one
	// table is a cheap statement, but it still runs against a customer's own database.
	OBJECT_DATA_STATS_TIMEOUT_SEC = 30
)

// ObjectTypeRef names one object type in one knowledge network branch.
type ObjectTypeRef struct {
	KNID   string `json:"kn_id"`
	Branch string `json:"branch,omitempty"`
	OTID   string `json:"ot_id"`
}

// ObjectDataStatsRequest asks for the data statistics of two object types, one per side of a
// comparison.
type ObjectDataStatsRequest struct {
	Base   ObjectTypeRef `json:"base"`
	Target ObjectTypeRef `json:"target"`
}

// ObjectDataStats is what one side's data looks like from the outside.
//
// It is deliberately two numbers. Anything richer — per-column null rates, value distributions —
// costs another pass over the table per column, and this is computed while someone waits with a
// comparison open in front of them.
type ObjectDataStats struct {
	KNID   string `json:"kn_id"`
	Branch string `json:"branch"`
	OTID   string `json:"ot_id"`
	OTName string `json:"ot_name"`

	ResourceID  string   `json:"resource_id"`
	PrimaryKeys []string `json:"primary_keys"`

	RowCount int64 `json:"row_count"`

	// PrimaryKeyDistinct is absent when the object type declares no primary key: there is nothing
	// to count distinct values of, and reporting zero would read as "every row is a duplicate".
	PrimaryKeyDistinct *int64 `json:"primary_key_distinct,omitempty"`

	// MissingKeys counts rows with a NULL in any primary key column. Such a row identifies no
	// object at all, which is a different fault from two rows identifying the same one, so it is
	// reported apart from DuplicateKeys rather than inflating it.
	MissingKeys *int64 `json:"missing_keys,omitempty"`

	// DuplicateKeys counts rows with a complete key beyond the first for each distinct key. A
	// non-zero value means the bound resource holds rows the model believes are the same object.
	DuplicateKeys *int64 `json:"duplicate_keys,omitempty"`
}

// ObjectDataStatsDelta is target minus base.
type ObjectDataStatsDelta struct {
	RowCount           int64  `json:"row_count"`
	PrimaryKeyDistinct *int64 `json:"primary_key_distinct,omitempty"`
}

// ObjectDataStatsResult carries both sides and their difference.
type ObjectDataStatsResult struct {
	Base   *ObjectDataStats `json:"base"`
	Target *ObjectDataStats `json:"target"`

	// SameResource is true when both object types are bound to the same vega resource. Then the
	// two sides are reading one table and every number matches by construction — a zero delta
	// that says nothing about the data, which the caller should not present as a finding.
	SameResource bool `json:"same_resource"`

	Delta ObjectDataStatsDelta `json:"delta"`
}

// ObjectDataStatsService reports how much data sits behind an object type.
//
//go:generate mockgen -source ../interfaces/object_data_stats_service.go -destination ../interfaces/mock/mock_object_data_stats_service.go
type ObjectDataStatsService interface {
	ObjectDataStats(ctx context.Context, req ObjectDataStatsRequest) (*ObjectDataStatsResult, error)
}
