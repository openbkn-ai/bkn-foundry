// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "context"

const (
	// CYPHER_DEFAULT_LIMIT caps a query that did not write its own LIMIT.
	CYPHER_DEFAULT_LIMIT = 1000
	// CYPHER_MAX_LIMIT is the largest LIMIT a query may ask for. It matches
	// the page ceiling of vega-backend's raw query interface, which would
	// otherwise cut the result down without saying so.
	CYPHER_MAX_LIMIT = 10000
	// CYPHER_DEFAULT_TIMEOUT_SEC bounds one statement at the connector.
	CYPHER_DEFAULT_TIMEOUT_SEC = 30
	// CYPHER_MAX_PATH_LENGTH bounds how many relationships one pattern may
	// hold. Each is a join, and the row limit bounds what comes back rather
	// than what it costs to produce, so the number of joins is bounded here.
	CYPHER_MAX_PATH_LENGTH = 8
)

// CypherQuery is one read-only query against a knowledge network.
type CypherQuery struct {
	KNID   string
	Branch string
	Query  string
	// Parameters supply the values the query refers to as $name. They are
	// values only: a parameter can change which rows come back, never which
	// resource or column is read.
	Parameters map[string]any
}

// CypherQueryResult carries the rows a query produced. The generated SQL is
// deliberately absent: it names physical tables and columns, which the caller
// is not entitled to just because they may read the data.
type CypherQueryResult struct {
	Columns []RawQueryColumn `json:"columns"`
	Entries []map[string]any `json:"entries"`
}

//go:generate mockgen -source ../interfaces/cypher_query_service.go -destination ../interfaces/mock/mock_cypher_query_service.go
type CypherQueryService interface {
	Query(ctx context.Context, query CypherQuery) (*CypherQueryResult, error)
}
